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
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// The Issues port on GitHub (docs/plans/github-host.md, step 3, #76). Two
// things GitHub doesn't do are done here: an exclusive scoped label (adding
// status/x takes the issue's other status/* labels off), and a close refused
// while an issue it depends on is open.

// exclusiveNote ends the description of a label whose scope is exclusive.
// GitHub has no such flag, so the adapter keeps it where a person sees it
// too.
const exclusiveNote = " (one per issue)"

// maxPages bounds a listing: at 100 a page, a list longer than this fails
// instead of coming back cut short.
const maxPages = 50

// labelScope is the scope of a scoped label, "status" of "status/ready", and
// "" for a label without one.
func labelScope(name string) string {
	if i := strings.LastIndex(name, "/"); i > 0 {
		return name[:i]
	}
	return ""
}

// list reads every page of a GET that answers a JSON array.
func (h *host) list(ctx context.Context, path string, query url.Values) ([]json.RawMessage, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("per_page", "100")
	var all []json.RawMessage
	for page := 1; page <= maxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		status, data, err := h.do(ctx, http.MethodGet, path+"?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, failed("listing "+path, status, data)
		}
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, fmt.Errorf("github: listing %s: %w", path, err)
		}
		all = append(all, items...)
		if len(items) < 100 {
			return all, nil
		}
	}
	return nil, fmt.Errorf("github: listing %s: more than %d entries", path, maxPages*100)
}

