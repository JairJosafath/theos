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
	"errors"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	portsfake "github.com/JairJosafath/theos/internal/ports/fake"
)

func briefScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := c4v1beta1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

// missingAgentSecret says where to give a shared-token Agent's Secret. With
// the model proxy (#1136) the source in the operator's namespace is enough
// and the project namespace needs only the label; without it, the project
// namespace needs a copy, and the message says the copy is on its way.
func TestMissingAgentSecretSaysWhereTheTokenGoes(t *testing.T) {
	const (
		ns     = "shop"
		source = "theos-system"
		secret = "claude-credentials"
		proxy  = "http://c4-model-proxy.theos-system.svc:8080"
	)
	tests := []struct {
		name          string
		proxied       bool
		shared        bool
		labeled       bool
		sourcePresent bool
		want          string // "" wants nil
	}{
		{
			name: "a Secret the operator doesn't share is made in the project namespace",
			want: "Secret claude-credentials of Agent coder is missing: make it in namespace shop with the " +
				"Agents' token, so the Brief's planner can start",
		},
		{
			name: "a Secret the operator doesn't share, with the proxy set", proxied: true,
			want: "Secret claude-credentials of Agent coder is missing: make it in namespace shop with the " +
				"Agents' token, so the Brief's planner can start",
		},
		{
			name: "no label: label the namespace or make the Secret", shared: true,
			want: "Secret claude-credentials of Agent coder is missing: label namespace shop " +
				"theos.factory.io/shared-secrets=true, so the factory shares it from namespace theos-system, " +
				"or make it in shop for this project only",
		},
		{
			name: "no label with the proxy: label the namespace", shared: true, proxied: true,
			want: "Agent coder is on the factory's shared Secret claude-credentials: label namespace shop " +
				"theos.factory.io/shared-secrets=true, so its agents reach the factory's model proxy",
		},
		{
			name: "label and no source: make the Secret in the operator's namespace", shared: true, labeled: true,
			want: "Secret claude-credentials of Agent coder is missing: make it in namespace theos-system, " +
				"which the factory shares with every project, or in shop for this project only",
		},
		{
			name: "label and no source with the proxy: make the Secret the proxy uses", shared: true, labeled: true,
			proxied: true,
			want: "Secret claude-credentials of Agent coder is missing: make it in namespace theos-system " +
				"with the Agents' token, which the factory's model proxy uses for every project",
		},
		{
			name: "label and source: the copy is on its way", shared: true, labeled: true, sourcePresent: true,
			want: "Secret claude-credentials of Agent coder is not in namespace shop yet: the factory is " +
				"copying it from namespace theos-system, so the Brief's planner can start",
		},
		{
			name: "label and source with the proxy: nothing is missing", shared: true, labeled: true,
			sourcePresent: true, proxied: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
			if tt.labeled {
				namespace.Labels = map[string]string{c4v1beta1.LabelSharedSecrets: sharedSecretsOptIn}
			}
			objs := []client.Object{namespace}
			if tt.sourcePresent {
				objs = append(objs, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: source, Name: secret}})
			}
			r := &BriefReconciler{
				APIReader: fake.NewClientBuilder().WithScheme(briefScheme(t)).WithObjects(objs...).Build(),
			}
			if tt.shared {
				r.SharedSecrets = []string{secret}
				r.SourceNamespace = source
			}
			if tt.proxied {
				r.ModelProxyURL = proxy
			}
			err := r.missingAgentSecret(context.Background(), ns, secret, "coder", "planner")
			if tt.want == "" {
				if err != nil {
					t.Fatalf("missingAgentSecret = %v, want nil", err)
				}
				return
			}
			var needs errNeedsInput
			if !errors.As(err, &needs) {
				t.Fatalf("missingAgentSecret = %v, want errNeedsInput", err)
			}
			if needs.Error() != tt.want {
				t.Errorf("missingAgentSecret said\n  %s\nwant\n  %s", needs.Error(), tt.want)
			}
		})
	}
}

