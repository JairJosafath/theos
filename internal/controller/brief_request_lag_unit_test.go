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
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// lagTeam is the Linear team of the source the lag tests read.
const lagTeam = "ENG"

// lagRecheck is the suggestions recheck of the lag tests' reconcilers.
const lagRecheck = time.Minute

// lagWorld is a request source on a fake tracker whose search lags, and a
// Brief that reads it.
type lagWorld struct {
	t       *testing.T
	tracker *fake.IssueTracker
	port    ports.IssueTracker
	brief   *c4v1beta1.Brief
	secret  *corev1.Secret
}

func newLagWorld(t *testing.T, mode string) lagWorld {
	t.Helper()
	tr := fake.NewIssueTracker()
	tr.Project(lagTeam)
	token := tr.Account(trackerLogin, fake.TrackerAccess{Issues: true})
	src := linearSource("product", lagTeam)
	src.Mode = mode
	brief := &c4v1beta1.Brief{ObjectMeta: objectMeta("apps", "cel-lens"),
		Spec: c4v1beta1.BriefSpec{RequestSources: []c4v1beta1.BriefRequestSource{src}}}
	return lagWorld{t: t, tracker: tr, port: tr.For(lagTeam, token), brief: brief,
		secret: &corev1.Secret{ObjectMeta: objectMeta("apps", src.CredentialsSecretRef.Name),
			Data: map[string][]byte{fake.CredentialKey: []byte(token)}}}
}

// reconciler is a reconciler of the operator as it starts: it has read
// nothing yet.
func (w lagWorld) reconciler() *BriefReconciler {
	c := crfake.NewClientBuilder().WithScheme(briefScheme(w.t)).WithObjects(w.secret).Build()
	return &BriefReconciler{Client: c, APIReader: c, SuggestionsRecheck: lagRecheck,
		Issues: ports.IssueTrackers{model.TrackerLinear: w.tracker}}
}

// file files a labelled item and returns its number.
func (w lagWorld) file(title string) int {
	w.t.Helper()
	n, err := w.tracker.File(lagTeam, "ada", title, "Please.")
	if err != nil {
		w.t.Fatal(err)
	}
	return n
}

// read is one read of the source by r at now, with known the requests the
// Brief's status keeps; it returns the requests by number.
func (w lagWorld) read(r *BriefReconciler, known []model.Request, now time.Time) (map[int]model.Request,
	*requestsFailure) {
	w.t.Helper()
	r.Now = func() time.Time { return now }
	origins := requestOrigins(w.brief)
	if len(origins) != 1 {
		w.t.Fatalf("the Brief reads %d origins, want the source", len(origins))
	}
	got, fail := r.readOrigin(context.Background(), w.brief, origins[0], known, map[string][]int32{}, now)
	out := map[int]model.Request{}
	for _, req := range got {
		out[req.Number] = req
	}
	return out, fail
}

// TestAClosedItemTheSearchStillListsIsNoNewRequest covers #161: after the
// Brief closed an item, a search that lags (Jira's index, for minutes and
// longer) still lists it. It must not come back as a new request, which On,
// or Ask with its number still accepted, lets through into a second planning
// round: not after a restart, which forgets what the Brief closed, and not
// after a lag longer than the recheck.
func TestAClosedItemTheSearchStillListsIsNoNewRequest(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		mode     string
		accepted bool
	}{
		{"On (#161)", model.RequestsOn, false},
		{"Ask with the number still accepted (#161)", model.RequestsAsk, true},
	} {
		t.Run(tc.name+": after a restart", func(t *testing.T) {
			w := newLagWorld(t, tc.mode)
			closed, open := w.file("Skipped"), w.file("New")
			if tc.accepted {
				w.brief.Spec.RequestSources[0].Accepted = []int32{int32(closed), int32(open)}
			}
			w.tracker.Lag(lagTeam, true)
			if err := w.port.CloseIssue(context.Background(), closed, ports.CloseNotPlanned); err != nil {
				t.Fatal(err)
			}
			got, fail := w.read(w.reconciler(), nil, t0)
			if fail != nil {
				t.Fatalf("the read failed: %+v", fail)
			}
			if req, ok := got[closed]; ok {
				t.Errorf("the closed item the search still lists is a request again: %+v", req)
			}
			if got[open].Stage != model.RequestAccepted {
				t.Errorf("the open item is %+v, want it a request let through", got[open])
			}
		})

		t.Run(tc.name+": after a lag longer than the recheck", func(t *testing.T) {
			w := newLagWorld(t, tc.mode)
			n := w.file("Skipped")
			if tc.accepted {
				w.brief.Spec.RequestSources[0].Accepted = []int32{int32(n)}
			}
			w.tracker.Lag(lagTeam, true)
			r := w.reconciler()
			skipped := model.Request{Source: "product", Key: "REQ-1", Number: n, Title: "Skipped",
				Stage: model.RequestSkipped, Round: 2, Why: "No.", Since: t0}
			got, fail := w.read(r, []model.Request{skipped}, t0)
			if fail != nil || len(got) != 0 {
				t.Fatalf("answering the skipped request = %v, %+v; want it closed and gone", got, fail)
			}
			if issue, _ := w.tracker.Issue(lagTeam, n); !issue.Closed || len(issue.Comments) != 1 {
				t.Fatalf("the skipped item is %+v, want it closed with one comment", issue)
			}
			for _, later := range []time.Duration{time.Second, 2 * lagRecheck, 2*lagRecheck + time.Second,
				10 * lagRecheck} {
				got, fail := w.read(r, nil, t0.Add(later))
				if fail != nil {
					t.Fatalf("%s later: the read failed: %+v", later, fail)
				}
				if req, ok := got[n]; ok {
					t.Errorf("%s later: the item the Brief closed is a request again: %+v", later, req)
				}
			}
			if reads := w.tracker.Calls("IssueOpen"); reads != 2 {
				t.Errorf("the item was read on its own %d times, want twice: once per recheck at most", reads)
			}
			if issue, _ := w.tracker.Issue(lagTeam, n); len(issue.Comments) != 1 {
				t.Errorf("the item has %d comments, want the one", len(issue.Comments))
			}
		})
	}
}