type ghLabel struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type ghMilestone struct {
	Number      int64      `json:"number"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	DueOn       *time.Time `json:"due_on"`
}

func (m ghMilestone) milestone() ports.Milestone {
	out := ports.Milestone{ID: m.Number, Title: m.Title, Description: m.Description, Closed: m.State == stateClosed}
	if m.DueOn != nil {
		out.Due = *m.DueOn
	}
	return out
}

type ghIssue struct {
	ID            int64            `json:"id"`
	Number        int              `json:"number"`
	Title         string           `json:"title"`
	Body          *string          `json:"body"`
	State         string           `json:"state"`
	HTMLURL       string           `json:"html_url"`
	RepositoryURL string           `json:"repository_url"`
	Labels        []ghLabel        `json:"labels"`
	Milestone     *ghMilestone     `json:"milestone"`
	PullRequest   *json.RawMessage `json:"pull_request"`
}

// stateOpen is GitHub's state of an open issue or milestone; stateClosed is
// in github_issues.go. queryState is the query parameter that filters by it.
const (
	stateOpen  = "open"
	queryState = "state"
)

func (i ghIssue) issue() ports.Issue {
	out := ports.Issue{Number: i.Number, Title: i.Title, State: i.State, URL: i.HTMLURL}
	if i.Body != nil {
		out.Body = *i.Body
	}
	for _, l := range i.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	if m := i.Milestone; m != nil {
		out.Milestone, out.MilestoneTitle = m.Number, m.Title
		if m.DueOn != nil {
			out.MilestoneDue = *m.DueOn
		}
	}
	return out
}

func (h *host) issuePath(repo ports.Repository, number int) string {
	return h.repoPath(repo) + "/issues/" + strconv.Itoa(number)
}

// EnsureLabels makes the labels the repository lacks, and makes the
// exclusive note of those it has match the one asked for. A label's colour
// and the rest of its description, which a person may have edited, stay.
func (h *host) EnsureLabels(ctx context.Context, repo ports.Repository, labels []ports.Label) error {
	raw, err := h.list(ctx, h.repoPath(repo)+"/labels", nil)
	if err != nil {
		return err
	}
	have := map[string]ghLabel{}
	for _, r := range raw {
		var l ghLabel
		if err := json.Unmarshal(r, &l); err != nil {
			return fmt.Errorf("github: reading a label: %w", err)
		}
		have[strings.ToLower(l.Name)] = l
	}
	for _, want := range labels {
		existing, ok := have[strings.ToLower(want.Name)]
		if !ok {
			description := want.Description
			if want.Exclusive {
				description += exclusiveNote
			}
			status, data, err := h.do(ctx, http.MethodPost, h.repoPath(repo)+"/labels", ghLabel{Name: want.Name,
				Color: strings.TrimPrefix(want.Color, "#"), Description: description})
			if err != nil {
				return err
			}
			if status != http.StatusCreated {
				return failed("creating label "+want.Name, status, data)
			}
			continue
		}
		if exclusive := strings.HasSuffix(existing.Description, exclusiveNote); exclusive != want.Exclusive {
			description := strings.TrimSuffix(existing.Description, exclusiveNote)
			if want.Exclusive {
				description += exclusiveNote
			}
			status, data, err := h.do(ctx, http.MethodPatch, h.repoPath(repo)+"/labels/"+url.PathEscape(existing.Name),
				struct {
					Description string `json:"description"`
				}{description})
			if err != nil {
				return err
			}
			if status != http.StatusOK {
				return failed("updating label "+want.Name, status, data)
			}
		}
	}
	return nil
}

// ListIssues returns the open issues labelled label, or all of them for "",
// without pull requests.
func (h *host) ListIssues(ctx context.Context, repo ports.Repository, label string) ([]ports.Issue, error) {
	query := url.Values{queryState: {stateOpen}}
	if label != "" {
		query.Set("labels", label)
	}
	return h.listIssues(ctx, repo, query)
}

func (h *host) listIssues(ctx context.Context, repo ports.Repository, query url.Values) ([]ports.Issue, error) {
	raw, err := h.list(ctx, h.repoPath(repo)+"/issues", query)
	if err != nil {
		return nil, err
	}
	out := make([]ports.Issue, 0, len(raw))
	for _, r := range raw {
		var i ghIssue
		if err := json.Unmarshal(r, &i); err != nil {
			return nil, fmt.Errorf("github: reading an issue: %w", err)
		}
		// GitHub lists pull requests among the issues.
		if i.PullRequest == nil {
			out = append(out, i.issue())
		}
	}
	return out, nil
}

// getIssue reads one issue as GitHub gives it; ErrIssueNotFound without it.
func (h *host) getIssue(ctx context.Context, repo ports.Repository, number int) (ghIssue, error) {
	status, data, err := h.do(ctx, http.MethodGet, h.issuePath(repo, number), nil)
	if err != nil {
		return ghIssue{}, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusGone:
		return ghIssue{}, fmt.Errorf("%w: %s/%s#%d", ports.ErrIssueNotFound, repo.Owner, repo.Name, number)
	default:
		return ghIssue{}, failed("reading issue "+strconv.Itoa(number), status, data)
	}
	var issue ghIssue
	if err := json.Unmarshal(data, &issue); err != nil {
		return ghIssue{}, fmt.Errorf("github: reading issue %d: %w", number, err)
	}
	return issue, nil
}

// GetIssue reads one issue; ErrIssueNotFound when there is none.
func (h *host) GetIssue(ctx context.Context, repo ports.Repository, number int) (ports.Issue, error) {
	issue, err := h.getIssue(ctx, repo, number)
	return issue.issue(), err
}

// patchIssue changes an issue's fields.
func (h *host) patchIssue(ctx context.Context, repo ports.Repository, number int, what string, fields any) error {
	status, data, err := h.do(ctx, http.MethodPatch, h.issuePath(repo, number), fields)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return failed(what+" issue "+strconv.Itoa(number), status, data)
	}
	return nil
}

// CreateIssue files an issue with labels, in milestone when it isn't 0.
func (h *host) CreateIssue(ctx context.Context, repo ports.Repository, title, body string, labels []string,
	milestone int64) (ports.Issue, error) {
	create := struct {
		Title     string   `json:"title"`
		Body      string   `json:"body"`
		Labels    []string `json:"labels,omitempty"`
		Milestone *int64   `json:"milestone,omitempty"`
	}{Title: title, Body: body, Labels: labels}
	if milestone != 0 {
		create.Milestone = &milestone
	}
	status, data, err := h.do(ctx, http.MethodPost, h.repoPath(repo)+"/issues", create)
	if err != nil {
		return ports.Issue{}, err
	}
	if status != http.StatusCreated {
		return ports.Issue{}, failed("filing an issue in "+repo.Owner+"/"+repo.Name, status, data)
	}
	var issue ghIssue
	if err := json.Unmarshal(data, &issue); err != nil {
		return ports.Issue{}, fmt.Errorf("github: filing an issue: %w", err)
	}
	return issue.issue(), nil
}

// AddIssueLabel adds a label. One whose scope is exclusive replaces the
// issue's other labels of that scope, in one call that sets the issue's
// labels, so two writers at once leave one of the scope, not two.
func (h *host) AddIssueLabel(ctx context.Context, repo ports.Repository, number int, label string) error {
	exclusive, err := h.exclusive(ctx, repo, label)
	if err != nil {
		return err
	}
	labels := struct {
		Labels []string `json:"labels"`
	}{[]string{label}}
	method := http.MethodPost
	if exclusive {
		issue, err := h.getIssue(ctx, repo, number)
		if err != nil {
			return err
		}
		scope := labelScope(label)
		labels.Labels = nil
		for _, l := range issue.Labels {
			if labelScope(l.Name) != scope && !strings.EqualFold(l.Name, label) {
				labels.Labels = append(labels.Labels, l.Name)
			}
		}
		labels.Labels = append(labels.Labels, label)
		method = http.MethodPut
	}
	status, data, err := h.do(ctx, method, h.issuePath(repo, number)+"/labels", labels)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return failed("labelling issue "+strconv.Itoa(number)+" "+label, status, data)
	}
	return nil
}

// exclusive reports whether label's scope is exclusive: it has a scope and
// its description ends in the note EnsureLabels keeps.
func (h *host) exclusive(ctx context.Context, repo ports.Repository, label string) (bool, error) {
	if labelScope(label) == "" {
		return false, nil
	}
	status, data, err := h.do(ctx, http.MethodGet, h.repoPath(repo)+"/labels/"+url.PathEscape(label), nil)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, failed("reading label "+label, status, data)
	}
	var l ghLabel
	if err := json.Unmarshal(data, &l); err != nil {
		return false, fmt.Errorf("github: reading label %s: %w", label, err)
	}
	return strings.HasSuffix(l.Description, exclusiveNote), nil
}

// RemoveIssueLabel removes a label from an issue, if it has it.
func (h *host) RemoveIssueLabel(ctx context.Context, repo ports.Repository, number int, label string) error {
	status, data, err := h.do(ctx, http.MethodDelete, h.issuePath(repo, number)+"/labels/"+url.PathEscape(label), nil)
	if err != nil {
		return err
	}
	// 404: the issue doesn't have the label.
	if status != http.StatusOK && status != http.StatusNotFound {
		return failed("unlabelling issue "+strconv.Itoa(number), status, data)
	}
	return nil
}

// CommentIssue adds a comment to an issue or a pull request.
func (h *host) CommentIssue(ctx context.Context, repo ports.Repository, number int, body string) error {
	status, data, err := h.do(ctx, http.MethodPost, h.issuePath(repo, number)+"/comments", struct {
		Body string `json:"body"`
	}{body})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return failed("commenting on issue "+strconv.Itoa(number), status, data)
	}
	return nil
}

// CloseIssue closes an issue; ErrOpenDependencies while an issue it depends
// on is open. GitHub would close it, so the adapter looks first.
func (h *host) CloseIssue(ctx context.Context, repo ports.Repository, number int) error {
	open, err := h.OpenBlockers(ctx, repo, number)
	if err != nil {
		return err
	}
	if len(open) > 0 {
		return fmt.Errorf("%w: %s", ports.ErrOpenDependencies, strings.Join(open, ", "))
	}
	return h.patchIssue(ctx, repo, number, "closing", struct {
		State string `json:"state"`
	}{stateClosed})
}

// EditIssueBody replaces an issue's body.
func (h *host) EditIssueBody(ctx context.Context, repo ports.Repository, number int, body string) error {
	return h.patchIssue(ctx, repo, number, "editing", struct {
		Body string `json:"body"`
	}{body})
}

// SetIssueMilestone moves an issue into milestone, or out of every one for 0.
func (h *host) SetIssueMilestone(ctx context.Context, repo ports.Repository, number int, milestone int64) error {
	fields := struct {
		Milestone *int64 `json:"milestone"`
	}{}
	if milestone != 0 {
		fields.Milestone = &milestone
	}
	return h.patchIssue(ctx, repo, number, "moving", fields)
}

// AssignIssue makes users the issue's assignees.
func (h *host) AssignIssue(ctx context.Context, repo ports.Repository, number int, users []string) error {
	if users == nil {
		users = []string{}
	}
	return h.patchIssue(ctx, repo, number, "assigning", struct {
		Assignees []string `json:"assignees"`
	}{users})
}

// blockedBy lists the issues number depends on, open and closed.
func (h *host) blockedBy(ctx context.Context, repo ports.Repository, number int) ([]ghIssue, error) {
	raw, err := h.list(ctx, h.issuePath(repo, number)+"/dependencies/blocked_by", nil)
	if err != nil {
		return nil, err
	}
	out := make([]ghIssue, 0, len(raw))
	for _, r := range raw {
		var i ghIssue
		if err := json.Unmarshal(r, &i); err != nil {
			return nil, fmt.Errorf("github: reading a dependency: %w", err)
		}
		out = append(out, i)
	}
	return out, nil
}

// OpenBlockers returns the open issues number depends on: "#3" in its own
// repository, "owner/name#3" in another.
func (h *host) OpenBlockers(ctx context.Context, repo ports.Repository, number int) ([]string, error) {
	blockers, err := h.blockedBy(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	own := "/repos/" + repo.Owner + "/" + repo.Name
	var open []string
	for _, b := range blockers {
		if b.State != stateOpen {
			continue
		}
		_, full, found := strings.Cut(b.RepositoryURL, "/repos/")
		switch {
		case !found, strings.HasSuffix(strings.ToLower(b.RepositoryURL), strings.ToLower(own)):
			open = append(open, "#"+strconv.Itoa(b.Number))
		default:
			open = append(open, full+"#"+strconv.Itoa(b.Number))
		}
	}
	return open, nil
}

// AddIssueDependency makes issue number depend on issue blocker of
// blockerRepo. It is safe to repeat.
func (h *host) AddIssueDependency(ctx context.Context, repo ports.Repository, number int, blockerRepo ports.Repository,
	blocker int) error {
	on, err := h.getIssue(ctx, blockerRepo, blocker)
	if err != nil {
		return err
	}
	status, data, err := h.do(ctx, http.MethodPost, h.issuePath(repo, number)+"/dependencies/blocked_by", struct {
		IssueID int64 `json:"issue_id"`
	}{on.ID})
	if err != nil {
		return err
	}
	if status == http.StatusCreated {
		return nil
	}
	// 422 is also GitHub's answer to a dependency that is there already.
	if status == http.StatusUnprocessableEntity {
		have, listErr := h.blockedBy(ctx, repo, number)
		if listErr == nil && slices.ContainsFunc(have, func(i ghIssue) bool { return i.ID == on.ID }) {
			return nil
		}
	}
	return failed(fmt.Sprintf("making issue %d depend on %s/%s#%d", number, blockerRepo.Owner, blockerRepo.Name, blocker),
		status, data)
}

// milestones lists the repository's milestones of state: open, or all.
func (h *host) milestones(ctx context.Context, repo ports.Repository, state string) ([]ports.Milestone, error) {
	raw, err := h.list(ctx, h.repoPath(repo)+"/milestones", url.Values{queryState: {state}})
	if err != nil {
		return nil, err
	}
	out := make([]ports.Milestone, 0, len(raw))
	for _, r := range raw {
		var m ghMilestone
		if err := json.Unmarshal(r, &m); err != nil {
			return nil, fmt.Errorf("github: reading a milestone: %w", err)
		}
		out = append(out, m.milestone())
	}
	return out, nil
}

// ListMilestones returns the repository's open milestones.
func (h *host) ListMilestones(ctx context.Context, repo ports.Repository) ([]ports.Milestone, error) {
	return h.milestones(ctx, repo, stateOpen)
}

// ListAllMilestones returns the repository's milestones, open and closed.
func (h *host) ListAllMilestones(ctx context.Context, repo ports.Repository) ([]ports.Milestone, error) {
	return h.milestones(ctx, repo, "all")
}

// EnsureMilestone returns the ID of the milestone titled title, open or
// closed, and makes it, open and with description, when there is none.
func (h *host) EnsureMilestone(ctx context.Context, repo ports.Repository, title, description string) (int64, error) {
	all, err := h.ListAllMilestones(ctx, repo)
	if err != nil {
		return 0, err
	}
	for _, m := range all {
		if m.Title == title {
			return m.ID, nil
		}
	}
	made, err := h.CreateMilestone(ctx, repo, title, description, time.Time{})
	return made.ID, err
}

// CreateMilestone makes an open milestone, due at due when it isn't zero. A
// repeat makes a second one where GitHub lets two share a title, and fails
// where it doesn't.
func (h *host) CreateMilestone(ctx context.Context, repo ports.Repository, title, description string,
	due time.Time) (ports.Milestone, error) {
	create := struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		DueOn       string `json:"due_on,omitempty"`
	}{Title: title, Description: description}
	if !due.IsZero() {
		create.DueOn = due.UTC().Format(time.RFC3339)
	}
	status, data, err := h.do(ctx, http.MethodPost, h.repoPath(repo)+"/milestones", create)
	if err != nil {
		return ports.Milestone{}, err
	}
	if status != http.StatusCreated {
		return ports.Milestone{}, failed("creating milestone "+title, status, data)
	}
	var m ghMilestone
	if err := json.Unmarshal(data, &m); err != nil {
		return ports.Milestone{}, fmt.Errorf("github: creating milestone %s: %w", title, err)
	}
	return m.milestone(), nil
}

// MilestoneIssues returns the open and the closed issues in milestone id,
// without pull requests.
func (h *host) MilestoneIssues(ctx context.Context, repo ports.Repository, id int64) ([]ports.Issue, error) {
	return h.listIssues(ctx, repo, url.Values{queryState: {"all"}, "milestone": {strconv.FormatInt(id, 10)}})
}

// patchMilestone changes the named fields of milestone id; GitHub keeps the
// others, its due date among them.
func (h *host) patchMilestone(ctx context.Context, repo ports.Repository, id int64, what string, fields any) error {
	status, data, err := h.do(ctx, http.MethodPatch, h.repoPath(repo)+"/milestones/"+strconv.FormatInt(id, 10), fields)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return failed(what+" milestone "+strconv.FormatInt(id, 10), status, data)
	}
	return nil
}

// CloseMilestone closes milestone id. It is safe to repeat.
func (h *host) CloseMilestone(ctx context.Context, repo ports.Repository, id int64) error {
	return h.patchMilestone(ctx, repo, id, "closing", struct {
		State string `json:"state"`
	}{stateClosed})
}

// DescribeMilestone replaces milestone id's description.
func (h *host) DescribeMilestone(ctx context.Context, repo ports.Repository, id int64, description string) error {
	return h.patchMilestone(ctx, repo, id, "describing", struct {
		Description string `json:"description"`
	}{description})
}

// EditMilestoneTitle retitles milestone id.
func (h *host) EditMilestoneTitle(ctx context.Context, repo ports.Repository, id int64, title string) error {
	return h.patchMilestone(ctx, repo, id, "retitling", struct {
		Title string `json:"title"`
	}{title})
}

// The host is both ports.
var _ ports.Issues = (*host)(nil)