// A reconcile on a stale copy of the Brief must not start a round, nor write
// the stale status over the newer one.
func TestStartRoundOnAStaleBriefWritesNothing(t *testing.T) {
	scheme := briefScheme(t)
	brief := &c4v1beta1.Brief{ObjectMeta: objectMeta("apps", "cel-lens"),
		Spec: c4v1beta1.BriefSpec{Idea: "A linter.", Agents: c4v1beta1.BriefAgents{
			Planner: c4v1beta1.LocalRef{Name: "planner"}}}}
	statusWrites := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1beta1.Brief{}).
		WithObjects(brief,
			&c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "planner")},
			&c4v1beta1.Framework{ObjectMeta: objectMeta("apps", "python-fastapi")}).
		WithInterceptorFuncs(interceptor.Funcs{SubResourcePatch: func(ctx context.Context, c client.Client,
			sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			statusWrites++
			return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		}}).Build()
	r := &BriefReconciler{Client: c, Scheme: scheme}
	ctx := context.Background()

	var stale c4v1beta1.Brief
	if err := c.Get(ctx, client.ObjectKeyFromObject(brief), &stale); err != nil {
		t.Fatal(err)
	}
	var current c4v1beta1.Brief
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
	var tasks c4v1beta1.TaskList
	if err := c.List(ctx, &tasks); err != nil || len(tasks.Items) != 0 {
		t.Errorf("Tasks = %d (%v), want none", len(tasks.Items), err)
	}
}

// The operator's own wait shows over the planner's question; once it ends,
// the question is open again until a new round answers it.
func TestClearOperatorNeedsInputRestoresThePlannersQuestion(t *testing.T) {
	brief := &c4v1beta1.Brief{}
	brief.Status.Round = 2
	brief.Status.History = []c4v1beta1.BriefRound{
		{Round: 1, Outcome: model.ReasonApplied},
		{Round: 2, Outcome: model.ReasonPlannerNeedsInput, NeedsInput: "Which CRDs first?"},
	}
	operatorNeedsInput(brief, "GitProvider git is missing")
	clearOperatorNeedsInput(brief)
	cond := meta.FindStatusCondition(brief.Status.Conditions, c4v1beta1.ConditionNeedsInput)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != model.ReasonPlannerNeedsInput ||
		cond.Message != "Which CRDs first?" {
		t.Errorf("NeedsInput = %+v, want the planner's question again", cond)
	}

	brief.Status.Round = 3 // a new round runs
	operatorNeedsInput(brief, "Agent planner is missing")
	clearOperatorNeedsInput(brief)
	cond = meta.FindStatusCondition(brief.Status.Conditions, c4v1beta1.ConditionNeedsInput)
	if cond.Status != metav1.ConditionFalse || cond.Reason != c4v1beta1.ReasonResolved {
		t.Errorf("NeedsInput = %+v, want resolved once a new round runs", cond)
	}
}

// #540: a reconcile on a copy of the Brief from before its round was applied
// must not fail the round again, over the newer status that says it applied.
func TestEndingARoundOnAStaleBriefWritesNothing(t *testing.T) {
	scheme := briefScheme(t)
	brief := &c4v1beta1.Brief{ObjectMeta: objectMeta("apps", "cel-lens"),
		Spec: c4v1beta1.BriefSpec{Idea: "A linter."}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1beta1.Brief{}).
		WithObjects(brief).Build()
	r := &BriefReconciler{Client: c, Scheme: scheme}
	ctx := context.Background()

	var stale c4v1beta1.Brief
	if err := c.Get(ctx, client.ObjectKeyFromObject(brief), &stale); err != nil {
		t.Fatal(err)
	}
	stale.Status.Round = 1
	if err := c.Status().Update(ctx, &stale); err != nil {
		t.Fatal(err)
	}
	var current c4v1beta1.Brief
	if err := c.Get(ctx, client.ObjectKeyFromObject(brief), &current); err != nil {
		t.Fatal(err)
	}
	current.Status.History = []c4v1beta1.BriefRound{{Round: 1, Outcome: model.ReasonApplied}}
	setCondition(&current.Status.Conditions, current.Generation, c4v1beta1.ConditionPlanned,
		metav1.ConditionTrue, model.ReasonApplied, "Round 1 applied")
	if err := c.Status().Update(ctx, &current); err != nil {
		t.Fatal(err)
	}

	orig := stale.DeepCopy()
	r.endRoundFailed(ctx, &stale, &c4v1beta1.Task{}, "the plan no longer fits the namespace: "+
		model.RefusalText([]model.Refusal{{Subject: "Container/orders-api", Reason: "is in the namespace"}}))
	result, err := r.finishReconcile(ctx, &stale, orig, ctrl.Result{}, nil)
	if err != nil || result.RequeueAfter == 0 {
		t.Errorf("finishReconcile = %+v, %v; want a requeue and no error", result, err)
	}
	var got c4v1beta1.Brief
	if err := c.Get(ctx, client.ObjectKeyFromObject(brief), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.History) != 1 || got.Status.History[0].Outcome != model.ReasonApplied {
		t.Errorf("history = %+v, want round 1 applied", got.Status.History)
	}
	if cond := meta.FindStatusCondition(got.Status.Conditions, c4v1beta1.ConditionPlanned); cond == nil ||
		cond.Reason != model.ReasonApplied {
		t.Errorf("Planned = %+v, want reason %s", cond, model.ReasonApplied)
	}
}

