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
	"reflect"
	"slices"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
)

// LabelBenchmark names the Benchmark a Task is a run of.
const LabelBenchmark = "theos.factory.io/benchmark"

// BenchmarkReconciler runs a Benchmark (docs/plans/done/measure-and-improve.md,
// step 6): it reads its goals once, makes its Tasks in turn, up to parallel
// and within its budget, closes each finished Task's pull request and deletes
// its branch once its Scorers judged it, and compares the variants. The Task
// reconciler runs each Task, which never merges.
type BenchmarkReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads the Git provider's Secret live.
	APIReader client.Reader
	// Git reaches the Git host: the issues a goal names, the pull requests
	// and branches to clean up.
	Git GitClient
	// PollInterval is how often a running Benchmark looks again; 0 means a
	// minute.
	PollInterval time.Duration
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=benchmarks,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=benchmarks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=tasks,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=theos.factory.io,resources=scorers,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=containers,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=gitproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// Reconcile takes the Benchmark's next steps and writes how it goes.
func (r *BenchmarkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var bench c4v1beta1.Benchmark
	if err := r.Get(ctx, req.NamespacedName, &bench); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !bench.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	status := bench.Status.DeepCopy()
	poll := ctrl.Result{RequeueAfter: r.pollInterval()}
	done := func(reason, message string) (ctrl.Result, error) {
		state := metav1.ConditionFalse
		if reason == model.BenchmarkFinished {
			state = metav1.ConditionTrue
		}
		setCondition(&status.Conditions, bench.Generation, c4v1beta1.ConditionBenchmarkDone, state, reason, message)
		if reflect.DeepEqual(status, &bench.Status) {
			if state == metav1.ConditionTrue {
				return ctrl.Result{}, nil
			}
			return poll, nil
		}
		bench.Status = *status
		if err := r.Status().Update(ctx, &bench); err != nil {
			return ctrl.Result{}, err
		}
		if state == metav1.ConditionTrue {
			return ctrl.Result{}, nil
		}
		return poll, nil
	}

	var container c4v1beta1.Container
	err := r.Get(ctx, client.ObjectKey{Namespace: bench.Namespace, Name: bench.Spec.ContainerRef.Name}, &container)
	if apierrors.IsNotFound(err) {
		return done(model.BenchmarkContainerNotFound, "Container "+bench.Spec.ContainerRef.Name+" not found")
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	git, err := gitAccessFor(ctx, r.Client, r.APIReader, r.Git, bench.Namespace, container.Spec.GitProviderRef.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	if problem := readGoals(ctx, &bench, status, git); problem != "" {
		return done(model.BenchmarkIssueUnreadable, problem)
	}

	var list c4v1beta1.TaskList
	if err := r.List(ctx, &list, client.InNamespace(bench.Namespace),
		client.MatchingLabels{LabelBenchmark: bench.Name}); err != nil {
		return ctrl.Result{}, err
	}
	tasks := map[string]*c4v1beta1.Task{}
	status.CostMilliUSD = 0
	active := 0
	for i := range list.Items {
		t := &list.Items[i]
		tasks[t.Name] = t
		status.CostMilliUSD += t.Status.CostMilliUSD
		if !isFinal(t.Status.Phase) {
			active++
		}
	}
	scores, err := r.scoresOf(ctx, &bench)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Close each finished Task's pull request and delete its branch, once
	// every Scorer the Benchmark names judged it.
	if err := r.cleanUp(ctx, &bench, status, git, list.Items, scores); err != nil {
		return ctrl.Result{}, err
	}

	variants := make([]string, 0, len(bench.Spec.Variants))
	for _, v := range bench.Spec.Variants {
		variants = append(variants, v.Name)
	}
	runs := model.BenchmarkRuns(len(status.Goals), variants, int(bench.Spec.Repetitions))
	overBudget := status.CostMilliUSD >= int64(bench.Spec.BudgetUSD)*1000
	active, err = r.startRuns(ctx, &bench, status, runs, tasks, active, overBudget)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.compare(&bench, status, list.Items, scores)
	return done(benchmarkState(&bench, status, runs, tasks, active, overBudget, len(list.Items)))
}

// readGoals reads the Benchmark's goals once, an issue as it reads when the
// Benchmark starts; it says why it couldn't, or "".
func readGoals(ctx context.Context, bench *c4v1beta1.Benchmark, status *c4v1beta1.BenchmarkStatus,
	git gitAccess) string {
	if len(status.Goals) > 0 {
		return ""
	}
	gitCtx, cancel := context.WithTimeout(ctx, defaultGitTimeout)
	defer cancel()
	repo := git.provider.Repository(bench.Spec.ContainerRef.Name)
	for _, g := range bench.Spec.Goals {
		if g.Issue == 0 {
			status.Goals = append(status.Goals, g.Goal)
			continue
		}
		issue, err := git.provider.GetIssue(gitCtx, repo, int(g.Issue))
		if err != nil {
			status.Goals = nil
			return fmt.Sprintf("Could not read issue #%d: %v", g.Issue, err)
		}
		status.Goals = append(status.Goals, model.IssueGoal(issue.Number, issue.Title, issue.Body))
	}
	return ""
}

// startRuns makes the next runs' Tasks, in order, while fewer than parallel
// run and the budget lasts; it returns how many run.
func (r *BenchmarkReconciler) startRuns(ctx context.Context, bench *c4v1beta1.Benchmark,
	status *c4v1beta1.BenchmarkStatus, runs []model.BenchmarkRun, tasks map[string]*c4v1beta1.Task, active int,
	overBudget bool) (int, error) {
	for _, run := range runs {
		if active >= int(bench.Spec.Parallel) || overBudget {
			break
		}
		name := model.BenchmarkTaskName(bench.Name, run)
		if _, made := tasks[name]; made {
			continue
		}
		if err := r.makeTask(ctx, bench, status, run, name); err != nil {
			return active, err
		}
		tasks[name] = &c4v1beta1.Task{}
		active++
	}
	return active, nil
}

// benchmarkState is the Done condition's reason and message.
func benchmarkState(bench *c4v1beta1.Benchmark, status *c4v1beta1.BenchmarkStatus, runs []model.BenchmarkRun,
	tasks map[string]*c4v1beta1.Task, active int, overBudget bool, listed int) (string, string) {
	made := 0
	for _, run := range runs {
		if _, ok := tasks[model.BenchmarkTaskName(bench.Name, run)]; ok {
			made++
		}
	}
	switch {
	case active > 0:
		return model.BenchmarkRunning, fmt.Sprintf("%d of %d runs made, %d running", made, len(runs), active)
	case overBudget && made < len(runs):
		return model.BenchmarkOverBudget, benchmarkOverBudgetMessage(status.CostMilliUSD, int(bench.Spec.BudgetUSD), made, len(runs))
	case made < len(runs) || len(status.Cleaned) < listed:
		return model.BenchmarkRunning, fmt.Sprintf("%d of %d runs made; cleaning up", made, len(runs))
	}
	return model.BenchmarkFinished, status.Comparison
}

// benchmarkOverBudgetMessage writes the budget as it writes the cost, in dollars
// and cents (#180).
func benchmarkOverBudgetMessage(costMilliUSD int64, budgetUSD, made, runs int) string {
	return fmt.Sprintf("Its runs cost $%.2f, its budgetUSD of $%.2f: %d of %d runs made",
		float64(costMilliUSD)/1000, float64(budgetUSD), made, runs)
}

// makeTask makes the Task of run, owned by the Benchmark.
func (r *BenchmarkReconciler) makeTask(ctx context.Context, bench *c4v1beta1.Benchmark,
	status *c4v1beta1.BenchmarkStatus, run model.BenchmarkRun, name string) error {
	var variant c4v1beta1.BenchmarkVariant
	for _, v := range bench.Spec.Variants {
		if v.Name == run.Variant {
			variant = v
		}
	}
	labels := projectLabels(bench.Namespace)
	labels[LabelBenchmark] = bench.Name
	labels[c4v1beta1.LabelContainer] = bench.Spec.ContainerRef.Name
	task := &c4v1beta1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: bench.Namespace, Name: name, Labels: labels},
		Spec: c4v1beta1.TaskSpec{
			Kind:         c4v1beta1.TaskImplement,
			ContainerRef: bench.Spec.ContainerRef,
			AgentRef:     variant.ImplementerRef,
			ReviewerRef:  variant.ReviewerRef,
			VerifierRef:  variant.VerifierRef,
			Goal:         status.Goals[run.Goal],
			Benchmark: &c4v1beta1.TaskBenchmarkSpec{BenchmarkRef: c4v1beta1.LocalRef{Name: bench.Name},
				Variant: run.Variant, Goal: int32(run.Goal), Repetition: int32(run.Repetition),
				Base: bench.Spec.Base},
		},
	}
	if err := controllerutil.SetControllerReference(bench, task, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, task); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	logf.FromContext(ctx).Info("Created Benchmark Task", "task", name, "variant", run.Variant,
		"goal", run.Goal+1, "repetition", run.Repetition)
	return nil
}

