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

package fake

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// The fake host's issues, labels and reviews. Issue numbers count separately
// from pull request numbers, which Gitea shares; the tests don't mix them.

// Issue is an issue as the fake host keeps it: the port's Issue with what
// else a test reads of it.
type Issue struct {
	ports.Issue
	// Blockers are the issues it depends on, "#3" of its own repository or
	// "owner/name#3" of another.
	Blockers []string
	// Assignees are the users it is assigned to.
	Assignees []string
	// Comments are its comments' bodies, oldest first.
	Comments []string
	// InMilestone is the title of the milestone the issue is in.
	InMilestone string
	// FiledLabels are the labels the issue was filed with, before anyone
	// (a Crew taking it) changed them.
	FiledLabels []string
}

// Review is a review posted on a pull request, and the user who posted it.
type Review struct {
	Event, Body, Commit string
	User                string
	Stale, Dismissed    bool
}

type issueStore struct {
	issues     map[string][]*Issue // owner/name, by number - 1
	labels     map[string][]string // owner/name
	exclusive  map[string]bool     // owner/name label, for the labels that are exclusive
	milestones map[string][]string // owner/name, titles by ID - 1
	// descriptions are the descriptions EnsureMilestone gave the milestones
	// it made, by owner/name#ID.
	descriptions map[string]string
	reviews      map[string][]Review // owner/name#number
	// late makes CreateIssue file the issue but answer only once the caller has
	// given up, as a host under load does.
	late bool
	// migrated are the owner/names whose labels stay as a migration left them:
	// EnsureLabels does not make their exclusive match.
	migrated map[string]bool
	// listErrs makes ListIssues of an owner/name fail.
	listErrs map[string]error
	// depErrs makes AddIssueDependency on an issue of an owner/name fail.
	depErrs map[string]error
	// closeBlockers, by owner/name#number, are blockers the issue gains when
	// CloseIssue is next called on it, as one opened after its blockers were
	// read.
	closeBlockers map[string]string
	// now is the clock CloseIssue stamps ClosedAt with; nil is time.Now.
	now func() time.Time
}

// SetClock makes now the clock a closed issue's ClosedAt is stamped with, so
// a test that moves its own clock sees the issue closed on it; nil is
// time.Now. now must not call back into the fake.
func (g *Git) SetClock(now func() time.Time) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.issueState().now = now
}