// readBrief reads the Brief from the API server.
func readBrief(t *testing.T, c client.Client, key client.ObjectKey) *c4v1beta1.Brief {
	t.Helper()
	var b c4v1beta1.Brief
	if err := c.Get(context.Background(), key, &b); err != nil {
		t.Fatal(err)
	}
	return &b
}

// requestInNeedsYou says whether the Brief's needsYou lists a request.
func requestInNeedsYou(b *c4v1beta1.Brief) bool {
	return slices.ContainsFunc(b.Status.NeedsYou, func(n c4v1beta1.BriefNeedsYou) bool {
		return n.Kind == model.AttentionRequest
	})
}

// staleRequestsOff is #199's Brief: a round's start writes a waiting request,
// a reconcile lists it in needsYou, and a person turns requests Off. It
// returns the reconcile for that change: the copy from before needsYou listed
// the request, with the new spec and requests read Off, and the copy its
// status patches are made from.
func staleRequestsOff(t *testing.T) (c client.Client, r *BriefReconciler, stale, orig *c4v1beta1.Brief) {
	t.Helper()
	scheme := briefScheme(t)
	const repository = "ada/cel-lens"
	brief := &c4v1beta1.Brief{ObjectMeta: objectMeta("apps", "cel-lens"),
		Spec: c4v1beta1.BriefSpec{Idea: "A linter.", Releases: []c4v1beta1.BriefRelease{{Container: "linter",
			CrewPublishReleases: c4v1beta1.CrewPublishReleases{GitHub: c4v1beta1.CrewPublishGitHub{
				Repository: repository, TokenSecretRef: c4v1beta1.LocalRef{Name: "cel-lens-github"}}},
			Requests: &c4v1beta1.BriefRequests{Mode: model.RequestsAsk, Authors: []string{"ada"}}}}}}
	c = fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1beta1.Brief{}).
		WithObjects(brief).Build()
	r = &BriefReconciler{Client: c, Scheme: scheme}
	ctx := context.Background()
	key := client.ObjectKeyFromObject(brief)

	// A round's start writes the waiting request; the cache keeps this copy.
	waiting := readBrief(t, c, key)
	waiting.Status.Round = 1
	waiting.Status.Requests = []c4v1beta1.BriefRequest{{Repository: repository, Number: 1, Author: "ada",
		Title: "Dark theme", Stage: model.RequestWaiting}}
	if err := c.Status().Update(ctx, waiting); err != nil {
		t.Fatal(err)
	}
	stale = readBrief(t, c, key)
	// The reconcile then tends the Brief and lists the request in needsYou.
	current := readBrief(t, c, key)
	n := model.RequestNeedsYou(modelRequests(current.Status.Requests)[0])
	current.Status.NeedsYou = []c4v1beta1.BriefNeedsYou{{Kind: n.Kind, Name: n.Name, Question: n.Question,
		Recommend: n.Recommend}}
	current.Status.NeedsYouWaiting = 1
	if err := c.Status().Update(ctx, current); err != nil {
		t.Fatal(err)
	}
	// A person turns requests Off.
	current = readBrief(t, c, key)
	current.Spec.Releases[0].Requests.Mode = model.RequestsOff
	if err := c.Update(ctx, current); err != nil {
		t.Fatal(err)
	}

	// The reconcile for that change reads the new spec over the older status.
	stale.Spec = *current.Spec.DeepCopy()
	orig = stale.DeepCopy()
	r.syncRequests(ctx, stale)
	if len(stale.Status.Requests) != 0 {
		t.Fatalf("requests = %+v, want none read with requests Off", stale.Status.Requests)
	}
	return c, r, stale, orig
}

