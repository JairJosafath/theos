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
	"reflect"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/taskfacts"
)

// LabelScorer names the Scorer a Score Task judges for.
const LabelScorer = "theos.factory.io/scorer"

// ScorerReconciler makes a Score Task for each finished Task a Scorer selects
// and samples, within its daily cap and budget, and counts the verdicts of
// its Score Tasks (docs/plans/done/measure-and-improve.md, step 4). The Task
// reconciler runs each Score Task's judge.
type ScorerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads the Git provider's Secret live.
	APIReader client.Reader
	// Git reaches the Git host, where spec.improve files its issues.
	Git GitClient
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=scorers,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=scorers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=tasks,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=theos.factory.io,resources=agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=benchmarks,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=containers,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=gitproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// Reconcile makes the Scorer's due Score Tasks and writes what it judged.
func (r *ScorerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var scorer c4v1beta1.Scorer
	if err := r.Get(ctx, req.NamespacedName, &scorer); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !scorer.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	now := r.now()
	var tasks c4v1beta1.TaskList
	if err := r.List(ctx, &tasks, client.InNamespace(scorer.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	scores := map[string]*c4v1beta1.Task{}
	for i := range tasks.Items {
		t := &tasks.Items[i]
		if t.Spec.Kind == c4v1beta1.TaskScore && t.Spec.Score != nil && t.Spec.Score.ScorerRef.Name == scorer.Name {
			scores[t.Name] = t
		}
	}

	status := scorer.Status.DeepCopy()
	status.ObservedGeneration = scorer.Generation
	tallyScores(status, &scorer, scores)
	today := model.ScorerDay(now)
	status.Day, status.StartedToday, status.CostTodayMilliUSD = today, 0, 0
	for _, t := range scores {
		if model.ScorerDay(t.CreationTimestamp.Time) == today {
			status.StartedToday++
			status.CostTodayMilliUSD += t.Status.CostMilliUSD
		}
	}

	reason, message, err := r.startScores(ctx, &scorer, status, tasks.Items, scores)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.improve(ctx, &scorer, tasks.Items, scores); err != nil {
		return ctrl.Result{}, err
	}
	condition := metav1.Condition{Type: c4v1beta1.ConditionScorerReady, Status: metav1.ConditionTrue,
		Reason: model.ScorerReady, Message: "Judging the Tasks it selects", ObservedGeneration: scorer.Generation}
	if reason != "" {
		condition.Status, condition.Reason, condition.Message = metav1.ConditionFalse, reason, message
	}
	apimeta.SetStatusCondition(&status.Conditions, condition)
	var result ctrl.Result
	if reason == model.ScorerDailyCapReached || reason == model.ScorerBudgetReached {
		// The cap lifts at the next UTC day.
		next := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+1, 0, 0, 1, 0, time.UTC)
		result.RequeueAfter = next.Sub(now)
	}
	if !reflect.DeepEqual(status, &scorer.Status) {
		scorer.Status = *status
		if err := r.Status().Update(ctx, &scorer); err != nil {
			return ctrl.Result{}, err
		}
	}
	return result, nil
}

// startScores makes a Score Task for each Task the Scorer judges that has
// none yet, oldest finished first, until its cap or budget; it says why it
// stopped, as the Ready condition's reason and message, or "".
func (r *ScorerReconciler) startScores(ctx context.Context, scorer *c4v1beta1.Scorer, status *c4v1beta1.ScorerStatus,
	tasks []c4v1beta1.Task, scores map[string]*c4v1beta1.Task) (string, string, error) {
	percent := 100
	if p := scorer.Spec.SamplePercent; p != nil {
		percent = int(*p)
	}
	var judge c4v1beta1.Agent
	err := r.Get(ctx, client.ObjectKey{Namespace: scorer.Namespace, Name: scorer.Spec.JudgeRef.Name}, &judge)
	if apierrors.IsNotFound(err) {
		return model.ScorerJudgeNotFound, "Agent " + scorer.Spec.JudgeRef.Name + " not found", nil
	}
	if err != nil {
		return "", "", err
	}
	sel := model.ScorerSelection{Containers: scorer.Spec.Select.Containers}
	for _, k := range scorer.Spec.Select.Kinds {
		sel.Kinds = append(sel.Kinds, string(k))
	}
	for _, p := range scorer.Spec.Select.Phases {
		sel.Phases = append(sel.Phases, string(p))
	}
	benchmarks, err := r.benchmarksNaming(ctx, scorer)
	if err != nil {
		return "", "", err
	}
	var due []*c4v1beta1.Task
	for i := range tasks {
		t := &tasks[i]
		if t.Spec.ContainerRef.Name == "" || !t.DeletionTimestamp.IsZero() || taskfacts.EndedWithNoChange(t) {
			// A Task that changed nothing has no change to judge (#72).
			continue
		}
		var completed time.Time
		if t.Status.CompletedAt != nil {
			completed = t.Status.CompletedAt.Time
		}
		benchmarked := t.Spec.Benchmark != nil && benchmarks[t.Spec.Benchmark.BenchmarkRef.Name]
		judged := model.ScoredTask{Name: t.Name, Kind: string(t.Spec.Kind), Container: t.Spec.ContainerRef.Name,
			Phase: string(t.Status.Phase), Completed: completed, Benchmarked: benchmarked}
		if !model.ScorerSelects(sel, scorer.CreationTimestamp.Time, judged) ||
			!benchmarked && !model.ScoreSampled(string(scorer.UID), t.Name, percent) {
			continue
		}
		if _, made := scores[model.ScoreTaskName(scorer.Name, t.Name)]; made {
			continue
		}
		due = append(due, t)
	}
	slices.SortFunc(due, func(a, b *c4v1beta1.Task) int {
		return a.Status.CompletedAt.Compare(b.Status.CompletedAt.Time)
	})
	for _, t := range due {
		if reason, message := model.ScorerLimit(int(status.StartedToday), int(scorer.Spec.MaxPerDay),
			status.CostTodayMilliUSD, int(scorer.Spec.BudgetUSD)); reason != "" {
			return reason, message, nil
		}
		if err := r.makeScoreTask(ctx, scorer, t); err != nil {
			return "", "", err
		}
		status.StartedToday++
	}
	if reason, message := model.ScorerLimit(int(status.StartedToday), int(scorer.Spec.MaxPerDay),
		status.CostTodayMilliUSD, int(scorer.Spec.BudgetUSD)); reason != "" {
		return reason, message, nil
	}
	if percent == 0 {
		return model.ScorerPaused, "samplePercent is 0: it judges only the Tasks of the Benchmarks that name it", nil
	}
	return "", "", nil
}

// benchmarksNaming are the Benchmarks of the Scorer's namespace that name it:
// it judges every finished Task of theirs.
func (r *ScorerReconciler) benchmarksNaming(ctx context.Context, scorer *c4v1beta1.Scorer) (map[string]bool, error) {
	var benchmarks c4v1beta1.BenchmarkList
	if err := r.List(ctx, &benchmarks, client.InNamespace(scorer.Namespace)); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, b := range benchmarks.Items {
		for _, ref := range b.Spec.ScorerRefs {
			if ref.Name == scorer.Name {
				out[b.Name] = true
			}
		}
	}
	return out, nil
}

// makeScoreTask makes the Score Task in which the Scorer judges t, owned by
// the Scorer. One made already, by a pass that lost its status write, is no
// error.
func (r *ScorerReconciler) makeScoreTask(ctx context.Context, scorer *c4v1beta1.Scorer, t *c4v1beta1.Task) error {
	name := model.ScoreTaskName(scorer.Name, t.Name)
	labels := projectLabels(scorer.Namespace)
	labels[LabelScorer] = scorer.Name
	labels[c4v1beta1.LabelContainer] = t.Spec.ContainerRef.Name
	score := &c4v1beta1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: scorer.Namespace, Name: name, Labels: labels},
		Spec: c4v1beta1.TaskSpec{
			Kind:         c4v1beta1.TaskScore,
			ContainerRef: t.Spec.ContainerRef,
			AgentRef:     scorer.Spec.JudgeRef,
			Goal:         model.ScoreGoal(scoreEvidence(t)),
			Score: &c4v1beta1.TaskScoreSpec{ScorerRef: c4v1beta1.LocalRef{Name: scorer.Name},
				ScorerGeneration: scorer.Generation, TaskRef: c4v1beta1.LocalRef{Name: t.Name},
				Criteria: scorer.Spec.Criteria, Config: implementerConfig(t)},
		},
	}
	if err := controllerutil.SetControllerReference(scorer, score, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, score); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	logf.FromContext(ctx).Info("Created Score Task", "task", name, "judged", t.Name)
	return nil
}

