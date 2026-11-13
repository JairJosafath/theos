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
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/taskfacts"
)

// reportFixture is the report's fixture, which internal/model's test and the
// UI's read too (docs/plans/results-report.md): Tasks as the API serves
// them, and the report and the page they give over the fixture's period.
const (
	reportFixture     = "../../ui/src/model/testdata/report"
	reportFixtureFrom = "2026-10-06T12:00:00Z"
	reportFixtureTo   = "2026-11-05T12:00:00Z"
)

// fixtureTasks decodes the fixture's tasks.json into the typed TaskList, as
// the command's client does a cluster's answer. A field the API's types don't
// have fails it: the fixture is Tasks as the API serves them.
func fixtureTasks(t *testing.T) *c4v1beta1.TaskList {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(reportFixture, "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list c4v1beta1.TaskList
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&list); err != nil {
		t.Fatalf("tasks.json is no TaskList of the API's: %v", err)
	}
	return &list
}

// cluster is a cluster that holds lists and objects, and opened counts how
// often the command opened it.
type cluster struct {
	reader client.Reader
	opened int
}

func newCluster(t *testing.T, tasks *c4v1beta1.TaskList, objects ...client.Object) *cluster {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := c4v1beta1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return &cluster{reader: fake.NewClientBuilder().WithScheme(scheme).WithLists(tasks).WithObjects(objects...).Build()}
}

func (c *cluster) open() (client.Reader, error) {
	c.opened++
	return c.reader, nil
}

// container is a Container whose repository is at url.
func container(namespace, name, url string) *c4v1beta1.Container {
	return &c4v1beta1.Container{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Status: c4v1beta1.ContainerStatus{RepositoryURL: url}}
}

// written reads the report the command wrote into dir under name.
func written(t *testing.T, dir, name string) (page, document []byte) {
	t.Helper()
	page, err := os.ReadFile(filepath.Join(dir, name+".md"))
	if err != nil {
		t.Fatal(err)
	}
	document, err = os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return page, document
}

// #66, step 2: the command's mapping from the API's typed Tasks is the one
// the fixture's report was made with. Decoded into the typed TaskList and
// read through the command, the fixture's Tasks give its report.json and its
// page byte for byte, in a directory the command made.
func TestReportOfTheFixture(t *testing.T) {
	cluster := newCluster(t, fixtureTasks(t))
	dir := filepath.Join(t.TempDir(), "site", "numbers")
	var stdout bytes.Buffer
	f := reportFlags{name: "report", dir: dir, from: reportFixtureFrom, to: reportFixtureTo}
	if err := report(context.Background(), f, cluster.open, time.Now(), &stdout); err != nil {
		t.Fatal(err)
	}
	page, document := written(t, dir, "report")
	for name, got := range map[string][]byte{"report.json": document, "report.md": page} {
		want, err := os.ReadFile(filepath.Join(reportFixture, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not the fixture's: the command reads a Task otherwise than the report's rules were "+
				"written for.\n--- got ---\n%s", name, got)
		}
	}
	if want := "Wrote " + dir + "/report.md and " + dir + "/report.json: of 38 Tasks read, 14 merged, 4 failed and " +
		"1 ended with no change in the period.\n"; stdout.String() != want {
		t.Errorf("it said %q, want %q", stdout.String(), want)
	}
	if cluster.opened != 1 {
		t.Errorf("the cluster was opened %d times, want once", cluster.opened)
	}
}