// #199: a reconcile that reads requests turned Off on a copy from before an
// earlier reconcile listed the waiting request in needsYou must not clear
// status.requests over the newer status and leave the request in needsYou;
// it writes nothing and reads the Brief again, and that read drops both.
func TestTurningRequestsOffOnAStaleBriefLeavesNoRequestInNeedsYou(t *testing.T) {
	c, r, stale, orig := staleRequestsOff(t)
	ctx := context.Background()
	key := client.ObjectKeyFromObject(stale)
	result, err := r.finishReconcile(ctx, stale, orig, ctrl.Result{}, nil)
	if err != nil || result.RequeueAfter == 0 {
		t.Errorf("finishReconcile on the stale copy = %+v, %v; want a requeue and no error", result, err)
	}
	if got := readBrief(t, c, key); len(got.Status.Requests) == 0 && requestInNeedsYou(got) {
		t.Fatalf("status.requests is empty but needsYou lists %+v: a stale write half applied", got.Status.NeedsYou)
	}

	// The next reconcile reads the Brief as it is.
	fresh := readBrief(t, c, key)
	orig = fresh.DeepCopy()
	r.syncRequests(ctx, fresh)
	if _, err := r.finishReconcile(ctx, fresh, orig, ctrl.Result{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := readBrief(t, c, key); len(got.Status.Requests) != 0 || requestInNeedsYou(got) {
		t.Errorf("requests = %+v, needsYou = %+v; want the request in neither", got.Status.Requests,
			got.Status.NeedsYou)
	}
}

// #199: recording a filed issue writes the status at once; on a stale copy
// that write would carry the rest of what the reconcile computed (requests
// cleared) over the newer status, and hand writeStatus a version that already
// holds it, so the lock there let the half-write through. It writes nothing.
func TestRecordingAnIssueOnAStaleBriefWritesNothing(t *testing.T) {
	c, r, stale, orig := staleRequestsOff(t)
	ctx := context.Background()
	run := &roundApply{brief: stale, orig: orig}
	err := r.recordIssue(ctx, run, "apps/linter", 1, ports.Issue{Number: 7, Title: "Lint rule 1",
		URL: "http://git.example/apps/linter/issues/7"})
	if !errors.Is(err, errStaleBrief) {
		t.Fatalf("recordIssue on a stale Brief = %v, want errStaleBrief", err)
	}
	result, err := r.finishReconcile(ctx, stale, orig, ctrl.Result{}, err)
	if err != nil || result.RequeueAfter == 0 {
		t.Errorf("finishReconcile = %+v, %v; want a requeue and no error", result, err)
	}
	got := readBrief(t, c, client.ObjectKeyFromObject(stale))
	if len(got.Status.Requests) == 0 && requestInNeedsYou(got) {
		t.Fatalf("status.requests is empty but needsYou lists %+v: a stale write half applied", got.Status.NeedsYou)
	}
	if len(got.Status.Issues) != 0 {
		t.Errorf("issues = %+v, want none recorded from the stale copy", got.Status.Issues)
	}
}

// issueRound is a Brief in round 1 whose plan files one issue in Container
// linter, on a fake Git host; funcs intercept the fake client's calls.
func issueRound(t *testing.T, funcs interceptor.Funcs) (c client.Client, r *BriefReconciler, git *portsfake.Git,
	key client.ObjectKey, ready map[string]*c4v1beta1.Container, issues []model.PlanIssue) {
	t.Helper()
	scheme := briefScheme(t)
	const ns = "apps"
	brief := &c4v1beta1.Brief{ObjectMeta: objectMeta(ns, "cel-lens"), Spec: c4v1beta1.BriefSpec{Idea: "A linter."}}
	linter := &c4v1beta1.Container{ObjectMeta: objectMeta(ns, "linter"),
		Spec: c4v1beta1.ContainerSpec{GitProviderRef: c4v1beta1.LocalRef{Name: "git"}}}
	c = fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1beta1.Brief{}).
		WithObjects(brief, linter,
			&corev1.Secret{ObjectMeta: objectMeta(ns, "git-credentials"),
				Data: map[string][]byte{"username": []byte("theos"), "token": []byte("t")}},
			&c4v1beta1.GitProvider{ObjectMeta: objectMeta(ns, "git"), Spec: c4v1beta1.GitProviderSpec{
				Provider: c4v1beta1.GitProviderGitea, URL: "http://git.example:3000", Owner: ns,
				CredentialsSecretRef: c4v1beta1.LocalRef{Name: "git-credentials"}}}).
		WithInterceptorFuncs(funcs).Build()
	git = portsfake.New()
	r = &BriefReconciler{Client: c, APIReader: c, Scheme: scheme, Git: fakeGitClient{git}}
	key = client.ObjectKeyFromObject(brief)
	started := readBrief(t, c, key)
	started.Status.Round = 1
	if err := c.Status().Update(context.Background(), started); err != nil {
		t.Fatal(err)
	}
	return c, r, git, key, map[string]*c4v1beta1.Container{"linter": linter},
		[]model.PlanIssue{{N: 1, Container: "linter", Title: "Lint rule 1", Body: "Lint rule 1."}}
}

// #199: a round whose Brief the API server holds newer than the reconcile
// read files nothing: the newer status may have ended or cancelled the round,
// and an issue filed from the stale copy could not be recorded, so it would
// stay open and ready, unrecorded.
func TestARoundOnAStaleBriefFilesNoIssue(t *testing.T) {
	c, r, git, key, ready, issues := issueRound(t, interceptor.Funcs{})
	ctx := context.Background()
	stale := readBrief(t, c, key)
	newer := readBrief(t, c, key)
	newer.Status.Phase = c4v1beta1.BriefFailed
	if err := c.Status().Update(ctx, newer); err != nil {
		t.Fatal(err)
	}
	err := r.fileIssues(ctx, &roundApply{brief: stale, orig: stale.DeepCopy(), round: "1"}, ready, issues)
	if !errors.Is(err, errStaleBrief) {
		t.Fatalf("fileIssues on a stale Brief = %v, want errStaleBrief", err)
	}
	if all := git.AllIssues(key.Namespace, "linter"); len(all) != 0 {
		t.Errorf("issues on the Git host = %+v, want none filed from the stale copy", all)
	}
}

// #199: a record refused because the Brief changed after the round checked
// it does not file the issue twice: the next reconcile finds the filed issue
// by its marker and records it.
func TestARefusedIssueRecordFilesTheIssueOnce(t *testing.T) {
	changed := false
	c, r, git, key, ready, issues := issueRound(t, interceptor.Funcs{SubResourcePatch: func(ctx context.Context,
		c client.Client, sub string, obj client.Object, patch client.Patch,
		opts ...client.SubResourcePatchOption) error {
		if !changed {
			// Another writer, between the check and the record.
			changed = true
			newer := &c4v1beta1.Brief{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), newer); err != nil {
				return err
			}
			newer.Status.NeedsYouWaiting = 1
			if err := c.Status().Update(ctx, newer); err != nil {
				return err
			}
		}
		return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
	}})
	ctx := context.Background()
	stale := readBrief(t, c, key)
	err := r.fileIssues(ctx, &roundApply{brief: stale, orig: stale.DeepCopy(), round: "1"}, ready, issues)
	if !errors.Is(err, errStaleBrief) {
		t.Fatalf("fileIssues on a Brief changed meanwhile = %v, want errStaleBrief", err)
	}
	if got := readBrief(t, c, key); len(got.Status.Issues) != 0 || got.Status.NeedsYouWaiting != 1 {
		t.Fatalf("issues = %+v, needsYouWaiting = %d; want none recorded and the newer status kept",
			got.Status.Issues, got.Status.NeedsYouWaiting)
	}
	if open := git.OpenIssues(key.Namespace, "linter"); len(open) != 1 {
		t.Fatalf("open issues = %+v, want the one filed before the refused record", open)
	}

	fresh := readBrief(t, c, key)
	if err := r.fileIssues(ctx, &roundApply{brief: fresh, orig: fresh.DeepCopy(), round: "1"}, ready,
		issues); err != nil {
		t.Fatal(err)
	}
	open := git.OpenIssues(key.Namespace, "linter")
	if len(open) != 1 {
		t.Fatalf("open issues = %+v, want the one filed once", open)
	}
	if got := readBrief(t, c, key).Status.Issues; len(got) != 1 || int(got[0].Number) != open[0].Number {
		t.Errorf("issues = %+v, want issue %d recorded", got, open[0].Number)
	}
}