// benchScores are the Score Tasks of the Scorers a Benchmark names, by the
// Task they judge, and the Scorers that exist.
type benchScores struct {
	byTask  map[string][]*c4v1beta1.Task
	scorers map[string]bool
}

// scoresOf reads the Score Tasks of the Scorers the Benchmark names.
func (r *BenchmarkReconciler) scoresOf(ctx context.Context, bench *c4v1beta1.Benchmark) (benchScores, error) {
	out := benchScores{byTask: map[string][]*c4v1beta1.Task{}, scorers: map[string]bool{}}
	for _, ref := range bench.Spec.ScorerRefs {
		var s c4v1beta1.Scorer
		err := r.Get(ctx, client.ObjectKey{Namespace: bench.Namespace, Name: ref.Name}, &s)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return out, err
		}
		out.scorers[ref.Name] = true
		var list c4v1beta1.TaskList
		if err := r.List(ctx, &list, client.InNamespace(bench.Namespace),
			client.MatchingLabels{LabelScorer: ref.Name}); err != nil {
			return out, err
		}
		for i := range list.Items {
			t := &list.Items[i]
			if t.Spec.Score != nil {
				out.byTask[t.Spec.Score.TaskRef.Name] = append(out.byTask[t.Spec.Score.TaskRef.Name], t)
			}
		}
	}
	return out, nil
}

