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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/taskfacts"
)

const reportUsage = `theos-session report writes the factory's numbers over a period as one page and
one JSON document (docs/plans/results-report.md): the Tasks that merged and
failed, who wrote the code, how long a merged Task took and what it cost, and
what the Scorers found. It reads the Tasks and Containers of the cluster your
kubeconfig names, and changes nothing there. Without --namespace that is one
list of each over the whole cluster, which you must be allowed to make.

Usage:
  theos-session report --name <release tag> [--from <time>] [--to <time>] [flags]

It writes <dir>/<name>.md and <dir>/<name>.json, over a report of that name
if there is one. A release's report runs from the end of the report before
it, so give that report's "to" as --from and no Task is counted twice:

  theos-session report --name v0.5.0 --from 2026-10-06T12:00:00Z --to 2026-11-05T12:00:00Z

The name, the period and the directory are checked before the cluster is
read: a name that is no name, a period that ends after now, or a directory
that can't be one, writes nothing.

Flags:
`

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
	set.StringVar(&f.to, "to", "",
		"The period's end, RFC 3339, no later than now: the Tasks that finished up to and at it (default: now)")
	set.StringVar(&f.namespace, "namespace", "",
		"List the Tasks and Containers of one project's namespace only (default: of the whole cluster, in one "+
			"list of each, which needs the right to list both in every namespace)")
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
	if set.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "theos-session report: %q is not understood: the report takes flags only, and its "+
			"name is --name's\n", set.Arg(0))
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
// writes only once both files are rendered, through writeReport, which says
// what a failure leaves.
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
	// The document first and the page last: the page is what a release links.
	files := []reportFile{{path: filepath.FromSlash(documentPath)}, {path: filepath.FromSlash(pagePath)}}
	if err := checkReportFiles(files); err != nil {
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
	files[0].content, files[1].content = document, []byte(page)
	if err := writeReport(files); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Wrote %s and %s: of %d Tasks read, %d merged, %d failed and %d ended with no change "+
		"in the period.\n", pagePath, documentPath, len(tasks.Items), r.Merged, r.Failed, r.NoChange)
	return err
}

// reportFile is one of a report's two files: where it goes and what it holds.
type reportFile struct {
	path    string
	content []byte
}

// checkReportFiles is the error of a report's file that can't be written
// over: a directory has its name.
func checkReportFiles(files []reportFile) error {
	for _, f := range files {
		if info, err := os.Lstat(f.path); err == nil && info.IsDir() {
			return fmt.Errorf("can't write %s: a directory has that name", f.path)
		}
	}
	return nil
}

// writeReport writes files, in their order, so that a failure leaves the
// report that was there, or none: each is written whole beside its place
// under another name, and only when all are, and no place has become a
// directory since report asked, is each moved to its place. What that leaves
// to fail is a move after the first, which no check here can rule out; its
// error names the file that is new and the one that is not.
func writeReport(files []reportFile) error {
	temporary := make([]string, len(files))
	defer func() {
		for _, t := range temporary {
			if t != "" {
				_ = os.Remove(t)
			}
		}
	}()
	for i, f := range files {
		// A report's name begins with no dot (model.CheckReportName), so this is no report's file.
		t := filepath.Join(filepath.Dir(f.path), "."+filepath.Base(f.path)+".tmp")
		temporary[i] = t
		//nolint:gosec // a published page and its document, in the person's own repository
		if err := os.WriteFile(t, f.content, 0o644); err != nil {
			return err
		}
	}
	if err := checkReportFiles(files); err != nil {
		return err
	}
	moved := ""
	for i, f := range files {
		if err := os.Rename(temporary[i], f.path); err != nil {
			if moved != "" {
				return fmt.Errorf("%s is new and %s is not, so the two are of different reports: %w", moved, f.path, err)
			}
			return err
		}
		temporary[i], moved = "", f.path
	}
	return nil
}

// reportPeriod is the period f names: after --from, up to and including
// --to, which is now when it isn't given and is never after it. A report
// that ended later would say it holds a time that hasn't been: the next
// report begins at this one's end, so a Task that finishes between now and
// that end would be in neither.
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
	if period.To.After(now) {
		return period, fmt.Errorf("the period can't end after now: --to is %s and it is %s; a Task that finishes "+
			"between the two would be in no report", period.To.UTC().Format(time.RFC3339),
			now.UTC().Format(time.RFC3339))
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
// is at repository. Its metric facts are taskfacts.Metric's, which the
// operator's Prometheus series read too (#142), so the report and the series
// count a Task alike; what is added here is what the report alone reads. The
// report's fixture holds the whole to the UI's numbers
// (TestReportOfTheFixture).
func reportTask(task *c4v1beta1.Task, repository string) model.ReportTask {
	status := &task.Status
	t := model.ReportTask{
		TaskMetricFacts: taskfacts.Metric(task),
		Name:            task.Name,
		Repository:      repository,
	}
	if status.CompletedAt != nil {
		t.Completed = status.CompletedAt.Time
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
