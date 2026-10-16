package gitprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Milestone is a milestone on a repository.
type Milestone struct {
	ID    int64
	Title string
	// Due is the milestone's due date, zero when it has none.
	Due time.Time
	// Description is the milestone's description, and Closed whether it is closed.
	Description string
	Closed      bool
}

func (m giteaMilestone) milestone() Milestone {
	out := Milestone{ID: m.ID, Title: m.Title, Description: m.Description, Closed: m.State == stateClosed}
	if m.DueOn != nil {
		out.Due = *m.DueOn
	}
	return out
}

func milestonePath(repo Repository, id int64) string {
	return "/api/v1/repos/" + repoPath(repo) + "/milestones/" + strconv.FormatInt(id, 10)
}

// ListMilestones returns repo's open milestones.
func (g *gitea) ListMilestones(ctx context.Context, repo Repository) ([]Milestone, error) {
	return g.listMilestones(ctx, repo, "open")
}

// ListAllMilestones returns repo's milestones, open and closed.
func (g *gitea) ListAllMilestones(ctx context.Context, repo Repository) ([]Milestone, error) {
	return g.listMilestones(ctx, repo, "all")
}

// listMilestones returns repo's milestones in state: open, closed or all. A
// repository with more milestones than it can list in full is an error, not cut
// short: a milestone left out could be an open release.
func (g *gitea) listMilestones(ctx context.Context, repo Repository, state string) ([]Milestone, error) {
	var out []Milestone
	for page := 1; ; page++ {
		if page > maxIssuePages {
			return nil, fmt.Errorf("gitea: listing %s milestones of %s: %d or more, too many to list in full", state,
				repoPath(repo), maxIssuePages*issuePageSize)
		}
		path := fmt.Sprintf("/api/v1/repos/%s/milestones?state=%s&limit=%d&page=%d", repoPath(repo), state,
			issuePageSize, page)
		status, data, err := g.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var milestones []giteaMilestone
		if status != http.StatusOK || json.Unmarshal(data, &milestones) != nil {
			return nil, fmt.Errorf("gitea: listing milestones of %s failed (HTTP %d): %s", repoPath(repo), status, apiMessage(data))
		}
		for _, m := range milestones {
			out = append(out, m.milestone())
		}
		if len(milestones) < issuePageSize {
			return out, nil
		}
	}
}

// CreateMilestone creates an open milestone in repo with description, due at
// due, or with no due date when due is zero, and returns it. It doesn't look for
// one of the same title first: a repeat makes a second milestone.
func (g *gitea) CreateMilestone(ctx context.Context, repo Repository, title, description string,
	due time.Time) (Milestone, error) {
	option := giteaMilestoneOption{Title: title, Description: description}
	if !due.IsZero() {
		option.DueOn = &due
	}
	payload, _ := json.Marshal(option)
	status, data, err := g.do(ctx, http.MethodPost, "/api/v1/repos/"+repoPath(repo)+"/milestones", payload)
	if err != nil {
		return Milestone{}, err
	}
	var created giteaMilestone
	if status != http.StatusCreated || json.Unmarshal(data, &created) != nil {
		return Milestone{}, fmt.Errorf("gitea: creating milestone %s in %s failed (HTTP %d): %s", title, repoPath(repo),
			status, apiMessage(data))
	}
	return created.milestone(), nil
}

