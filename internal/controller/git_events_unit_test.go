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
	"slices"
	"testing"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// A Task is found by its repository, its issue, Standard-change issue and
// pull request, and its branches (docs/plans/done/git-events.md, decision 7).
func TestTaskRepositoryKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		task c4v1beta1.Task
		want []string
	}{
		{name: "a Task with nothing yet: its repository (#1221)",
			task: c4v1beta1.Task{Spec: c4v1beta1.TaskSpec{ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"}}},
			want: []string{"orders-api"}},
		{name: "a Task's issue, pull request, branch and Standard change (#1221)",
			task: c4v1beta1.Task{
				Spec: c4v1beta1.TaskSpec{ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"}, Issue: 7},
				Status: c4v1beta1.TaskStatus{PullRequest: 9, Branch: "theos/7-health",
					StandardChange: &c4v1beta1.TaskStandardChange{Issue: 11, Branch: "theos/7-standard"}}},
			want: []string{"orders-api", "orders-api#11", "orders-api#7", "orders-api#9",
				"orders-api@theos/7-health", "orders-api@theos/7-standard"}},
		{name: "a Plan Task: its Brief's repository (#1221)",
			task: c4v1beta1.Task{Spec: c4v1beta1.TaskSpec{BriefRef: &c4v1beta1.LocalRef{Name: "cel-lens"}},
				Status: c4v1beta1.TaskStatus{Branch: "plan/1"}},
			want: []string{"brief-cel-lens", "brief-cel-lens@plan/1"}},
		{name: "the same number twice is one key (#1221)",
			task: c4v1beta1.Task{Spec: c4v1beta1.TaskSpec{ContainerRef: c4v1beta1.LocalRef{Name: "a"}, Issue: 3},
				Status: c4v1beta1.TaskStatus{PullRequest: 3}},
			want: []string{"a", "a#3"}},
		{name: "a Task on neither: no keys (#1221)", task: c4v1beta1.Task{}, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskRepositoryKeys(&tc.task); !slices.Equal(got, tc.want) {
				t.Errorf("taskRepositoryKeys = %q, want %q", got, tc.want)
			}
		})
	}
}

// The receiver enqueues a GitProvider only when a delivery changes which of
// the last verified and the last refused came last.
func TestGitEventsRecordFlips(t *testing.T) {
	g := &GitEventsReceiver{}
	g.ProviderSource()
	ch := g.providers[0]
	key := requestFor("ns", "git").NamespacedName
	for i, step := range []struct {
		verified, enqueued, receiving bool
	}{
		{verified: true, enqueued: true, receiving: true},
		{verified: true, enqueued: false, receiving: true},
		{verified: false, enqueued: true, receiving: false},
		{verified: false, enqueued: false, receiving: false},
		{verified: true, enqueued: true, receiving: true},
	} {
		g.record(key, step.verified)
		enqueued := len(ch) > 0
		for len(ch) > 0 {
			<-ch
		}
		rec, known := g.deliveries(key)
		if enqueued != step.enqueued || !known || rec.receiving() != step.receiving {
			t.Errorf("step %d: enqueued %v, receiving %v; want %v, %v (#1221)", i, enqueued, rec.receiving(),
				step.enqueued, step.receiving)
		}
	}
	g.forgetDeliveries(key)
	if _, known := g.deliveries(key); known {
		t.Error("forgetDeliveries kept the record (#1221)")
	}
}
