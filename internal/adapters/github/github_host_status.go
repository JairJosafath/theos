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
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/JairJosafath/theos/internal/ports"
)

// Commit statuses on GitHub, step 2 of docs/plans/github-host.md (#75).
// GitHub has two kinds of check on a commit: the statuses anyone reports
// with a context, and the check runs of an App, which is how Actions reports
// each job, under the job's name. The port has one kind, a state by context,
// so a check run reads as a status whose context is its name.

type ghStatus struct {
	Context     string `json:"context"`
	State       string `json:"state"`
	TargetURL   string `json:"target_url"`
	Description string `json:"description"`
}

type ghCheckRun struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
	HTMLURL    string  `json:"html_url"`
}

// state is the check run's state as the port has it: pending until it has
// completed, then success for a run that passed, was neutral or was skipped,
// and failure for any other conclusion.
func (c ghCheckRun) state() string {
	if c.Status != "completed" || c.Conclusion == nil {
		return ports.CheckPending
	}
	switch *c.Conclusion {
	case "success", "neutral", "skipped":
		return ports.CheckSuccess
	}
	return ports.CheckFailure
}

// maxStatusPages bounds how many pages of a commit's statuses and check runs
// are read, 100 a page.
const maxStatusPages = 10

// checkRuns returns the latest check run of each name on ref.
func (h *host) checkRuns(ctx context.Context, repo ports.Repository, ref string) ([]ghCheckRun, error) {
	var runs []ghCheckRun
	for page := 1; page <= maxStatusPages; page++ {
		query := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}, "filter": {"latest"}}
		status, data, err := h.do(ctx, http.MethodGet,
			h.repoPath(repo)+"/commits/"+pathEscapeRef(ref)+"/check-runs?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, failed("reading the check runs of "+ref+" in "+repo.Owner+"/"+repo.Name, status, data)
		}
		var body struct {
			TotalCount int          `json:"total_count"`
			CheckRuns  []ghCheckRun `json:"check_runs"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			return nil, fmt.Errorf("github: reading the check runs of %s in %s/%s: %w", ref, repo.Owner, repo.Name, err)
		}
		runs = append(runs, body.CheckRuns...)
		if len(body.CheckRuns) == 0 || len(runs) >= body.TotalCount {
			return runs, nil
		}
	}
	return nil, fmt.Errorf("github: %s in %s/%s has more than %d check runs", ref, repo.Owner, repo.Name,
		maxStatusPages*100)
}

// combined reads the latest status of each context on ref, and the commit
// ref names.
func (h *host) combined(ctx context.Context, repo ports.Repository, ref string) (string, []ghStatus, error) {
	var sha string
	var statuses []ghStatus
	for page := 1; page <= maxStatusPages; page++ {
		query := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		status, data, err := h.do(ctx, http.MethodGet,
			h.repoPath(repo)+"/commits/"+pathEscapeRef(ref)+"/status?"+query.Encode(), nil)
		if err != nil {
			return "", nil, err
		}
		if status != http.StatusOK {
			return "", nil, failed("reading the status of "+ref+" in "+repo.Owner+"/"+repo.Name, status, data)
		}
		var body struct {
			SHA        string     `json:"sha"`
			TotalCount int        `json:"total_count"`
			Statuses   []ghStatus `json:"statuses"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			return "", nil, fmt.Errorf("github: reading the status of %s in %s/%s: %w", ref, repo.Owner, repo.Name, err)
		}
		sha = body.SHA
		statuses = append(statuses, body.Statuses...)
		if len(body.Statuses) == 0 || len(statuses) >= body.TotalCount {
			return sha, statuses, nil
		}
	}
	return "", nil, fmt.Errorf("github: %s in %s/%s has more than %d statuses", ref, repo.Owner, repo.Name,
		maxStatusPages*100)
}

// CombinedStatus returns the state of the checks on ref, statuses and check
// runs alike, each by its context. GitHub calls a commit with no status
// pending; the port calls it nothing, and so does this. Of the checks: any
// failed is failure, else any in error is error, else any pending is
// pending, else success. The URL is the page of a check in that state, the
// first by name that has one.
func (h *host) CombinedStatus(ctx context.Context, repo ports.Repository, ref string) (ports.CheckStatus, error) {
	sha, statuses, err := h.combined(ctx, repo, ref)
	if err != nil {
		return ports.CheckStatus{}, err
	}
	runs, err := h.checkRuns(ctx, repo, ref)
	if err != nil {
		return ports.CheckStatus{}, err
	}
	out := ports.CheckStatus{Commit: sha}
	if len(statuses)+len(runs) == 0 {
		return out, nil
	}
	out.Contexts = make(map[string]string, len(statuses)+len(runs))
	pages := map[string]string{}
	for _, st := range statuses {
		out.Contexts[st.Context], pages[st.Context] = st.State, st.TargetURL
	}
	for _, run := range runs {
		out.Contexts[run.Name], pages[run.Name] = run.state(), run.HTMLURL
	}
	names := slices.Sorted(maps.Keys(out.Contexts))
	for _, state := range []string{ports.CheckFailure, ports.CheckError, ports.CheckPending, ports.CheckSuccess} {
		in := slices.DeleteFunc(slices.Clone(names), func(name string) bool { return out.Contexts[name] != state })
		if len(in) == 0 {
			continue
		}
		out.State = state
		for _, name := range in {
			if pages[name] != "" {
				out.URL = pages[name]
				break
			}
		}
		break
	}
	return out, nil
}

// CommitStatuses returns the latest status of each check on the commit sha:
// its statuses, newest first, and its check runs as statuses.
func (h *host) CommitStatuses(ctx context.Context, repo ports.Repository, sha string) ([]ports.CommitStatus, error) {
	raw, err := h.list(ctx, h.repoPath(repo)+"/commits/"+pathEscapeRef(sha)+"/statuses", nil)
	if err != nil {
		return nil, err
	}
	var out []ports.CommitStatus
	seen := map[string]bool{}
	for _, r := range raw {
		var st ghStatus
		if err := json.Unmarshal(r, &st); err != nil {
			return nil, fmt.Errorf("github: reading the statuses of %s in %s/%s: %w", sha, repo.Owner, repo.Name, err)
		}
		if !seen[st.Context] {
			seen[st.Context] = true
			out = append(out, ports.CommitStatus{Context: st.Context, State: st.State, TargetURL: st.TargetURL,
				Description: st.Description})
		}
	}
	runs, err := h.checkRuns(ctx, repo, sha)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if !seen[run.Name] {
			seen[run.Name] = true
			out = append(out, ports.CommitStatus{Context: run.Name, State: run.state(), TargetURL: run.HTMLURL})
		}
	}
	return out, nil
}

// maxStatusDescription is how long a status's description may be on GitHub.
const maxStatusDescription = 140

// SetCommitStatus writes a check's status onto the commit sha, its
// description cut to what GitHub takes.
func (h *host) SetCommitStatus(ctx context.Context, repo ports.Repository, sha string, st ports.CommitStatus) error {
	description := []rune(st.Description)
	if len(description) > maxStatusDescription {
		description = append(description[:maxStatusDescription-1], '…')
	}
	status, data, err := h.do(ctx, http.MethodPost, h.repoPath(repo)+"/statuses/"+url.PathEscape(sha), ghStatus{
		Context: st.Context, State: st.State, TargetURL: st.TargetURL, Description: string(description)})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return failed("setting the status "+st.Context+" of "+sha+" in "+repo.Owner+"/"+repo.Name, status, data)
	}
	return nil
}
