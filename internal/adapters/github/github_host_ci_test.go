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

package github

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/adapters/github/githubtest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// noJobRunners is why the CI contract's JobRunners subtests don't run on
// GitHub.
const noJobRunners = "a project's own runners are optional on GitHub, whose hosted runners take the jobs; a pool of " +
	"just-in-time runners is a later step (docs/plans/github-host.md, decision 3)"

// TestCIContract covers #78: the GitHub adapter passes the CI contract
// against the fake GitHub, but for the runners a project registers for one
// job, which GitHub Actions doesn't need.
func TestCIContract(t *testing.T) {
	gh := githubtest.New(t, "ada", hostToken)
	orgs := 0
	contract.CI(t, contract.CIEnv{
		New: func(t *testing.T) (ports.GitHost, ports.CI) {
			t.Helper()
			orgs++
			org := fmt.Sprintf("ci-%d", orgs)
			gh.AddOrg(org)
			h, err := (Client{}).Host(ports.Config{URL: gh.URL(), Owner: org, Credentials: ports.Credentials{Token: hostToken}})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := h.(ports.JobRunners); ok {
				t.Error("the GitHub adapter registers runners for a job; the contract's subtests should run then")
			}
			return h, h
		},
		// A job a runner took and that has sent no line yet.
		NoLog: func(t *testing.T, host ports.GitHost, repo ports.Repository) string {
			t.Helper()
			head, err := host.BranchCommit(t.Context(), repo, ports.DefaultBranch)
			if err != nil {
				t.Fatal(err)
			}
			run := gh.AddRun(repo.Owner, repo.Name, "check.yaml", "push", ports.DefaultBranch, head, 0)
			_, page := gh.AddJob(repo.Owner, repo.Name, run, "check", "in_progress", "", "")
			return page
		},
		Cannot: map[string]string{
			"lists the jobs that wait for a runner of their labels":                     noJobRunners,
			"registers a runner for one job, whose files hold its own credential alone": noJobRunners,
		},
	})
	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

// TestActionsOnGitHub covers #78: the runs of a commit and of a tag, a
// run's jobs, the failed ones run again, and the log a check links to.
func TestActionsOnGitHub(t *testing.T) {
	s := newShop(t)
	t.Run("the runs of a commit and of a tag, newest first, each with its workflow and ref", s.runs)
	t.Run("a run's jobs, and its failed ones run again as a new attempt", s.jobs)
	t.Run("the log of the job a check links to", s.logs)
}

func (s shop) runs(t *testing.T) {
	if err := s.h.CreateTag(s.ctx, s.repo, "v1.0.0", s.head, "Release"); err != nil {
		t.Fatal(err)
	}
	pr := s.open(t, "task/runs", scaffold.Tree{"src/r.txt": {Data: []byte("r\n")}})
	onMain := s.gh.AddRun("acme", "shop", "check.yaml", "push", ports.DefaultBranch, s.head, 0)
	s.gh.AddJob("acme", "shop", onMain, "check", "completed", "success", "ok\n")
	onPull := s.gh.AddRun("acme", "shop", "check.yaml", "pull_request", "task/runs", pr.HeadSHA, pr.Number)
	s.gh.AddJob("acme", "shop", onPull, "check", "in_progress", "", "")
	onTag := s.gh.AddRun("acme", "shop", "release.yaml", "push", "v1.0.0", s.head, 0)
	ci := s.h.(ports.CI)

	got, err := ci.CommitRuns(s.ctx, s.repo, s.head)
	if err != nil || len(got) != 2 {
		t.Fatalf("CommitRuns() = %+v, %v; want the tag's run, then main's", got, err)
	}
	// GitHub names a tag where it names a branch: only TagRuns knows which.
	want := []ports.WorkflowRun{
		{ID: onTag, Workflow: "release.yaml", Event: "push", Ref: "refs/heads/v1.0.0", Status: "queued"},
		{ID: onMain, Workflow: "check.yaml", Event: "push", Ref: "refs/heads/main", Status: "completed", Conclusion: "success"},
	}
	for i, w := range want {
		g := got[i]
		page := fmt.Sprintf("/acme/shop/actions/runs/%d", w.ID)
		if g.ID != w.ID || g.Workflow != w.Workflow || g.Event != w.Event || g.Ref != w.Ref || g.Status != w.Status ||
			g.Conclusion != w.Conclusion || !strings.HasSuffix(g.URL, page) || g.Started.IsZero() == (w.Status != "queued") {
			t.Errorf("CommitRuns()[%d] = %+v, want %+v at %s, started unless queued", i, g, w, page)
		}
	}
	if got, err := ci.CommitRuns(s.ctx, s.repo, pr.HeadSHA); err != nil || len(got) != 1 || got[0].ID != onPull ||
		got[0].Ref != fmt.Sprintf("refs/pull/%d/head", pr.Number) || got[0].Status != "in_progress" {
		t.Errorf("CommitRuns() of the pull request's head = %+v, %v; want its one run, of refs/pull/%d/head", got, err,
			pr.Number)
	}
	if got, err := ci.TagRuns(s.ctx, s.repo, "v1.0.0", s.head); err != nil || len(got) != 1 || got[0].ID != onTag ||
		got[0].Ref != "refs/tags/v1.0.0" {
		t.Errorf("TagRuns() = %+v, %v; want the release's run, of refs/tags/v1.0.0", got, err)
	}
	for what, runs := range map[string]func() ([]ports.WorkflowRun, error){
		"a tag nothing ran for":        func() ([]ports.WorkflowRun, error) { return ci.TagRuns(s.ctx, s.repo, "v2.0.0", s.head) },
		"the tag at another commit":    func() ([]ports.WorkflowRun, error) { return ci.TagRuns(s.ctx, s.repo, "v1.0.0", pr.HeadSHA) },
		"main, which is no tag's push": func() ([]ports.WorkflowRun, error) { return ci.TagRuns(s.ctx, s.repo, "main", pr.HeadSHA) },
	} {
		if got, err := runs(); err != nil || len(got) != 0 {
			t.Errorf("TagRuns() of %s = %+v, %v; want none", what, got, err)
		}
	}
}

func (s shop) jobs(t *testing.T) {
	ci := s.h.(ports.CI)
	run := s.gh.AddRun("acme", "shop", "check.yaml", "push", ports.DefaultBranch, s.head, 0)
	passed, _ := s.gh.AddJob("acme", "shop", run, "lint", "completed", "success", "ok\n")
	failed, _ := s.gh.AddJob("acme", "shop", run, "test", "completed", "failure", "FAIL\n")
	jobs, err := ci.RunJobs(s.ctx, s.repo, run)
	if err != nil || len(jobs) != 2 || jobs[0].ID != passed || jobs[1].ID != failed || jobs[1].Status != "completed" ||
		jobs[1].Conclusion != "failure" || jobs[1].Completed.IsZero() || jobs[1].RunnerID == 0 || jobs[1].RunnerName == "" {
		t.Fatalf("RunJobs() = %+v, %v; want the two jobs, the second failed on a runner", jobs, err)
	}
	if err := ci.RerunFailedJobs(s.ctx, s.repo, run); err != nil {
		t.Fatalf("RerunFailedJobs() = %v", err)
	}
	again, err := ci.RunJobs(s.ctx, s.repo, run)
	if err != nil || len(again) != 1 || again[0].ID == failed || again[0].Status != "queued" || again[0].RunnerID != 0 ||
		!again[0].Completed.IsZero() {
		t.Errorf("RunJobs() after the re-run = %+v, %v; want the failed job alone, queued anew", again, err)
	}
	if err := ci.RerunFailedJobs(s.ctx, s.repo, run); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("RerunFailedJobs() of a run that is running = %v, want GitHub's refusal", err)
	}
	if _, err := ci.RunJobs(s.ctx, s.repo, run+1000); err == nil {
		t.Error("RunJobs() of a run GitHub lacks went through")
	}
}

