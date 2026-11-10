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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// The CI port on GitHub Actions, step 5 of docs/plans/github-host.md (#78).
//
// GitHub's own runners take the jobs, so the host has no runners of the
// owner to count and none to register for a job: RunnersOnline says it
// doesn't know, and the adapter is no ports.JobRunners or
// ports.OwnerRunners. A pool of just-in-time runners is a later step.

// ghRun is a workflow run as GitHub lists it.
type ghRun struct {
	ID int64 `json:"id"`
	// Path is the workflow's file, such as .github/workflows/release.yaml.
	Path  string `json:"path"`
	Event string `json:"event"`
	// HeadBranch is the branch a run is of, or the tag a push of one
	// started it for.
	HeadBranch   string     `json:"head_branch"`
	HeadSHA      string     `json:"head_sha"`
	Status       string     `json:"status"`
	Conclusion   *string    `json:"conclusion"`
	RunStartedAt *time.Time `json:"run_started_at"`
	HTMLURL      string     `json:"html_url"`
	PullRequests []struct {
		Number int `json:"number"`
	} `json:"pull_requests"`
}

// run is r as the port has it. GitHub names no ref, only the branch or tag:
// a pull request's run is of its head, any other of a branch.
func (r ghRun) run() ports.WorkflowRun {
	out := ports.WorkflowRun{ID: r.ID, Workflow: path.Base(r.Path), Event: r.Event, Ref: "refs/heads/" + r.HeadBranch,
		Status: r.Status, URL: r.HTMLURL}
	if r.Conclusion != nil {
		out.Conclusion = *r.Conclusion
	}
	// A run waiting for a runner has not started, whatever time it was made.
	if r.RunStartedAt != nil && r.Status != "queued" && r.Status != "waiting" {
		out.Started = *r.RunStartedAt
	}
	if r.Event == "pull_request" && len(r.PullRequests) > 0 {
		out.Ref = "refs/pull/" + strconv.Itoa(r.PullRequests[0].Number) + "/head"
	}
	return out
}

// maxRunPages bounds how many pages of a commit's runs are read, 100 a page.
const maxRunPages = 3

// runs lists the workflow runs at commit that query selects, newest first.
func (h *host) runs(ctx context.Context, repo ports.Repository, commit string, query url.Values) ([]ghRun, error) {
	var out []ghRun
	what := "listing the workflow runs of " + commit + " in " + repo.Owner + "/" + repo.Name
	for page := 1; page <= maxRunPages; page++ {
		query.Set("head_sha", commit)
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		status, data, err := h.do(ctx, http.MethodGet, h.repoPath(repo)+"/actions/runs?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, failed(what, status, data)
		}
		var listed struct {
			WorkflowRuns []ghRun `json:"workflow_runs"`
		}
		if err := json.Unmarshal(data, &listed); err != nil {
			return nil, fmt.Errorf("github: %s: %w", what, err)
		}
		for _, r := range listed.WorkflowRuns {
			if r.HeadSHA == commit {
				out = append(out, r)
			}
		}
		if len(listed.WorkflowRuns) < 100 {
			break
		}
	}
	return out, nil
}

// CommitRuns returns the workflow runs at commit, of any event, newest first.
func (h *host) CommitRuns(ctx context.Context, repo ports.Repository, commit string) ([]ports.WorkflowRun, error) {
	listed, err := h.runs(ctx, repo, commit, url.Values{})
	if err != nil {
		return nil, err
	}
	out := make([]ports.WorkflowRun, 0, len(listed))
	for _, r := range listed {
		out = append(out, r.run())
	}
	return out, nil
}

// TagRuns returns the workflow runs a push of tag at commit started, newest
// first. GitHub names the tag where it names a branch's runs their branch.
func (h *host) TagRuns(ctx context.Context, repo ports.Repository, tag, commit string) ([]ports.WorkflowRun, error) {
	listed, err := h.runs(ctx, repo, commit, url.Values{"event": {"push"}, "branch": {tag}})
	if err != nil {
		return nil, err
	}
	var out []ports.WorkflowRun
	for _, r := range listed {
		if r.Event == "push" && r.HeadBranch == tag {
			run := r.run()
			run.Ref = "refs/tags/" + tag
			out = append(out, run)
		}
	}
	return out, nil
}

// RunnersOnline says it doesn't know: the jobs run on GitHub's own runners,
// which the API counts for nobody.
func (h *host) RunnersOnline(context.Context) (online int, known bool) { return 0, false }

// RunnerOnline says it doesn't know, as RunnersOnline does.
func (h *host) RunnerOnline(context.Context, int64) (online, known bool) { return false, false }

