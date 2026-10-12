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

// LeavesAllowedAsOnBase reports whether a Standard change's changed paths
// touch none of the paths it is for: nothing is left for its gated merge.
func LeavesAllowedAsOnBase(changed, allowed []string) bool {
	return !slices.ContainsFunc(changed, func(p string) bool { return coveredByAny(allowed, p) })
}

// EmptiedMessage is the message of a Standard change whose round left every
// path it is for as on main, most often because the review found the change
// itself breaks a rule a Standard change keeps: more rounds and a review can't
// give it anything to merge, so it stops. feedback is what the round was sent
// back with, which names that rule.
func EmptiedMessage(allowed []string, feedback string) string {
	msg := fmt.Sprintf("The change leaves %s as on main, so a Standard change has nothing to merge", joinAnd(allowed))
	if fb := ClipSentences(strings.Join(strings.Fields(feedback), " "), MaxBlockedNote); fb != "" {
		msg += ". The round before was sent back with: " + fb
	}
	return msg
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

// StandardVersion is what a Container's Standard files render from: the
// Standard's template ref and when CI runs the full check. Checks is
// ChecksEveryChange or ChecksBeforeRelease; empty reads as ChecksEveryChange.
type StandardVersion struct {
	Ref, Checks string
}

// Normal returns v with empty checks read as ChecksEveryChange.
func (v StandardVersion) Normal() StandardVersion {
	if v.Checks == "" {
		v.Checks = ChecksEveryChange
	}
	return v
}

// String is the ref, followed by "with the full check before release" when
// the checks are BeforeRelease.
func (v StandardVersion) String() string {
	if v.Checks == ChecksBeforeRelease {
		return v.Ref + " with the full check before release"
	}
	return v.Ref
}

// StandardUpdateBranch is the branch that updates a Container to version to of
// the Standard name: "standard-update/<name>-<ref>", where every character of
// ref outside [A-Za-z0-9._-] becomes "-", followed by "-before-release" when
// the checks are BeforeRelease.
func StandardUpdateBranch(name string, to StandardVersion) string {
	branch := "standard-update/" + name + "-" + branchSafe(to.Ref)
	if to.Checks == ChecksBeforeRelease {
		branch += "-before-release"
	}
	return branch
}

// StandardTakeBranch is the branch that takes the Standard name's version at
// ref of the files its updates left out: "standard-take/<name>-<ref>", ref as
// in StandardUpdateBranch.
func StandardTakeBranch(name, ref string) string {
	return "standard-take/" + name + "-" + branchSafe(ref)
}

// branchSafe is ref with every character outside [A-Za-z0-9._-] made "-".
func branchSafe(ref string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, ref)
}

// StandardUpdateTitle is the title of that update's pull request and commit:
// "Update the Standard <name> to <ref>", followed by when CI runs the full
// check when the update changes that.
func StandardUpdateTitle(name string, from, to StandardVersion) string {
	title := "Update the Standard " + name + " to " + to.Ref
	if from, to := from.Normal(), to.Normal(); from.Checks != to.Checks {
		title += ", with the full check " + checksPhrase(to.Checks)
	}
	return title
}

// checksPhrase says when CI runs the full check: "on every change" or "before
// release".
func checksPhrase(checks string) string {
	if checks == ChecksBeforeRelease {
		return "before release"
	}
	return "on every change"
}

// LeftOutFile is a file a Standard update leaves out: what the Standard
// changed in it, and what taking the Standard's version would drop.
type LeftOutFile struct {
	Path string
	// Ref is the Standard's ref the repository's copy was last brought to.
	Ref string
	// Diff is the unified diff of the Standard's renders of the file at Ref
	// and at the update's ref, its hunks only, each line without its ending.
	Diff []string
	// Note says what the Standard did to the file as a whole, or why no diff
	// shows, or "".
	Note string
	// Take is what taking the Standard's version would do to the file.
	Take TakenFile
}

// LeftOut is what a Standard update's body says of the files it leaves out.
type LeftOut struct {
	Files []LeftOutFile
	// Take and Keep are the commands that settle them: take the Standard's
	// version, or keep the repository's.
	Take, Keep string
}