// TestACloseTheWorkflowRefusesWaitsForTheRecheck covers #161: a skipped
// request whose close the tracker's workflow refuses (CannotClose) is not
// tried again, nor its source read again, on every reconcile, but once per
// suggestions recheck; the Requests condition says why all along.
func TestACloseTheWorkflowRefusesWaitsForTheRecheck(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	w := newLagWorld(t, model.RequestsAsk)
	n := w.file("Skipped")
	w.tracker.CannotClose(lagTeam, `the team has no state of type "canceled"`)
	r := w.reconciler()
	skipped := model.Request{Source: "product", Key: "REQ-1", Number: n, Title: "Skipped",
		Stage: model.RequestSkipped, Round: 2, Why: "No.", Since: t0}
	calls := func() [3]int {
		return [3]int{w.tracker.Calls("ListOpenIssues"), w.tracker.Calls("FindComment"),
			w.tracker.Calls("CloseIssue")}
	}
	got, fail := w.read(r, []model.Request{skipped}, t0)
	if fail == nil || fail.reason != ports.ReasonCannotClose || got[n].Stage != model.RequestSkipped {
		t.Fatalf("the first answer = %v, %+v; want the request kept and CannotClose", got, fail)
	}
	first := calls()
	for _, later := range []time.Duration{time.Second, lagRecheck / 2} {
		got, fail = w.read(r, []model.Request{skipped}, t0.Add(later))
		if fail == nil || fail.reason != ports.ReasonCannotClose || got[n].Stage != model.RequestSkipped {
			t.Errorf("%s later: %v, %+v; want the request kept and CannotClose", later, got, fail)
		}
		if now := calls(); now != first {
			t.Errorf("%s later: lists, comment lookups and closes went from %v to %v, want none", later, first, now)
		}
	}
	w.tracker.CannotClose(lagTeam, "")
	got, fail = w.read(r, []model.Request{skipped}, t0.Add(lagRecheck))
	if fail != nil || len(got) != 0 {
		t.Errorf("a recheck later = %v, %+v; want the item closed and gone", got, fail)
	}
	if issue, _ := w.tracker.Issue(lagTeam, n); !issue.Closed || len(issue.Comments) != 1 {
		t.Errorf("the item is %+v, want it closed with its one comment", issue)
	}
}

// failingReads is the fake tracker as an adapter whose reads of one item
// fail, once the fake counted them: fail holds the reason by item number,
// 0 for every item.
type failingReads struct {
	adapter ports.TrackerAdapter
	fail    map[int]string
}

func (f failingReads) Tracker(ctx context.Context, source ports.TrackerSource,
	secret map[string][]byte) (ports.IssueTracker, error) {
	tracker, err := f.adapter.Tracker(ctx, source, secret)
	if err != nil {
		return nil, err
	}
	return failingReader{IssueTracker: tracker, reader: tracker.(ports.IssueReader), fail: f.fail}, nil
}

type failingReader struct {
	ports.IssueTracker
	reader ports.IssueReader
	fail   map[int]string
}

func (f failingReader) IssueOpen(ctx context.Context, number int) (bool, error) {
	open, err := f.reader.IssueOpen(ctx, number)
	reason, failing := f.fail[number]
	if !failing {
		reason, failing = f.fail[0]
	}
	if err != nil || !failing {
		return open, err
	}
	return false, &ports.IssuesError{Reason: reason, Message: fmt.Sprintf("reading item %d failed", number)}
}