// MilestoneIssues returns the numbers of the open and the closed issues in
// milestone id, without pull requests. A milestone with more issues than it can
// list in full is an error, not cut short: an issue left out could be open.
func (g *gitea) MilestoneIssues(ctx context.Context, repo Repository, id int64) ([]Issue, error) {
	var issues []Issue
	for page := 1; ; page++ {
		if page > maxIssuePages {
			return nil, fmt.Errorf("gitea: milestone %d of %s has more than %d issues", id, repoPath(repo),
				maxIssuePages*issuePageSize)
		}
		// Gitea matches milestones by title first, then by ID: a milestone titled
		// with this number would be listed instead.
		query := issueQuery("all", page)
		query.Set("milestones", strconv.FormatInt(id, 10))
		status, data, err := g.do(ctx, http.MethodGet, "/api/v1/repos/"+repoPath(repo)+"/issues?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var batch []giteaIssue
		if status != http.StatusOK || json.Unmarshal(data, &batch) != nil {
			return nil, fmt.Errorf("gitea: listing issues of milestone %d of %s failed (HTTP %d): %s",
				id, repoPath(repo), status, apiMessage(data))
		}
		for _, i := range batch {
			issues = append(issues, i.issue())
		}
		if len(batch) < issuePageSize {
			return issues, nil
		}
	}
}

// CloseMilestone closes milestone id, keeping its due date's day. It is safe to
// repeat.
//
// Gitea clears the due date of a milestone it edits unless the edit gives one,
// so the due date read first goes along; Gitea keeps that one to the end of
// its day.
func (g *gitea) CloseMilestone(ctx context.Context, repo Repository, id int64) error {
	status, data, err := g.do(ctx, http.MethodGet, milestonePath(repo, id), nil)
	if err != nil {
		return err
	}
	var current giteaMilestone
	if status != http.StatusOK || json.Unmarshal(data, &current) != nil {
		return fmt.Errorf("gitea: reading milestone %d of %s failed (HTTP %d): %s", id, repoPath(repo), status, apiMessage(data))
	}
	payload, _ := json.Marshal(struct {
		State string     `json:"state"`
		DueOn *time.Time `json:"due_on,omitempty"`
	}{stateClosed, current.DueOn})
	status, data, err = g.do(ctx, http.MethodPatch, milestonePath(repo, id), payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("gitea: closing milestone %d of %s failed (HTTP %d): %s", id, repoPath(repo), status, apiMessage(data))
	}
	return nil
}

// GetTag returns the commit tag points at, or "" when repo has no such tag. An
// annotated tag gives the commit it tags, not the tag object.
func (g *gitea) GetTag(ctx context.Context, repo Repository, tag string) (string, error) {
	status, data, err := g.do(ctx, http.MethodGet, "/api/v1/repos/"+repoPath(repo)+"/tags/"+url.PathEscape(tag), nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return "", nil
	}
	var t struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if status != http.StatusOK || json.Unmarshal(data, &t) != nil || t.Commit.SHA == "" {
		return "", fmt.Errorf("gitea: reading tag %s of %s failed (HTTP %d): %s", tag, repoPath(repo), status, apiMessage(data))
	}
	return t.Commit.SHA, nil
}

// CreateTag tags commit as tag, annotated with message. It fails if the tag
// exists, wherever it points.
func (g *gitea) CreateTag(ctx context.Context, repo Repository, tag, commit, message string) error {
	payload, _ := json.Marshal(map[string]string{"tag_name": tag, "target": commit, "message": message})
	status, data, err := g.do(ctx, http.MethodPost, "/api/v1/repos/"+repoPath(repo)+"/tags", payload)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("gitea: tagging %s as %s in %s failed (HTTP %d): %s", commit, tag, repoPath(repo), status, apiMessage(data))
	}
	return nil
}

// WorkflowRun is a CI workflow run.
type WorkflowRun struct {
	ID int64
	// Workflow is the workflow file it runs, such as release.yaml.
	Workflow string
	// Event is what started it: push, pull_request and the like.
	Event string
	// Ref is the ref it ran at, such as refs/heads/main or refs/pull/3/head.
	Ref string
	// Status is queued, waiting, in_progress or completed.
	Status string
	// Conclusion is how a completed run ended: success, failure, cancelled or
	// skipped.
	Conclusion string
	// Started is when a runner started it, zero while it waits for one.
	Started time.Time
	// URL is the run's page.
	URL string
}

// runPages bounds the runs TagRuns and CommitRuns read: a commit's runs are
// among the newest at it.
const runPages = 3