// #66, decision 15: each merged Task carries its Container's repository, so
// a reader has the pull request's address; a Container that is gone, or has
// no repository yet, leaves it out, and the numbers are the same either way.
func TestReportAddsEachContainersRepository(t *testing.T) {
	tasks := fixtureTasks(t)
	var want model.Report
	raw, err := os.ReadFile(filepath.Join(reportFixture, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	// Every Container of a merged Task but the first has a repository.
	gone := want.MergedTasks[0]
	var containers []client.Object
	seen := map[client.ObjectKey]bool{{Namespace: gone.Namespace, Name: gone.Container}: true}
	for _, m := range want.MergedTasks {
		key := client.ObjectKey{Namespace: m.Namespace, Name: m.Container}
		if !seen[key] {
			seen[key] = true
			containers = append(containers, container(m.Namespace, m.Container,
				"https://git.example.com/"+m.Namespace+"/"+m.Container))
		}
	}
	if len(containers) == 0 {
		t.Fatal("the fixture's merged Tasks are of one Container; this test needs two")
	}
	// A Container of the same name in another namespace is another's.
	containers = append(containers, container("elsewhere", gone.Container, "https://git.example.com/elsewhere/x"),
		container(gone.Namespace, "no-repository-yet", ""))

	dir := t.TempDir()
	f := reportFlags{name: "report", dir: dir, from: reportFixtureFrom, to: reportFixtureTo}
	cluster := newCluster(t, tasks, containers...)
	if err := report(context.Background(), f, cluster.open, time.Now(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	_, document := written(t, dir, "report")
	var got model.Report
	if err := json.Unmarshal(document, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.MergedTasks) != len(want.MergedTasks) {
		t.Fatalf("%d merged Tasks, want %d", len(got.MergedTasks), len(want.MergedTasks))
	}
	for i, m := range got.MergedTasks {
		repository := "https://git.example.com/" + m.Namespace + "/" + m.Container
		if m.Namespace == gone.Namespace && m.Container == gone.Container {
			repository = ""
		}
		if m.Repository != repository {
			t.Errorf("%s/%s of Container %s: repository %q, want %q", m.Namespace, m.Task, m.Container, m.Repository, repository)
		}
		got.MergedTasks[i].Repository = ""
	}
	again, err := got.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, raw) {
		t.Errorf("the repositories changed more than the merged Tasks' repository:\n%s", again)
	}
}

// #66: the name, the period and the directory are checked before anything
// is read or written. A name that is none, a time that is none, a period
// that ends before it begins or after now, and a directory that can't be one
// each fail with the cluster unopened and no file made.
func TestReportChecksBeforeItReadsOrWrites(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(root, "results")
	for name, c := range map[string]struct {
		f    reportFlags
		want string
	}{
		"no name":            {reportFlags{dir: fresh}, "--name is required"},
		"a path for a name":  {reportFlags{name: "../../README", dir: fresh}, `a report can't be called "../../README"`},
		"a name with a dir":  {reportFlags{name: "a/b", dir: fresh}, `a report can't be called "a/b"`},
		"a hidden name":      {reportFlags{name: ".hidden", dir: fresh}, `a report can't be called ".hidden"`},
		"a from that's none": {reportFlags{name: "v1", dir: fresh, from: "yesterday"}, `--from "yesterday" is no time`},
		"a date for a to":    {reportFlags{name: "v1", dir: fresh, to: "2026-11-05"}, `--to "2026-11-05" is no time`},
		"ends as it begins": {reportFlags{name: "v1", dir: fresh, from: reportFixtureTo, to: reportFixtureTo},
			"which is not after its beginning"},
		"ends before it begins": {reportFlags{name: "v1", dir: fresh, from: reportFixtureTo, to: reportFixtureFrom},
			"the period ends at 2026-10-06T12:00:00Z, which is not after its beginning, 2026-11-05T12:00:00Z"},
		"begins after now": {reportFlags{name: "v1", dir: fresh, from: "2027-01-01T00:00:00Z"},
			"which is not after its beginning"},
		// A Task that finishes between now and such an end would be in no
		// report: the next one begins at this one's end.
		"ends after now": {reportFlags{name: "v1", dir: fresh, to: "2026-11-13T09:00:01Z"},
			"the period can't end after now: --to is 2026-11-13T09:00:01Z and it is 2026-11-13T09:00:00Z"},
		"ends after now, in another zone": {reportFlags{name: "v1", dir: fresh, to: "2026-11-13T11:30:00+02:00"},
			"the period can't end after now: --to is 2026-11-13T09:30:00Z"},
		"a file for a directory": {reportFlags{name: "v1", dir: file}, file + " is no directory"},
		"a directory in a file": {reportFlags{name: "v1", dir: filepath.Join(file, "results", "deep")},
			"can't hold a report"},
	} {
		cluster := newCluster(t, fixtureTasks(t))
		var stdout bytes.Buffer
		now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
		err := report(context.Background(), c.f, cluster.open, now, &stdout)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one that says %q", name, err, c.want)
		}
		if cluster.opened != 0 {
			t.Errorf("%s: the cluster was read before the check failed", name)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: it said %q", name, stdout.String())
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Errorf("%s: something was written under %s: %v", name, root, entries)
		}
	}
}

// The period defaults to everything up to now, and --namespace reads one
// project only.
func TestReportDefaultsAndNamespace(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 500, time.UTC)
	merged := func(namespace, name string, at time.Time) c4v1beta1.Task {
		done := metav1.NewTime(at)
		return c4v1beta1.Task{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
			Spec:   c4v1beta1.TaskSpec{ContainerRef: c4v1beta1.LocalRef{Name: "api"}},
			Status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskMerged, PullRequest: 7, CompletedAt: &done}}
	}
	tasks := &c4v1beta1.TaskList{Items: []c4v1beta1.Task{
		merged("shop", "old", now.Add(-400*24*time.Hour)), merged("shop", "new", now.Truncate(time.Second)),
		merged("docs", "other", now.Add(-time.Hour)), merged("shop", "tomorrow", now.Add(24*time.Hour)),
	}}
	for name, c := range map[string]struct {
		namespace string
		want      []string
	}{
		"every namespace": {"", []string{"old", "other", "new"}},
		"one project":     {"shop", []string{"old", "new"}},
	} {
		dir := t.TempDir()
		f := reportFlags{name: "v0.5.0", dir: dir, namespace: c.namespace}
		cluster := newCluster(t, tasks, container("shop", "api", "https://git.example.com/shop/api"))
		if err := report(context.Background(), f, cluster.open, now, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		page, document := written(t, dir, "v0.5.0")
		var r model.Report
		if err := json.Unmarshal(document, &r); err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(r.MergedTasks))
		for _, m := range r.MergedTasks {
			got = append(got, m.Task)
		}
		if strings.Join(got, " ") != strings.Join(c.want, " ") || r.From != "" || r.To != "2026-11-13T09:00:00Z" {
			t.Errorf("%s: merged %v from %q to %q, want %v up to now", name, got, r.From, r.To, c.want)
		}
		if !strings.Contains(string(page), "The Tasks that finished up to 2026-11-13 09:00 UTC") ||
			!strings.Contains(string(page), "[v0.5.0.json](v0.5.0.json)") {
			t.Errorf("%s: the page doesn't say its period or link its document:\n%s", name, page)
		}
	}
}

