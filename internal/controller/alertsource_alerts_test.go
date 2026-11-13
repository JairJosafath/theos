/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// alertSkew moves the AlertSource reconciler's and the receiver's clock.
var alertSkew atomic.Int64

// alertNow is the clock of the suite's AlertSource reconciler and receiver.
func alertNow() time.Time { return time.Now().Add(time.Duration(alertSkew.Load())) }

// alertClockAt moves the alerts' clock to at, until the spec ends.
func alertClockAt(at time.Time) {
	alertSkew.Store(int64(time.Until(at)))
	DeferCleanup(func() { alertSkew.Store(0) })
}

// alertDay is today at 10:00 UTC, plus days.
func alertDay(days int) time.Time {
	return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, days).Add(10 * time.Hour)
}

// alertStatusFaults fail an AlertSource's status writes, as a lost write.
var alertStatusFaults = &alertFaults{left: map[client.ObjectKey]int{},
	when: map[client.ObjectKey]func(*c4v1beta1.AlertSource) bool{}}

type alertFaults struct {
	mu   sync.Mutex
	left map[client.ObjectKey]int
	when map[client.ObjectKey]func(*c4v1beta1.AlertSource) bool
}

// failNext fails the next n status writes of key that when says yes to.
func (f *alertFaults) failNext(key client.ObjectKey, n int, when func(*c4v1beta1.AlertSource) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.left[key], f.when[key] = n, when
}

func (f *alertFaults) pending(key client.ObjectKey) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.left[key]
}

func (f *alertFaults) wrap(c client.Client) client.Client {
	return alertFaultyClient{Client: c, faults: f}
}

type alertFaultyClient struct {
	client.Client
	faults *alertFaults
}

func (c alertFaultyClient) Status() client.SubResourceWriter {
	return alertFaultyStatus{SubResourceWriter: c.Client.Status(), faults: c.faults}
}

type alertFaultyStatus struct {
	client.SubResourceWriter
	faults *alertFaults
}

func (s alertFaultyStatus) Patch(ctx context.Context, obj client.Object, patch client.Patch,
	opts ...client.SubResourcePatchOption) error {
	if src, ok := obj.(*c4v1beta1.AlertSource); ok {
		s.faults.mu.Lock()
		key := client.ObjectKeyFromObject(src)
		take := s.faults.left[key] > 0 && s.faults.when[key](src)
		if take {
			s.faults.left[key]--
		}
		s.faults.mu.Unlock()
		if take {
			return errStatusFault
		}
	}
	return s.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

// alertsSource creates the generic AlertSource alerts in ns, checked by a
// token, with edit applied to its spec, and waits for it to be Receiving. It
// returns the token.
func alertsSource(ns string, edit func(*c4v1beta1.AlertSourceSpec)) string {
	GinkgoHelper()
	token := rand.Text()
	mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "alerts-webhook"),
		Data: map[string][]byte{model.AlertSecretKeyToken: []byte(token)}})
	src := newAlertSource(ns)
	src.Spec.Type = c4v1beta1.AlertSourceGeneric
	if edit != nil {
		edit(&src.Spec)
	}
	mustCreate(src)
	alertSourceReady(src, c4v1beta1.ReasonAlertSourceReceiving)
	return token
}

// postAlertTo delivers a generic alert to the AlertSource alerts of ns on h,
// with token, and returns the answer's status.
func postAlertTo(h http.Handler, ns, token string, alert map[string]any) int {
	GinkgoHelper()
	body, err := json.Marshal(alert)
	Expect(err).NotTo(HaveOccurred())
	req := httptest.NewRequest(http.MethodPost, model.AlertsURL("", ns, "alerts"), strings.NewReader(string(body)))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

// fire delivers a firing alert titled title for orders-api through the
// suite's listener, and expects it taken.
func fire(ns, token, title string) {
	GinkgoHelper()
	Expect(postAlertTo(gitEvents.Handler(), ns, token,
		map[string]any{"title": title, "container": "orders-api"})).To(Equal(http.StatusAccepted))
}

// alertStatus waits for the status of the AlertSource alerts of ns to pass
// check, and returns it.
func alertStatus(ns string, check func(Gomega, c4v1beta1.AlertSourceStatus)) c4v1beta1.AlertSourceStatus {
	GinkgoHelper()
	var src c4v1beta1.AlertSource
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, "alerts"), &src)).To(Succeed())
		check(g, src.Status)
	}).Should(Succeed())
	return src.Status
}

