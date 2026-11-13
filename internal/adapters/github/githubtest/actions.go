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

package githubtest

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// The fake's GitHub Actions, step 5 of docs/plans/github-host.md (#78): the
// runs and jobs a test says ran, as GitHub lists them. Nothing runs here.
//
//	GET  /repos/{o}/{r}/actions/runs?head_sha=&event=&branch=   the runs, newest first; branch is a tag's name for
//	                                                            the push of a tag
//	GET  /repos/{o}/{r}/actions/runs/{id}/jobs?filter=latest    the jobs of a run's newest attempt
//	POST /repos/{o}/{r}/actions/runs/{id}/rerun-failed-jobs     queue its failed jobs again, as a new attempt; 403
//	                                                            while the run is not completed
//	GET  /repos/{o}/{r}/actions/jobs/{id}/logs                  a redirect to the job's log; 404 for a job with none
//
// https://docs.github.com/en/rest/actions/workflow-runs and
// /rest/actions/workflow-jobs. A job reports as a check run of its name on
// the run's commit, which is where a branch rule reads it (host.go).

type run struct {
	id                      int64
	workflow, event, branch string
	sha                     string
	pull                    int
	created                 time.Time
	attempt                 int
	jobs                    []*job
}

type job struct {
	id                       int64
	name, status, conclusion string
	attempt                  int
	completed                time.Time
	log                      string
}

// The states of a run and of a job.
const (
	statusQueued     = "queued"
	statusInProgress = "in_progress"
	statusCompleted  = "completed"
)

// AddRun says a workflow ran: the file workflow under .github/workflows/,
// started by event for branch (a tag's name for the push of a tag) at the
// commit sha, for pull request pull when that is not 0. It returns the
// run's id; the run is queued until it has a job.
func (s *Server) AddRun(owner, name, workflow, event, branch, sha string, pull int) int64 {
	s.mu.Lock()
	defer s.done()
	kept := s.repos[owner+"/"+name]
	s.nextID++
	made := &run{id: s.nextID, workflow: workflow, event: event, branch: branch, sha: sha,
		pull: pull, created: time.Now().UTC().Truncate(time.Second), attempt: 1}
	kept.runs = append(kept.runs, made)
	s.runEvent(kept, made, "requested")
	return made.id
}

// AddJob says a job of run is at status, queued, in_progress or completed,
// a completed one with a conclusion such as success or failure, and what it
// logged, "" for no line. The job reports as a check run of its name on the
// run's commit. It returns the job's id and the address of its page, which
// is what a check links to.
func (s *Server) AddJob(owner, name string, runID int64, jobName, status, conclusion, log string) (int64, string) {
	s.mu.Lock()
	defer s.done()
	kept := s.repos[owner+"/"+name]
	r := kept.runs[slices.IndexFunc(kept.runs, func(r *run) bool { return r.id == runID })]
	s.nextID++
	j := &job{id: s.nextID, name: jobName, status: status, conclusion: conclusion, attempt: r.attempt, log: log}
	if status == statusCompleted {
		j.completed = time.Now().UTC().Truncate(time.Second)
	}
	r.jobs = append(r.jobs, j)
	page := s.srv.URL + "/" + owner + "/" + name + "/actions/runs/" + strconv.FormatInt(r.id, 10) + "/job/" +
		strconv.FormatInt(j.id, 10)
	check := checkRun{Name: jobName, Status: status, HTMLURL: page}
	if conclusion != "" {
		check.Conclusion = &conclusion
	}
	kept.checkRuns[r.sha] = append(kept.checkRuns[r.sha], check)
	s.checkRunEvent(kept, check, r.sha, &r.branch, r.pulls())
	if state, _ := r.state(); state != statusQueued {
		s.runEvent(kept, r, state)
	}
	return j.id, page
}

// latest are the jobs of a run's newest attempt.
func (r *run) latest() []*job {
	var jobs []*job
	for _, j := range r.jobs {
		if j.attempt == r.attempt {
			jobs = append(jobs, j)
		}
	}
	return jobs
}