func (s *issueStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// BlockOnClose makes the next CloseIssue of an issue of owner/name first give
// it the blocker ref, as a blocker opened after its blockers were read.
func (g *Git) BlockOnClose(owner, name string, number int, ref string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.issueState()
	if s.closeBlockers == nil {
		s.closeBlockers = map[string]string{}
	}
	s.closeBlockers[fmt.Sprintf("%s/%s#%d", owner, name, number)] = ref
}

// KeepLabelsAsMigrated makes the labels of owner/name non-exclusive, as Gitea's
// migration from GitHub leaves them, and EnsureLabels leave them so from now on.
func (g *Git) KeepLabelsAsMigrated(owner, name string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.issueState()
	if s.migrated == nil {
		s.migrated = map[string]bool{}
	}
	s.migrated[owner+"/"+name] = true
}

// FailDependencies makes adding a dependency to an issue of owner/name fail
// with err from now on; a nil err adds them again.
func (g *Git) FailDependencies(owner, name string, err error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.issueState()
	if s.depErrs == nil {
		s.depErrs = map[string]error{}
	}
	s.depErrs[owner+"/"+name] = err
}

// FailListIssues makes listing the issues of owner/name fail with err from
// now on; a nil err lists them again.
func (g *Git) FailListIssues(owner, name string, err error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.issueState()
	if s.listErrs == nil {
		s.listErrs = map[string]error{}
	}
	s.listErrs[owner+"/"+name] = err
}

func (s *state) issueState() *issueStore {
	if s.issues == nil {
		s.issues = &issueStore{issues: map[string][]*Issue{}, labels: map[string][]string{},
			exclusive:  map[string]bool{},
			milestones: map[string][]string{}, descriptions: map[string]string{}, reviews: map[string][]Review{}}
	}
	return s.issues
}

// AddIssue files an issue as a person would, and returns its number.
func (g *Git) AddIssue(owner, name, title, body string, labels []string, blockers ...string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.issueState()
	key := owner + "/" + name
	n := len(s.issues[key]) + 1
	s.issues[key] = append(s.issues[key], &Issue{Issue: ports.Issue{Number: n, Title: title, Body: body,
		State: stateOpen, Labels: labels, URL: fmt.Sprintf("http://gitea/%s/issues/%d", key, n)}, Blockers: blockers})
	g.s.deliver(ports.Repository{Owner: owner, Name: name}, EventIssue, n)
	return n
}

// IssueCount is how many issues a repository has, open or closed.
func (g *Git) IssueCount(owner, name string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return len(g.s.issueState().issues[owner+"/"+name])
}

// Issue returns a copy of an issue, with its blockers, assignees and comments.
func (g *Git) Issue(owner, name string, number int) Issue {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i := g.s.issueState().find(owner+"/"+name, number)
	if i == nil {
		return Issue{}
	}
	out := *i
	out.Labels, out.Comments, out.Blockers = slices.Clone(i.Labels), slices.Clone(i.Comments), slices.Clone(i.Blockers)
	return out
}

// SetBlockers replaces the open issues an issue depends on.
func (g *Git) SetBlockers(owner, name string, number int, blockers ...string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.issueState().find(owner+"/"+name, number).Blockers = blockers
}

// Reviews lists the reviews posted on a pull request.
func (g *Git) Reviews(owner, name string, number int) []Review {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return slices.Clone(g.s.issueState().reviews[fmt.Sprintf("%s/%s#%d", owner, name, number)])
}

// OpenIssues lists the open issues of a repository, by number.
func (g *Git) OpenIssues(owner, name string) []Issue {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	var out []Issue
	for _, i := range g.s.issueState().issues[owner+"/"+name] {
		if i.State == stateOpen {
			out = append(out, *i)
		}
	}
	return out
}

func (s *issueStore) find(key string, number int) *Issue {
	if number < 1 || number > len(s.issues[key]) {
		return nil
	}
	return s.issues[key][number-1]
}

func (g *Git) issueOf(repo ports.Repository, number int) (*Issue, error) {
	i := g.s.issueState().find(repo.Owner+"/"+repo.Name, number)
	if i == nil {
		return nil, fmt.Errorf("issue %d of %s/%s: %w", number, repo.Owner, repo.Name, ports.ErrIssueNotFound)
	}
	return i, nil
}

// ReviewPullRequest refuses a review by the pull request's author, as Gitea does.
func (g *Git) ReviewPullRequest(_ context.Context, repo ports.Repository, number int,
	commit, event, body string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)
	if g.s.agentState().authors[key] == g.cfg.Credentials.Username {
		return fmt.Errorf("review %s: approve your own pull is not allowed", key)
	}
	s := g.s.issueState()
	s.reviews[key] = append(s.reviews[key], Review{Event: event, Body: body, Commit: commit,
		User: g.cfg.Credentials.Username})
	g.s.deliver(repo, EventPullRequest, number)
	return nil
}

// AddReview submits a review on a pull request as user, as a person on the
// host would.
func (g *Git) AddReview(owner, name string, number int, review Review) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := fmt.Sprintf("%s/%s#%d", owner, name, number)
	s := g.s.issueState()
	s.reviews[key] = append(s.reviews[key], review)
	g.s.deliver(ports.Repository{Owner: owner, Name: name}, EventPullRequest, number)
}

// PullRequestReviews lists the reviews submitted on a pull request, oldest first.
func (g *Git) PullRequestReviews(_ context.Context, repo ports.Repository, number int) ([]ports.PullReview, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if _, err := g.pull(repo, number); err != nil {
		return nil, err
	}
	var out []ports.PullReview
	for _, r := range g.s.issueState().reviews[fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)] {
		out = append(out, ports.PullReview{User: r.User, State: r.Event, Commit: r.Commit, Body: r.Body,
			Stale: r.Stale, Dismissed: r.Dismissed})
	}
	return out, nil
}