// heldBrief is a Brief whose reconcile put the finalizer on (holdForBot), which
// moves brief's version on and not orig's.
func heldBrief(t *testing.T, objs ...client.Object) (c client.Client, r *BriefReconciler, brief,
	orig *c4v1beta1.Brief) {
	t.Helper()
	scheme := briefScheme(t)
	b := &c4v1beta1.Brief{ObjectMeta: objectMeta("apps", "cel-lens"),
		Spec: c4v1beta1.BriefSpec{Idea: "A linter.", Agents: c4v1beta1.BriefAgents{
			Planner: c4v1beta1.LocalRef{Name: "planner"}}}}
	c = fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1beta1.Brief{}).
		WithObjects(append(objs, b)...).Build()
	r = &BriefReconciler{Client: c, Scheme: scheme}
	brief = readBrief(t, c, client.ObjectKeyFromObject(b))
	orig = brief.DeepCopy()
	if err := r.holdForBot(context.Background(), brief); err != nil {
		t.Fatal(err)
	}
	if brief.ResourceVersion == orig.ResourceVersion {
		t.Fatal("holdForBot left brief's version as it was")
	}
	return c, r, brief, orig
}

// #199: the status patch locks on the version this reconcile last wrote:
// putting the finalizer on is the reconcile's own write, not a stale copy.
func TestWriteStatusLocksOnTheVersionTheReconcileLastWrote(t *testing.T) {
	c, r, brief, orig := heldBrief(t)
	brief.Status.Phase = c4v1beta1.BriefPlanning
	if err := r.writeStatus(context.Background(), brief, orig); err != nil {
		t.Fatalf("writeStatus after holdForBot = %v, want the status written", err)
	}
	if got := readBrief(t, c, client.ObjectKeyFromObject(brief)); got.Status.Phase != c4v1beta1.BriefPlanning ||
		!controllerutil.ContainsFinalizer(got, briefBotFinalizer) {
		t.Errorf("phase = %q, finalizers = %v; want the status and the finalizer", got.Status.Phase, got.Finalizers)
	}
}

