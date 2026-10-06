package gitprovider

import (
	"context"
	"encoding/json"
	"fmt"
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
}

func milestonePath(repo Repository, id int64) string {
	return "/api/v1/repos/" + repoPath(repo) + "/milestones/" + strconv.FormatInt(id, 10)
}

// ListMilestones returns repo's open milestones.
func (g *gitea) ListMilestones(ctx context.Context, repo Repository) ([]Milestone, error) {
	var out []Milestone
	for page := 1; page <= maxIssuePages; page++ {
		path := fmt.Sprintf("/api/v1/repos/%s/milestones?state=open&limit=%d&page=%d", repoPath(repo), issuePageSize, page)
		status, data, err := g.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var milestones []giteaMilestone
		if status != http.StatusOK || json.Unmarshal(data, &milestones) != nil {
			return nil, fmt.Errorf("gitea: listing milestones of %s failed (HTTP %d): %s", repoPath(repo), status, apiMessage(data))
		}
		for _, m := range milestones {
			ms := Milestone{ID: m.ID, Title: m.Title}
			if m.DueOn != nil {
				ms.Due = *m.DueOn
			}
			out = append(out, ms)
		}
		if len(milestones) < issuePageSize {
			break
		}
	}
	return out, nil
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

// CloseMilestone closes milestone id. It is safe to repeat.
func (g *gitea) CloseMilestone(ctx context.Context, repo Repository, id int64) error {
	payload, _ := json.Marshal(struct {
		State string `json:"state"`
	}{stateClosed})
	status, data, err := g.do(ctx, http.MethodPatch, milestonePath(repo, id), payload)
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