// EnsureLabels creates the missing labels and makes the existing ones' exclusive
// flag match, as the port promises.
func (g *Git) EnsureLabels(_ context.Context, repo ports.Repository, labels []ports.Label) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.issueState()
	key := repo.Owner + "/" + repo.Name
	for _, l := range labels {
		if !slices.Contains(s.labels[key], l.Name) {
			s.labels[key] = append(s.labels[key], l.Name)
		}
		if !s.migrated[key] {
			s.exclusive[key+" "+l.Name] = l.Exclusive
		}
	}
	return nil
}

func (g *Git) EnsureMilestone(_ context.Context, repo ports.Repository, title, description string) (int64, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.issueState()
	key := repo.Owner + "/" + repo.Name
	if i := slices.Index(s.milestones[key], title); i >= 0 {
		return int64(i + 1), nil
	}
	s.milestones[key] = append(s.milestones[key], title)
	id := int64(len(s.milestones[key]))
	s.descriptions[fmt.Sprintf("%s#%d", key, id)] = description
	return id, nil
}

func (g *Git) ListIssues(_ context.Context, repo ports.Repository, label string) ([]ports.Issue, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if err := g.s.issueState().listErrs[repo.Owner+"/"+repo.Name]; err != nil {
		return nil, err
	}
	var out []ports.Issue
	for _, i := range g.s.issueState().issues[repo.Owner+"/"+repo.Name] {
		if i.State == stateOpen && (label == "" || slices.Contains(i.Labels, label)) {
			// A copy of the labels: AddIssueLabel changes them in place
			// while the caller reads them (#1820).
			issue := i.Issue
			issue.Labels = slices.Clone(i.Labels)
			if i.InMilestone != "" {
				issue.Milestone = int64(slices.Index(g.s.issueState().milestones[repo.Owner+"/"+repo.Name],
					i.InMilestone) + 1)
				issue.MilestoneTitle = i.InMilestone
			}
			out = append(out, issue)
		}
	}
	return out, nil
}

func (g *Git) GetIssue(_ context.Context, repo ports.Repository, number int) (ports.Issue, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return ports.Issue{}, err
	}
	out := i.Issue
	out.Labels = slices.Clone(i.Labels)
	if i.InMilestone != "" {
		out.Milestone = int64(slices.Index(g.s.issueState().milestones[repo.Owner+"/"+repo.Name], i.InMilestone) + 1)
		out.MilestoneTitle = i.InMilestone
	}
	return out, nil
}

// AddIssueDependency makes an issue depend on another, of the same repository
// ("#3") or another ("owner/name#3").
func (g *Git) AddIssueDependency(_ context.Context, repo ports.Repository, number int,
	blockerRepo ports.Repository, blocker int) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if err := g.s.issueState().depErrs[repo.Owner+"/"+repo.Name]; err != nil {
		return err
	}
	i, err := g.issueOf(repo, number)
	if err != nil {
		return err
	}
	ref := fmt.Sprintf("#%d", blocker)
	if blockerRepo.Owner != repo.Owner || blockerRepo.Name != repo.Name {
		ref = blockerRepo.Owner + "/" + blockerRepo.Name + ref
	}
	if !slices.Contains(i.Blockers, ref) {
		i.Blockers = append(i.Blockers, ref)
	}
	g.s.deliver(repo, EventIssue, number)
	return nil
}

// PutInMilestone puts an issue in the milestone titled title, creating it if needed.
func (g *Git) PutInMilestone(owner, name string, number int, title string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.issueState()
	key := owner + "/" + name
	if !slices.Contains(s.milestones[key], title) {
		s.milestones[key] = append(s.milestones[key], title)
	}
	s.find(key, number).InMilestone = title
}

// SetIssueRelease puts an issue in the milestone titled title, due on due.
func (g *Git) SetIssueRelease(owner, name string, number int, title string, due time.Time) {
	g.PutInMilestone(owner, name, number, title)
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i := g.s.issueState().find(owner+"/"+name, number)
	i.MilestoneTitle, i.MilestoneDue = title, due
}