func (s shop) logs(t *testing.T) {
	ci := s.h.(ports.CI)
	head := s.commit(t, "logged")
	run := s.gh.AddRun("acme", "shop", "check.yaml", "push", ports.DefaultBranch, head, 0)
	_, failed := s.gh.AddJob("acme", "shop", run, "check", "completed", "failure", "--- FAIL: TestShop\n")
	// The check's page is where the controllers get the address from.
	checks, err := s.h.CombinedStatus(s.ctx, s.repo, head)
	if err != nil || checks.State != ports.CheckFailure || checks.URL != failed {
		t.Fatalf("CombinedStatus() = %+v, %v; want the failed job's page %s", checks, err, failed)
	}
	for _, at := range []string{checks.URL, checks.URL + "?pr=1", checks.URL + "/"} {
		if log, err := ci.CheckLog(s.ctx, s.repo, at); err != nil || log != "--- FAIL: TestShop\n" {
			t.Errorf("CheckLog(%s) = %q, %v; want the job's log", at, log, err)
		}
	}
	_, running := s.gh.AddJob("acme", "shop", run, "slow", "in_progress", "", "")
	_, silent := s.gh.AddJob("acme", "shop", run, "lost", "completed", "cancelled", "")
	for what, at := range map[string]string{"a job still running": running, "a job that sent no line": silent} {
		if log, err := ci.CheckLog(s.ctx, s.repo, at); !errors.Is(err, ports.ErrNoCILog) {
			t.Errorf("CheckLog() of %s = %q, %v; want ErrNoCILog", what, log, err)
		}
	}
	for _, at := range []string{"https://example.com/status/1", s.gh.URL() + "/acme/shop/actions/runs/7", failed + "/x"} {
		if _, err := ci.CheckLog(s.ctx, s.repo, at); err == nil || !strings.Contains(err.Error(), "does not link to a job") {
			t.Errorf("CheckLog(%s) = %v, want it refused as no job's address", at, err)
		}
	}
}