// Bounds of a Standard update's body: the diff lines shown per left-out file,
// and the body's length, below GitHub's 65,536 characters.
const (
	maxDiffLines   = 200
	maxBodyLength  = 60000
	bodyCloseSlack = 1000
)

// StandardUpdateBody is that pull request's description. merged are the paths
// it writes that the repository changed too, merged with the repository's
// changes.
func StandardUpdateBody(name string, from, to StandardVersion, paths, merged []string, leftOut LeftOut) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The operator opened this pull request to bring the files of the Standard `%s` from `%s` to `%s`",
		name, from.Ref, to.Ref)
	if from, to := from.Normal(), to.Normal(); from.Checks != to.Checks {
		fmt.Fprintf(&b, ", and CI from running the full check %s to running it %s", checksPhrase(from.Checks),
			checksPhrase(to.Checks))
	}
	b.WriteString(".\n\n")
	if len(paths) > 0 {
		b.WriteString("## Files\n\n")
		writeCodeList(&b, paths)
	}
	if len(merged) > 0 {
		fmt.Fprintf(&b, "## Merged with the repository's changes\n\nThese files changed in the repository since `%s` "+
			"too, in other lines, so the update merges the Standard's changes with the repository's, line by line; "+
			"read them as a merge:\n\n", from.Ref)
		writeCodeList(&b, merged)
	}
	if len(leftOut.Files) > 0 {
		writeLeftOut(&b, to.Ref, leftOut)
	}
	b.WriteString("The operator merges this pull request itself once CI passes. " +
		"Close it to drop the update until the Standard's ref or the Container's checks change again.\n")
	return b.String()
}

// writeLeftOut writes the section of an update's body on the files it leaves
// out: per file, the Standard's change and the repository's lines a take would
// drop, each file whole or, once the body would pass maxBodyLength, only named.
func writeLeftOut(b *strings.Builder, to string, leftOut LeftOut) {
	b.WriteString("## Left out\n\nThese files changed in the repository in lines the Standard changed too, so the " +
		"update leaves them out. Below is what the Standard changed in each, and the repository's lines taking " +
		"the Standard's version would drop; every other line the repository changed stays. Once this pull " +
		"request merges, a person settles them all on the Container:\n\n")
	fmt.Fprintf(b, "- take the Standard's version: `%s`\n- or keep the repository's: `%s`\n\n", leftOut.Take, leftOut.Keep)
	for i, f := range leftOut.Files {
		var section strings.Builder
		fmt.Fprintf(&section, "### `%s`\n\n", f.Path)
		fmt.Fprintf(&section, "What the Standard changed in it from `%s` to `%s`", f.Ref, to)
		if f.Note != "" {
			fmt.Fprintf(&section, ": %s", f.Note)
		}
		section.WriteString("\n\n")
		if len(f.Diff) > 0 {
			lines := append([]string{fmt.Sprintf("--- %s (%s)", f.Path, f.Ref), fmt.Sprintf("+++ %s (%s)", f.Path, to)},
				f.Diff...)
			writeDiff(&section, lines, maxDiffLines+2)
		}
		switch {
		case f.Take.Note != "":
			fmt.Fprintf(&section, "Taking it: %s\n\n", f.Take.Note)
		case len(f.Take.Dropped) == 0:
			section.WriteString("Taking it drops none of the repository's lines.\n\n")
		}
		if len(f.Take.Dropped) > 0 {
			section.WriteString("The repository's lines taking it drops:\n\n")
			writeDropped(&section, f.Take.Dropped)
		}
		if b.Len()+section.Len() > maxBodyLength-bodyCloseSlack {
			writeTooLarge(b, leftOut.Files[i:])
			return
		}
		b.WriteString(section.String())
	}
}

// writeTooLarge names the left-out files past the body's bound, as many as fit.
func writeTooLarge(b *strings.Builder, files []LeftOutFile) {
	b.WriteString("Too large to show in this pull request:")
	for i, f := range files {
		if b.Len()+len(f.Path)+4 > maxBodyLength-bodyCloseSlack/2 {
			fmt.Fprintf(b, " and %d more files", len(files)-i)
			break
		}
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(b, " `%s`", f.Path)
	}
	b.WriteString(".\n\n")
}

