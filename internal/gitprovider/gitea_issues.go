package gitprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Issue is an issue on a repository.
type Issue struct {
	Number int
	Title  string
	Body   string
	// State is open or closed.
	State  string
	Labels []string
	URL    string
	// Milestone is the ID of the issue's milestone, or 0 when it has none.
	Milestone int64
	// MilestoneTitle is the milestone's title; MilestoneDue its due date, zero when
	// it has none.
	MilestoneTitle string
	MilestoneDue   time.Time
}

// Label is a label a repository should have.
type Label struct {
	Name        string
	Color       string
	Description string
	// Exclusive makes a scoped label ("scope/value") replace the others of its scope.
	Exclusive bool
}

// Review events, as Gitea names them.
const (
	ReviewApprove        = "APPROVED"
	ReviewRequestChanges = "REQUEST_CHANGES"
	ReviewComment        = "COMMENT"
)

// issuePageSize is how many issues one list call returns; maxIssuePages bounds a list.
const (
	issuePageSize = 50
	maxIssuePages = 20
)

// stateClosed is the state of a closed issue, pull request or milestone.
const stateClosed = "closed"

// fieldBody is the JSON field of an issue's, pull request's or release's text.
const fieldBody = "body"

// labelLocks holds one lock per repository, by URL, for EnsureLabels. Gitea
// takes two labels with one name and then filters issues by neither, so two
// reconcilers making sure of a new repository's labels at once (a Brief filing
// its issues while the Crew it made polls) take turns, each provider of this
// process included.
var labelLocks sync.Map // string -> chan struct{}, holding one token while locked

type giteaLabel struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Request bodies of the issue API.
type (
	giteaLabelOption struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
		Exclusive   bool   `json:"exclusive"`
	}
	giteaIssueLabels struct {
		Labels []string `json:"labels"`
	}
	giteaComment struct {
		Body string `json:"body"`
	}
	giteaNewIssue struct {
		Title     string  `json:"title"`
		Body      string  `json:"body"`
		Labels    []int64 `json:"labels"`
		Milestone int64   `json:"milestone,omitempty"`
	}
	giteaMilestoneOption struct {
		Title       string     `json:"title"`
		Description string     `json:"description"`
		DueOn       *time.Time `json:"due_on,omitempty"`
	}
)

type giteaMilestone struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	DueOn       *time.Time `json:"due_on"`
}

