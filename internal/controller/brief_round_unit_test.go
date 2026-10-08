package controller

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

func briefScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := c4v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

// A reconcile on a stale copy of the Brief must not start a round, nor write
// the stale status over the newer one.
func TestStartRoundOnAStaleBriefWritesNothing(t *testing.T) {
	scheme := briefScheme(t)
	brief := &c4v1alpha1.Brief{ObjectMeta: objectMeta("apps", "cel-lens"),
		Spec: c4v1alpha1.BriefSpec{Idea: "A linter.", Agents: c4v1alpha1.BriefAgents{
			Planner: c4v1alpha1.LocalRef{Name: "planner"}}}}
	statusWrites := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1alpha1.Brief{}).
		WithObjects(brief,
			&c4v1alpha1.Agent{ObjectMeta: objectMeta("apps", "planner")},
			&c4v1alpha1.Framework{ObjectMeta: objectMeta("apps", "python-fastapi")}).
		WithInterceptorFuncs(interceptor.Funcs{SubResourcePatch: func(ctx context.Context, c client.Client,
			sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			statusWrites++
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		}}).Build()
	r := &BriefReconciler{Client: c, Scheme: scheme}
	ctx := context.Background()

	var stale c4v1alpha1.Brief
	if err := c.Get(ctx, client.ObjectKeyFromObject(brief), &stale); err != nil {
		t.Fatal(err)
	}
	var current c4v1alpha1.Brief
	if err := c.Get(ctx, client.ObjectKeyFromObject(brief), &current); err != nil {
		t.Fatal(err)
	}
	current.Status.Round, current.Status.RoundPrompts = 1, 0
	if err := c.Status().Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
	statusWrites = 0

	orig := stale.DeepCopy()
	err := r.startRound(ctx, &stale, orig, false)
	if !errors.Is(err, errStaleBrief) {
		t.Fatalf("startRound on a stale Brief = %v, want errStaleBrief", err)
	}
	result, err := r.finishReconcile(ctx, &stale, orig, ctrl.Result{}, err)
	if err != nil || result.RequeueAfter == 0 {
		t.Errorf("finishReconcile = %+v, %v; want a requeue and no error", result, err)
	}
	if statusWrites != 1 {
		t.Errorf("status writes = %d, want only startRound's refused one", statusWrites)
	}
	var tasks c4v1alpha1.TaskList
	if err := c.List(ctx, &tasks); err != nil || len(tasks.Items) != 0 {
		t.Errorf("Tasks = %d (%v), want none", len(tasks.Items), err)
	}
}

// The operator's own wait shows over the planner's question; once it ends,
// the question is open again until a new round answers it.
func TestClearOperatorNeedsInputRestoresThePlannersQuestion(t *testing.T) {
	brief := &c4v1alpha1.Brief{}
	brief.Status.Round = 2
	brief.Status.History = []c4v1alpha1.BriefRound{
		{Round: 1, Outcome: model.ReasonApplied},
		{Round: 2, Outcome: model.ReasonPlannerNeedsInput, NeedsInput: "Which CRDs first?"},
	}
	operatorNeedsInput(brief, "GitProvider gitea is missing")
	clearOperatorNeedsInput(brief)
	cond := meta.FindStatusCondition(brief.Status.Conditions, c4v1alpha1.ConditionNeedsInput)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != model.ReasonPlannerNeedsInput ||
		cond.Message != "Which CRDs first?" {
		t.Errorf("NeedsInput = %+v, want the planner's question again", cond)
	}

	brief.Status.Round = 3 // a new round runs
	operatorNeedsInput(brief, "Agent planner is missing")
	clearOperatorNeedsInput(brief)
	cond = meta.FindStatusCondition(brief.Status.Conditions, c4v1alpha1.ConditionNeedsInput)
	if cond.Status != metav1.ConditionFalse || cond.Reason != c4v1alpha1.ReasonResolved {
		t.Errorf("NeedsInput = %+v, want resolved once a new round runs", cond)
	}
}

