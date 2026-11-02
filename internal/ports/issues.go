package ports

import (
	"context"
	"errors"
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
	// Exclusive makes a scoped label ("scope/value") one of a kind: the port's
	// rule is that adding an exclusive label to an issue removes the issue's
	// other labels of its scope. An adapter whose host has exclusive labels
	// hands the rule to the host; one whose host lacks them does it itself,
	// in AddIssueLabel.
	Exclusive bool
}

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

// ErrIssueNotFound is GetIssue's error for an issue the repository doesn't have.
var ErrIssueNotFound = errors.New("no such issue")

// ErrOpenDependencies is CloseIssue's error for an issue the host won't close
// while an issue it depends on is open.
var ErrOpenDependencies = errors.New("the issue still has open dependencies")

// Issues is the issue tracker port (docs/architecture.md, "Ports"): labels,
// issues, comments, assignment, dependencies and milestones of the
// repositories of one owner.
type Issues interface {
	// EnsureLabels creates the labels repo doesn't have yet and makes the
	// Exclusive flag of those it has match the one asked for (a migrated
	// repository brings its labels over non-exclusive). It is safe to repeat.
	EnsureLabels(ctx context.Context, repo Repository, labels []Label) error
	// EnsureMilestone returns the ID of repo's milestone titled title, open or
	// closed, and creates it, open and with description, if there is none. It
	// leaves an existing milestone as it is, and is safe to repeat.
	EnsureMilestone(ctx context.Context, repo Repository, title, description string) (int64, error)
	// ListIssues returns the open issues of repo labelled label, or all of them
	// when label is "", without pull requests.
	ListIssues(ctx context.Context, repo Repository, label string) ([]Issue, error)
	// GetIssue reads one issue; ErrIssueNotFound when there is none.
	GetIssue(ctx context.Context, repo Repository, number int) (Issue, error)
	// OpenBlockers returns the open issues an issue depends on: "#3" in the same
	// repository, "owner/name#3" in another.
	OpenBlockers(ctx context.Context, repo Repository, number int) ([]string, error)
	// AddIssueDependency makes issue number of repo depend on issue blocker of
	// blockerRepo, which may be repo itself or another repository. It is safe
	// to repeat.
	AddIssueDependency(ctx context.Context, repo Repository, number int, blockerRepo Repository, blocker int) error
	// AddIssueLabel adds a label to an issue by name. An exclusive scoped label
	// replaces the issue's other labels of its scope (Label.Exclusive).
	AddIssueLabel(ctx context.Context, repo Repository, number int, label string) error
	// RemoveIssueLabel removes a label from an issue, if it has it.
	RemoveIssueLabel(ctx context.Context, repo Repository, number int, label string) error
	// CommentIssue adds a comment to an issue or a pull request.
	CommentIssue(ctx context.Context, repo Repository, number int, body string) error
	// CloseIssue closes an issue; ErrOpenDependencies when the host won't
	// while an issue it depends on is open.
	CloseIssue(ctx context.Context, repo Repository, number int) error
	// EditIssueBody replaces an issue's body.
	EditIssueBody(ctx context.Context, repo Repository, number int, body string) error
	// SetIssueMilestone moves an issue into the milestone with ID milestone, or
	// out of every milestone when it is 0.
	SetIssueMilestone(ctx context.Context, repo Repository, number int, milestone int64) error
	// AssignIssue makes users the issue's assignees.
	AssignIssue(ctx context.Context, repo Repository, number int, users []string) error
	// CreateIssue files an issue with labels, which must exist in the repository,
	// in the milestone with ID milestone, or in none when it is 0.
	CreateIssue(ctx context.Context, repo Repository, title, body string, labels []string, milestone int64) (Issue, error)
	// ListMilestones returns repo's open milestones. Both lists fail rather than
	// come back cut short when there are too many to list in full.
	ListMilestones(ctx context.Context, repo Repository) ([]Milestone, error)
	// ListAllMilestones returns repo's milestones, open and closed, with their
	// descriptions.
	ListAllMilestones(ctx context.Context, repo Repository) ([]Milestone, error)
	// CreateMilestone creates an open milestone in repo with description, due at
	// due, or with no due date when due is zero, and returns it. It doesn't look
	// for one of the same title first: a repeat makes a second milestone.
	CreateMilestone(ctx context.Context, repo Repository, title, description string, due time.Time) (Milestone, error)
	// MilestoneIssues returns the open and the closed issues in milestone id,
	// without pull requests.
	MilestoneIssues(ctx context.Context, repo Repository, id int64) ([]Issue, error)
	// CloseMilestone closes milestone id, keeping the day it is due. It is safe
	// to repeat.
	CloseMilestone(ctx context.Context, repo Repository, id int64) error
}
