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

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

const reportUsage = `theos-session report writes the factory's numbers over a period as one page and
one JSON document (docs/plans/results-report.md): the Tasks that merged and
failed, who wrote the code, how long a merged Task took and what it cost, and
what the Scorers found. It reads the Tasks and Containers of the cluster your
kubeconfig names, and changes nothing there.

Usage:
  theos-session report --name <release tag> [--from <time>] [--to <time>] [flags]

It writes <dir>/<name>.md and <dir>/<name>.json, over a report of that name
if there is one. A release's report runs from the end of the report before
it, so give that report's "to" as --from and no Task is counted twice:

  theos-session report --name v0.5.0 --from 2026-10-06T12:00:00Z --to 2026-11-05T12:00:00Z

The name and the directory are checked before the cluster is read: a name
that is no name, or a directory that can't be one, writes nothing.

Flags:
`

// conditionSucceeded is the Task condition whose reason says how a Task
// ended (internal/controller's ConditionSucceeded).
const conditionSucceeded = "Succeeded"

// reportFlags are what a person says of the report they want.
type reportFlags struct {
	name, dir, from, to, namespace string
}

// reportMain runs theos-session report with args, and returns its exit code.
func reportMain(args []string, stdout, stderr io.Writer) int {
	var f reportFlags
	set := flag.NewFlagSet("report", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&f.name, "name", "", "What the period is called: the release's tag (required)")
	set.StringVar(&f.dir, "dir", model.ReportDir, "The directory the repository keeps its reports in")
	set.StringVar(&f.from, "from", "",
		"The period's beginning, RFC 3339: the Tasks that finished after it (default: from the first Task)")
	set.StringVar(&f.to, "to", "", "The period's end, RFC 3339: the Tasks that finished up to and at it (default: now)")
	set.StringVar(&f.namespace, "namespace", "",
		"Read one project's namespace only (default: every namespace you may list Tasks in)")
	// --kubeconfig, as run and start have it.
	config.RegisterFlags(set)
	set.Usage = func() {
		_, _ = io.WriteString(stderr, reportUsage)
		set.PrintDefaults()
	}
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := report(context.Background(), f, clusterReader, time.Now(), stdout); err != nil {
		_, _ = io.WriteString(stderr, "theos-session report: "+err.Error()+"\n")
		return 1
	}
	return 0
}

// clusterReader reads the cluster the kubeconfig names.
func clusterReader() (client.Reader, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("no cluster to read the Tasks of: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := c4v1beta1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	return client.New(cfg, client.Options{Scheme: scheme})
}

// report writes the report f asks for. It asks for the paths, the period and
// the directory first, and opens the cluster only once all three hold, so a
// name that is no name fails before anything is read or written; and it
// writes only once both files are made, so a failure leaves no half a report.
func report(ctx context.Context, f reportFlags, open func() (client.Reader, error), now time.Time,
	stdout io.Writer) error {
	if f.name == "" {
		return errors.New("--name is required: the release's tag, which the report's two files are named after")
	}
	pagePath, documentPath, err := model.ReportPaths(f.dir, f.name)
	if err != nil {
		return err
	}
	period, err := reportPeriod(f, now)
	if err != nil {
		return err
	}
	dir := filepath.Dir(filepath.FromSlash(pagePath))
	if err := checkReportDir(dir); err != nil {
		return err
	}

	reader, err := open()
	if err != nil {
		return err
	}
	var tasks c4v1beta1.TaskList
	if err := reader.List(ctx, &tasks, client.InNamespace(f.namespace)); err != nil {
		return fmt.Errorf("listing the Tasks: %w", err)
	}
	var containers c4v1beta1.ContainerList
	if err := reader.List(ctx, &containers, client.InNamespace(f.namespace)); err != nil {
		return fmt.Errorf("listing the Containers, whose repositories the merged Tasks' pull requests are in: %w", err)
	}
	r := model.FactoryReport(period, reportTasks(&tasks, &containers))
	document, err := r.JSON()
	if err != nil {
		return err
	}
	page, err := r.Markdown()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a directory of the repository's pages
		return err
	}
	for path, content := range map[string][]byte{documentPath: document, pagePath: []byte(page)} {
		//nolint:gosec // a published page and its document, in the person's own repository
		if err := os.WriteFile(filepath.FromSlash(path), content, 0o644); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(stdout, "Wrote %s and %s: of %d Tasks read, %d merged, %d failed and %d ended with no change "+
		"in the period.\n", pagePath, documentPath, len(tasks.Items), r.Merged, r.Failed, r.NoChange)
	return err
}

// reportPeriod is the period f names: after --from, up to and including
// --to, which is now when it isn't given.
func reportPeriod(f reportFlags, now time.Time) (model.ReportPeriod, error) {
	period := model.ReportPeriod{Name: f.name, To: now.UTC().Truncate(time.Second)}
	for _, at := range []struct {
		flag, value string
		into        *time.Time
	}{{"--from", f.from, &period.From}, {"--to", f.to, &period.To}} {
		if at.value == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, at.value)
		if err != nil {
			return period, fmt.Errorf("%s %q is no time: write it as 2026-11-05T12:00:00Z", at.flag, at.value)
		}
		*at.into = t
	}
	if !period.From.IsZero() && !period.From.Before(period.To) {
		return period, fmt.Errorf("the period ends at %s, which is not after its beginning, %s",
			period.To.UTC().Format(time.RFC3339), period.From.UTC().Format(time.RFC3339))
	}
	return period, nil
}