// improve files an issue for each criterion of the current generation that
// failed often enough for one configuration (spec.improve), unless one is
// open: in the repository of the newest failing Task's Container.
func (r *ScorerReconciler) improve(ctx context.Context, scorer *c4v1beta1.Scorer, tasks []c4v1beta1.Task,
	scores map[string]*c4v1beta1.Task) error {
	imp := scorer.Spec.Improve
	if imp == nil {
		return nil
	}
	judged := map[string]*c4v1beta1.Task{}
	for i := range tasks {
		judged[tasks[i].Name] = &tasks[i]
	}
	var records []model.ScoreRecord
	for _, t := range scores {
		if t.Spec.Score.ScorerGeneration != scorer.Generation || !isFinal(t.Status.Phase) ||
			len(t.Status.Score.Criteria) == 0 || t.Status.CompletedAt == nil {
			continue
		}
		c := t.Spec.Score.Config
		config := model.NotRecordedConfig
		if c.Harness != "" {
			config = model.ConfigKey(c.Harness, c.Model)
		}
		rec := model.ScoreRecord{Task: t.Spec.Score.TaskRef.Name, Container: t.Spec.ContainerRef.Name, Config: config,
			Completed: t.Status.CompletedAt.Time}
		if j := judged[rec.Task]; j != nil {
			rec.PullURL = j.Status.PullRequestURL
		}
		for _, v := range t.Status.Score.Criteria {
			rec.Verdicts = append(rec.Verdicts, model.ScoreCriterion{Criterion: v.Criterion, Verdict: string(v.Verdict),
				Reason: v.Reason})
		}
		records = append(records, rec)
	}
	for _, due := range model.ImprovementsDue(scorer.Spec.Criteria, records, int(imp.Failures), int(imp.Window)) {
		if err := r.fileImprovement(ctx, scorer, due); err != nil {
			return err
		}
	}
	return nil
}