// #199: starting a round locks on the version this reconcile last wrote too,
// so the first reconcile of a Brief doesn't conflict with its own finalizer.
func TestStartRoundLocksOnTheVersionTheReconcileLastWrote(t *testing.T) {
	c, r, brief, orig := heldBrief(t, &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "planner")},
		&c4v1beta1.Framework{ObjectMeta: objectMeta("apps", "python-fastapi")})
	if err := r.startRound(context.Background(), brief, orig, false); err != nil {
		t.Fatalf("startRound after holdForBot = %v, want round 1 started", err)
	}
	if got := readBrief(t, c, client.ObjectKeyFromObject(brief)); got.Status.Round != 1 {
		t.Errorf("round = %d, want 1", got.Status.Round)
	}
}

// #199: putting the finalizer on a stale Brief writes nothing: the version it
// would hand the status patch would hold changes the reconcile never saw.
func TestHoldForBotOnAStaleBriefWritesNothing(t *testing.T) {
	scheme := briefScheme(t)
	brief := &c4v1beta1.Brief{ObjectMeta: objectMeta("apps", "cel-lens"), Spec: c4v1beta1.BriefSpec{Idea: "A linter."}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1beta1.Brief{}).
		WithObjects(brief).Build()
	r := &BriefReconciler{Client: c, Scheme: scheme}
	ctx := context.Background()
	key := client.ObjectKeyFromObject(brief)
	stale := readBrief(t, c, key)
	newer := readBrief(t, c, key)
	newer.Status.Round = 1
	if err := c.Status().Update(ctx, newer); err != nil {
		t.Fatal(err)
	}

	orig := stale.DeepCopy()
	err := r.holdForBot(ctx, stale)
	if !errors.Is(err, errStaleBrief) {
		t.Fatalf("holdForBot on a stale Brief = %v, want errStaleBrief", err)
	}
	stale.Status.Phase = c4v1beta1.BriefPlanning
	result, err := r.finishReconcile(ctx, stale, orig, ctrl.Result{}, err)
	if err != nil || result.RequeueAfter == 0 {
		t.Errorf("finishReconcile = %+v, %v; want a requeue and no error", result, err)
	}
	got := readBrief(t, c, key)
	if controllerutil.ContainsFinalizer(got, briefBotFinalizer) || got.Status.Round != 1 || got.Status.Phase != "" {
		t.Errorf("finalizers = %v, round = %d, phase = %q; want the newer Brief as it was", got.Finalizers,
			got.Status.Round, got.Status.Phase)
	}
}