type giteaIssue struct {
	Number     int             `json:"number"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	State      string          `json:"state"`
	HTMLURL    string          `json:"html_url"`
	Labels     []giteaLabel    `json:"labels"`
	Milestone  *giteaMilestone `json:"milestone"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func (i giteaIssue) issue() Issue {
	labels := make([]string, 0, len(i.Labels))
	for _, l := range i.Labels {
		labels = append(labels, l.Name)
	}
	out := Issue{Number: i.Number, Title: i.Title, Body: i.Body, State: i.State, Labels: labels, URL: i.HTMLURL}
	if m := i.Milestone; m != nil {
		out.Milestone, out.MilestoneTitle = m.ID, m.Title
		if m.DueOn != nil {
			out.MilestoneDue = *m.DueOn
		}
	}
	return out
}

// issueQuery lists one page of issues in state, without pull requests.
func issueQuery(state string, page int) url.Values {
	return url.Values{"state": {state}, "type": {"issues"}, "limit": {strconv.Itoa(issuePageSize)},
		"page": {strconv.Itoa(page)}}
}

func issuePath(repo Repository, number int) string {
	return "/api/v1/repos/" + repoPath(repo) + "/issues/" + strconv.Itoa(number)
}

// EnsureLabels creates the labels repo doesn't have yet. It is safe to repeat,
// and to call at once from more than one provider of this process.
func (g *gitea) EnsureLabels(ctx context.Context, repo Repository, labels []Label) error {
	lock, _ := labelLocks.LoadOrStore(g.base.JoinPath(repoPath(repo)).String(), make(chan struct{}, 1))
	select {
	case lock.(chan struct{}) <- struct{}{}:
		defer func() { <-lock.(chan struct{}) }()
	case <-ctx.Done():
		return ctx.Err()
	}
	existing, err := g.labels(ctx, repo)
	if err != nil {
		return err
	}
	for _, l := range labels {
		if _, ok := existing[l.Name]; ok {
			continue
		}
		payload, _ := json.Marshal(giteaLabelOption(l))
		status, data, err := g.do(ctx, http.MethodPost, "/api/v1/repos/"+repoPath(repo)+"/labels", payload)
		if err != nil {
			return err
		}
		if status != http.StatusCreated {
			return fmt.Errorf("gitea: creating label %s in %s failed (HTTP %d): %s", l.Name, repoPath(repo), status, apiMessage(data))
		}
	}
	return nil
}

// labels returns the repository's labels by name.
func (g *gitea) labels(ctx context.Context, repo Repository) (map[string]int64, error) {
	ids := map[string]int64{}
	for page := 1; page <= maxIssuePages; page++ {
		path := fmt.Sprintf("/api/v1/repos/%s/labels?limit=%d&page=%d", repoPath(repo), issuePageSize, page)
		status, data, err := g.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var labels []giteaLabel
		if status != http.StatusOK || json.Unmarshal(data, &labels) != nil {
			return nil, fmt.Errorf("gitea: listing labels of %s failed (HTTP %d): %s", repoPath(repo), status, apiMessage(data))
		}
		for _, l := range labels {
			ids[l.Name] = l.ID
		}
		if len(labels) < issuePageSize {
			break
		}
	}
	return ids, nil
}

// EnsureMilestone returns the ID of repo's milestone titled title, open or closed,
// and creates it, open and with description, if there is none. It leaves an
// existing milestone as it is.
func (g *gitea) EnsureMilestone(ctx context.Context, repo Repository, title, description string) (int64, error) {
	milestones, err := g.ListAllMilestones(ctx, repo)
	if err != nil {
		return 0, err
	}
	for _, m := range milestones {
		if m.Title == title {
			return m.ID, nil
		}
	}
	created, err := g.CreateMilestone(ctx, repo, title, description, time.Time{})
	return created.ID, err
}

// ListIssues returns the open issues of repo labelled label, without pull requests.
func (g *gitea) ListIssues(ctx context.Context, repo Repository, label string) ([]Issue, error) {
	var issues []Issue
	for page := 1; page <= maxIssuePages; page++ {
		query := issueQuery("open", page)
		if label != "" {
			query.Set("labels", label)
		}
		status, data, err := g.do(ctx, http.MethodGet, "/api/v1/repos/"+repoPath(repo)+"/issues?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var batch []giteaIssue
		if status != http.StatusOK || json.Unmarshal(data, &batch) != nil {
			return nil, fmt.Errorf("gitea: listing issues of %s failed (HTTP %d): %s", repoPath(repo), status, apiMessage(data))
		}
		for _, i := range batch {
			issues = append(issues, i.issue())
		}
		if len(batch) < issuePageSize {
			break
		}
	}
	return issues, nil
}

// GetIssue reads one issue.
func (g *gitea) GetIssue(ctx context.Context, repo Repository, number int) (Issue, error) {
	status, data, err := g.do(ctx, http.MethodGet, issuePath(repo, number), nil)
	if err != nil {
		return Issue{}, err
	}
	if status == http.StatusNotFound {
		return Issue{}, fmt.Errorf("gitea: issue %d of %s: %w", number, repoPath(repo), ErrIssueNotFound)
	}
	var issue giteaIssue
	if status != http.StatusOK || json.Unmarshal(data, &issue) != nil {
		return Issue{}, fmt.Errorf("gitea: reading issue %d of %s failed (HTTP %d): %s", number, repoPath(repo), status, apiMessage(data))
	}
	return issue.issue(), nil
}

// OpenBlockers returns the open issues an issue depends on: "#3" in the same
// repository, "owner/name#3" in another.
func (g *gitea) OpenBlockers(ctx context.Context, repo Repository, number int) ([]string, error) {
	status, data, err := g.do(ctx, http.MethodGet, issuePath(repo, number)+"/dependencies?limit=50", nil)
	if err != nil {
		return nil, err
	}
	var deps []giteaIssue
	if status != http.StatusOK || json.Unmarshal(data, &deps) != nil {
		return nil, fmt.Errorf("gitea: reading the dependencies of issue %d of %s failed (HTTP %d): %s",
			number, repoPath(repo), status, apiMessage(data))
	}
	var open []string
	self := repo.Owner + "/" + repo.Name
	for _, d := range deps {
		if d.State != "open" {
			continue
		}
		ref := "#" + strconv.Itoa(d.Number)
		if d.Repository.FullName != "" && d.Repository.FullName != self {
			ref = d.Repository.FullName + ref
		}
		open = append(open, ref)
	}
	return open, nil
}

// AddIssueDependency makes issue number of repo depend on issue blocker of
// blockerRepo, which may be repo itself or another repository. It is safe to
// repeat.
func (g *gitea) AddIssueDependency(ctx context.Context, repo Repository, number int, blockerRepo Repository, blocker int) error {
	path := issuePath(repo, number) + "/dependencies"
	status, data, err := g.do(ctx, http.MethodGet, path+"?limit=50", nil)
	if err != nil {
		return err
	}
	var deps []giteaIssue
	if status != http.StatusOK || json.Unmarshal(data, &deps) != nil {
		return fmt.Errorf("gitea: reading the dependencies of issue %d of %s failed (HTTP %d): %s",
			number, repoPath(repo), status, apiMessage(data))
	}
	self, want := repo.Owner+"/"+repo.Name, blockerRepo.Owner+"/"+blockerRepo.Name
	if slices.ContainsFunc(deps, func(d giteaIssue) bool {
		of := d.Repository.FullName
		if of == "" {
			of = self
		}
		return d.Number == blocker && strings.EqualFold(of, want)
	}) {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{"owner": blockerRepo.Owner, "repo": blockerRepo.Name, "index": blocker})
	status, data, err = g.do(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("gitea: making issue %d of %s depend on %s#%d failed (HTTP %d): %s",
			number, repoPath(repo), want, blocker, status, apiMessage(data))
	}
	return nil
}

// AddIssueLabel adds a label to an issue by name. An exclusive scoped label
// replaces the issue's other labels of its scope.
func (g *gitea) AddIssueLabel(ctx context.Context, repo Repository, number int, label string) error {
	payload, _ := json.Marshal(giteaIssueLabels{Labels: []string{label}})
	status, data, err := g.do(ctx, http.MethodPost, issuePath(repo, number)+"/labels", payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("gitea: labelling issue %d %s failed (HTTP %d): %s", number, label, status, apiMessage(data))
	}
	return nil
}

// RemoveIssueLabel removes a label from an issue, if it has it.
func (g *gitea) RemoveIssueLabel(ctx context.Context, repo Repository, number int, label string) error {
	issue, err := g.issueLabels(ctx, repo, number)
	if err != nil {
		return err
	}
	for _, l := range issue {
		if l.Name != label {
			continue
		}
		status, data, err := g.do(ctx, http.MethodDelete,
			issuePath(repo, number)+"/labels/"+strconv.FormatInt(l.ID, 10), nil)
		if err != nil {
			return err
		}
		if status != http.StatusNoContent && status != http.StatusNotFound {
			return fmt.Errorf("gitea: removing label %s from issue %d failed (HTTP %d): %s", label, number, status, apiMessage(data))
		}
	}
	return nil
}

func (g *gitea) issueLabels(ctx context.Context, repo Repository, number int) ([]giteaLabel, error) {
	status, data, err := g.do(ctx, http.MethodGet, issuePath(repo, number)+"/labels", nil)
	if err != nil {
		return nil, err
	}
	var labels []giteaLabel
	if status != http.StatusOK || json.Unmarshal(data, &labels) != nil {
		return nil, fmt.Errorf("gitea: reading the labels of issue %d failed (HTTP %d): %s", number, status, apiMessage(data))
	}
	return labels, nil
}

// CommentIssue adds a comment to an issue or a pull request.
func (g *gitea) CommentIssue(ctx context.Context, repo Repository, number int, body string) error {
	payload, _ := json.Marshal(giteaComment{Body: body})
	status, data, err := g.do(ctx, http.MethodPost, issuePath(repo, number)+"/comments", payload)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("gitea: commenting on #%d of %s failed (HTTP %d): %s", number, repoPath(repo), status, apiMessage(data))
	}
	return nil
}

// CloseIssue closes an issue.
func (g *gitea) CloseIssue(ctx context.Context, repo Repository, number int) error {
	return g.editIssue(ctx, repo, number, map[string]any{"state": stateClosed})
}

// EditIssueBody replaces an issue's body.
func (g *gitea) EditIssueBody(ctx context.Context, repo Repository, number int, body string) error {
	return g.editIssue(ctx, repo, number, map[string]any{fieldBody: body})
}

// SetIssueMilestone moves an issue into the milestone with ID milestone, or
// out of every milestone when it is 0.
func (g *gitea) SetIssueMilestone(ctx context.Context, repo Repository, number int, milestone int64) error {
	return g.editIssue(ctx, repo, number, map[string]any{"milestone": milestone})
}

// AssignIssue makes users the issue's assignees.
func (g *gitea) AssignIssue(ctx context.Context, repo Repository, number int, users []string) error {
	return g.editIssue(ctx, repo, number, map[string]any{"assignees": users})
}

func (g *gitea) editIssue(ctx context.Context, repo Repository, number int, fields map[string]any) error {
	payload, _ := json.Marshal(fields)
	status, data, err := g.do(ctx, http.MethodPatch, issuePath(repo, number), payload)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("gitea: editing issue %d of %s failed (HTTP %d): %s", number, repoPath(repo), status, apiMessage(data))
	}
	return nil
}

