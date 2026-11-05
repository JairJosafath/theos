package model

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A Spec Task writes an issue's spec in a pull request (docs/plans/done/sdlc-loop.md,
// "Spec"): the problem, the approach and an "Acceptance criteria" checklist in
// SpecPath. After CI and the reviewer it waits for its review point, then
// merges, and the operator copies the spec's criteria into the issue, which is
// then ready to build and stays open.

// SpecDir is where specs live in a Container's repository.
const SpecDir = "docs/specs/"

// Who approves a spec's pull request before it merges, mirroring the API.
const (
	// SpecApprovalPerson waits for a person, someone other than the factory's
	// own accounts, to approve the pull request on the Git host.
	SpecApprovalPerson = "Person"
	// SpecApprovalReviewer takes the reviewer Agent's approval as enough.
	SpecApprovalReviewer = "Reviewer"
)

// Reasons of a Spec Task: waiting for a person's approval, and failed because
// a person closed its pull request unmerged.
const (
	ReasonAwaitingApproval = "WaitingForApproval"
	ReasonSpecDeclined     = "SpecDeclined"
)

// MaxSpecCriteria is how many of a spec's acceptance criteria are copied into
// its issue, and maxSpecCriterion how long each may be, in bytes.
const (
	MaxSpecCriteria  = 20
	maxSpecCriterion = 500
)

// SpecPath is the spec file of issue: docs/specs/<issue>.md.
func SpecPath(issue int) string { return SpecDir + strconv.Itoa(issue) + ".md" }

// SpecTaskName names the Spec Task of an issue: <container>-<issue>-spec, with
// the attempt from the second one on.
func SpecTaskName(container string, issue, attempt int) string {
	name := container + "-" + strconv.Itoa(issue) + "-spec"
	if attempt > 1 {
		name += "-" + strconv.Itoa(attempt)
	}
	return name
}

// SpecTitle is the title of a spec's pull request.
func SpecTitle(issueTitle string) string { return "Spec: " + strings.TrimSpace(issueTitle) }

// SpecStray are the paths of changed other than the spec file path: a spec
// changes nothing else.
func SpecStray(path string, changed []string) []string {
	var out []string
	for _, p := range changed {
		if p != path {
			out = append(out, p)
		}
	}
	return out
}

// SpecScopeRefusal is the refusal of a spec round that changed stray paths
// beside its spec file. It is a ScopeRefusal: a second in a row stops the
// Task (ScopeBlockedMessage).
func SpecScopeRefusal(path string, stray []string) string {
	return fmt.Sprintf("%sthe change touches %s; a spec changes only %s: put the rest in the spec, as what the "+
		"issue's implementation will do", ScopePrefix, strings.Join(stray, ", "), path)
}

