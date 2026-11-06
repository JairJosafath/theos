/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// gitEventsServer serves the suite's receiver for the spec, and returns
// where.
func gitEventsServer() string {
	GinkgoHelper()
	server := httptest.NewServer(gitEvents.Handler())
	DeferCleanup(server.Close)
	return server.URL
}

// fakeDelivery is a delivery in the fake host's format
// (internal/ports/fake/events.go) concerning repo and number, signed with
// secret.
func fakeDelivery(secret, repo string, number int) (body []byte, header http.Header) {
	GinkgoHelper()
	body, err := json.Marshal(map[string]any{"repository": repo, "number": number})
	Expect(err).NotTo(HaveOccurred())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	header = http.Header{}
	header.Set(fake.EventHeader, fake.EventIssue)
	header.Set(fake.SignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return body, header
}

// postDelivery posts body with header to the receiver at base, as the hook of
// the GitProvider ns/name would, and returns the status code.
func postDelivery(base, ns, name string, body []byte, header http.Header) int {
	GinkgoHelper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/git-events/"+ns+"/"+name,
		bytes.NewReader(body))
	Expect(err).NotTo(HaveOccurred())
	req.Header = header
	res, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	Expect(res.Body.Close()).To(Succeed())
	return res.StatusCode
}

// registeredSecret waits until gp's hook is registered and returns its
// delivery Secret's value.
func registeredSecret(gp *c4v1beta1.GitProvider) string {
	GinkgoHelper()
	eventsReason(gp, c4v1beta1.ReasonEventsRegistered)
	return string(eventsSecret(Default, gp).Data[model.GitEventsSecretKey])
}

// eventsStatus waits until gp's Events condition has reason and status.
func eventsStatus(gp *c4v1beta1.GitProvider, status metav1.ConditionStatus, reason string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		cond := eventsCondition(g, gp)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(reason), cond.Message)
		g.Expect(cond.Status).To(Equal(status))
	}).Should(Succeed())
}