// CreateIssue files an issue with labels, which must exist in the repository, in
// the milestone with ID milestone, or in none when it is 0.
func (g *gitea) CreateIssue(ctx context.Context, repo Repository, title, body string, labels []string,
	milestone int64) (Issue, error) {
	ids := make([]int64, 0, len(labels))
	if len(labels) > 0 {
		existing, err := g.labels(ctx, repo)
		if err != nil {
			return Issue{}, err
		}
		for _, name := range labels {
			id, ok := existing[name]
			if !ok {
				return Issue{}, fmt.Errorf("gitea: %s has no label %s", repoPath(repo), name)
			}
			ids = append(ids, id)
		}
	}
	payload, _ := json.Marshal(giteaNewIssue{Title: title, Body: body, Labels: ids, Milestone: milestone})
	status, data, err := g.do(ctx, http.MethodPost, "/api/v1/repos/"+repoPath(repo)+"/issues", payload)
	if err != nil {
		return Issue{}, err
	}
	var issue giteaIssue
	if status != http.StatusCreated || json.Unmarshal(data, &issue) != nil {
		return Issue{}, fmt.Errorf("gitea: filing issue %q in %s failed (HTTP %d): %s", title, repoPath(repo), status, apiMessage(data))
	}
	return issue.issue(), nil
}

// ReviewPullRequest posts a review of commit: ReviewApprove, ReviewRequestChanges
// or ReviewComment, with body.
func (g *gitea) ReviewPullRequest(ctx context.Context, repo Repository, number int, commit, event, body string) error {
	payload, _ := json.Marshal(struct {
		Event    string `json:"event"`
		Body     string `json:"body"`
		CommitID string `json:"commit_id"`
	}{event, body, commit})
	status, data, err := g.do(ctx, http.MethodPost,
		"/api/v1/repos/"+repoPath(repo)+"/pulls/"+strconv.Itoa(number)+"/reviews", payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("gitea: reviewing pull request %d failed (HTTP %d): %s", number, status, apiMessage(data))
	}
	return nil
}