// A report written again replaces the one before it, both files.
func TestReportWritesOverTheReportBefore(t *testing.T) {
	dir := t.TempDir()
	for _, stale := range []string{"report.md", "report.json"} {
		if err := os.WriteFile(filepath.Join(dir, stale), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f := reportFlags{name: "report", dir: dir, from: reportFixtureFrom, to: reportFixtureTo}
	cluster := newCluster(t, fixtureTasks(t))
	if err := report(context.Background(), f, cluster.open, time.Now(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	page, document := written(t, dir, "report")
	if bytes.Contains(page, []byte("stale")) || bytes.Contains(document, []byte("stale")) {
		t.Error("the report before is still there")
	}
}

// #66: a report that can't be written whole leaves the one that was there.
// The page's place is a directory, so the page can't be written: the command
// says so before it reads the cluster, and when the directory appears only
// after that check (here, as the cluster is opened), the document that could
// be written is not, and nothing else is left in the folder. The two files
// were written in a map's order, so the new document stood beside the old
// page in most runs.
func TestReportLeavesNoHalfAReport(t *testing.T) {
	for name, c := range map[string]struct {
		late   bool
		opened int
	}{
		"a directory has the page's name":                        {false, 0},
		"a directory takes the page's name after it was checked": {true, 1},
	} {
		dir := t.TempDir()
		page, document := filepath.Join(dir, "report.md"), filepath.Join(dir, "report.json")
		if err := os.WriteFile(document, []byte("the report before"), 0o600); err != nil {
			t.Fatal(err)
		}
		inTheWay := func() {
			if err := os.Mkdir(page, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		cluster := newCluster(t, fixtureTasks(t))
		open := cluster.open
		if c.late {
			open = func() (client.Reader, error) {
				inTheWay()
				return cluster.open()
			}
		} else {
			inTheWay()
		}
		var stdout bytes.Buffer
		f := reportFlags{name: "report", dir: dir, from: reportFixtureFrom, to: reportFixtureTo}
		err := report(context.Background(), f, open, time.Now(), &stdout)
		if want := "can't write " + page + ": a directory has that name"; err == nil || err.Error() != want {
			t.Errorf("%s: error %v, want %q", name, err, want)
		}
		if cluster.opened != c.opened {
			t.Errorf("%s: the cluster was opened %d times, want %d", name, cluster.opened, c.opened)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: it said %q", name, stdout.String())
		}
		if got, err := os.ReadFile(document); err != nil || string(got) != "the report before" {
			t.Errorf("%s: the document is %q (%v), want the report before's: half a report was written", name, got, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Errorf("%s: the folder holds more than the document before and the directory: %v", name, entries)
		}
	}
}

// The command's flags: help exits 0 and says what it does, a flag it doesn't
// have and a word after the flags exit 2, a name that is none exits 1 before
// any cluster is asked for, and a good name exits 1 where the cluster is
// asked for (this test has none to give).
func TestReportMain(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "none"))
	dir := t.TempDir()
	for name, c := range map[string]struct {
		args []string
		code int
		want string
	}{
		"help":           {[]string{"-h"}, 0, "theos-session report --name <release tag>"},
		"its flags":      {[]string{"-h"}, 0, "-kubeconfig"},
		"an unknown one": {[]string{"--period", "x"}, 2, "flag provided but not defined"},
		"no name":        {nil, 1, "theos-session report: --name is required"},
		"a bad name":     {[]string{"--name", "../x"}, 1, `theos-session report: a report can't be called "../x"`},
		"a name with no flag": {[]string{"v0.5.0"}, 2,
			`theos-session report: "v0.5.0" is not understood: the report takes flags only`},
		"a word after the flags": {[]string{"--name", "v0.5.0", "--dir", dir, "now"}, 2, `"now" is not understood`},
		"no cluster": {[]string{"--name", "v0.5.0", "--dir", dir}, 1,
			"theos-session report: no cluster to read the Tasks of"},
	} {
		var stdout, stderr bytes.Buffer
		if code := reportMain(c.args, &stdout, &stderr); code != c.code || !strings.Contains(stderr.String(), c.want) {
			t.Errorf("%s: exit %d saying %q, want %d and %q", name, code, stderr.String(), c.code, c.want)
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: it wrote %q to standard output", name, stdout.String())
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("with no cluster to read, something was written: %v (%v)", entries, err)
	}
}

// What the fixture has no Task for, and the command's own reading decides
// (docs/plans/results-report.md, "What step 2 found"): the name, the pull
// request and the repository, when it finished, a size that says nothing
// was not read, and a Score with no verdicts yet. The metric facts are
// taskfacts.Metric's (#142), whose own table holds them (internal/taskfacts).
func TestReportTask(t *testing.T) {
	task := func(change func(*c4v1beta1.Task)) model.ReportTask {
		task := &c4v1beta1.Task{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "checkout-1"},
			Spec:   c4v1beta1.TaskSpec{ContainerRef: c4v1beta1.LocalRef{Name: "api"}},
			Status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskMerged, PullRequest: 7, Round: 2}}
		change(task)
		return reportTask(task, "https://git.example.com/shop/api")
	}
	plain := task(func(*c4v1beta1.Task) {})
	if plain.Kind != "Implement" || plain.Namespace != "shop" || plain.Container != "api" || plain.Name != "checkout-1" ||
		plain.PullRequest != 7 || plain.Round != 2 || plain.Repository != "https://git.example.com/shop/api" ||
		plain.NoChange || plain.Unreviewed || plain.ApprovedByPerson || plain.Size != nil || plain.Score != nil ||
		plain.PersonCommits != nil || !plain.Completed.IsZero() {
		t.Errorf("a merged Task that says no more reads as %+v", plain)
	}
	at := metav1.NewTime(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	succeeded := func(reason string) []metav1.Condition {
		return []metav1.Condition{{Type: taskfacts.ConditionSucceeded, Status: metav1.ConditionTrue, Reason: reason}}
	}
	for name, c := range map[string]struct {
		change func(*c4v1beta1.Task)
		holds  func(model.ReportTask) bool
	}{
		"nothing to do: no change": {func(t *c4v1beta1.Task) { t.Status.Conditions = succeeded(model.ReasonNothingToDo) },
			func(r model.ReportTask) bool { return r.NoChange }},
		"merged for another reason: a change": {func(t *c4v1beta1.Task) { t.Status.Conditions = succeeded("Merged") },
			func(r model.ReportTask) bool { return !r.NoChange }},
		"when it finished": {func(t *c4v1beta1.Task) { t.Status.CompletedAt = &at },
			func(r model.ReportTask) bool { return r.Completed.Equal(at.Time) }},
		"a size of files only was read": {func(t *c4v1beta1.Task) {
			t.Status.PullRequestSize = c4v1beta1.TaskPullRequestSize{Files: 1}
		}, func(r model.ReportTask) bool { return r.Size != nil && *r.Size == model.ReportSize{} }},
		"a Score with no verdicts yet": {func(t *c4v1beta1.Task) {
			t.Spec.Kind = c4v1beta1.TaskScore
			t.Spec.Score = &c4v1beta1.TaskScoreSpec{ScorerRef: c4v1beta1.LocalRef{Name: "quality"}, ScorerGeneration: 3,
				Criteria: []string{"reads plainly"}, Config: c4v1beta1.TaskRunConfig{Harness: "claude-code"}}
		}, func(r model.ReportTask) bool {
			return r.Kind == "Score" && r.Score != nil && r.Score.Scorer == "quality" && r.Score.Generation == 3 &&
				r.Score.Harness == "claude-code" && len(r.Score.Verdicts) == 0
		}},
	} {
		if got := task(c.change); !c.holds(got) {
			t.Errorf("%s: read as %+v", name, got)
		}
	}
}

// #142: the report reads a Task's metric facts through the one mapping the
// operator's Prometheus series read them through, taskfacts.Metric, and adds
// only its own. Every Task of the fixture says so, and the fixture holds
// what tells the ways a Task ends done apart: its merged Tasks carry the
// Succeeded condition the operator writes, reason Merged where a pull
// request merged, Scored on a judge's run, PlanAccepted, Triaged and
// DoneByStandardChange (#204) on one Task each, and NothingToDo on one. A
// mapping that took any Succeeded=True for "no change" then gives another
// report (TestReportOfTheFixture).
func TestReportReadsATaskAsTheSeriesDo(t *testing.T) {
	reasons := map[string]int{}
	tasks := fixtureTasks(t)
	for i := range tasks.Items {
		task := &tasks.Items[i]
		if got, want := reportTask(task, "").TaskMetricFacts, taskfacts.Metric(task); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the report reads its metric facts as\n%+v, and the series as\n%+v", task.Name, got, want)
		}
		if task.Status.Phase != c4v1beta1.TaskMerged {
			continue
		}
		done := apimeta.FindStatusCondition(task.Status.Conditions, taskfacts.ConditionSucceeded)
		if done == nil || done.Status != metav1.ConditionTrue {
			t.Errorf("%s is Merged with no Succeeded=True condition: the API serves one for every Task that "+
				"ended done", task.Name)
			continue
		}
		reasons[done.Reason]++
		want := "Merged"
		switch {
		case task.Spec.Kind == c4v1beta1.TaskScore:
			want = "Scored"
		case task.Spec.Kind == c4v1beta1.TaskPlan:
			want = "PlanAccepted"
		case task.Spec.Kind == c4v1beta1.TaskTriage:
			want = "Triaged"
		case task.Status.StandardChange != nil:
			want = model.ReasonDoneByStandardChange
		case task.Status.PullRequest == 0:
			want = model.ReasonNothingToDo
		}
		if done.Reason != want {
			t.Errorf("%s ended done with reason %s, want %s: what the operator writes for it", task.Name,
				done.Reason, want)
		}
	}
	if reasons["Merged"] != 16 || reasons["Scored"] != 10 || reasons[model.ReasonNothingToDo] != 1 ||
		reasons["PlanAccepted"] != 1 || reasons["Triaged"] != 1 || reasons[model.ReasonDoneByStandardChange] != 1 {
		t.Errorf("the fixture's merged Tasks ended done as %v, want 16 Merged, 10 Scored, 1 PlanAccepted, "+
			"1 Triaged, 1 DoneByStandardChange and 1 NothingToDo (#204)", reasons)
	}
}