var _ = Describe("Git events", func() {
	It("reconciles the Container a signed delivery names at once, and nothing for a forged one (#1221)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepo(ns, "orders-api", "Initial commit\n")
		createWebshop(ns)
		// A Container stopped on foreign commits never polls, so a read of its
		// history is a reconcile something started.
		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		Expect(condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)).
			To(HaveField("Reason", c4v1beta1.ReasonForeignCommits))
		var gp c4v1beta1.GitProvider
		Expect(k8sClient.Get(ctx, key(ns, container.Spec.GitProviderRef.Name), &gp)).To(Succeed())
		secret := registeredSecret(&gp)
		base := gitEventsServer()
		reads := gitFake.HistoryCount(ns, "orders-api")
		Consistently(func() int { return gitFake.HistoryCount(ns, "orders-api") }, "2s").Should(Equal(reads))

		By("refusing a forged delivery with 401, reconciling nothing, and turning Events DeliveryRefused")
		body, header := fakeDelivery("not-the-secret", "orders-api", 0)
		Expect(postDelivery(base, ns, gp.Name, body, header)).To(Equal(http.StatusUnauthorized))
		_, unsigned := fakeDelivery(secret, "orders-api", 0)
		unsigned.Del(fake.SignatureHeader)
		Expect(postDelivery(base, ns, gp.Name, body, unsigned)).To(Equal(http.StatusUnauthorized))
		eventsStatus(&gp, metav1.ConditionFalse, c4v1beta1.ReasonEventsRefused)
		Consistently(func() int { return gitFake.HistoryCount(ns, "orders-api") }, "2s").Should(Equal(reads))

		By("accepting a signed one with 202, reconciling the Container, and turning Events Receiving")
		body, header = fakeDelivery(secret, "orders-api", 0)
		Expect(postDelivery(base, ns, gp.Name, body, header)).To(Equal(http.StatusAccepted))
		Eventually(func() int { return gitFake.HistoryCount(ns, "orders-api") }, "2s").
			Should(BeNumerically(">", reads), "the delivery did not reconcile the Container")
		eventsStatus(&gp, metav1.ConditionTrue, c4v1beta1.ReasonEventsReceiving)

		By("turning Events DeliveryRefused again on a forged one after it")
		body, header = fakeDelivery("not-the-secret", "orders-api", 0)
		Expect(postDelivery(base, ns, gp.Name, body, header)).To(Equal(http.StatusUnauthorized))
		eventsStatus(&gp, metav1.ConditionFalse, c4v1beta1.ReasonEventsRefused)
	})

	It("maps a delivery to the Brief whose repository it names, on its GitProvider only (#1221)", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		repo := model.BriefRepository("cel-lens")
		var gp c4v1beta1.GitProvider
		Expect(k8sClient.Get(ctx, key(ns, brief.Spec.GitProviderRef.Name), &gp)).To(Succeed())
		secret := registeredSecret(&gp)
		Eventually(func(g Gomega) {
			g.Expect(managerClient.Get(ctx, key(ns, "cel-lens"), &c4v1beta1.Brief{})).To(Succeed())
		}).Should(Succeed())

		// A Brief waiting for its planner Agent reads the host every second in
		// the suite, so its reconcile is seen through the map, not the host.
		at := func(provider, repo string) GitEvent {
			return GitEvent{Namespace: ns, Provider: provider, Event: ports.Event{Repository: repo, Number: 3}}
		}
		c := managerClient
		Expect(gitEventBriefRequest(c)(ctx, at(gp.Name, repo))).
			To(Equal([]reconcile.Request{requestFor(ns, "cel-lens")}))
		Expect(gitEventContainerRequest(c)(ctx, at(gp.Name, repo))).To(BeEmpty())
		Expect(gitEventBriefRequest(c)(ctx, at("elsewhere", repo))).To(BeEmpty())
		Expect(gitEventBriefRequest(c)(ctx, at(gp.Name, model.BriefRepository("")))).To(BeEmpty())

		body, header := fakeDelivery(secret, repo, 3)
		Expect(postDelivery(gitEventsServer(), ns, gp.Name, body, header)).To(Equal(http.StatusAccepted))
	})

	It("answers 404 for an unknown GitProvider or one without events, and 413 past 5 MiB (#1221)", func() {
		ns := newProjectNamespace()
		base := gitEventsServer()
		body, header := fakeDelivery("any", "orders-api", 1)
		Expect(postDelivery(base, ns, "missing", body, header)).To(Equal(http.StatusNotFound))

		gp := newEventsProvider(ns, noEventsOwnerPrefix+ns)
		eventsReason(gp, c4v1beta1.ReasonEventsUnsupported)
		Expect(postDelivery(base, ns, gp.Name, body, header)).To(Equal(http.StatusNotFound))

		large := []byte(`{"repository":"orders-api","pad":"` + strings.Repeat("x", maxGitEventBody) + `"}`)
		Expect(postDelivery(base, ns, gp.Name, large, header)).To(Equal(http.StatusRequestEntityTooLarge))
	})

	It("maps a delivery to the Container, its Crews and the Tasks it names, on its GitProvider only (#1221)", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		mustCreate(newSystem(ns, "webshop-core"), container, newCrew(ns))
		issue := newTask(ns, "issue-7", "")
		issue.Spec.Issue = 7
		other := newTask(ns, "issue-8", "")
		other.Spec.Issue = 8
		mustCreate(issue, other)
		// The cache sees the Tasks' issues before the maps read it.
		Eventually(func(g Gomega) {
			for _, n := range []string{"issue-7", "issue-8"} {
				var t c4v1beta1.Task
				g.Expect(managerClient.Get(ctx, key(ns, n), &t)).To(Succeed())
			}
			var crew c4v1beta1.Crew
			g.Expect(managerClient.Get(ctx, key(ns, "orders-api"), &crew)).To(Succeed())
		}).Should(Succeed())

		names := func(reqs []reconcile.Request) []string {
			out := make([]string, 0, len(reqs))
			for _, r := range reqs {
				Expect(r.Namespace).To(Equal(ns))
				out = append(out, r.Name)
			}
			return out
		}
		at := func(provider, repo string, number int) GitEvent {
			return GitEvent{Namespace: ns, Provider: provider,
				Event: ports.Event{Repository: repo, Number: number, Kind: fake.EventIssue}}
		}
		provider := container.Spec.GitProviderRef.Name
		c := managerClient
		Expect(names(gitEventContainerRequest(c)(ctx, at(provider, "orders-api", 7)))).To(Equal([]string{"orders-api"}))
		Expect(names(gitEventCrewRequests(c)(ctx, at(provider, "orders-api", 7)))).To(Equal([]string{"orders-api"}))
		Eventually(func() []string { return names(gitEventTaskRequests(c)(ctx, at(provider, "orders-api", 7))) }).
			Should(Equal([]string{"issue-7"}))

		By("enqueuing every unfinished Task of the Container for a delivery that names no number or branch")
		Expect(names(gitEventTaskRequests(c)(ctx, at(provider, "orders-api", 0)))).
			To(ConsistOf("issue-7", "issue-8"))

		By("enqueuing nothing for another GitProvider's delivery, or a repository no Container or Brief has")
		for _, e := range []GitEvent{at("elsewhere", "orders-api", 7), at(provider, "unknown", 7)} {
			Expect(gitEventContainerRequest(c)(ctx, e)).To(BeEmpty())
			Expect(gitEventCrewRequests(c)(ctx, e)).To(BeEmpty())
			Expect(gitEventTaskRequests(c)(ctx, e)).To(BeEmpty())
			Expect(gitEventBriefRequest(c)(ctx, e)).To(BeEmpty())
		}
	})

	It("stretches the polls that wait for the host to 10 minutes while deliveries arrive (#1222)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 2)
		mustCreate(newCrew(ns))
		var gp c4v1beta1.GitProvider
		Expect(k8sClient.Get(ctx, key(ns, "gitea"), &gp)).To(Succeed())
		secret := registeredSecret(&gp)
		var container c4v1beta1.Container
		Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())

		// Reconcilers with the default intervals, not the suite's second.
		crewR := &CrewReconciler{Client: managerClient, Scheme: scheme.Scheme, APIReader: secretReader{k8sClient},
			Git: fakeGitClient{gitFake}, Now: crewNow}
		taskR := &TaskReconciler{Client: managerClient, Now: taskNow}
		containerR := &ContainerReconciler{Client: managerClient, APIReader: k8sClient, Git: fakeGitClient{gitFake}}
		task := newTask(ns, "orders-api-1", "")
		wait := model.TaskStep{Action: model.ActionWait}
		// The Crew's requeue, and a Task's waits for checks and for a
		// Standard change and a Container's for its checks, in a reconcile
		// that read the GitProvider as it is now.
		polls := func(g Gomega) (crew, checks, standard, containerChecks time.Duration) {
			result, err := crewR.Reconcile(ctx, reconcile.Request{NamespacedName: key(ns, "orders-api")})
			g.Expect(err).NotTo(HaveOccurred())
			hostCtx := withHostEvents(ctx, managerClient, ns, gp.Name)
			checks = taskR.wait(hostCtx, task, &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskChecking}, wait).RequeueAfter
			standard = taskR.wait(hostCtx, task, &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskBlocked}, wait).RequeueAfter
			containerChecks = containerR.refreshChecks(hostCtx, &container, container.Status.DeepCopy()).RequeueAfter
			return result.RequeueAfter, checks, standard, containerChecks
		}

		By("polling at the usual intervals while Events is Registered")
		Eventually(func(g Gomega) {
			crew, checks, standard, containerChecks := polls(g)
			g.Expect(crew).To(Equal(time.Minute))
			g.Expect(checks).To(Equal(15 * time.Second))
			g.Expect(standard).To(Equal(time.Minute))
			g.Expect(containerChecks).To(Equal(time.Minute))
		}).Should(Succeed())

		By("waiting 10 minutes once a verified delivery turns Events Receiving")
		body, header := fakeDelivery(secret, "orders-api", 0)
		Expect(postDelivery(gitEventsServer(), ns, gp.Name, body, header)).To(Equal(http.StatusAccepted))
		eventsStatus(&gp, metav1.ConditionTrue, c4v1beta1.ReasonEventsReceiving)
		Eventually(func(g Gomega) {
			crew, checks, standard, containerChecks := polls(g)
			g.Expect(crew).To(Equal(10 * time.Minute))
			g.Expect(checks).To(Equal(10 * time.Minute))
			g.Expect(standard).To(Equal(10 * time.Minute))
			g.Expect(containerChecks).To(Equal(10 * time.Minute))
		}).Should(Succeed())

		By("polling as usual again once a forged delivery turns Events DeliveryRefused")
		body, header = fakeDelivery("not-the-secret", "orders-api", 0)
		Expect(postDelivery(gitEventsServer(), ns, gp.Name, body, header)).To(Equal(http.StatusUnauthorized))
		eventsStatus(&gp, metav1.ConditionFalse, c4v1beta1.ReasonEventsRefused)
		Eventually(func(g Gomega) {
			crew, checks, _, _ := polls(g)
			g.Expect(crew).To(Equal(time.Minute))
			g.Expect(checks).To(Equal(15 * time.Second))
		}).Should(Succeed())
	})
})