// TakenFile is one file a take writes or removes, and the repository's lines
// it drops for the Standard's.
type TakenFile struct {
	Path string
	// Dropped are the stretches of the repository's lines the take replaces
	// with the Standard's, each line with its ending.
	Dropped [][]string
	// Note says what the take does with the file as a whole, when it doesn't
	// merge it line by line, or "".
	Note string
}

// maxDroppedLines bounds the dropped lines a take's body shows per file.
const maxDroppedLines = 100

// StandardTakeTitle is the title of the pull request and commit that take the
// Standard name's version at ref of paths: "Bring <paths> up to date with
// <name> <ref>", naming at most three paths.
func StandardTakeTitle(name, ref string, paths []string) string {
	what := joinAnd(paths)
	if len(paths) > 3 {
		what = fmt.Sprintf("%d files", len(paths))
	}
	return fmt.Sprintf("Bring %s up to date with %s %s", what, name, ref)
}

// StandardTakeBody is that pull request's description: per file, the
// repository's lines it drops.
func StandardTakeBody(name, ref string, files []TakenFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "A person took the Standard's version of files the updates of `%s` left out, at `%s`, so the "+
		"operator opened this pull request. Each file is merged with the repository's changes line by line; where "+
		"both changed the same lines, the Standard's lines win. The repository's lines it drops are listed per "+
		"file: add back what is still needed in a later change.\n\n", name, ref)
	for _, f := range files {
		fmt.Fprintf(&b, "## `%s`\n\n", f.Path)
		if f.Note != "" {
			b.WriteString(f.Note + "\n\n")
		}
		if len(f.Dropped) == 0 {
			if f.Note == "" {
				b.WriteString("Drops none of the repository's lines.\n\n")
			}
			continue
		}
		b.WriteString("The repository's lines it drops:\n\n")
		writeDropped(&b, f.Dropped)
	}
	b.WriteString("The operator merges this pull request itself once CI passes. Close it to keep the files as " +
		"they are; they stay waiting on a person.\n")
	return b.String()
}

// writeDropped writes stretches of lines as a diff of removed lines, a gap
// between stretches, at most maxDroppedLines of them.
func writeDropped(b *strings.Builder, stretches [][]string) {
	var lines []string
	for i, stretch := range stretches {
		if i > 0 {
			lines = append(lines, "@@")
		}
		for _, line := range stretch {
			lines = append(lines, "-"+strings.TrimRight(line, "\r\n"))
		}
	}
	writeDiff(b, lines, maxDroppedLines)
}

// writeDiff writes lines as a diff block, at most limit of them.
func writeDiff(b *strings.Builder, lines []string, limit int) {
	more := 0
	if len(lines) > limit {
		lines, more = lines[:limit], len(lines)-limit
	}
	// The fence is longer than any run of backticks in the lines.
	fence := "```"
	for _, line := range lines {
		for strings.Contains(line, fence) {
			fence += "`"
		}
	}
	fmt.Fprintf(b, "%sdiff\n%s\n%s\n\n", fence, strings.Join(lines, "\n"), fence)
	if more > 0 {
		fmt.Fprintf(b, "And %d more lines.\n\n", more)
	}
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

// When a Container's CI runs its full check (its spec.checks).
const (
	// ChecksEveryChange runs the full check on every pull request and push to main.
	ChecksEveryChange = "EveryChange"
	// ChecksBeforeRelease runs a light check on each change and the full one on
	// the commit a release will be tagged at.
	ChecksBeforeRelease = "BeforeRelease"
)

// EffectiveChecks is what a Container's templates render: BeforeRelease only
// when the Container asks for it and its Standard can do it, else EveryChange.
// asked is the Container's spec.checks, where empty reads as EveryChange.
func EffectiveChecks(asked string, standardCan bool) string {
	if asked == ChecksBeforeRelease && standardCan {
		return ChecksBeforeRelease
	}
	return ChecksEveryChange
}