// A Brief round takes what its Plan Task used, and the Brief adds its rounds up.
func TestRecordRoundAddsUpTheTokens(t *testing.T) {
	status := &c4v1beta1.BriefStatus{Round: 1}
	plan := func(tokens *c4v1beta1.TokenUsage) *c4v1beta1.Task {
		return &c4v1beta1.Task{Status: c4v1beta1.TaskStatus{Tokens: tokens}}
	}
	recordRound(status, plan(&c4v1beta1.TokenUsage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4,
		LargestSession: 120000, RunsWithout: 1}), c4v1beta1.BriefRound{Outcome: model.ReasonApplied})
	status.Round = 2
	recordRound(status, plan(&c4v1beta1.TokenUsage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40,
		LargestSession: 80000}), c4v1beta1.BriefRound{Outcome: model.ReasonApplied})
	status.Round = 3
	recordRound(status, plan(nil), c4v1beta1.BriefRound{Outcome: model.ReasonApplied})

	want := c4v1beta1.TokenUsage{Input: 11, Output: 22, CacheRead: 33, CacheWrite: 44, LargestSession: 120000,
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

// Only the status write that starts the operator's own wait reconciles a
// Brief again (#783), not the planner's question nor the wait ending.
func TestStartsOperatorWaitPassesOnlyTheWaitStarting(t *testing.T) {
	brief := func(reason string) *c4v1beta1.Brief {
		b := &c4v1beta1.Brief{ObjectMeta: objectMeta("apps", "cel-lens")}
		if reason != "" {
			meta.SetStatusCondition(&b.Status.Conditions, metav1.Condition{
				Type: c4v1beta1.ConditionNeedsInput, Status: metav1.ConditionTrue, Reason: reason})
		}
		return b
	}
	pred := startsOperatorWait()
	for _, tc := range []struct {
		name     string
		old, new string
		want     bool
	}{
		{"the planner's question gives way to the wait", model.ReasonPlannerNeedsInput, c4v1beta1.ReasonNotFound, true},
		{"nothing asked, then the wait", "", c4v1beta1.ReasonNotFound, true},
		{"the wait goes on", c4v1beta1.ReasonNotFound, c4v1beta1.ReasonNotFound, false},
		{"the wait ends", c4v1beta1.ReasonNotFound, model.ReasonPlannerNeedsInput, false},
		{"the planner asks", "", model.ReasonPlannerNeedsInput, false},
	} {
		got := pred.Update(event.UpdateEvent{ObjectOld: brief(tc.old), ObjectNew: brief(tc.new)})
		if got != tc.want {
			t.Errorf("%s: passed %v, want %v", tc.name, got, tc.want)
		}
	}
	if pred.Create(event.CreateEvent{Object: brief(c4v1beta1.ReasonNotFound)}) {
		t.Error("a creation passed; the spec predicate passes those")
	}
}

// A round's apply split over two reconciles: the second one's create of an
// object the first made meets AlreadyExists while the cache's list doesn't
// show it yet. Status.Created still names the object (#1870).
func TestCreatedNamesAnObjectAnEarlierReconcileMade(t *testing.T) {
	scheme := briefScheme(t)
	brief := &c4v1beta1.Brief{ObjectMeta: objectMeta("apps", "cel-lens")}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object,
				opts ...client.GetOption) error {
				return apierrors.NewNotFound(c4v1beta1.GroupVersion.WithResource("persons").GroupResource(), key.Name)
			},
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				return apierrors.NewAlreadyExists(c4v1beta1.GroupVersion.WithResource("persons").GroupResource(),
					obj.GetName())
			},
		}).Build()
	r := &BriefReconciler{Client: c, Scheme: scheme}
	ctx := context.Background()
	run := &roundApply{brief: brief, orig: brief.DeepCopy(), round: "1"}

	isNew, err := r.applyObject(ctx, run, model.PlanObject{Kind: model.KindPerson, Name: "rule-author"})
	if err != nil || !isNew {
		t.Fatalf("applyObject = %v, %v; want new and no error", isNew, err)
	}
	created, err := r.createdObjects(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	want := c4v1beta1.BriefObject{Kind: model.KindPerson, Name: "rule-author"}
	if !slices.Contains(created, want) {
		t.Errorf("Status.Created = %v, want %v in it", created, want)
	}
}