// #540: a reconcile on a copy of the Brief from before its round was applied
// must not fail the round again, over the newer status that says it applied.
func TestEndingARoundOnAStaleBriefWritesNothing(t *testing.T) {
	scheme := briefScheme(t)
	brief := &c4v1alpha1.Brief{ObjectMeta: objectMeta("apps", "cel-lens"),
		Spec: c4v1alpha1.BriefSpec{Idea: "A linter."}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1alpha1.Brief{}).
		WithObjects(brief).Build()
	r := &BriefReconciler{Client: c, Scheme: scheme}
	ctx := context.Background()

	var stale c4v1alpha1.Brief
	if err := c.Get(ctx, client.ObjectKeyFromObject(brief), &stale); err != nil {
		t.Fatal(err)
	}
	stale.Status.Round = 1
	if err := c.Status().Update(ctx, &stale); err != nil {
		t.Fatal(err)
	}
	var current c4v1alpha1.Brief
	if err := c.Get(ctx, client.ObjectKeyFromObject(brief), &current); err != nil {
		t.Fatal(err)
	}
	current.Status.History = []c4v1alpha1.BriefRound{{Round: 1, Outcome: model.ReasonApplied}}
	setCondition(&current.Status.Conditions, current.Generation, c4v1alpha1.ConditionPlanned,
		metav1.ConditionTrue, model.ReasonApplied, "Round 1 applied")
	if err := c.Status().Update(ctx, &current); err != nil {
		t.Fatal(err)
	}

	orig := stale.DeepCopy()
	r.endRoundFailed(ctx, &stale, &c4v1alpha1.Task{}, "the plan no longer fits the namespace: "+
		model.RefusalText([]model.Refusal{{Subject: "Container/orders-api", Reason: "is in the namespace"}}))
	result, err := r.finishReconcile(ctx, &stale, orig, ctrl.Result{}, nil)
	if err != nil || result.RequeueAfter == 0 {
		t.Errorf("finishReconcile = %+v, %v; want a requeue and no error", result, err)
	}
	var got c4v1alpha1.Brief
	if err := c.Get(ctx, client.ObjectKeyFromObject(brief), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.History) != 1 || got.Status.History[0].Outcome != model.ReasonApplied {
		t.Errorf("history = %+v, want round 1 applied", got.Status.History)
	}
	if cond := meta.FindStatusCondition(got.Status.Conditions, c4v1alpha1.ConditionPlanned); cond == nil ||
		cond.Reason != model.ReasonApplied {
		t.Errorf("Planned = %+v, want reason %s", cond, model.ReasonApplied)
	}
}

// A Brief round takes what its Plan Task used, and the Brief adds its rounds up.
func TestRecordRoundAddsUpTheTokens(t *testing.T) {
	status := &c4v1alpha1.BriefStatus{Round: 1}
	plan := func(tokens *c4v1alpha1.TokenUsage) *c4v1alpha1.Task {
		return &c4v1alpha1.Task{Status: c4v1alpha1.TaskStatus{Tokens: tokens}}
	}
	recordRound(status, plan(&c4v1alpha1.TokenUsage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4,
		LargestSession: 120000, RunsWithout: 1}), c4v1alpha1.BriefRound{Outcome: model.ReasonApplied})
	status.Round = 2
	recordRound(status, plan(&c4v1alpha1.TokenUsage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40,
		LargestSession: 80000}), c4v1alpha1.BriefRound{Outcome: model.ReasonApplied})
	status.Round = 3
	recordRound(status, plan(nil), c4v1alpha1.BriefRound{Outcome: model.ReasonApplied})

	want := c4v1alpha1.TokenUsage{Input: 11, Output: 22, CacheRead: 33, CacheWrite: 44, LargestSession: 120000,
		RunsWithout: 1}
	if status.Tokens == nil || *status.Tokens != want {
		t.Errorf("the Brief's tokens = %+v, want %+v", status.Tokens, want)
	}
	if got := status.History[1].Tokens; got == nil || got.LargestSession != 80000 || got.Input != 10 {
		t.Errorf("round 2's tokens = %+v, want its Plan Task's", got)
	}
	if got := status.History[2].Tokens; got != nil {
		t.Errorf("round 3's tokens = %+v, want none: its Plan Task ran no agent", got)
	}
}
