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
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	portsfake "github.com/JairJosafath/theos/internal/ports/fake"
)

// alertDrainUnit is the AlertSource shop/alerts with a daily limit of one,
// checked by a token, and the Container orders-api on a fake Git host, in a
// fake API server: a drain with no poll beside it.
type alertDrainUnit struct {
	t     *testing.T
	c     client.Client
	git   *portsfake.Git
	r     *AlertSourceReconciler
	token string
	mu    sync.Mutex
	now   func() time.Time
}

func newAlertDrainUnit(t *testing.T) *alertDrainUnit {
	t.Helper()
	u := &alertDrainUnit{t: t, token: rand.Text(), git: portsfake.New()}
	src := &c4v1beta1.AlertSource{ObjectMeta: objectMeta("shop", "alerts"),
		Spec: c4v1beta1.AlertSourceSpec{Type: c4v1beta1.AlertSourceGeneric, DailyLimit: 1,
			SecretRef: c4v1beta1.LocalRef{Name: "alerts-webhook"}}}
	u.c = fake.NewClientBuilder().WithScheme(briefScheme(t)).WithStatusSubresource(&c4v1beta1.AlertSource{}).
		WithObjects(src,
			&corev1.Secret{ObjectMeta: objectMeta("shop", "alerts-webhook"),
				Data: map[string][]byte{model.AlertSecretKeyToken: []byte(u.token)}},
			&c4v1beta1.Container{ObjectMeta: objectMeta("shop", "orders-api"),
				Spec: c4v1beta1.ContainerSpec{GitProviderRef: c4v1beta1.LocalRef{Name: "git"}}},
			&corev1.Secret{ObjectMeta: objectMeta("shop", "git-credentials"),
				Data: map[string][]byte{"username": []byte("theos"), "token": []byte("t")}},
			&c4v1beta1.GitProvider{ObjectMeta: objectMeta("shop", "git"), Spec: c4v1beta1.GitProviderSpec{
				Provider: c4v1beta1.GitProviderKind("github"), URL: "https://github.example", Owner: "shop",
				CredentialsSecretRef: c4v1beta1.LocalRef{Name: "git-credentials"}}}).
		Build()
	receiver := &AlertReceiver{Client: u.c, APIReader: u.c, Now: u.clock}
	u.r = &AlertSourceReconciler{Client: u.c, Scheme: u.c.Scheme(), APIReader: u.c, Git: fakeGitClient{u.git},
		Receiver: receiver, Now: u.clock}
	return u
}

func (u *alertDrainUnit) clock() time.Time {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.now()
}

// clockAt sets the clock to at.
func (u *alertDrainUnit) clockAt(at time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.now = func() time.Time { return at }
}

// fire puts a firing alert titled title for orders-api in the inbox.
func (u *alertDrainUnit) fire(title string) {
	u.t.Helper()
	if got := postAlert(u.r.Receiver.Handler(), model.AlertsURL("", "shop", "alerts"), u.token,
		`{"title":"`+title+`","container":"orders-api"}`); got != http.StatusAccepted {
		u.t.Fatalf("delivering %q: status = %d, want 202", title, got)
	}
}

func (u *alertDrainUnit) drain() error {
	_, err := u.r.drain(context.Background(), alertUnitKey)
	return err
}

func (u *alertDrainUnit) mustDrain() {
	u.t.Helper()
	if err := u.drain(); err != nil {
		u.t.Fatal(err)
	}
}

// issues are the alert issues of orders-api.
func (u *alertDrainUnit) issues() []portsfake.Issue {
	var out []portsfake.Issue
	for _, i := range u.git.AllIssues("shop", "orders-api") {
		if strings.HasPrefix(i.Title, "Alert: ") {
			out = append(out, i)
		}
	}
	return out
}

func (u *alertDrainUnit) status() c4v1beta1.AlertSourceStatus {
	u.t.Helper()
	var src c4v1beta1.AlertSource
	if err := u.c.Get(context.Background(), alertUnitKey, &src); err != nil {
		u.t.Fatal(err)
	}
	return src.Status
}