// TagRuns returns the CI workflow runs a push of tag at commit started, newest
// first: the push runs at commit whose workflow ran at refs/tags/<tag>.
func (g *gitea) TagRuns(ctx context.Context, repo Repository, tag, commit string) ([]WorkflowRun, error) {
	runs, err := g.listRuns(ctx, repo, commit, url.Values{"event": {"push"}})
	if err != nil {
		return nil, err
	}
	var out []WorkflowRun
	for _, r := range runs {
		if workflow, ok := strings.CutSuffix(r.path, "@refs/tags/"+tag); ok {
			r.Workflow = workflow
			out = append(out, r.WorkflowRun)
		}
	}
	return out, nil
}

// CommitRuns returns the CI workflow runs at commit, of any event, newest
// first.
func (g *gitea) CommitRuns(ctx context.Context, repo Repository, commit string) ([]WorkflowRun, error) {
	runs, err := g.listRuns(ctx, repo, commit, url.Values{})
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowRun, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.WorkflowRun)
	}
	return out, nil
}

// giteaRun is a workflow run with the path Gitea gives its workflow, such as
// check.yaml@refs/pull/3/head.
type giteaRun struct {
	WorkflowRun
	path string
}

// listRuns reads the runs at commit, newest first, up to runPages pages,
// narrowed by query.
func (g *gitea) listRuns(ctx context.Context, repo Repository, commit string, query url.Values) ([]giteaRun, error) {
	var out []giteaRun
	for page := 1; page <= runPages; page++ {
		query.Set("head_sha", commit)
		query.Set("limit", strconv.Itoa(issuePageSize))
		query.Set("page", strconv.Itoa(page))
		status, data, err := g.do(ctx, http.MethodGet, "/api/v1/repos/"+repoPath(repo)+"/actions/runs?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var runs struct {
			WorkflowRuns []struct {
				ID         int64     `json:"id"`
				Path       string    `json:"path"`
				Event      string    `json:"event"`
				HeadSHA    string    `json:"head_sha"`
				Status     string    `json:"status"`
				Conclusion string    `json:"conclusion"`
				StartedAt  time.Time `json:"started_at"`
				HTMLURL    string    `json:"html_url"`
			} `json:"workflow_runs"`
		}
		if status != http.StatusOK || json.Unmarshal(data, &runs) != nil {
			return nil, fmt.Errorf("gitea: listing the runs of %s failed (HTTP %d): %s", repoPath(repo), status, apiMessage(data))
		}
		for _, r := range runs.WorkflowRuns {
			if r.HeadSHA != commit {
				continue
			}
			workflow, ref, _ := strings.Cut(r.Path, "@")
			started := r.StartedAt
			// Gitea writes a run no runner has started as starting at the epoch.
			if started.Unix() <= 0 {
				started = time.Time{}
			}
			out = append(out, giteaRun{path: r.Path, WorkflowRun: WorkflowRun{ID: r.ID, Workflow: workflow,
				Event: r.Event, Ref: ref, Status: r.Status, Conclusion: r.Conclusion, Started: started, URL: r.HTMLURL}})
		}
		if len(runs.WorkflowRuns) < issuePageSize {
			break
		}
	}
	return out, nil
}

// runnerPages bounds the pages of runners RunnersOnline and Runners read.
const runnerPages = 10

// RunnersOnline returns how many of the instance's runners, global,
// organisation and repository ones, are online, idle or busy. known is false
// when Gitea doesn't say: an older Gitea without the call, a token that isn't
// an admin's, or a failed call.
func (g *gitea) RunnersOnline(ctx context.Context) (online int, known bool) {
	runners, err := g.listRunners(ctx, "/api/v1/admin/actions/runners", "the instance")
	if err != nil {
		return 0, false
	}
	for _, r := range runners {
		if r.Busy || r.online() {
			online++
		}
	}
	return online, true
}