// fileImprovement files due's issue in its newest failure's Container's
// repository, unless one is open there.
func (r *ScorerReconciler) fileImprovement(ctx context.Context, scorer *c4v1beta1.Scorer, due model.Improvement) error {
	var container c4v1beta1.Container
	err := r.Get(ctx, client.ObjectKey{Namespace: scorer.Namespace, Name: due.Failures[0].Container}, &container)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	git, err := gitAccessFor(ctx, r.Client, r.APIReader, r.Git, scorer.Namespace, container.Spec.GitProviderRef.Name)
	if err != nil {
		return err
	}
	repo := git.provider.Repository(container.Name)
	gitCtx, cancel := context.WithTimeout(ctx, defaultGitTimeout)
	defer cancel()
	l := model.ImprovementLabel()
	labels := append(issueLabels(), ports.Label{Name: l.Name, Color: l.Color, Description: l.Description,
		Exclusive: l.Exclusive})
	if err := git.provider.EnsureLabels(gitCtx, repo, labels); err != nil {
		return err
	}
	open, err := git.provider.ListIssues(gitCtx, repo, model.LabelImprovement)
	if err != nil {
		return err
	}
	for _, issue := range open {
		if model.HasImprovementMarker(issue.Body, scorer.Name, due.Criterion, due.Config) {
			return nil
		}
	}
	title, body := model.ImprovementIssue(scorer.Name, due)
	issue, err := git.provider.CreateIssue(gitCtx, repo, title, body,
		[]string{model.LabelImprovement, model.LabelProposed}, 0)
	if err != nil {
		return err
	}
	logf.FromContext(ctx).Info("Filed improvement issue", "issue", issue.Number, "repository", container.Name,
		"criterion", due.Criterion, "config", due.Config)
	return nil
}

