package contract

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// IssuesEnv builds the Issues under test.
type IssuesEnv struct {
	// New returns the GitHost of a fresh owner that holds no repository,
	// where the suite makes the repositories, and the owner's Issues.
	New func(t *testing.T) (ports.GitHost, ports.Issues)
	// Assignee is a user the host lets the operator assign the owner's
	// issues to.
	Assignee string
	// Cannot names the subtests the host can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// otherRepo is the second repository a subtest makes, where one is not
// enough.
const otherRepo = "contract-other"

// Issues states what the controllers rely on of every Issues: labels made
// once, their Exclusive flag brought to the one asked for, and an exclusive
// scoped label replacing the issue's others of its scope; issues filed,
// listed by label, read (ErrIssueNotFound), commented, assigned, edited and
// closed; dependencies in the issue's repository and in another, which
// OpenBlockers reads and which keep an issue open (ErrOpenDependencies); and
// milestones made once, listed and closed, with their issues.
func Issues(t *testing.T, env IssuesEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, issuesSuite{ctx: ctx, env: env}, env.Cannot, []subtest[issuesSuite]{
		{"makes labels once, an exclusive one replacing its scope's others", issuesSuite.labels},
		{"files, lists, reads, edits and closes issues", issuesSuite.files},
		{"keeps an issue open while an issue it depends on is, in its repository or another",
			issuesSuite.dependencies},
		{"makes, lists and closes milestones, with their issues", issuesSuite.milestones},
		{"retitles a dated milestone, keeping its due date and issues", issuesSuite.retitle},
	})
}

// issuesSuite holds what every subtest of Issues shares.
type issuesSuite struct {
	ctx context.Context
	env IssuesEnv
}

// repos is a fresh owner's Issues and its repositories names.
func (s issuesSuite) repos(t *testing.T, names ...string) (ports.Issues, []ports.Repository) {
	t.Helper()
	h, issues := s.env.New(t)
	repos := make([]ports.Repository, 0, len(names))
	for _, name := range names {
		repos = append(repos, must(h.EnsureRepository(s.ctx, name, "the Issues contract suite"))(t))
	}
	return issues, repos
}

// wantLabels fails t unless issue number of repo has labels, in any order.
func (s issuesSuite) wantLabels(t *testing.T, issues ports.Issues, repo ports.Repository, number int,
	labels ...string) {
	t.Helper()
	got := must(issues.GetIssue(s.ctx, repo, number))(t)
	if !slices.Equal(sorted(got.Labels), sorted(labels)) {
		t.Errorf("issue %d has labels %q, want %q", number, got.Labels, labels)
	}
}

func (s issuesSuite) labels(t *testing.T) {
	issues, repos := s.repos(t, contractRepo)
	repo := repos[0]
	labels := []ports.Label{
		{Name: labelReady, Color: "#0e8a16", Exclusive: true},
		{Name: "status/doing", Color: "#fbca04", Exclusive: true},
		{Name: labelAPI, Color: "#1d76db"},
		{Name: "area/ui", Color: "#1d76db"},
		{Name: labelBug, Color: "#d73a4a"},
		{Name: "kind/feature", Color: "#a2eeef"},
	}
	for range 2 {
		if err := issues.EnsureLabels(s.ctx, repo, labels); err != nil {
			t.Fatalf("EnsureLabels() = %v", err)
		}
	}
	issue := must(issues.CreateIssue(s.ctx, repo, "Labelled", "Body",
		[]string{labelReady, labelAPI, labelBug}, 0))(t)
	if want := []string{labelAPI, labelBug, labelReady}; !slices.Equal(sorted(issue.Labels), want) {
		t.Errorf("CreateIssue() filed labels %q, want %q", issue.Labels, want)
	}
	n := issue.Number

	label := func(name string) {
		t.Helper()
		if err := issues.AddIssueLabel(s.ctx, repo, n, name); err != nil {
			t.Fatalf("AddIssueLabel(%s) = %v", name, err)
		}
	}
	label("status/doing")
	s.wantLabels(t, issues, repo, n, labelAPI, labelBug, "status/doing")
	label("area/ui")
	s.wantLabels(t, issues, repo, n, labelAPI, "area/ui", labelBug, "status/doing")
	label("area/ui")
	s.wantLabels(t, issues, repo, n, labelAPI, "area/ui", labelBug, "status/doing")

	// EnsureLabels makes the Exclusive flag of the labels it has match.
	for i := range labels {
		labels[i].Exclusive = labels[i].Name == labelBug || labels[i].Name == "kind/feature" ||
			labels[i].Exclusive
	}
	if err := issues.EnsureLabels(s.ctx, repo, labels); err != nil {
		t.Fatalf("EnsureLabels() of exclusive kind/ labels = %v", err)
	}
	label("kind/feature")
	s.wantLabels(t, issues, repo, n, labelAPI, "area/ui", "kind/feature", "status/doing")

	for range 2 {
		if err := issues.RemoveIssueLabel(s.ctx, repo, n, labelAPI); err != nil {
			t.Fatalf("RemoveIssueLabel() = %v", err)
		}
	}
	s.wantLabels(t, issues, repo, n, "area/ui", "kind/feature", "status/doing")
}