// RunJobs returns the jobs of workflow run id, of its newest attempt.
func (h *host) RunJobs(ctx context.Context, repo ports.Repository, id int64) ([]ports.WorkflowJob, error) {
	raw, err := h.listIn(ctx, h.repoPath(repo)+"/actions/runs/"+strconv.FormatInt(id, 10)+"/jobs",
		url.Values{"filter": {"latest"}}, "jobs")
	if err != nil {
		return nil, err
	}
	jobs := make([]ports.WorkflowJob, 0, len(raw))
	for _, r := range raw {
		var j struct {
			ID          int64      `json:"id"`
			Status      string     `json:"status"`
			Conclusion  *string    `json:"conclusion"`
			RunnerID    *int64     `json:"runner_id"`
			RunnerName  *string    `json:"runner_name"`
			CompletedAt *time.Time `json:"completed_at"`
		}
		if err := json.Unmarshal(r, &j); err != nil || j.ID == 0 {
			return nil, fmt.Errorf("github: reading the jobs of run %d of %s/%s: no job in the answer", id, repo.Owner,
				repo.Name)
		}
		job := ports.WorkflowJob{ID: j.ID, Status: j.Status}
		if j.Conclusion != nil {
			job.Conclusion = *j.Conclusion
		}
		if j.RunnerID != nil {
			job.RunnerID = *j.RunnerID
		}
		if j.RunnerName != nil {
			job.RunnerName = *j.RunnerName
		}
		if j.CompletedAt != nil {
			job.Completed = *j.CompletedAt
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// listIn reads every page of a list GitHub wraps in an object, under key.
func (h *host) listIn(ctx context.Context, at string, query url.Values, key string) ([]json.RawMessage, error) {
	var out []json.RawMessage
	for page := 1; page <= maxRunPages; page++ {
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		status, data, err := h.do(ctx, http.MethodGet, at+"?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, failed("reading "+at, status, data)
		}
		var wrapped map[string]json.RawMessage
		var items []json.RawMessage
		if err := json.Unmarshal(data, &wrapped); err != nil {
			return nil, fmt.Errorf("github: reading %s: %w", at, err)
		}
		if err := json.Unmarshal(wrapped[key], &items); err != nil {
			return nil, fmt.Errorf("github: reading %s: no %s in the answer", at, key)
		}
		out = append(out, items...)
		if len(items) < 100 {
			break
		}
	}
	return out, nil
}

// RerunFailedJobs runs the failed jobs of workflow run id again, as a new
// attempt of the same run.
func (h *host) RerunFailedJobs(ctx context.Context, repo ports.Repository, id int64) error {
	status, data, err := h.do(ctx, http.MethodPost,
		h.repoPath(repo)+"/actions/runs/"+strconv.FormatInt(id, 10)+"/rerun-failed-jobs", struct{}{})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return failed("running the failed jobs of run "+strconv.FormatInt(id, 10)+" of "+repo.Owner+"/"+repo.Name+
			" again", status, data)
	}
	return nil
}

// maxCheckLog bounds the log CheckLog reads; a longer one is read to there.
const maxCheckLog = 8 << 20

// CheckLog returns the log of the job a check links to, as a check run of
// Actions does: .../actions/runs/<run>/job/<job>. GitHub keeps no log of a
// job that sent none, and gives none of one still running: ErrNoCILog.
func (h *host) CheckLog(ctx context.Context, repo ports.Repository, checkURL string) (string, error) {
	_, job, ok := strings.Cut(checkURL, "/job/")
	job, _, _ = strings.Cut(job, "?")
	job = strings.Trim(job, "/")
	if _, err := strconv.ParseInt(job, 10, 64); !ok || err != nil {
		return "", fmt.Errorf("github: %q does not link to a job of GitHub Actions", checkURL)
	}
	// GitHub answers with a redirect to where the log is, which the client
	// follows, leaving the token behind.
	resp, err := h.download(ctx, h.repoPath(repo)+"/actions/jobs/"+job+"/logs", "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCheckLog))
	if err != nil {
		return "", fmt.Errorf("github: reading the log of job %s: %w", job, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusOK && strings.TrimSpace(string(data)) == "":
		return "", fmt.Errorf("github: job %s of %s/%s: %w", job, repo.Owner, repo.Name, ports.ErrNoCILog)
	case resp.StatusCode != http.StatusOK:
		return "", failed("reading the log of job "+job+" of "+repo.Owner+"/"+repo.Name, resp.StatusCode, data)
	}
	return string(data), nil
}