// alertRecord is status's record of the alert titled title, nil when none.
func alertRecord(status c4v1beta1.AlertSourceStatus, title string) *c4v1beta1.AlertSourceAlert {
	for i := range status.Alerts {
		if status.Alerts[i].Name == title {
			return &status.Alerts[i]
		}
	}
	return nil
}

// alertIssues are the issues of orders-api in ns titled for an alert.
func alertIssues(ns string) []fake.Issue {
	var out []fake.Issue
	for _, i := range gitFake.AllIssues(ns, "orders-api") {
		if strings.HasPrefix(i.Title, "Alert: ") {
			out = append(out, i)
		}
	}
	return out
}

var _ = Describe("An AlertSource's deliveries", func() {
	It("files one issue for a signed delivery, and a second comments once the interval passed", func() {
		ns := newProjectNamespace()
		alertClockAt(alertDay(0))
		readyContainer(ns)
		token := alertsSource(ns, nil)

		fire(ns, token, "Disk full")
		status := alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			g.Expect(s.Deliveries).To(Equal(int64(1)))
			rec := alertRecord(s, "Disk full")
			g.Expect(rec).NotTo(BeNil())
			g.Expect(rec.Issue).NotTo(BeZero())
		})
		Expect(status.URL).To(Equal("/alerts/" + ns + "/alerts"))
		issues := alertIssues(ns)
		Expect(issues).To(HaveLen(1))
		Expect(issues[0].Title).To(Equal("Alert: Disk full"))
		Expect(issues[0].Labels).To(ConsistOf(model.LabelBug, model.LabelMonitor, model.LabelProposed))
		rec := alertRecord(status, "Disk full")
		Expect(issues[0].Number).To(Equal(int(rec.Issue)))
		Expect(model.HasAlertMarker(issues[0].Body, "alerts", rec.Key)).To(BeTrue())

		By("counting the second delivery, and saying nothing before the interval")
		fire(ns, token, "Disk full")
		alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			g.Expect(s.Deliveries).To(Equal(int64(2)))
			g.Expect(alertRecord(s, "Disk full").Count).To(Equal(int64(2)))
		})
		Consistently(func() []string {
			return gitFake.Issue(ns, "orders-api", int(rec.Issue)).Comments
		}, 2*time.Second).Should(BeEmpty())

		By("commenting once the interval passed, with no second issue")
		alertClockAt(alertDay(0).Add(61 * time.Minute))
		Eventually(func() []string {
			return gitFake.Issue(ns, "orders-api", int(rec.Issue)).Comments
		}).Should(ConsistOf(ContainSubstring("It fired once more since")))
		alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			g.Expect(alertRecord(s, "Disk full").Reported).To(Equal(int64(2)))
		})
		Expect(alertIssues(ns)).To(HaveLen(1))
	})

	It("refuses an unsigned delivery and writes nothing", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		token := alertsSource(ns, nil)
		before := testutil.ToFloat64(alertDeliveries.WithLabelValues(alertUnauthorized))
		h := gitEvents.Handler()
		alert := map[string]any{"title": "Disk full", "container": "orders-api"}
		Expect(postAlertTo(h, ns, "", alert)).To(Equal(http.StatusUnauthorized))
		Expect(postAlertTo(h, ns, rand.Text(), alert)).To(Equal(http.StatusUnauthorized))
		Expect(testutil.ToFloat64(alertDeliveries.WithLabelValues(alertUnauthorized)) - before).To(Equal(2.0))
		Expect(postAlertTo(h, ns, token, map[string]any{"summary": "no title"})).To(Equal(http.StatusBadRequest))
		Consistently(func(g Gomega) {
			var src c4v1beta1.AlertSource
			g.Expect(k8sClient.Get(ctx, key(ns, "alerts"), &src)).To(Succeed())
			g.Expect(src.Status.Deliveries).To(BeZero())
			g.Expect(src.Status.Alerts).To(BeEmpty())
		}, 2*time.Second).Should(Succeed())
		Expect(alertIssues(ns)).To(BeEmpty())
	})

	It("counts an alert that names no known Container on status.unmatched and files nothing", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		token := alertsSource(ns, nil)
		Expect(postAlertTo(gitEvents.Handler(), ns, token,
			map[string]any{"title": "Disk full", "container": "billing"})).To(Equal(http.StatusAccepted))
		status := alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			g.Expect(s.Unmatched).NotTo(BeNil())
			g.Expect(s.Unmatched.Count).To(Equal(int64(1)))
		})
		Expect(status.Unmatched.Message).To(ContainSubstring(`"billing"`))
		Expect(status.Unmatched.LastTime).NotTo(BeNil())
		Expect(status.Deliveries).To(Equal(int64(1)))
		Expect(status.Alerts).To(BeEmpty())
		Expect(alertIssues(ns)).To(BeEmpty())
	})

	It("folds the alerts past the daily limit, and says yesterday's fold before today's first alert steps", func() {
		ns := newProjectNamespace()
		alertClockAt(alertDay(0))
		readyContainer(ns)
		token := alertsSource(ns, func(s *c4v1beta1.AlertSourceSpec) { s.DailyLimit = 1 })

		fire(ns, token, "Disk full")
		status := alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			g.Expect(alertRecord(s, "Disk full")).NotTo(BeNil())
		})
		first := int(alertRecord(status, "Disk full").Issue)

		By("folding the second alert of the day, said at once on the day's last issue")
		fire(ns, token, "Queue backed up")
		Eventually(func() []string { return gitFake.Issue(ns, "orders-api", first).Comments }).
			Should(ConsistOf(ContainSubstring("And 1 more alert fired for this Container on " +
				alertDay(0).Format(time.DateOnly))))
		status = alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			rec := alertRecord(s, "Queue backed up")
			g.Expect(rec).NotTo(BeNil())
			g.Expect(rec.Folded).To(BeTrue())
		})
		Expect(status.Containers).To(HaveLen(1))
		Expect(status.Containers[0].Folded).To(Equal(int64(1)))

		By("counting a third on the day, unsaid within the interval")
		fire(ns, token, "Latency high")
		alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			g.Expect(s.Containers).To(HaveLen(1))
			g.Expect(s.Containers[0].Folded).To(Equal(int64(2)))
		})
		Consistently(func() []string { return gitFake.Issue(ns, "orders-api", first).Comments }, 2*time.Second).
			Should(HaveLen(1))
		Expect(alertIssues(ns)).To(HaveLen(1))

		By("saying the day's two on its last issue on the next day, and filing the next day's first")
		alertClockAt(alertDay(1))
		fire(ns, token, "Cache cold")
		Eventually(func() []string { return gitFake.Issue(ns, "orders-api", first).Comments }).
			Should(ContainElement(ContainSubstring("And 2 more alerts fired for this Container on " +
				alertDay(0).Format(time.DateOnly))))
		status = alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			rec := alertRecord(s, "Cache cold")
			g.Expect(rec).NotTo(BeNil())
			g.Expect(rec.Issue).NotTo(BeZero())
			g.Expect(rec.Folded).To(BeFalse())
		})
		Expect(status.Containers).To(HaveLen(1))
		Expect(status.Containers[0].Date).To(Equal(alertDay(1).Format(time.DateOnly)))
		Expect(status.Containers[0].Filed).To(Equal(int32(1)))
		Expect(status.Containers[0].Folded).To(BeZero())
		Expect(alertIssues(ns)).To(HaveLen(2))
	})

	It("files no second issue across a lost status write", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		token := alertsSource(ns, nil)
		recorded := func(src *c4v1beta1.AlertSource) bool { return len(src.Status.Alerts) > 0 }
		alertStatusFaults.failNext(key(ns, "alerts"), 1, recorded)
		DeferCleanup(alertStatusFaults.failNext, key(ns, "alerts"), 0, recorded)

		fire(ns, token, "Disk full")
		status := alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			g.Expect(alertRecord(s, "Disk full")).NotTo(BeNil())
		})
		Expect(alertStatusFaults.pending(key(ns, "alerts"))).To(BeZero())
		Expect(status.Deliveries).To(Equal(int64(1)))
		issues := alertIssues(ns)
		Expect(issues).To(HaveLen(1))
		Expect(int(alertRecord(status, "Disk full").Issue)).To(Equal(issues[0].Number))
		Consistently(func() []fake.Issue { return alertIssues(ns) }, 2*time.Second).Should(HaveLen(1))
	})

	It("files no second issue for an alert whose record made room while its issue is open", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		token := alertsSource(ns, nil)
		fire(ns, token, "Disk full")
		status := alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			g.Expect(alertRecord(s, "Disk full")).NotTo(BeNil())
		})
		number := alertRecord(status, "Disk full").Issue

		By("dropping its record, as one that made room for others")
		Eventually(func() error {
			var src c4v1beta1.AlertSource
			if err := k8sClient.Get(ctx, key(ns, "alerts"), &src); err != nil {
				return err
			}
			orig := src.DeepCopy()
			src.Status.Alerts = nil
			return k8sClient.Status().Patch(ctx, &src,
				client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
		}).Should(Succeed())

		fire(ns, token, "Disk full")
		alertStatus(ns, func(g Gomega, s c4v1beta1.AlertSourceStatus) {
			rec := alertRecord(s, "Disk full")
			g.Expect(rec).NotTo(BeNil())
			g.Expect(rec.Issue).To(Equal(number))
		})
		Consistently(func() []fake.Issue { return alertIssues(ns) }, 2*time.Second).Should(HaveLen(1))
	})

	It("drains on SIGTERM: files what the Git host takes once it answers, and drops what is left", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		token := alertsSource(ns, nil)
		dropped := func() float64 { return testutil.ToFloat64(alertDeliveries.WithLabelValues(alertDropped)) }
		down := errors.New("the Git host is down")
		DeferCleanup(func() { gitFake.FailListIssues(ns, "orders-api", nil) })

		// A receiver of its own, as the operator's at its end: the suite's
		// keeps taking deliveries for the other specs.
		run := func(timeout time.Duration, signal bool, alert string, whileRunning func()) {
			GinkgoHelper()
			sig, stopSig := context.WithCancel(context.Background())
			defer stopSig()
			receiver := &AlertReceiver{Client: k8sClient, APIReader: k8sClient, SignalContext: sig,
				DrainTimeout: timeout, Now: alertNow}
			receiver.Drainer = &AlertSourceReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(),
				APIReader: k8sClient, Git: fakeGitClient{gitFake}, Receiver: receiver, Now: alertNow}
			Expect(postAlertTo(receiver.Handler(), ns, token,
				map[string]any{"title": alert, "container": "orders-api"})).To(Equal(http.StatusAccepted))
			runCtx, stop := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- receiver.Start(runCtx) }()
			if signal {
				stopSig()
			}
			stop()
			Eventually(receiver.isClosed).Should(BeTrue())
			Expect(postAlertTo(receiver.Handler(), ns, token,
				map[string]any{"title": "Too late", "container": "orders-api"})).
				To(Equal(http.StatusServiceUnavailable))
			if whileRunning != nil {
				whileRunning()
			}
			Eventually(done).WithTimeout(timeout + 10*time.Second).Should(Receive(Succeed()))
		}
		titles := func() []string {
			issues := alertIssues(ns)
			out := make([]string, 0, len(issues))
			for _, i := range issues {
				out = append(out, i.Title)
			}
			return out
		}

		By("filing an alert taken while the Git host fails, once the host answers")
		gitFake.FailListIssues(ns, "orders-api", down)
		before := dropped()
		run(time.Minute, true, "Disk full", func() {
			Consistently(titles, time.Second).Should(BeEmpty())
			gitFake.FailListIssues(ns, "orders-api", nil)
		})
		Expect(titles()).To(ConsistOf("Alert: Disk full"))
		Expect(dropped()).To(Equal(before))

		By("dropping what the inbox holds at the deadline")
		gitFake.FailListIssues(ns, "orders-api", down)
		run(500*time.Millisecond, true, "Queue backed up", nil)
		Expect(dropped()).To(Equal(before + 1))
		gitFake.FailListIssues(ns, "orders-api", nil)

		By("filing nothing more when the context ends without a signal")
		run(time.Minute, false, "Latency high", nil)
		Expect(dropped()).To(Equal(before + 2))
		Consistently(titles, 2*time.Second).Should(ConsistOf("Alert: Disk full"))
	})
})