// state is the run's status and conclusion, by its newest attempt's jobs:
// queued without a job that started, completed once all are, a failure when
// one failed.
func (r *run) state() (status string, conclusion *string) {
	jobs := r.latest()
	started := slices.ContainsFunc(jobs, func(j *job) bool { return j.status != statusQueued })
	done := len(jobs) > 0 && !slices.ContainsFunc(jobs, func(j *job) bool { return j.status != statusCompleted })
	switch {
	case done:
		c := stateSuccess
		if slices.ContainsFunc(jobs, func(j *job) bool { return j.conclusion != stateSuccess && j.conclusion != "skipped" }) {
			c = stateFailure
		}
		return statusCompleted, &c
	case started:
		return statusInProgress, nil
	}
	return statusQueued, nil
}

var (
	runJobsPath = regexp.MustCompile(`^/actions/runs/(\d+)/jobs$`)
	rerunPath   = regexp.MustCompile(`^/actions/runs/(\d+)/rerun-failed-jobs$`)
	jobLogPath  = regexp.MustCompile(`^/actions/jobs/(\d+)/logs$`)
	actionsAt   = regexp.MustCompile(`^/actions/(runs|jobs)(/|$)`)
	logPath     = regexp.MustCompile(`^/_logs/(\d+)$`)
)

const runsRest = "/actions/runs"

// actionsAPI serves the calls of this file, and says whether rest was one.
func (s *Server) actionsAPI(w http.ResponseWriter, r *http.Request, kept *repo, rest string) bool {
	get := r.Method == http.MethodGet
	find := func(id string) *run {
		n, _ := strconv.ParseInt(id, 10, 64)
		if i := slices.IndexFunc(kept.runs, func(r *run) bool { return r.id == n }); i >= 0 {
			return kept.runs[i]
		}
		reply(w, http.StatusNotFound, message("Not Found"))
		return nil
	}
	switch {
	case get && rest == runsRest:
		s.listRuns(w, r, kept)
	case get && runJobsPath.MatchString(rest):
		if found := find(runJobsPath.FindStringSubmatch(rest)[1]); found != nil {
			jobs := found.jobs
			if r.URL.Query().Get("filter") != listAll {
				jobs = found.latest()
			}
			listed := make([]jobJSON, 0, len(jobs))
			for _, j := range jobs {
				listed = append(listed, s.jobJSON(kept, found, j))
			}
			reply(w, http.StatusOK, struct {
				TotalCount int       `json:"total_count"`
				Jobs       []jobJSON `json:"jobs"`
			}{len(listed), page(r.URL.Query(), listed)})
		}
	case r.Method == http.MethodPost && rerunPath.MatchString(rest):
		if found := find(rerunPath.FindStringSubmatch(rest)[1]); found != nil {
			s.rerun(w, found)
		}
	case get && jobLogPath.MatchString(rest):
		id, _ := strconv.ParseInt(jobLogPath.FindStringSubmatch(rest)[1], 10, 64)
		if j := s.job(id); j == nil || j.log == "" || j.status != statusCompleted {
			reply(w, http.StatusNotFound, message("Not Found"))
		} else {
			// The log is elsewhere, at an address that needs no token.
			http.Redirect(w, r, s.srv.URL+"/_logs/"+strconv.FormatInt(id, 10), http.StatusFound)
		}
	default:
		return false
	}
	return true
}

// runJSON and jobJSON are a run and a job as GitHub lists them.
type runJSON struct {
	ID           int64     `json:"id"`
	Path         string    `json:"path"`
	Event        string    `json:"event"`
	HeadBranch   string    `json:"head_branch"`
	HeadSHA      string    `json:"head_sha"`
	Status       string    `json:"status"`
	Conclusion   *string   `json:"conclusion"`
	RunAttempt   int       `json:"run_attempt"`
	RunStartedAt string    `json:"run_started_at"`
	PullRequests []pullRef `json:"pull_requests"`
	HTMLURL      string    `json:"html_url"`
}

type pullRef struct {
	Number int `json:"number"`
}

