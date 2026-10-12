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

// SplitChange sorts the changed paths. An unprotected path is the Task's. With
// allowed set the Task is gated: a protected path it covers is the Task's too,
// and every other protected path is refused, so a Standard change may carry the
// unprotected changes its checks need (a test that pins what a protected file
// produces). Otherwise a protected path under AgentNeverPaths is refused, and
// any other protected one is proposed when canPropose, else refused. Order
// follows changed.
func SplitChange(changed, protected, allowed []string, canPropose bool) ChangeSplit {
	var s ChangeSplit
	gated := len(allowed) > 0
	for _, p := range changed {
		switch {
		case gated && coveredByAny(allowed, p), !coveredByAny(protected, p) && (!gated || !coveredFold(protected, p)):
			// A gated Task's extra path must not even differ from a protected
			// one in case only: its merge bypasses the Git host's protection.
			s.Task = append(s.Task, p)
		case gated, !canPropose, coveredByAny(AgentNeverPaths, p):
			s.Refused = append(s.Refused, p)
		default:
			s.Proposal = append(s.Proposal, p)
		}
	}
	return s
}

// StandardChangeExtras are the paths of a Standard change outside its allowed
// paths: the unprotected files it changes so the repository's checks pass.
func StandardChangeExtras(changed, allowed []string) []string {
	var out []string
	for _, p := range changed {
		if !coveredByAny(allowed, p) {
			out = append(out, p)
		}
	}
	return out
}

// ScopePrefix starts a round's error when the change touches paths the Task
// may not change.
const ScopePrefix = "Out of scope: "

// ScopeRefusal is a round's error when the change touches refused paths: with
// allowed set, protected files outside a Standard change's paths; otherwise
// files nobody may change through a Task.
func ScopeRefusal(refused, allowed []string) string {
	if len(allowed) > 0 {
		return fmt.Sprintf("%sthe change touches %s; this Task may change only %s, and files no Standard protects",
			ScopePrefix, strings.Join(refused, ", "), strings.Join(allowed, ", "))
	}
	return fmt.Sprintf("%sthe change touches %s, which the platform and the Standard protect; leave them as they are",
		ScopePrefix, strings.Join(refused, ", "))
}

// IsScopeRefusal reports whether a round's error or feedback is a ScopeRefusal.
func IsScopeRefusal(s string) bool { return strings.HasPrefix(s, ScopePrefix) }

// ScopeBlockedMessage is the message of a Task whose round was turned back for
// scope twice in a row: more rounds won't change that, so it stops.
func ScopeBlockedMessage(refusal string) string {
	return "Two rounds in a row changed files this Task may not change, so it stops instead of spending " +
		"more rounds: " + strings.TrimPrefix(refusal, ScopePrefix)
}

func coveredByAny(prots []string, path string) bool {
	return slices.ContainsFunc(prots, func(prot string) bool { return covers(prot, path) })
}

// coveredFold is coveredByAny ignoring case, as the Git host matches its
// protected file patterns: `makefile` falls under a protected `Makefile`.
func coveredFold(prots []string, path string) bool {
	path = strings.ToLower(path)
	return slices.ContainsFunc(prots, func(prot string) bool { return covers(strings.ToLower(prot), path) })
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

// ProposalBranchOf returns the branch whose marker body carries on a line of
// its own, the proposal of a Standard change issue; "" when it has none.
func ProposalBranchOf(body string) string {
	prefix, suffix, _ := strings.Cut(ProposalMarker("\x00"), "\x00")
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if branch, ok := strings.CutPrefix(line, prefix); ok {
			if branch, ok := strings.CutSuffix(branch, suffix); ok && branch != "" && !strings.ContainsAny(branch, " \t") {
				return branch
			}
		}
	}
	return ""
}

// ProposalGoneNote says a Standard change's proposal branch is gone, so a
// retry would have nothing to merge.
func ProposalGoneNote(branch string) string {
	return fmt.Sprintf("This Standard change's proposal branch `%s` is gone, so a retry would have nothing to merge: close it, "+
		"and retry the issue that waits on it, which proposes the change again.", branch)
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

// StandardUpdateBranch is the branch that updates a Container to ref of the
// Standard name: "standard-update/<name>-<ref>", where every character of ref
// outside [A-Za-z0-9._-] becomes "-".
func StandardUpdateBranch(name, ref string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, ref)
	return "standard-update/" + name + "-" + safe
}

// StandardUpdateTitle is the title of that update's pull request and commit:
// "Update the Standard <name> to <ref>".
func StandardUpdateTitle(name, ref string) string {
	return "Update the Standard " + name + " to " + ref
}

// StandardUpdateBody is that pull request's description. merged are the paths
// it writes that the repository changed too, merged with the repository's
// changes.
func StandardUpdateBody(name, from, to string, paths, merged, leftOut []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The operator opened this pull request to bring the files of the Standard `%s` from `%s` to `%s`.\n\n",
		name, from, to)
	if len(paths) > 0 {
		b.WriteString("## Files\n\n")
		writeCodeList(&b, paths)
	}
	if len(merged) > 0 {
		fmt.Fprintf(&b, "## Merged with the repository's changes\n\nThese files changed in the repository since `%s` "+
			"too, in other lines, so the update merges the Standard's changes with the repository's, line by line; "+
			"read them as a merge:\n\n", from)
		writeCodeList(&b, merged)
	}
	if len(leftOut) > 0 {
		fmt.Fprintf(&b, "## Left out\n\nThese files changed in the repository since `%s` in ways that don't "+
			"merge with the Standard's changes, so the update leaves them out; a person brings them up to date by hand:\n\n", from)
		writeCodeList(&b, leftOut)
	}
	b.WriteString("The operator merges this pull request itself once CI passes. " +
		"Close it to drop the update until the Standard's ref changes again.\n")
	return b.String()
}

// writeCodeList writes items as a Markdown list in code font, then a blank line.
func writeCodeList(b *strings.Builder, items []string) {
	for _, item := range items {
		fmt.Fprintf(b, "- `%s`\n", item)
	}
	b.WriteString("\n")
}

// StandardUpdateRefusal says why a Standard update's pull request may not merge,
// or "" when it may: CI is green, every check main's protection requires
// passed, and it changes something and only the planned paths.
func StandardUpdateRefusal(checks string, contexts map[string]string, changed, planned []string) string {
	if refusal := checksRefusal(checks, contexts); refusal != "" {
		return refusal
	}
	if len(changed) == 0 {
		return "the change changes nothing"
	}
	var outside []string
	for _, p := range changed {
		if !slices.Contains(planned, p) {
			outside = append(outside, p)
		}
	}
	if len(outside) > 0 {
		return fmt.Sprintf("the pull request changes %s, which the update did not plan", joinAnd(outside))
	}
	return ""
}

// ScaffoldedRef returns the ref the catalog entry name was scaffolded at, from
// scaffoldedFrom's "<name>@<ref>" entries, or "" when it wasn't part of it.
func ScaffoldedRef(scaffoldedFrom []string, name string) string {
	for _, pinned := range scaffoldedFrom {
		if entry, ref, ok := strings.Cut(pinned, "@"); ok && entry == name {
			return ref
		}
	}
	return ""
}