func (s issuesSuite) files(t *testing.T) {
	issues, repos := s.repos(t, contractRepo)
	repo := repos[0]
	if err := issues.EnsureLabels(s.ctx, repo, []ports.Label{{Name: labelReady, Color: "#0e8a16",
		Exclusive: true}}); err != nil {
		t.Fatal(err)
	}
	if listed := must(issues.ListIssues(s.ctx, repo, ""))(t); len(listed) != 0 {
		t.Errorf("ListIssues() of a new repository = %+v, want none", listed)
	}
	a := must(issues.CreateIssue(s.ctx, repo, "A", "Body A", []string{labelReady}, 0))(t)
	b := must(issues.CreateIssue(s.ctx, repo, "B", "Body B", nil, 0))(t)
	if a.Number == b.Number || a.Title != "A" || a.Body != "Body A" || a.State != issueOpen || a.URL == "" ||
		a.Milestone != 0 {
		t.Errorf("CreateIssue() = %+v, then %+v; want two open issues", a, b)
	}

	wantNumbers := func(label string, want ...int) {
		t.Helper()
		got := numbersOf(must(issues.ListIssues(s.ctx, repo, label))(t))
		if !slices.Equal(sorted(got), sorted(want)) {
			t.Errorf("ListIssues(%q) lists %v, want %v", label, got, want)
		}
	}
	wantNumbers(labelReady, a.Number)
	wantNumbers("", a.Number, b.Number)

	got := must(issues.GetIssue(s.ctx, repo, b.Number))(t)
	if got.Number != b.Number || got.Title != "B" || got.Body != "Body B" || got.State != issueOpen ||
		len(got.Labels) != 0 {
		t.Errorf("GetIssue() = %+v, want B as filed", got)
	}
	if _, err := issues.GetIssue(s.ctx, repo, b.Number+100); !errors.Is(err, ports.ErrIssueNotFound) {
		t.Errorf("GetIssue() of a missing issue = %v, want ErrIssueNotFound", err)
	}

	if err := issues.CommentIssue(s.ctx, repo, a.Number, "Picked up"); err != nil {
		t.Errorf("CommentIssue() = %v", err)
	}
	for _, users := range [][]string{{s.env.Assignee}, {}} {
		if err := issues.AssignIssue(s.ctx, repo, a.Number, users); err != nil {
			t.Errorf("AssignIssue(%q) = %v", users, err)
		}
	}
	if err := issues.EditIssueBody(s.ctx, repo, a.Number, "Body A\n\n## Clarification\n\nMore."); err != nil {
		t.Fatal(err)
	}
	got = must(issues.GetIssue(s.ctx, repo, a.Number))(t)
	if got.Body != "Body A\n\n## Clarification\n\nMore." || got.Title != "A" ||
		!slices.Equal(got.Labels, []string{labelReady}) {
		t.Errorf("GetIssue() after EditIssueBody() = %+v, want the new body, title and labels kept", got)
	}

	if err := issues.CloseIssue(s.ctx, repo, b.Number); err != nil {
		t.Fatal(err)
	}
	if got := must(issues.GetIssue(s.ctx, repo, b.Number))(t); got.State != issueClosed {
		t.Errorf("GetIssue() of a closed issue = %+v, want closed", got)
	}
	wantNumbers("", a.Number)
}

