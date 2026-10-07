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

// listMilestones returns repo's milestones in state: open, closed or all.
func (g *gitea) listMilestones(ctx context.Context, repo Repository, state string) ([]Milestone, error) {
	var out []Milestone
	for page := 1; page <= maxIssuePages; page++ {
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
			break
		}
	}
	return out, nil
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
	// Status is queued, waiting, in_progress or completed.
	Status string
	// Conclusion is how a completed run ended: success, failure, cancelled or
	// skipped.
	Conclusion string
	// URL is the run's page.
	URL string
}

// tagRunPages bounds the runs TagRuns reads: a tag's runs are among the newest
// at its commit.
const tagRunPages = 3

// TagRuns returns the CI workflow runs a push of tag at commit started, newest
// first: the push runs at commit whose workflow ran at refs/tags/<tag>.
func (g *gitea) TagRuns(ctx context.Context, repo Repository, tag, commit string) ([]WorkflowRun, error) {
	suffix := "@refs/tags/" + tag
	var out []WorkflowRun
	for page := 1; page <= tagRunPages; page++ {
		query := url.Values{"event": {"push"}, "head_sha": {commit}, "limit": {strconv.Itoa(issuePageSize)},
			"page": {strconv.Itoa(page)}}
		status, data, err := g.do(ctx, http.MethodGet, "/api/v1/repos/"+repoPath(repo)+"/actions/runs?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var runs struct {
			WorkflowRuns []struct {
				ID         int64  `json:"id"`
				Path       string `json:"path"`
				HeadSHA    string `json:"head_sha"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				HTMLURL    string `json:"html_url"`
			} `json:"workflow_runs"`
		}
		if status != http.StatusOK || json.Unmarshal(data, &runs) != nil {
			return nil, fmt.Errorf("gitea: listing the runs of %s failed (HTTP %d): %s", repoPath(repo), status, apiMessage(data))
		}
		for _, r := range runs.WorkflowRuns {
			workflow, ok := strings.CutSuffix(r.Path, suffix)
			if !ok || r.HeadSHA != commit {
				continue
			}
			out = append(out, WorkflowRun{ID: r.ID, Workflow: workflow, Status: r.Status, Conclusion: r.Conclusion,
				URL: r.HTMLURL})
		}
		if len(runs.WorkflowRuns) < issuePageSize {
			break
		}
	}
	return out, nil
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
