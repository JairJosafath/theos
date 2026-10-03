package model

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// AgentNeverPaths are the protected paths no agent may change, even through a
// Standard change: the agents' own rules and the platform's contract.
var AgentNeverPaths = []string{"AGENTS.md", "docs/agents/", ".c4factory.yaml"}

const (
	// MaxStandardChanges caps the Standard changes one Task may propose.
	MaxStandardChanges = 2
	// MaxProposalPaths caps the paths one Standard change may carry.
	MaxProposalPaths = 20
)

// LabelStandardChange marks an issue the operator filed to change protected files
// for another issue.
const LabelStandardChange = "kind/standard-change"

// StandardChangeLabel is the label the operator keeps for Standard change issues.
func StandardChangeLabel() IssueLabel {
	return IssueLabel{LabelStandardChange, "#5319e7", "Changes protected files for another issue; filed by the operator", false}
}

// Proposal is a Standard change an agent asks for: a branch holding the changes
// to protected paths, and why they are needed.
type Proposal struct {
	Branch string   `json:"branch"`
	Paths  []string `json:"paths"`
	Reason string   `json:"reason,omitempty"`
}

// ChangeSplit sorts a change's paths by who may make them: the Task, a Standard
// change proposal, or nobody.
type ChangeSplit struct {
	Task, Proposal, Refused []string
}

// SplitChange sorts the changed paths. With allowed set the Task is gated: a path
// it covers is the Task's, every other path is refused. Otherwise an unprotected
// path is the Task's, a protected one under AgentNeverPaths is refused, and any
// other protected one is proposed when canPropose, else refused. Order follows
// changed.
func SplitChange(changed, protected, allowed []string, canPropose bool) ChangeSplit {
	var s ChangeSplit
	for _, p := range changed {
		switch {
		case len(allowed) > 0:
			if coveredByAny(allowed, p) {
				s.Task = append(s.Task, p)
			} else {
				s.Refused = append(s.Refused, p)
			}
		case !coveredByAny(protected, p):
			s.Task = append(s.Task, p)
		case !canPropose || coveredByAny(AgentNeverPaths, p):
			s.Refused = append(s.Refused, p)
		default:
			s.Proposal = append(s.Proposal, p)
		}
	}
	return s
}

func coveredByAny(prots []string, path string) bool {
	return slices.ContainsFunc(prots, func(prot string) bool { return covers(prot, path) })
}

// EligibleAllowance keeps the paths a Task may be allowed to change: those not
// under AgentNeverPaths.
func EligibleAllowance(paths []string) []string {
	var out []string
	for _, p := range paths {
		if !coveredByAny(AgentNeverPaths, p) {
			out = append(out, p)
		}
	}
	return out
}

// ProposalBranch names the branch holding a Task's Standard change.
func ProposalBranch(task string) string { return "standard/" + task }

// ProposalMarker is the hidden line in a Standard change issue that identifies
// its branch, so the operator finds the issue again.
func ProposalMarker(branch string) string {
	return "<!-- c4factory:standard-change " + branch + " -->"
}

// HasProposalMarker reports whether body carries branch's marker on a line of its
// own, as StandardChangeIssue writes it.
func HasProposalMarker(body, branch string) bool {
	marker := ProposalMarker(branch)
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

const reasonPrefix = "Standard change:"

// ProposalReason takes the reason from an agent's summary: everything after the
// first line starting "Standard change:", else the whole summary, trimmed.
func ProposalReason(summary string) string {
	lines := strings.Split(summary, "\n")
	for i, l := range lines {
		if rest, ok := strings.CutPrefix(l, reasonPrefix); ok {
			return strings.TrimSpace(strings.Join(append([]string{rest}, lines[i+1:]...), "\n"))
		}
	}
	return strings.TrimSpace(summary)
}

// maxIssueTitle is the longest issue title, in bytes, the Git host stores.
const maxIssueTitle = 255

// standardChangeTitle names a Standard change issue by the issue that waits on it
// and the paths it changes. A list too long for a title becomes the first path
// and how many more, cut short if it must be; the body lists them all.
func standardChangeTitle(issue int, paths []string) string {
	prefix := fmt.Sprintf("Standard change for #%d: ", issue)
	if title := prefix + joinAnd(paths); len(title) <= maxIssueTitle || len(paths) == 0 {
		return title
	}
	first, more := paths[0], ""
	if n := len(paths) - 1; n > 0 {
		more = fmt.Sprintf(" and %d more", n)
	}
	if room := maxIssueTitle - len(prefix) - len(more); len(first) > room {
		const ellipsis = "…"
		cut := room - len(ellipsis)
		for cut > 0 && !utf8.RuneStart(first[cut]) {
			cut--
		}
		first = first[:cut] + ellipsis
	}
	return prefix + first + more
}

// StandardChangeIssue renders the issue that asks a person to review a Task's
// proposal for the issue that waits on it.
func StandardChangeIssue(issue int, task string, p Proposal) (title, body string) {
	quoted := make([]string, len(p.Paths))
	for i, path := range p.Paths {
		quoted[i] = "`" + path + "`"
	}
	title = standardChangeTitle(issue, p.Paths)
	var b strings.Builder
	fmt.Fprintf(&b, "Task `%s` needs protected files changed for #%d, which waits on this issue.\n\n", task, issue)
	fmt.Fprintf(&b, "## Why\n\n%s\n\n", p.Reason)
	fmt.Fprintf(&b, "## Change\n\nChange %s; merge `origin/%s` first.\n\n", joinAnd(quoted), p.Branch)
	b.WriteString("## Acceptance criteria\n\n")
	fmt.Fprintf(&b, "- [ ] %s change as proposed on `origin/%s`, and nothing else\n", joinAnd(quoted), p.Branch)
	b.WriteString("- [ ] The change weakens no check: no target, test, lint rule or CI job is removed or skipped\n")
	b.WriteString("- [ ] `main` works with this change alone\n\n")
	// The agent wrote the reason and named the paths: no comment of theirs may pass
	// for a marker, so the operator never takes this issue for another branch's.
	body = strings.ReplaceAll(b.String(), "<!--", "&lt;!--") + ProposalMarker(p.Branch) + "\n"
	return title, body
}