func (s issuesSuite) dependencies(t *testing.T) {
	issues, repos := s.repos(t, contractRepo, otherRepo)
	repo, other := repos[0], repos[1]
	near := must(issues.CreateIssue(s.ctx, repo, "Near blocker", "", nil, 0))(t)
	far := must(issues.CreateIssue(s.ctx, other, "Far blocker", "", nil, 0))(t)
	blocked := must(issues.CreateIssue(s.ctx, repo, "Blocked", "", nil, 0))(t)
	if open := must(issues.OpenBlockers(s.ctx, repo, blocked.Number))(t); len(open) != 0 {
		t.Errorf("OpenBlockers() of an issue with no dependency = %q, want none", open)
	}
	nearRef := fmt.Sprintf("#%d", near.Number)
	farRef := fmt.Sprintf("%s/%s#%d", other.Owner, other.Name, far.Number)
	for range 2 {
		if err := issues.AddIssueDependency(s.ctx, repo, blocked.Number, repo, near.Number); err != nil {
			t.Fatalf("AddIssueDependency() in the repository = %v", err)
		}
		if err := issues.AddIssueDependency(s.ctx, repo, blocked.Number, other, far.Number); err != nil {
			t.Fatalf("AddIssueDependency() on another repository = %v", err)
		}
	}
	wantBlockers := func(want ...string) {
		t.Helper()
		if open := must(issues.OpenBlockers(s.ctx, repo, blocked.Number))(t); !slices.Equal(sorted(open),
			sorted(want)) {
			t.Errorf("OpenBlockers() = %q, want %q", open, want)
		}
	}
	wantBlockers(nearRef, farRef)

	if err := issues.CloseIssue(s.ctx, repo, blocked.Number); !errors.Is(err, ports.ErrOpenDependencies) {
		t.Errorf("CloseIssue() of an issue with open blockers = %v, want ErrOpenDependencies", err)
	}
	if got := must(issues.GetIssue(s.ctx, repo, blocked.Number))(t); got.State != issueOpen {
		t.Errorf("a refused CloseIssue() left the issue %s", got.State)
	}
	if err := issues.CloseIssue(s.ctx, repo, near.Number); err != nil {
		t.Fatal(err)
	}
	wantBlockers(farRef)
	if err := issues.CloseIssue(s.ctx, other, far.Number); err != nil {
		t.Fatal(err)
	}
	wantBlockers()
	if err := issues.CloseIssue(s.ctx, repo, blocked.Number); err != nil {
		t.Errorf("CloseIssue() once its blockers closed = %v", err)
	}
}