// judged reports whether every Scorer the Benchmark names that exists has
// finished judging task.
func (s benchScores) judged(task string) bool {
	finished := map[string]bool{}
	for _, t := range s.byTask[task] {
		if isFinal(t.Status.Phase) {
			finished[t.Spec.Score.ScorerRef.Name] = true
		}
	}
	for scorer := range s.scorers {
		if !finished[scorer] {
			return false
		}
	}
	return true
}

// cleanUp closes the pull request of each finished Task the Scorers judged and
// deletes its branch, once, and records it in status.cleaned.
func (r *BenchmarkReconciler) cleanUp(ctx context.Context, bench *c4v1beta1.Benchmark,
	status *c4v1beta1.BenchmarkStatus, git gitAccess, tasks []c4v1beta1.Task, scores benchScores) error {
	repo := git.provider.Repository(bench.Spec.ContainerRef.Name)
	for i := range tasks {
		t := &tasks[i]
		if !isFinal(t.Status.Phase) || slices.Contains(status.Cleaned, t.Name) || !scores.judged(t.Name) {
			continue
		}
		gitCtx, cancel := context.WithTimeout(ctx, defaultGitTimeout)
		if t.Status.PullRequest > 0 {
			if err := git.provider.ClosePullRequest(gitCtx, repo, int(t.Status.PullRequest)); err != nil {
				logf.FromContext(ctx).Info("Could not close a Benchmark Task's pull request", "task", t.Name,
					"error", err.Error())
			}
		}
		branch := t.Status.Branch
		if branch == "" {
			branch = model.TaskBranch(t.Name)
		}
		if has, err := git.provider.HasBranch(gitCtx, repo, branch); err == nil && has {
			if err := git.provider.DeleteBranch(gitCtx, repo, branch); err != nil {
				cancel()
				return err
			}
		}
		cancel()
		status.Cleaned = append(status.Cleaned, t.Name)
	}
	return nil
}