// RunnerOnline reports whether runner id, global, organisation or repository,
// is online: false for one that is offline or no longer registered. known is false when Gitea doesn't say: an older Gitea without
// the call, a token that isn't an admin's, or a failed call.
func (g *gitea) RunnerOnline(ctx context.Context, id int64) (online, known bool) {
	status, data, err := g.do(ctx, http.MethodGet, "/api/v1/admin/actions/runners/"+strconv.FormatInt(id, 10), nil)
	switch {
	case err != nil:
		return false, false
	case status == http.StatusNotFound && strings.Contains(strings.ToLower(apiMessage(data)), "runner"):
		// Gitea says "runner with id N: resource does not exist", of a deleted
		// runner too; a bare 404 is an older Gitea without the call.
		return false, true
	case status != http.StatusOK:
		return false, false
	}
	var runner giteaRunner
	if json.Unmarshal(data, &runner) != nil {
		return false, false
	}
	// Not Busy: a runner that stopped mid-job keeps the job Gitea gave it.
	return runner.online(), true
}

// pageQuery asks for page of a list Gitea pages.
func pageQuery(page int) url.Values {
	return url.Values{"limit": {strconv.Itoa(issuePageSize)}, "page": {strconv.Itoa(page)}}
}

// WorkflowJob is a job of a CI workflow run.
type WorkflowJob struct {
	ID int64
	// Status is queued, waiting, in_progress or completed, and Conclusion how a
	// completed job ended.
	Status     string
	Conclusion string
	// RunnerID is the runner that ran it, zero when none started it.
	RunnerID int64
	// Completed is when it ended, zero while it runs.
	Completed time.Time
}

// jobPages bounds the jobs RunJobs reads.
const jobPages = 3

// RunJobs returns the jobs of workflow run id, of its newest attempt.
func (g *gitea) RunJobs(ctx context.Context, repo Repository, id int64) ([]WorkflowJob, error) {
	var out []WorkflowJob
	for page := 1; page <= jobPages; page++ {
		path := "/api/v1/repos/" + repoPath(repo) + "/actions/runs/" + strconv.FormatInt(id, 10) + "/jobs?" +
			pageQuery(page).Encode()
		status, data, err := g.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var jobs struct {
			Jobs []struct {
				ID          int64     `json:"id"`
				Status      string    `json:"status"`
				Conclusion  string    `json:"conclusion"`
				RunnerID    int64     `json:"runner_id"`
				CompletedAt time.Time `json:"completed_at"`
			} `json:"jobs"`
		}
		if status != http.StatusOK || json.Unmarshal(data, &jobs) != nil {
			return nil, fmt.Errorf("gitea: listing the jobs of run %d of %s failed (HTTP %d): %s", id,
				repoPath(repo), status, apiMessage(data))
		}
		for _, j := range jobs.Jobs {
			completed := j.CompletedAt
			// Gitea writes a job that hasn't ended as ending at the epoch.
			if completed.Unix() <= 0 {
				completed = time.Time{}
			}
			out = append(out, WorkflowJob{ID: j.ID, Status: j.Status, Conclusion: j.Conclusion,
				RunnerID: j.RunnerID, Completed: completed})
		}
		if len(jobs.Jobs) < issuePageSize {
			break
		}
	}
	return out, nil
}

// RerunFailedJobs runs the failed jobs of workflow run id again, as a new
// attempt of the same run.
func (g *gitea) RerunFailedJobs(ctx context.Context, repo Repository, id int64) error {
	path := "/api/v1/repos/" + repoPath(repo) + "/actions/runs/" + strconv.FormatInt(id, 10) + "/rerun-failed-jobs"
	status, data, err := g.do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("gitea: re-running run %d of %s failed (HTTP %d): %s", id, repoPath(repo), status, apiMessage(data))
	}
	return nil
}

// Release is a release on a repository, with the files attached to it.
type Release struct {
	ID int64
	// Tag is the tag it releases.
	Tag  string
	Name string
	// Body is its notes, "" when nobody wrote any.
	Body  string
	Draft bool
	// URL is the release's page.
	URL    string
	Assets []ReleaseAsset
}

// ReleaseAsset is a file attached to a release.
type ReleaseAsset struct {
	ID   int64
	Name string
	Size int64
	// UUID names the file's download, independent of the release's tag.
	UUID string
}