func (g *Git) OpenBlockers(_ context.Context, repo ports.Repository, number int) ([]string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return nil, err
	}
	return g.openBlockers(repo, i), nil
}

// openBlockers are the blockers of an issue of repo that are open: a blocker
// the fake host doesn't have counts as open. The caller holds the lock.
func (g *Git) openBlockers(repo ports.Repository, i *Issue) []string {
	var out []string
	for _, ref := range i.Blockers {
		of, n, _ := strings.Cut(ref, "#")
		if of == "" {
			of = repo.Owner + "/" + repo.Name
		}
		number, _ := strconv.Atoi(n)
		if b := g.s.issueState().find(of, number); b == nil || b.State != stateClosed {
			out = append(out, ref)
		}
	}
	return out
}

// AddIssueLabel emulates Gitea's exclusive scoped labels: an exclusive label
// replaces the issue's other labels of its scope.
func (g *Git) AddIssueLabel(_ context.Context, repo ports.Repository, number int, label string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return err
	}
	if !slices.Contains(g.s.issueState().labels[repo.Owner+"/"+repo.Name], label) {
		return fmt.Errorf("no label %s", label)
	}
	scope, _, _ := strings.Cut(label, "/")
	exclusive := g.s.issueState().exclusive[repo.Owner+"/"+repo.Name+" "+label]
	i.Labels = slices.DeleteFunc(i.Labels, func(l string) bool {
		return l == label || exclusive && strings.HasPrefix(l, scope+"/")
	})
	i.Labels = append(i.Labels, label)
	g.s.deliver(repo, EventIssue, number)
	return nil
}

func (g *Git) RemoveIssueLabel(ctx context.Context, repo ports.Repository, number int, label string) error {
	if err := ctx.Err(); err != nil {
		return err // a request on a spent context fails, as over HTTP
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return err
	}
	i.Labels = slices.DeleteFunc(i.Labels, func(l string) bool { return l == label })
	g.s.deliver(repo, EventIssue, number)
	return nil
}

func (g *Git) CommentIssue(ctx context.Context, repo ports.Repository, number int, body string) error {
	if err := ctx.Err(); err != nil {
		return err // a request on a spent context fails, as over HTTP
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return err
	}
	i.Comments = append(i.Comments, body)
	g.s.deliver(repo, EventIssue, number)
	return nil
}

func (g *Git) CloseIssue(ctx context.Context, repo ports.Repository, number int) error {
	if err := ctx.Err(); err != nil {
		return err // a request on a spent context fails, as over HTTP
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)
	if ref, ok := g.s.issueState().closeBlockers[key]; ok {
		delete(g.s.issueState().closeBlockers, key)
		i.Blockers = append(i.Blockers, ref)
	}
	if blockers := g.openBlockers(repo, i); len(blockers) > 0 {
		// As Gitea does, with HTTP 412.
		return fmt.Errorf("closing issue %d of %s/%s, which waits on %v: %w", number, repo.Owner, repo.Name,
			blockers, ports.ErrOpenDependencies)
	}
	i.State, i.ClosedAt = stateClosed, g.s.issueState().clock()
	g.s.deliver(repo, EventIssue, number)
	return nil
}

func (g *Git) EditIssueBody(ctx context.Context, repo ports.Repository, number int, body string) error {
	if err := ctx.Err(); err != nil {
		return err // a request on a spent context fails, as over HTTP
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return err
	}
	i.Body = body
	g.s.deliver(repo, EventIssue, number)
	return nil
}

func (g *Git) SetIssueMilestone(_ context.Context, repo ports.Repository, number int, milestone int64) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return err
	}
	key := repo.Owner + "/" + repo.Name
	titles := g.s.issueState().milestones[key]
	switch {
	case milestone == 0:
		i.InMilestone = ""
		i.MilestoneTitle, i.MilestoneDue = "", time.Time{}
	case int(milestone) <= len(titles):
		// The issue takes its new milestone's due date, as the host reads it.
		for _, m := range g.s.milestones(key, true) {
			if m.ID == milestone {
				i.MilestoneTitle, i.MilestoneDue = m.Title, m.Due
			}
		}
		i.InMilestone = titles[milestone-1]
	default:
		return fmt.Errorf("no milestone %d", milestone)
	}
	g.s.deliver(repo, EventIssue, number)
	return nil
}