// compare writes each variant's results and the comparison.
func (r *BenchmarkReconciler) compare(bench *c4v1beta1.Benchmark, status *c4v1beta1.BenchmarkStatus,
	tasks []c4v1beta1.Task, scores benchScores) {
	status.Variants = nil
	rates := make([]model.VariantRate, 0, len(bench.Spec.Variants))
	for _, v := range bench.Spec.Variants {
		vs := c4v1beta1.BenchmarkVariantStatus{Name: v.Name}
		approved, rounds := 0, 0
		var minutes, costs []float64
		criteria := map[[2]string]*model.ScoreCount{}
		var order [][2]string
		for i := range tasks {
			t := &tasks[i]
			if t.Spec.Benchmark == nil || t.Spec.Benchmark.Variant != v.Name {
				continue
			}
			vs.Tasks++
			if !isFinal(t.Status.Phase) {
				continue
			}
			vs.Finished++
			if t.Status.Phase == c4v1beta1.TaskApproved {
				approved++
			}
			rounds += int(max(t.Status.Round, 1))
			if t.Status.CompletedAt != nil {
				minutes = append(minutes, t.Status.CompletedAt.Sub(t.CreationTimestamp.Time).Minutes())
			}
			if t.Status.CostMilliUSD > 0 {
				costs = append(costs, float64(t.Status.CostMilliUSD))
			}
			for _, s := range scores.byTask[t.Name] {
				for _, c := range s.Status.Score.Criteria {
					k := [2]string{s.Spec.Score.ScorerRef.Name, c.Criterion}
					if criteria[k] == nil {
						criteria[k] = &model.ScoreCount{Criterion: c.Criterion}
						order = append(order, k)
					}
					switch c.Verdict {
					case c4v1beta1.ScorePass:
						criteria[k].Pass++
					case c4v1beta1.ScoreFail:
						criteria[k].Fail++
					default:
						criteria[k].Unclear++
					}
				}
			}
		}
		rate := model.NewRate(approved, int(vs.Finished))
		vs.Approval = benchRate(rate)
		rates = append(rates, model.VariantRate{Name: v.Name, Rate: rate})
		if vs.Finished > 0 {
			vs.MeanRounds = strconv.FormatFloat(float64(rounds)/float64(vs.Finished), 'f', 1, 64)
		}
		vs.MedianMinutes = int32(medianOf(minutes))
		vs.MedianCostMilliUSD = int64(medianOf(costs))
		for _, k := range order {
			c := criteria[k]
			vs.Criteria = append(vs.Criteria, c4v1beta1.BenchmarkCriterionRate{Scorer: k[0], Criterion: k[1],
				Rate: benchRate(model.NewRate(c.Pass, c.Pass+c.Fail)), Unclear: int32(c.Unclear)})
		}
		status.Variants = append(status.Variants, vs)
	}
	status.Comparison = model.CompareVariants(rates)
}

func benchRate(r model.Rate) c4v1beta1.BenchmarkRate {
	return c4v1beta1.BenchmarkRate{Of: int32(r.N), Percent: int32(r.Percent), Low: int32(r.Low), High: int32(r.High)}
}

// medianOf is the median of values, 0 for none.
func medianOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(values))
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func (r *BenchmarkReconciler) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return time.Minute
}

// SetupWithManager runs the Benchmark on its own changes, its Tasks', and its
// Scorers' Score Tasks'.
func (r *BenchmarkReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toBenchmarks := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		if name := obj.GetLabels()[LabelBenchmark]; name != "" {
			return []reconcile.Request{requestFor(obj.GetNamespace(), name)}
		}
		if obj.GetLabels()[LabelScorer] == "" {
			return nil
		}
		var benchmarks c4v1beta1.BenchmarkList
		if err := mgr.GetClient().List(ctx, &benchmarks, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(benchmarks.Items))
		for _, b := range benchmarks.Items {
			reqs = append(reqs, requestFor(b.Namespace, b.Name))
		}
		return reqs
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.Benchmark{}).
		Watches(&c4v1beta1.Task{}, toBenchmarks).
		Named("benchmark").
		Complete(r)
}