func (s issuesSuite) milestones(t *testing.T) {
	issues, repos := s.repos(t, contractRepo)
	repo := repos[0]
	later := must(issues.EnsureMilestone(s.ctx, repo, milestoneLater, "Not part of any release"))(t)
	if again := must(issues.EnsureMilestone(s.ctx, repo, milestoneLater, "Again"))(t); again != later {
		t.Errorf("EnsureMilestone() again = %d, want %d", again, later)
	}
	// Noon, so the day is the same in the host's time zone.
	y, m, d := time.Now().UTC().AddDate(0, 0, 7).Date()
	due := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	made := must(issues.CreateMilestone(s.ctx, repo, firstTag, "The first release.", due))(t)
	if made.ID == later || made.Title != firstTag || made.Closed || !sameDay(made.Due, due) {
		t.Errorf("CreateMilestone() = %+v, want v0.1.0, open, due %v", made, due)
	}

	in := must(issues.CreateIssue(s.ctx, repo, "In", "", nil, made.ID))(t)
	if got := must(issues.GetIssue(s.ctx, repo, in.Number))(t); got.Milestone != made.ID ||
		got.MilestoneTitle != firstTag {
		t.Errorf("GetIssue() of an issue filed in v0.1.0 = %+v, want milestone %d", got, made.ID)
	}
	moved := must(issues.CreateIssue(s.ctx, repo, "Moved", "", nil, later))(t)
	if err := issues.SetIssueMilestone(s.ctx, repo, moved.Number, made.ID); err != nil {
		t.Fatal(err)
	}
	out := must(issues.CreateIssue(s.ctx, repo, "Out", "", nil, made.ID))(t)
	if err := issues.SetIssueMilestone(s.ctx, repo, out.Number, 0); err != nil {
		t.Fatal(err)
	}
	if got := must(issues.GetIssue(s.ctx, repo, out.Number))(t); got.Milestone != 0 || got.MilestoneTitle != "" {
		t.Errorf("GetIssue() of an issue taken out of its milestone = %+v, want none", got)
	}
	if err := issues.CloseIssue(s.ctx, repo, moved.Number); err != nil {
		t.Fatal(err)
	}
	numbers := numbersOf(must(issues.MilestoneIssues(s.ctx, repo, made.ID))(t))
	if want := sorted([]int{in.Number, moved.Number}); !slices.Equal(sorted(numbers), want) {
		t.Errorf("MilestoneIssues() = %v, want the open and the closed issue %v", numbers, want)
	}

	wantMilestones := func(open []string, all map[string]ports.Milestone) {
		t.Helper()
		listedOpen := must(issues.ListMilestones(s.ctx, repo))(t)
		titles := make([]string, 0, len(listedOpen))
		for _, m := range listedOpen {
			titles = append(titles, m.Title)
		}
		if !slices.Equal(sorted(titles), sorted(open)) {
			t.Errorf("ListMilestones() = %q, want %q", titles, open)
		}
		listed := must(issues.ListAllMilestones(s.ctx, repo))(t)
		if len(listed) != len(all) {
			t.Errorf("ListAllMilestones() = %+v, want %d", listed, len(all))
		}
		for _, m := range listed {
			w, ok := all[m.Title]
			if !ok || m.ID != w.ID || m.Description != w.Description || m.Closed != w.Closed ||
				!w.Due.IsZero() && !sameDay(m.Due, w.Due) {
				t.Errorf("ListAllMilestones() lists %+v, want %+v", m, w)
			}
		}
	}
	all := map[string]ports.Milestone{
		milestoneLater: {ID: later, Title: milestoneLater, Description: "Not part of any release"},
		firstTag:       {ID: made.ID, Title: firstTag, Description: "The first release.", Due: due},
	}
	wantMilestones([]string{milestoneLater, firstTag}, all)
	for range 2 {
		if err := issues.CloseMilestone(s.ctx, repo, made.ID); err != nil {
			t.Fatalf("CloseMilestone() = %v", err)
		}
	}
	closed := all[firstTag]
	closed.Closed = true
	all[firstTag] = closed
	wantMilestones([]string{milestoneLater}, all)
	if id := must(issues.EnsureMilestone(s.ctx, repo, firstTag, ""))(t); id != made.ID {
		t.Errorf("EnsureMilestone() of a closed milestone = %d, want %d", id, made.ID)
	}
}

// retitle retitles a dated release, as a cut does with a release of fixes
// (docs/plans/release-cadence.md, C7): its due date, description, state and
// issues stay.
func (s issuesSuite) retitle(t *testing.T) {
	issues, repos := s.repos(t, contractRepo)
	repo := repos[0]
	// Noon, so the day is the same in the host's time zone.
	y, m, d := time.Now().UTC().AddDate(0, 0, 1).Date()
	due := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	const description = "The day's release."
	made := must(issues.CreateMilestone(s.ctx, repo, firstTag, description, due))(t)
	in := must(issues.CreateIssue(s.ctx, repo, "In", "", nil, made.ID))(t)
	for range 2 {
		if err := issues.EditMilestoneTitle(s.ctx, repo, made.ID, patchTag); err != nil {
			t.Fatalf("EditMilestoneTitle() = %v", err)
		}
	}
	listed := must(issues.ListAllMilestones(s.ctx, repo))(t)
	if len(listed) != 1 || listed[0].ID != made.ID || listed[0].Title != patchTag ||
		listed[0].Description != description || listed[0].Closed || !sameDay(listed[0].Due, due) {
		t.Errorf("ListAllMilestones() after EditMilestoneTitle = %+v, want %s, open, due %v", listed, patchTag, due)
	}
	if got := must(issues.GetIssue(s.ctx, repo, in.Number))(t); got.Milestone != made.ID ||
		got.MilestoneTitle != patchTag {
		t.Errorf("GetIssue() of an issue of the retitled milestone = %+v, want milestone %d, %s", got, made.ID,
			patchTag)
	}
	if numbers := numbersOf(must(issues.MilestoneIssues(s.ctx, repo, made.ID))(t)); !slices.Equal(numbers,
		[]int{in.Number}) {
		t.Errorf("MilestoneIssues() of the retitled milestone = %v, want its issue kept", numbers)
	}
}

// numbersOf is the numbers of issues.
func numbersOf(issues []ports.Issue) []int {
	out := make([]int, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Number)
	}
	return out
}

// sameDay is whether a and b fall on the same day in UTC: a host may keep a
// due date as a day.
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}