func (g *Git) AssignIssue(_ context.Context, repo ports.Repository, number int, users []string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return err
	}
	i.Assignees = slices.Clone(users)
	g.s.deliver(repo, EventIssue, number)
	return nil
}

func (g *Git) CreateIssue(ctx context.Context, repo ports.Repository, title, body string,
	labels []string, milestone int64) (ports.Issue, error) {
	g.s.mu.Lock()
	s := g.s.issueState()
	key := repo.Owner + "/" + repo.Name
	for _, l := range labels {
		if !slices.Contains(s.labels[key], l) {
			g.s.mu.Unlock()
			return ports.Issue{}, fmt.Errorf("no label %s", l)
		}
	}
	var milestoneTitle string
	if milestone != 0 {
		if milestone < 0 || milestone > int64(len(s.milestones[key])) {
			g.s.mu.Unlock()
			return ports.Issue{}, fmt.Errorf("no milestone %d", milestone)
		}
		milestoneTitle = s.milestones[key][milestone-1]
	}
	g.s.mu.Unlock()
	n := g.AddIssue(repo.Owner, repo.Name, title, body, labels)
	g.s.mu.Lock()
	filed := s.find(key, n)
	filed.InMilestone = milestoneTitle
	filed.FiledLabels = slices.Clone(labels)
	late := s.late
	g.s.mu.Unlock()
	if late {
		<-ctx.Done()
		return ports.Issue{}, ctx.Err()
	}
	return g.GetIssue(context.Background(), repo, n)
}

// LabelIssue adds a label to an issue, as a person would in the Git host.
func (g *Git) LabelIssue(owner, name string, number int, label string) error {
	return g.AddIssueLabel(context.Background(), ports.Repository{Owner: owner, Name: name}, number, label)
}

// CloseIssueByHand closes an issue, as a person would in the Git host.
func (g *Git) CloseIssueByHand(owner, name string, number int) error {
	return g.CloseIssue(context.Background(), ports.Repository{Owner: owner, Name: name}, number)
}

// FallBehind makes a pull request's branch miss what merged into main since it
// took main in.
func (g *Git) FallBehind(owner, name string, number int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	for _, pr := range g.s.agentState().pulls[owner+"/"+name] {
		if pr.Number == number {
			pr.Behind = true
		}
	}
}

// PullAuthor is who opened a pull request.
func (g *Git) PullAuthor(owner, name string, number int) string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.agentState().authors[fmt.Sprintf("%s/%s#%d", owner, name, number)]
}

// Conflict makes a pull request conflict with its base, as main moved with
// conflicting changes, or merge cleanly again.
func (g *Git) Conflict(owner, name string, number int, conflicted bool) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().conflicts[fmt.Sprintf("%s/%s#%d", owner, name, number)] = conflicted
	for _, pr := range g.s.agentState().pulls[owner+"/"+name] {
		if pr.Number == number {
			pr.Mergeable = !conflicted
			pr.Behind = conflicted // resolving it takes in main
		}
	}
}

// Retest makes the Git host say a pull request doesn't merge while it tests it
// against its base, which conflicts with it no more than before, or finish.
func (g *Git) Retest(owner, name string, number int, testing bool) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	for _, pr := range g.s.agentState().pulls[owner+"/"+name] {
		if pr.Number == number {
			pr.Mergeable = !testing
		}
	}
}

// AllIssues returns each issue of owner/name, open or closed, by number. The
// copies are shallow: their slices are the fake's own, so read them only
// while no reconciler edits the issue.
func (g *Git) AllIssues(owner, name string) []Issue {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	issues := g.s.issueState().issues[owner+"/"+name]
	out := make([]Issue, 0, len(issues))
	for _, i := range issues {
		out = append(out, *i)
	}
	return out
}

// AnswerLate makes CreateIssue file the issue but answer only once the caller
// has given up, as a host under load does.
func (g *Git) AnswerLate() {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.issueState().late = true
}