// scoreEvidence is what a judge is told of t.
func scoreEvidence(t *c4v1beta1.Task) model.ScoreEvidence {
	s := t.Status
	e := model.ScoreEvidence{Task: t.Name, Kind: string(t.Spec.Kind), Container: t.Spec.ContainerRef.Name,
		Phase: string(s.Phase), Issue: int(t.Spec.Issue), IssueTitle: s.IssueTitle, Rounds: int(s.Round),
		PullRequest: int(s.PullRequest), Commit: s.MergeCommit, Branch: s.Branch, Summary: s.Summary}
	if s.Checks != nil {
		e.Checks = s.Checks.State
	}
	if s.Review != nil {
		e.ReviewVerdict, e.ReviewSummary = s.Review.Verdict, s.Review.Summary
	}
	if s.Phase == c4v1beta1.TaskFailed {
		e.FailedReason = conditionMessageOf(s.Conditions, ConditionSucceeded)
	}
	if s.PersonCommits != nil {
		n := int(*s.PersonCommits)
		e.PersonCommits = &n
	}
	if b := t.Spec.Benchmark; b != nil && s.MergeCommit == "" {
		// A Benchmark's change is its branch's, from the pinned base.
		e.Head, e.Base = s.Commit, b.Base
	}
	return e
}

func conditionMessageOf(conds []metav1.Condition, typ string) string {
	if c := apimeta.FindStatusCondition(conds, typ); c != nil {
		return c.Message
	}
	return ""
}

// implementerConfig is the configuration t's newest implement run recorded.
func implementerConfig(t *c4v1beta1.Task) c4v1beta1.TaskRunConfig {
	for _, run := range slices.Backward(t.Status.Runs) {
		if run.Role == model.RoleImplementer && run.Harness != "" {
			return c4v1beta1.TaskRunConfig{Agent: run.Agent, Harness: run.Harness, Model: run.Model}
		}
	}
	return c4v1beta1.TaskRunConfig{}
}

// tallyScores counts the verdicts of the Scorer's finished Score Tasks of its
// current generation.
func tallyScores(status *c4v1beta1.ScorerStatus, scorer *c4v1beta1.Scorer, scores map[string]*c4v1beta1.Task) {
	var verdicts [][]model.ScoreCriterion
	for _, t := range scores {
		if t.Spec.Score.ScorerGeneration != scorer.Generation || len(t.Status.Score.Criteria) == 0 ||
			!isFinal(t.Status.Phase) {
			continue
		}
		var v []model.ScoreCriterion
		for _, c := range t.Status.Score.Criteria {
			v = append(v, model.ScoreCriterion{Criterion: c.Criterion, Verdict: string(c.Verdict)})
		}
		verdicts = append(verdicts, v)
	}
	status.Scored = int32(len(verdicts))
	status.Criteria = nil
	for _, c := range model.TallyScores(scorer.Spec.Criteria, verdicts) {
		status.Criteria = append(status.Criteria, c4v1beta1.ScorerCriterionCount{Criterion: c.Criterion,
			Pass: int32(c.Pass), Fail: int32(c.Fail), Unclear: int32(c.Unclear)})
	}
}

func (r *ScorerReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// SetupWithManager runs the Scorer on its own changes and whenever a Task of
// its namespace changes: one that finished, or one of its Score Tasks.
func (r *ScorerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toScorers := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var scorers c4v1beta1.ScorerList
		if err := mgr.GetClient().List(ctx, &scorers, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(scorers.Items))
		for _, s := range scorers.Items {
			reqs = append(reqs, requestFor(s.Namespace, s.Name))
		}
		return reqs
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.Scorer{}).
		Watches(&c4v1beta1.Task{}, toScorers).
		Named("scorer").
		Complete(r)
}