type giteaRelease struct {
	ID      int64  `json:"id"`
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	Draft   bool   `json:"draft"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Size int64  `json:"size"`
		UUID string `json:"uuid"`
	} `json:"assets"`
}

// GetRelease returns the release of tag with its assets, a draft too, or nil
// when repo has none.
func (g *gitea) GetRelease(ctx context.Context, repo Repository, tag string) (*Release, error) {
	status, data, err := g.do(ctx, http.MethodGet, "/api/v1/repos/"+repoPath(repo)+"/releases/tags/"+url.PathEscape(tag), nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	var r giteaRelease
	if status != http.StatusOK || json.Unmarshal(data, &r) != nil || r.ID == 0 {
		return nil, fmt.Errorf("gitea: reading the release of %s in %s failed (HTTP %d): %s", tag, repoPath(repo), status,
			apiMessage(data))
	}
	rel := &Release{ID: r.ID, Tag: r.TagName, Name: r.Name, Body: r.Body, Draft: r.Draft, URL: r.HTMLURL}
	for _, a := range r.Assets {
		rel.Assets = append(rel.Assets, ReleaseAsset{ID: a.ID, Name: a.Name, Size: a.Size, UUID: a.UUID})
	}
	return rel, nil
}

// OpenReleaseAsset starts the download of asset and returns its body, which the
// caller closes, and its size in bytes. The body is streamed, never held whole,
// and fails as it is read if it is not that size. ctx bounds the whole
// download, which may take longer than the provider's timeout for one call.
func (g *gitea) OpenReleaseAsset(ctx context.Context, repo Repository, asset ReleaseAsset) (io.ReadCloser, int64, error) {
	if asset.UUID == "" {
		return nil, 0, fmt.Errorf("gitea: asset %s of %s has no download", asset.Name, repoPath(repo))
	}
	// The download is a web route, not under /api/v1; it is built on the
	// configured base, like the repository's addresses.
	req, err := g.request(ctx, http.MethodGet, "/attachments/"+url.PathEscape(asset.UUID), nil)
	if err != nil {
		return nil, 0, err
	}
	client := *g.http
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("gitea: downloading %s of %s: %w", asset.Name, repoPath(repo), err)
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("gitea: downloading %s of %s failed (HTTP %d): %s", asset.Name, repoPath(repo),
			resp.StatusCode, apiMessage(data))
	}
	if resp.ContentLength >= 0 && resp.ContentLength != asset.Size {
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("gitea: %s of %s is %d bytes, the release says %d", asset.Name, repoPath(repo),
			resp.ContentLength, asset.Size)
	}
	return &sizedBody{body: resp.Body, left: asset.Size,
		name: asset.Name + " of " + repoPath(repo)}, asset.Size, nil
}

// sizedBody is a download that must be exactly as long as the release says,
// also when the host sends no Content-Length.
type sizedBody struct {
	body io.ReadCloser
	left int64
	name string
}

func (b *sizedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		var one [1]byte
		if n, _ := b.body.Read(one[:]); n > 0 {
			return 0, fmt.Errorf("gitea: %s is longer than the release says", b.name)
		}
		return 0, io.EOF
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.body.Read(p)
	b.left -= int64(n)
	if errors.Is(err, io.EOF) {
		if b.left > 0 {
			return n, fmt.Errorf("gitea: %s ended %d bytes short: %w", b.name, b.left, io.ErrUnexpectedEOF)
		}
		err = nil
	}
	return n, err
}

func (b *sizedBody) Close() error { return b.body.Close() }

// SetReleaseBody replaces the notes of release id with body. Gitea keeps the
// notes it has when body is "".
func (g *gitea) SetReleaseBody(ctx context.Context, repo Repository, id int64, body string) error {
	payload, _ := json.Marshal(map[string]string{fieldBody: body})
	path := "/api/v1/repos/" + repoPath(repo) + "/releases/" + strconv.FormatInt(id, 10)
	status, data, err := g.do(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("gitea: setting the notes of release %d of %s failed (HTTP %d): %s", id, repoPath(repo), status,
			apiMessage(data))
	}
	return nil
}