// TestAFailedReadOfAnItemWaitsForTheRecheck covers #245: a read of one item
// that fails is kept until the suggestions recheck, as one that worked is,
// so the item isn't read again on every reconcile (twice in a reconcile that
// reads requests again after the round), and the Requests condition says
// why all along. A rate limit ends the pass's reads of items: the rest wait
// for the recheck with it.
func TestAFailedReadOfAnItemWaitsForTheRecheck(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		// failing is the item whose reads fail, by its index; -1 for all.
		failing int
		reason  string
		// first are the reads of items the first pass makes, and listed
		// the items it lets through, by index.
		first  int
		listed []int
	}{
		{"a rate limit ends the pass's reads (#245)", -1, ports.ReasonTrackerUnavailable, 1, nil},
		{"a read of one item that fails waits alone (#245)", 1, ports.ReasonTrackerError, 3, []int{0, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newLagWorld(t, model.RequestsOn)
			items := []int{w.file("One"), w.file("Two"), w.file("Three")}
			refuse := map[int]string{0: tc.reason}
			if tc.failing >= 0 {
				refuse = map[int]string{items[tc.failing]: tc.reason}
			}
			r := w.reconciler()
			r.Issues = ports.IssueTrackers{model.TrackerLinear: failingReads{adapter: w.tracker, fail: refuse}}
			want := map[int]bool{}
			for _, i := range tc.listed {
				want[items[i]] = true
			}
			for _, later := range []time.Duration{0, time.Second, lagRecheck / 2} {
				got, fail := w.read(r, nil, t0.Add(later))
				if fail == nil || fail.reason != tc.reason {
					t.Errorf("%s later: the failure is %+v, want %s", later, fail, tc.reason)
				}
				if len(got) != len(want) {
					t.Errorf("%s later: the requests are %v, want %v", later, got, tc.listed)
				}
				for n := range want {
					if _, ok := got[n]; !ok {
						t.Errorf("%s later: item %d is no request, want it one", later, n)
					}
				}
				if reads := w.tracker.Calls("IssueOpen"); reads != tc.first {
					t.Errorf("%s later: the items were read on their own %d times, want %d", later, reads, tc.first)
				}
			}
			clear(refuse)
			got, failure := w.read(r, nil, t0.Add(lagRecheck))
			if failure != nil || len(got) != len(items) {
				t.Errorf("a recheck later = %v, %+v; want the three items requests", got, failure)
			}
		})
	}
}

// TestAHeldCloseGoesWithItsRequest covers #245: a declined request whose
// close the workflow refused, and whose label is taken off, leaves the list,
// and its held close goes with it. Labelled again, and declined again, it is
// closed on the next read, not left until the old refusal's recheck.
func TestAHeldCloseGoesWithItsRequest(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	w := newLagWorld(t, model.RequestsAsk)
	src := &w.brief.Spec.RequestSources[0]
	a, b, c := w.file("Unlabelled"), w.file("Declined"), w.file("Declined later")
	src.Declined = []int32{int32(a)}
	w.tracker.CannotClose(lagTeam, `the team has no state of type "canceled"`)
	r := w.reconciler()
	values := func(got map[int]model.Request) []model.Request {
		out := make([]model.Request, 0, len(got))
		for _, req := range got {
			out = append(out, req)
		}
		return out
	}

	got, fail := w.read(r, nil, t0)
	if fail == nil || fail.reason != ports.ReasonCannotClose || got[a].Stage != model.RequestDeclined {
		t.Fatalf("declining %d = %v, %+v; want it kept and CannotClose", a, got, fail)
	}

	// The label goes while the close is held, and the workflow is fixed;
	// declining b reads the list again.
	if err := w.tracker.Unlabel(lagTeam, a); err != nil {
		t.Fatal(err)
	}
	w.tracker.CannotClose(lagTeam, "")
	src.Declined = []int32{int32(a), int32(b)}
	got, fail = w.read(r, values(got), t0.Add(10*time.Second))
	if _, ok := got[a]; ok || fail != nil {
		t.Fatalf("after the label went = %v, %+v; want %d gone and no failure", got, fail, a)
	}

	// The label is back, and declining c reads the list again.
	if err := w.tracker.Label(lagTeam, a); err != nil {
		t.Fatal(err)
	}
	src.Declined = []int32{int32(a), int32(b), int32(c)}
	got, fail = w.read(r, values(got), t0.Add(20*time.Second))
	if fail != nil || len(got) != 0 {
		t.Errorf("declining it again = %v, %+v; want every item closed and gone", got, fail)
	}
	if issue, _ := w.tracker.Issue(lagTeam, a); !issue.Closed || len(issue.Comments) != 1 {
		t.Errorf("item %d is %+v, want it closed with its one comment", a, issue)
	}
}
