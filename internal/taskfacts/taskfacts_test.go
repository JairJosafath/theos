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

package taskfacts

import (
	"go/build"
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// #142: the one reading of a Task that the Prometheus series and the report
// share. A Task ended with no change only when it is Merged and its Succeeded
// condition's reason is NothingToDo (#72): every other reason the operator
// writes there is a Task that ended done another way.
func TestEndedWithNoChange(t *testing.T) {
	succeeded := func(reason string) []metav1.Condition {
		return []metav1.Condition{
			{Type: "Progressing", Status: metav1.ConditionFalse, Reason: model.ReasonNothingToDo},
			{Type: ConditionSucceeded, Status: metav1.ConditionTrue, Reason: reason}}
	}
	for name, c := range map[string]struct {
		phase      c4v1beta1.TaskPhase
		conditions []metav1.Condition
		want       bool
	}{
		"nothing to do":                    {c4v1beta1.TaskMerged, succeeded(model.ReasonNothingToDo), true},
		"merged":                           {c4v1beta1.TaskMerged, succeeded("Merged"), false},
		"a plan accepted":                  {c4v1beta1.TaskMerged, succeeded("PlanAccepted"), false},
		"scored":                           {c4v1beta1.TaskMerged, succeeded("Scored"), false},
		"triaged":                          {c4v1beta1.TaskMerged, succeeded("Triaged"), false},
		"done by its Standard change":      {c4v1beta1.TaskMerged, succeeded("DoneByStandardChange"), false},
		"only Progressing says so":         {c4v1beta1.TaskMerged, succeeded("Merged")[:1], false},
		"no condition at all":              {c4v1beta1.TaskMerged, nil, false},
		"failed, with the reason":          {c4v1beta1.TaskFailed, succeeded(model.ReasonNothingToDo), false},
		"not ended yet, whatever it says":  {c4v1beta1.TaskImplementing, succeeded(model.ReasonNothingToDo), false},
		"no phase yet, and nothing was it": {"", nil, false},
	} {
		task := &c4v1beta1.Task{Status: c4v1beta1.TaskStatus{Phase: c.phase, Conditions: c.conditions}}
		if got := EndedWithNoChange(task); got != c.want {
			t.Errorf("%s: ended with no change = %v, want %v", name, got, c.want)
		}
		if got := Metric(task).NoChange; got != c.want {
			t.Errorf("%s: the facts say no change = %v, want %v", name, got, c.want)
		}
	}
}

// #142: every fact the numbers read, from a Task that says each of them.
func TestMetric(t *testing.T) {
	at := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	commits, cost := int32(2), int64(1250)
	task := &c4v1beta1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "checkout-1"},
		Spec: c4v1beta1.TaskSpec{Kind: c4v1beta1.TaskScore, ContainerRef: c4v1beta1.LocalRef{Name: "api"},
			Benchmark: &c4v1beta1.TaskBenchmarkSpec{}},
		Status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskMerged, Round: 2, CostMilliUSD: 1500, PersonCommits: &commits,
			Conditions: []metav1.Condition{{Type: ConditionSucceeded, Status: metav1.ConditionTrue, Reason: "Merged"}},
			History: []c4v1beta1.TaskTransition{{At: metav1.NewTime(at), Phase: c4v1beta1.TaskPending, Reason: "WaitingForSlot"},
				{At: metav1.NewTime(at.Add(time.Minute)), Phase: c4v1beta1.TaskMerged}},
			Runs: []c4v1beta1.TaskAgentRun{
				{Agent: "implementer", Harness: "claude-code", Model: "sonnet", Role: "implement", CostMilliUSD: &cost,
					Tokens: &c4v1beta1.TokenUsage{Input: 100, Output: 20, CacheRead: 7, CacheWrite: 3}},
				{Agent: "reviewer", Role: "review"}}},
	}
	two := 2
	want := model.TaskMetricFacts{Namespace: "shop", Container: "api", Kind: "Score", Benchmark: true, Phase: "Merged",
		Round: 2, PersonCommits: &two, CostMilliUSD: 1500,
		History: []model.TaskMetricStep{{At: at, Phase: "Pending", Reason: "WaitingForSlot"},
			{At: at.Add(time.Minute), Phase: "Merged"}},
		Runs: []model.TaskMetricRun{
			{Agent: "implementer", Harness: "claude-code", Model: "sonnet", Role: "implement", CostMilliUSD: &cost,
				Input: 100, Output: 20, CacheRead: 7, CacheWrite: 3},
			{Agent: "reviewer", Role: "review"}}}
	if got := Metric(task); !reflect.DeepEqual(got, want) {
		t.Errorf("read as\n%+v, want\n%+v", got, want)
	}

	bare := func(change func(*c4v1beta1.Task)) model.TaskMetricFacts {
		task := &c4v1beta1.Task{Status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskMerged}}
		change(task)
		return Metric(task)
	}
	if got := bare(func(*c4v1beta1.Task) {}); got.Kind != "Implement" || got.Benchmark || got.PersonCommits != nil ||
		got.ApprovedByPerson || got.Unreviewed || got.NoChange || got.History != nil || got.Runs != nil {
		t.Errorf("a Task that says no more reads as %+v", got)
	}
	for name, c := range map[string]struct {
		change func(*c4v1beta1.Task)
		holds  func(model.TaskMetricFacts) bool
	}{
		"no review asked and none made: unreviewed": {func(t *c4v1beta1.Task) {
			t.Status.Approval = &c4v1beta1.TaskApproval{Require: c4v1beta1.ApprovalNone}
		}, func(f model.TaskMetricFacts) bool { return f.Unreviewed && !f.ApprovedByPerson }},
		"no review asked, one made: reviewed": {func(t *c4v1beta1.Task) {
			t.Status.Approval = &c4v1beta1.TaskApproval{Require: c4v1beta1.ApprovalNone}
			t.Status.Review = &c4v1beta1.TaskReview{}
		}, func(f model.TaskMetricFacts) bool { return !f.Unreviewed }},
		"a person approved the held change": {func(t *c4v1beta1.Task) {
			t.Status.Approval = &c4v1beta1.TaskApproval{Require: c4v1beta1.ApprovalPerson, ApprovedBy: "ada"}
		}, func(f model.TaskMetricFacts) bool { return f.ApprovedByPerson && !f.Unreviewed }},
		"a person approved the spec": {func(t *c4v1beta1.Task) {
			t.Status.Spec = &c4v1beta1.TaskSpecResult{ApprovedBy: "ada"}
		}, func(f model.TaskMetricFacts) bool { return f.ApprovedByPerson }},
		"no person's commit is a count, of none": {func(t *c4v1beta1.Task) { t.Status.PersonCommits = new(int32) },
			func(f model.TaskMetricFacts) bool { return f.PersonCommits != nil && *f.PersonCommits == 0 }},
	} {
		if got := bare(c.change); !c.holds(got) {
			t.Errorf("%s: read as %+v", name, got)
		}
	}
}

// #142: the package is what a devcontainer's command and the operator both
// link (docs/architecture.md, "Layers"), so it stays the API's types,
// apimachinery and the business rules: nothing of controller-runtime or
// client-go, and no other package of the module.
func TestImports(t *testing.T) {
	const module = "github.com/JairJosafath/theos/"
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Imports) == 0 {
		t.Fatal("the package's imports were not read")
	}
	for _, path := range pkg.Imports {
		switch {
		case path == module+"api/v1beta1", path == module+"internal/model", strings.HasPrefix(path, "k8s.io/apimachinery/"):
		case strings.HasPrefix(path, module), strings.Contains(path, "k8s.io/"):
			t.Errorf("internal/taskfacts imports %s: it reads the API's Task into internal/model's values, and "+
				"a command links it without the operator (docs/architecture.md)", path)
		}
	}
}