// countSaid is how many of comments contain said.
func countSaid(comments []string, said string) int {
	n := 0
	for _, c := range comments {
		if strings.Contains(c, said) {
			n++
		}
	}
	return n
}

// Yesterday's fold is said by the step of today's first alert, before it
// files, and once: a step that fails after the fold was said still writes
// the settled day, so the next pass doesn't say it again (#64, decision 11).
// No poll runs beside this drain, so only take's own settle can say it.
func TestAlertDrainSaysYesterdaysFoldOnceAcrossAFailedStep(t *testing.T) {
	u := newAlertDrainUnit(t)
	day0 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	u.clockAt(day0)
	u.fire("Disk full")
	u.mustDrain()
	if got := u.issues(); len(got) != 1 {
		t.Fatalf("alert issues = %d, want 1", len(got))
	}
	first := u.issues()[0].Number
	u.fire("Queue backed up") // folded, and said at once
	u.mustDrain()
	u.fire("Latency high") // folded, unsaid within the interval
	u.mustDrain()
	yesterday := "And 2 more alerts fired for this Container on 2026-03-01"
	if got := countSaid(u.git.Issue("shop", "orders-api", first).Comments, yesterday); got != 0 {
		t.Fatalf("%q said %d times on the day, want 0", yesterday, got)
	}

	u.clockAt(day0.AddDate(0, 0, 1))
	u.git.FailListIssues("shop", "orders-api", errors.New("the Git host is down"))
	u.fire("Cache cold")
	if err := u.drain(); err == nil {
		t.Fatal("the drain filed while the Git host failed")
	}
	if got := countSaid(u.git.Issue("shop", "orders-api", first).Comments, yesterday); got != 1 {
		t.Fatalf("%q said %d times by the failed step, want 1", yesterday, got)
	}
	if days := u.status().Containers; len(days) != 1 || days[0].FoldReported != 2 {
		t.Errorf("days = %+v, want yesterday's fold written as said", days)
	}

	u.git.FailListIssues("shop", "orders-api", nil)
	u.mustDrain()
	if got := countSaid(u.git.Issue("shop", "orders-api", first).Comments, yesterday); got != 1 {
		t.Errorf("%q said %d times once the host answered, want 1", yesterday, got)
	}
	if got := u.issues(); len(got) != 2 || got[1].Title != "Alert: Cache cold" {
		t.Errorf("alert issues = %+v, want Cache cold filed as today's first", got)
	}
	days := u.status().Containers
	if len(days) != 1 || days[0].Date != "2026-03-02" || days[0].Filed != 1 || days[0].Folded != 0 {
		t.Errorf("days = %+v, want 2026-03-02 with Cache cold filed", days)
	}
}

// The clock is read for each alert a drain takes: one taken after midnight
// counts on its own day, not on the day the drain began (#64).
func TestAlertDrainCountsAnAlertTakenAfterMidnightOnItsDay(t *testing.T) {
	u := newAlertDrainUnit(t)
	late := time.Date(2026, 3, 1, 23, 59, 59, 0, time.UTC)
	next := late.Add(2 * time.Second)
	// Midnight passes while the drain files the first alert.
	u.mu.Lock()
	u.now = func() time.Time {
		if len(u.git.AllIssues("shop", "orders-api")) == 0 {
			return late
		}
		return next
	}
	u.mu.Unlock()
	u.fire("Disk full")
	u.fire("Queue backed up")
	u.mustDrain()

	if got := u.issues(); len(got) != 2 {
		t.Fatalf("alert issues = %+v, want the second filed on the next day, not folded on the first", got)
	}
	days := u.status().Containers
	if len(days) != 1 || days[0].Date != "2026-03-02" || days[0].Filed != 1 || days[0].Folded != 0 {
		t.Errorf("days = %+v, want 2026-03-02 with one filed", days)
	}
}