// specCheckbox is a checklist item, its text in group 1.
var specCheckbox = regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]\s+(\S.*)$`)

// SpecCriteria are the checklist items under the spec's first "Acceptance
// criteria" heading, up to its next heading: each on one line without its
// marker, bounded, at most MaxSpecCriteria.
func SpecCriteria(spec string) []string {
	section, ok := criteriaSection(spec)
	if !ok {
		return nil
	}
	var out []string
	for _, m := range specCheckbox.FindAllStringSubmatch(section, -1) {
		c := strings.Join(strings.Fields(m[1]), " ")
		if c != "" && len(out) < MaxSpecCriteria {
			out = append(out, clip(c, maxSpecCriterion))
		}
	}
	return out
}

// SpecRefusal says why a spec round can't go to review, or returns "": its
// pull request changes files beside the spec file (a SpecScopeRefusal), or the
// spec file is missing (found false) or has no acceptance criteria.
func SpecRefusal(path string, changed []string, spec string, found bool) string {
	if stray := SpecStray(path, changed); len(stray) > 0 {
		return SpecScopeRefusal(path, stray)
	}
	if !found || strings.TrimSpace(spec) == "" {
		return fmt.Sprintf("The pull request has no spec: write it in %s, with the problem, the approach and an "+
			"\"Acceptance criteria\" checklist", path)
	}
	if len(SpecCriteria(spec)) == 0 {
		return fmt.Sprintf("%s has no \"Acceptance criteria\" checklist (\"- [ ] ...\" under that heading): the issue "+
			"gets its criteria from it, so a reviewer can tell when its implementation is done", path)
	}
	return ""
}

// Review states as the Git host reports them.
const (
	reviewApproved = "APPROVED"
	reviewChanges  = "REQUEST_CHANGES"
)

// PullReview is a review submitted on a pull request: its user, state
// (APPROVED, REQUEST_CHANGES or COMMENT), the commit it reviewed and its body.
// A stale or dismissed review counts no more.
type PullReview struct {
	User      string
	State     string
	Commit    string
	Body      string
	Stale     bool
	Dismissed bool
}

// PersonVerdict is what people said of a pull request's commit: who approved
// it, or who asked for changes and what; ApprovedInChat and ChangesInChat say
// they said it from chat (ChatSpecVerdict), not on the Git host.
type PersonVerdict struct {
	ApprovedBy     string
	ChangesBy      string
	Changes        string
	ApprovedInChat bool
	ChangesInChat  bool
}

// PersonReviews is the verdict of the people, not factory accounts
// (factory), on commit: each person's latest approval or request for changes
// that is neither stale nor dismissed and reviewed commit (or no commit the
// host named). A request for changes outweighs another person's approval.
func PersonReviews(reviews []PullReview, factory func(user string) bool, commit string) PersonVerdict {
	latest := map[string]PullReview{}
	var order []string
	for _, r := range reviews {
		if r.State != reviewApproved && r.State != reviewChanges || factory(r.User) {
			continue
		}
		if !slices.Contains(order, r.User) {
			order = append(order, r.User)
		}
		latest[r.User] = r
	}
	var v PersonVerdict
	for _, user := range order {
		r := latest[user]
		if r.Stale || r.Dismissed || r.Commit != "" && r.Commit != commit {
			continue
		}
		switch {
		case r.State == reviewChanges && v.ChangesBy == "":
			v.ChangesBy, v.Changes = user, strings.TrimSpace(r.Body)
		case r.State == reviewApproved && v.ApprovedBy == "":
			v.ApprovedBy = user
		}
	}
	if v.ChangesBy != "" {
		v.ApprovedBy = ""
	}
	return v
}

// FactoryAccount reports whether user is one of the factory's own accounts on
// a project's repository: the operator's user, or an agent bot of the project
// (AgentUsername). Neither's review is a person's.
func FactoryAccount(user, operator, project string) bool {
	u := strings.ToLower(user)
	return u == strings.ToLower(operator) ||
		strings.HasPrefix(u, strings.ToLower(project)+"-") && strings.HasSuffix(u, "-agent")
}

// SpecWaitMessage is the status of a spec waiting for a person to approve its
// pull request: what to do, and where.
func SpecWaitMessage(issue int, pullURL string) string {
	return fmt.Sprintf("The spec of issue #%d passed CI and the reviewer; approve pull request %s on the Git host "+
		"to merge it, ask for changes there for another round, or close it to decline the spec", issue,
		orUnknown(pullURL))
}

// SpecDeclinedMessage is the failure of a Spec Task whose pull request a
// person closed unmerged.
func SpecDeclinedMessage(pullURL string) string {
	return fmt.Sprintf("the spec was declined: pull request %s was closed without merging", orUnknown(pullURL))
}

// SpecIssue is the issue a Spec Task wrote the spec of, as the operator reads
// it once the spec merged.
type SpecIssue struct {
	Number int
	Body   string
	Closed bool
}

// SpecMerged is what the operator does to a spec's issue once the spec merged.
type SpecMerged struct {
	// Skip says why nothing is done: a person closed the issue meanwhile.
	Skip string
	// Body is the issue's new description, the spec's criteria in it; empty
	// leaves it as it is.
	Body string
	// Criteria are the spec's acceptance criteria.
	Criteria []string
	// Label is the status label the issue gets: status/ready, or
	// status/needs-human when the merged spec has no criteria.
	Label string
	// Comment says on the issue what was done.
	Comment string
	// Applied says what was done, for the Task's status.
	Applied string
}

// PlanSpecMerged decides what the operator does to issue once Task task
// merged its spec in pull request pullURL: the spec's acceptance criteria go
// into the description, with a link to the spec, in place of any it had, and
// the issue is labelled status/ready, so the Crew builds it. It stays open. A
// merged spec without criteria (a person merged one by hand) hands the issue
// to a person; a closed issue is left as it is.
func PlanSpecMerged(task string, issue SpecIssue, path, specURL, pullURL, spec string) SpecMerged {
	if issue.Closed {
		return SpecMerged{Skip: fmt.Sprintf("Issue #%d was closed while its spec was written; nothing done",
			issue.Number)}
	}
	criteria := SpecCriteria(spec)
	if len(criteria) == 0 {
		return SpecMerged{Label: LabelNeedsHuman,
			Applied: "Labelled " + LabelNeedsHuman + ": the merged spec has no acceptance criteria",
			Comment: fmt.Sprintf("Task `%s` merged the spec in %s, but %s has no \"Acceptance criteria\" checklist, "+
				"so the issue can't be built from it. Add the criteria to the description and label it %s.",
				task, pullURL, path, LabelReady)}
	}
	n := len(criteria)
	return SpecMerged{Body: WithSpecCriteria(issue.Body, path, specURL, criteria), Criteria: criteria,
		Label: LabelReady,
		Applied: fmt.Sprintf("Wrote %d acceptance %s from %s into the description and labelled %s", n,
			plural(n, "criterion", "criteria"), path, LabelReady),
		Comment: fmt.Sprintf("Task `%s` merged the spec in %s: [%s](%s). Its %d acceptance %s are now in the "+
			"description, and the issue is %s, so the Crew builds it.", task, pullURL, path, specURL, n,
			plural(n, "criterion", "criteria"), LabelReady)}
}

// WithSpecCriteria is body with criteria, from the spec at path (specURL), as
// its "Acceptance criteria" checklist: in place of the section's text when it
// has that heading, else in a section of its own at its end.
func WithSpecCriteria(body, path, specURL string, criteria []string) string {
	var section strings.Builder
	fmt.Fprintf(&section, "From the spec [%s](%s).\n\n", path, specURL)
	for _, c := range criteria {
		section.WriteString("- [ ] " + c + "\n")
	}
	if loc := criteriaHeading.FindStringIndex(body); loc != nil {
		end := len(body)
		if i := strings.IndexByte(body[loc[0]:], '\n'); i >= 0 {
			end = loc[0] + i
		}
		rest := ""
		if next := anyHeading.FindStringIndex(body[end:]); next != nil {
			rest = "\n" + body[end+next[0]:]
		}
		return body[:end] + "\n\n" + section.String() + rest
	}
	body = strings.TrimRight(body, " \t\n")
	if body != "" {
		body += "\n\n"
	}
	return body + "## Acceptance criteria\n\n" + section.String()
}

// StandInSpec is the stand-in spec writer's spec of an issue, so the whole
// loop can run without a model.
func StandInSpec(issue int, goal string) string {
	title, _, _ := strings.Cut(strings.TrimSpace(goal), "\n")
	return fmt.Sprintf("# Spec of issue #%d\n\n## Problem\n\nStand-in spec: %s\n\n## Approach\n\n"+
		"The stand-in agent adds a line to STAND-IN.md.\n\n## Acceptance criteria\n\n"+
		"- [ ] STAND-IN.md has a line for the issue\n- [ ] make check passes\n", issue, strings.TrimSpace(title))
}