// checkReportDir is the error of a directory no report can be written in,
// found without writing: dir, or the nearest of its parents that exists, is
// something other than a directory.
func checkReportDir(dir string) error {
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		info, err := os.Stat(p)
		switch {
		case err == nil && info.IsDir():
			return nil
		case err == nil:
			return fmt.Errorf("--dir %s can't hold a report: %s is no directory", dir, p)
		case !errors.Is(err, fs.ErrNotExist) || p == filepath.Dir(p):
			return fmt.Errorf("--dir %s can't hold a report: %w", dir, err)
		}
	}
}

// reportTasks is what the report reads of the Tasks, each with its
// Container's repository where the Container still says one.
func reportTasks(tasks *c4v1beta1.TaskList, containers *c4v1beta1.ContainerList) []model.ReportTask {
	repositories := map[client.ObjectKey]string{}
	for i := range containers.Items {
		c := &containers.Items[i]
		repositories[client.ObjectKeyFromObject(c)] = c.Status.RepositoryURL
	}
	out := make([]model.ReportTask, 0, len(tasks.Items))
	for i := range tasks.Items {
		task := &tasks.Items[i]
		out = append(out, reportTask(task,
			repositories[client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.ContainerRef.Name}]))
	}
	return out
}

// reportTask is what the report reads of task, whose Container's repository
// is at repository. Its metric facts are the ones the operator's
// taskMetricFacts reads (internal/controller/factory_metrics.go), so the
// report and the Prometheus series count a Task alike; the report's fixture
// holds this mapping to the UI's numbers (TestReportOfTheFixture).
func reportTask(task *c4v1beta1.Task, repository string) model.ReportTask {
	status := &task.Status
	t := model.ReportTask{
		TaskMetricFacts: model.TaskMetricFacts{
			Namespace:    task.Namespace,
			Container:    task.Spec.ContainerRef.Name,
			Kind:         string(task.Spec.Kind),
			Benchmark:    task.Spec.Benchmark != nil,
			Phase:        string(status.Phase),
			Round:        int(status.Round),
			CostMilliUSD: status.CostMilliUSD,
		},
		Name:        task.Name,
		PullRequest: int(status.PullRequest),
		Repository:  repository,
	}
	if t.Kind == "" {
		t.Kind = string(c4v1beta1.TaskImplement)
	}
	if n := status.PersonCommits; n != nil {
		commits := int(*n)
		t.PersonCommits = &commits
	}
	if status.CompletedAt != nil {
		t.Completed = status.CompletedAt.Time
	}
	spec, approval := status.Spec, status.Approval
	t.ApprovedByPerson = spec != nil && spec.ApprovedBy != "" || approval != nil && approval.ApprovedBy != ""
	t.Unreviewed = approval != nil && approval.Require == c4v1beta1.ApprovalNone && status.Review == nil
	done := apimeta.FindStatusCondition(status.Conditions, conditionSucceeded)
	t.NoChange = status.Phase == c4v1beta1.TaskMerged && done != nil && done.Reason == model.ReasonNothingToDo
	for _, step := range status.History {
		t.History = append(t.History, model.TaskMetricStep{At: step.At.Time, Phase: string(step.Phase),
			Reason: step.Reason})
	}
	for _, run := range status.Runs {
		t.Runs = append(t.Runs, model.TaskMetricRun{Agent: run.Agent, Harness: run.Harness, Model: run.Model,
			Role: run.Role, CostMilliUSD: run.CostMilliUSD})
	}
	// The size is read from the Git host when a Task merges; one that says
	// nothing was not read, which is not a change of no lines.
	if size := status.PullRequestSize; size != (c4v1beta1.TaskPullRequestSize{}) {
		t.Size = &model.ReportSize{Additions: int(size.Additions), Deletions: int(size.Deletions)}
	}
	if score := task.Spec.Score; score != nil {
		t.Score = &model.ReportScore{Scorer: score.ScorerRef.Name, Generation: score.ScorerGeneration,
			Criteria: score.Criteria, Harness: score.Config.Harness, Model: score.Config.Model}
		for _, c := range status.Score.Criteria {
			t.Score.Verdicts = append(t.Score.Verdicts, model.ScoreCriterion{Criterion: c.Criterion,
				Verdict: string(c.Verdict), Reason: c.Reason})
		}
	}
	return t
}