type jobJSON struct {
	ID          int64   `json:"id"`
	RunID       int64   `json:"run_id"`
	RunAttempt  int     `json:"run_attempt"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Conclusion  *string `json:"conclusion"`
	RunnerID    *int64  `json:"runner_id"`
	RunnerName  *string `json:"runner_name"`
	CompletedAt *string `json:"completed_at"`
	HTMLURL     string  `json:"html_url"`
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request, kept *repo) {
	q := r.URL.Query()
	listed := make([]runJSON, 0, len(kept.runs))
	for _, found := range slices.Backward(kept.runs) {
		if sha := q.Get("head_sha"); sha != "" && sha != found.sha {
			continue
		}
		if event := q.Get("event"); event != "" && event != found.event {
			continue
		}
		if branch := q.Get("branch"); branch != "" && branch != found.branch {
			continue
		}
		listed = append(listed, s.runJSON(kept, found))
	}
	reply(w, http.StatusOK, struct {
		TotalCount   int       `json:"total_count"`
		WorkflowRuns []runJSON `json:"workflow_runs"`
	}{len(listed), page(q, listed)})
}

// pulls are the pull requests a run is for, as its JSON lists them.
func (r *run) pulls() []pullRef {
	if r.pull == 0 {
		return []pullRef{}
	}
	return []pullRef{{r.pull}}
}

func (s *Server) runJSON(kept *repo, r *run) runJSON {
	status, conclusion := r.state()
	return runJSON{ID: r.id, Path: ".github/workflows/" + r.workflow, Event: r.event,
		HeadBranch: r.branch, HeadSHA: r.sha, Status: status, Conclusion: conclusion,
		RunAttempt: r.attempt, RunStartedAt: r.created.Format(time.RFC3339), PullRequests: r.pulls(),
		HTMLURL: s.srv.URL + "/" + kept.owner + "/" + kept.name + "/actions/runs/" + strconv.FormatInt(r.id, 10)}
}

func (s *Server) jobJSON(kept *repo, r *run, j *job) jobJSON {
	out := jobJSON{ID: j.id, RunID: r.id, RunAttempt: j.attempt, Name: j.name, Status: j.status,
		HTMLURL: s.srv.URL + "/" + kept.owner + "/" + kept.name + "/actions/runs/" + strconv.FormatInt(r.id, 10) +
			"/job/" + strconv.FormatInt(j.id, 10)}
	if j.conclusion != "" {
		out.Conclusion = &j.conclusion
	}
	if j.status != statusQueued {
		// One of GitHub's own runners took it.
		id, name := int64(1), "GitHub Actions 1"
		out.RunnerID, out.RunnerName = &id, &name
	}
	if !j.completed.IsZero() {
		at := j.completed.Format(time.RFC3339)
		out.CompletedAt = &at
	}
	return out
}

// rerun queues the failed jobs of a completed run again, as its next attempt.
func (s *Server) rerun(w http.ResponseWriter, r *run) {
	if status, _ := r.state(); status != statusCompleted {
		reply(w, http.StatusForbidden, message("This workflow is already running"))
		return
	}
	failed := slices.DeleteFunc(r.latest(), func(j *job) bool { return j.conclusion == stateSuccess })
	r.attempt++
	for _, j := range failed {
		s.nextID++
		r.jobs = append(r.jobs, &job{id: s.nextID, name: j.name, status: statusQueued, attempt: r.attempt})
	}
	reply(w, http.StatusCreated, struct{}{})
}

// job finds a job by its id, in any repository.
func (s *Server) job(id int64) *job {
	for _, kept := range s.repos {
		for _, r := range kept.runs {
			for _, j := range r.jobs {
				if j.id == id {
					return j
				}
			}
		}
	}
	return nil
}

// serveLog serves a job's log where the API redirects to, and says whether
// r asked for one.
func (s *Server) serveLog(w http.ResponseWriter, r *http.Request) bool {
	m := logPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		return false
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	s.mu.Lock()
	j := s.job(id)
	s.mu.Unlock()
	if j == nil {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(j.log))
	return true
}
