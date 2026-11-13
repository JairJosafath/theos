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

package model

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Triage decisions a triager reports, as the API names them.
const (
	TriageReady     = "ready"
	TriageSpec      = "spec"
	TriagePark      = "park"
	TriageDuplicate = "duplicate"
)

// TriageDecisions are the decisions a triager may report, in the order its
// prompt lists them.
var TriageDecisions = []string{TriageReady, TriageSpec, TriagePark, TriageDuplicate}

// Bounds of a triage decision: it travels in the pod's termination message
// and is kept in the Task's status.
const (
	// MaxTriageCriteria is how many acceptance criteria a ready decision keeps.
	MaxTriageCriteria = 8
	// maxTriageCriterion and maxTriageText bound a criterion, and a reason or
	// question, in bytes.
	maxTriageCriterion = 500
	maxTriageText      = 1900
)

// Triage is what a triager decided about an issue: one Decision and its
// Reason, with the Criteria of a ready one, the Question a person must answer
// for a parked one, and the issue a duplicate one duplicates.
type Triage struct {
	Decision  string   `json:"decision"`
	Reason    string   `json:"reason,omitempty"`
	Criteria  []string `json:"criteria,omitempty"`
	Question  string   `json:"question,omitempty"`
	Duplicate int      `json:"duplicate,omitempty"`
	// Environments are the test environments a ready issue needs, by name:
	// the operator labels it env/<name> for each the project has
	// (docs/plans/done/test-environments.md, T13).
	Environments []string `json:"environments,omitempty"`
	// Risk is RiskHigh when a mistake in the issue's change would be costly
	// or hard to undo: the operator labels the issue risk/high, which a
	// Crew's approval rules can name (docs/design.md, "Approval rules"; #92).
	Risk string `json:"risk,omitempty"`
}

// RiskHigh is the one risk a triager names; an issue is otherwise unmarked.
const RiskHigh = "high"

// LabelRiskHigh marks an issue whose change, done wrong, is costly or hard to
// undo. It decides nothing by itself: a Crew's approval rule that names it
// says what such a change needs.
const LabelRiskHigh = "risk/high"

// RiskLabel is the label the triager's risk, or a person, puts on an issue.
func RiskLabel() IssueLabel {
	return IssueLabel{LabelRiskHigh, "#e99695",
		"A mistake here is costly or hard to undo; the Crew's approval rules can hold its change for a person", false}
}

// checkbox is a checklist item's or a list item's marker, which a criterion
// may come with.
var checkbox = regexp.MustCompile(`^(?:[-*]\s+)?(?:\[[ xX]\]\s*)?`)

// NormalTriage is d as the operator keeps it: the decision in lower case, each
// text trimmed and bounded, each criterion one line without its marker, the
// empty ones dropped, at most MaxTriageCriteria, and a risk that is RiskHigh
// or nothing.
func NormalTriage(d Triage) Triage {
	out := Triage{Decision: strings.ToLower(strings.TrimSpace(d.Decision)),
		Reason: clip(strings.TrimSpace(d.Reason), maxTriageText), Question: clip(strings.TrimSpace(d.Question), maxTriageText),
		Duplicate: d.Duplicate, Environments: normalEnvironments(d.Environments)}
	if strings.EqualFold(strings.TrimSpace(d.Risk), RiskHigh) {
		out.Risk = RiskHigh
	}
	for _, c := range d.Criteria {
		c = strings.Join(strings.Fields(c), " ")
		c = strings.TrimSpace(checkbox.ReplaceAllString(c, ""))
		if c != "" && len(out.Criteria) < MaxTriageCriteria {
			out.Criteria = append(out.Criteria, clip(c, maxTriageCriterion))
		}
	}
	return out
}

// TriageProblem says why a decision about issue can't be applied as it
// stands, or returns "": an unknown decision, no reason, a park with no
// question, or a duplicate of no issue or of the issue itself. Whether a ready
// decision needs criteria depends on the issue's body (TriageActions).
func TriageProblem(issue int, d Triage) string {
	switch {
	case !slices.Contains(TriageDecisions, d.Decision):
		return fmt.Sprintf("the triager's decision %q is none of %s", d.Decision, strings.Join(TriageDecisions, ", "))
	case d.Reason == "":
		return "the triager gave no reason for its decision"
	case d.Decision == TriagePark && d.Question == "":
		return "the triager parked the issue without the question a person must answer"
	case d.Decision == TriageDuplicate && d.Duplicate < 1:
		return "the triager called the issue a duplicate without naming the issue it duplicates"
	case d.Decision == TriageDuplicate && d.Duplicate == issue:
		return fmt.Sprintf("the triager called issue #%d a duplicate of itself", issue)
	}
	return ""
}

// TriageIssue is the issue a Triage Task decided about, as the operator reads
// it before it acts.
type TriageIssue struct {
	Number int
	Body   string
	Labels []string
	Closed bool
	// Environments are the project's test environments, by name.
	Environments []string
}

// TriageActions are what the operator does to an issue for a triage decision.
type TriageActions struct {
	// Problem says why the decision can't be applied: the Task fails with it,
	// and the issue stays as it is.
	Problem string
	// Skip says why nothing is done: a person closed or triaged the issue
	// while the triager ran. The decision stays in the Task's status.
	Skip string
	// Body is the issue's new body, its acceptance criteria written in; empty
	// leaves the body as it is.
	Body string
	// Label is the status label the issue gets in place of status/proposed;
	// empty for none.
	Label string
	// EnvLabels are the env/<name> labels a ready issue gets, one per test
	// environment the decision names that the project has.
	EnvLabels []string
	// RiskLabel is LabelRiskHigh for an issue the decision calls high-risk
	// that stays open and doesn't have the label yet.
	RiskLabel string
	// Comment says on the issue what was decided and why.
	Comment string
	// Close closes the issue after the comment.
	Close bool
	// Applied says what was done, for the Task's status.
	Applied string
}

// PlanTriage decides what the operator does to issue for decision d of Task
// task: a ready issue gets its criteria written into a body that has none and
// status/ready; one that needs a spec status/needs-spec; a parked one
// status/needs-human and the question; a duplicate is closed. Each gets a
// comment with the reason, and one that stays open and is called high-risk
// the risk/high label, which is never taken off again. An issue that is closed, or no longer labelled
// status/proposed, was decided by a person meanwhile, and keeps that.
func PlanTriage(task string, issue TriageIssue, d Triage) TriageActions {
	switch {
	case issue.Closed:
		return TriageActions{Skip: fmt.Sprintf("Issue #%d was closed while it was triaged; nothing done", issue.Number)}
	case !slices.Contains(issue.Labels, LabelProposed):
		now := "no status label"
		if i := slices.IndexFunc(issue.Labels, func(l string) bool { return strings.HasPrefix(l, "status/") }); i >= 0 {
			now = issue.Labels[i]
		}
		return TriageActions{Skip: fmt.Sprintf("Issue #%d was triaged by a person meanwhile (it has %s); nothing done",
			issue.Number, now)}
	}
	if problem := TriageProblem(issue.Number, d); problem != "" {
		return TriageActions{Problem: problem}
	}
	out := planOpenTriage(task, issue, d)
	if out.Problem != "" || out.Close || d.Risk != RiskHigh || slices.Contains(issue.Labels, LabelRiskHigh) {
		return out
	}
	out.RiskLabel = LabelRiskHigh
	out.Comment += fmt.Sprintf("\n\nLabelled %s: a mistake in this change would be costly or hard to undo. What "+
		"that asks of the change is the Crew's to say, in an approval rule that names the label.", LabelRiskHigh)
	out.Applied += " and " + LabelRiskHigh
	return out
}

// planOpenTriage is PlanTriage for a decision that can be applied to an open
// issue still status/proposed, before its risk.
func planOpenTriage(task string, issue TriageIssue, d Triage) TriageActions {
	by := "Triaged by Task " + task
	switch d.Decision {
	case TriageReady:
		out := TriageActions{Label: LabelReady, Applied: "Labelled " + LabelReady,
			Comment: fmt.Sprintf("%s: ready to build. %s", by, d.Reason)}
		var names []string
		for _, name := range d.Environments {
			if slices.Contains(issue.Environments, name) {
				names = append(names, name)
				out.EnvLabels = append(out.EnvLabels, LabelEnvPrefix+name)
			}
		}
		if len(names) > 0 {
			out.Comment += fmt.Sprintf("\n\nIts runs lease the test environment %s, which the project has.",
				joinAnd(names))
			out.Applied += " and " + joinAnd(out.EnvLabels)
		}
		if missingCriteria(issue.Body) != "" {
			if len(d.Criteria) == 0 {
				return TriageActions{Problem: "the triager called the issue ready, but gave no acceptance criteria " +
					"and the issue has none"}
			}
			out.Body = WithAcceptanceCriteria(issue.Body, d.Criteria)
			out.Comment += "\n\nThe acceptance criteria are now in the description."
			out.Applied += fmt.Sprintf(" and wrote %d acceptance %s into it", len(d.Criteria),
				plural(len(d.Criteria), "criterion", "criteria"))
		}
		return out
	case TriageSpec:
		return TriageActions{Label: LabelNeedsSpec, Applied: "Labelled " + LabelNeedsSpec,
			Comment: fmt.Sprintf("%s: this issue needs a spec before it is built. %s", by, d.Reason)}
	case TriagePark:
		return TriageActions{Label: LabelNeedsHuman, Applied: "Labelled " + LabelNeedsHuman + " and asked its question",
			Comment: fmt.Sprintf("%s: this issue needs a person's answer before agents go on.\n\n**Question:** %s\n\n"+
				"Why: %s\n\nAnswer here, make the description say what to build with an \"Acceptance criteria\" "+
				"checklist, and label it %s.", by, d.Question, d.Reason, LabelReady)}
	}
	return TriageActions{Close: true, Applied: fmt.Sprintf("Closed as a duplicate of #%d", d.Duplicate),
		Comment: fmt.Sprintf("%s: a duplicate of #%d. %s", by, d.Duplicate, d.Reason)}
}

// WithAcceptanceCriteria is body with criteria as its "Acceptance criteria"
// checklist, when it has none: under its empty "Acceptance criteria" heading,
// or in a section of their own at its end. A body with a checklist stays as
// it is.
func WithAcceptanceCriteria(body string, criteria []string) string {
	if missingCriteria(body) == "" || len(criteria) == 0 {
		return body
	}
	var items strings.Builder
	for _, c := range criteria {
		items.WriteString("- [ ] " + c + "\n")
	}
	if loc := criteriaHeading.FindStringIndex(body); loc != nil {
		end := len(body)
		if i := strings.IndexByte(body[loc[0]:], '\n'); i >= 0 {
			end = loc[0] + i
		}
		rest := strings.TrimLeft(body[end:], "\n")
		if rest != "" {
			rest = "\n" + rest
		}
		return body[:end] + "\n\n" + items.String() + rest
	}
	body = strings.TrimRight(body, " \t\n")
	if body != "" {
		body += "\n\n"
	}
	return body + "## Acceptance criteria\n\n" + items.String()
}

// TriageFailedComment is what a failed Triage Task says on its issue, which
// stays status/proposed: the Crew tries again until tries Triage Tasks of the
// issue failed, MaxIssueAttempts, and then leaves it to a person.
func TriageFailedComment(task, message string, tries int) string {
	out := fmt.Sprintf("Triage by Task %s failed: %s\n\nThe issue stays %s; ", task, message, LabelProposed)
	if tries < MaxIssueAttempts {
		return out + fmt.Sprintf("the Crew triages it again (try %d of %d failed).", tries, MaxIssueAttempts)
	}
	return out + fmt.Sprintf("that was the last of %d tries, so a person triages it.", MaxIssueAttempts)
}

// ProposedIssue is an open issue labelled status/proposed, and its
// milestone's title, empty when it has none.
type ProposedIssue struct {
	Number    int
	Milestone string
}

// TriageTask is a Triage Task on a Crew's Container: its issue, and whether
// it ended and failed, and whether a person answered its parked issue's
// question from chat (PlanChatAnswer), which sends the issue back to be
// triaged again.
type TriageTask struct {
	Name     string
	Issue    int
	Final    bool
	Failed   bool
	Answered bool
}

// TriageFacts is everything a Crew's triage depends on.
type TriageFacts struct {
	// On means the Crew names a triager.
	On             bool
	Paused         bool
	ContainerReady bool
	// Proposed are the open issues labelled status/proposed.
	Proposed []ProposedIssue
	// Tasks are the Container's Triage Tasks.
	Tasks []TriageTask
}

// TriageStep is a Crew's triage: the Triage Task at work, or the one it
// starts now.
type TriageStep struct {
	// Active is the Triage Task at work, and ActiveIssue its issue.
	Active      string
	ActiveIssue int
	// Start is the Triage Task to create; nil when none.
	Start *TriageStart
}

// TriageStart is a Triage Task to create for an issue.
type TriageStart struct {
	Issue   int
	Task    string
	Attempt int
}

// NextTriage decides a Crew's triage: with a triager, not paused and its
// Container Ready, and no Triage Task at work, it starts one for the lowest
// proposed issue outside the Later milestone that no Triage Task decided (a
// park a person answered from chat decided nothing), and whose triage has
// not failed MaxIssueAttempts times. One triage runs at a time, and none
// takes a place from the Crew's parallel.
func NextTriage(container string, f TriageFacts) TriageStep {
	var step TriageStep
	tries, decided := map[int]int{}, map[int]bool{}
	failed := map[int]int{}
	for _, t := range f.Tasks {
		tries[t.Issue]++
		switch {
		case !t.Final:
			if step.Active == "" || t.Name < step.Active {
				step.Active, step.ActiveIssue = t.Name, t.Issue
			}
		case t.Failed:
			failed[t.Issue]++
		case t.Answered:
		default:
			decided[t.Issue] = true
		}
	}
	if !f.On || f.Paused || !f.ContainerReady || step.Active != "" {
		return step
	}
	issues := slices.Clone(f.Proposed)
	slices.SortFunc(issues, func(a, b ProposedIssue) int { return a.Number - b.Number })
	for _, issue := range issues {
		// Reviewers' follow-ups stay a person's call (FollowUpMilestone).
		if issue.Milestone == FollowUpMilestone || decided[issue.Number] || failed[issue.Number] >= MaxIssueAttempts {
			continue
		}
		attempt := tries[issue.Number] + 1
		step.Start = &TriageStart{Issue: issue.Number, Attempt: attempt,
			Task: TriageTaskName(container, issue.Number, attempt)}
		return step
	}
	return step
}

// TriageTaskName names the Triage Task of an issue: <container>-<issue>-triage,
// with the attempt from the second one on.
func TriageTaskName(container string, issue, attempt int) string {
	name := container + "-" + strconv.Itoa(issue) + "-triage"
	if attempt > 1 {
		name += "-" + strconv.Itoa(attempt)
	}
	return name
}

// AddTriage adds a Crew's triage to its step: the Triage Task it starts, or
// that works, in the message, and Working for a Crew that has nothing else at
// work.
func (s *CrewStep) AddTriage(t TriageStep) {
	var note string
	switch {
	case t.Start != nil:
		note = fmt.Sprintf("starting Triage Task %s for issue #%d", t.Start.Task, t.Start.Issue)
	case t.Active != "":
		note = fmt.Sprintf("Task %s triages issue #%d", t.Active, t.ActiveIssue)
	default:
		return
	}
	if s.Phase == CrewIdle {
		s.Phase = CrewWorking
	}
	s.Message += "; " + note
}

// StandInTriage is the stand-in triager's decision for an issue's text, so
// the whole loop can run without a model: "stand-in: spec" in it needs a
// spec, "stand-in: park" parks it, "stand-in: duplicate of #N" makes it a
// duplicate of N, and anything else is ready, with two criteria. "stand-in:
// risk high" makes any of them high-risk.
func StandInTriage(issue string) Triage {
	text := strings.ToLower(issue)
	d := standInDecision(text)
	if strings.Contains(text, "stand-in: risk high") {
		d.Risk = RiskHigh
	}
	return d
}

// standInDecision is the stand-in triager's decision for an issue's text in
// lower case, before its risk.
func standInDecision(text string) Triage {
	switch {
	case strings.Contains(text, "stand-in: spec"):
		return Triage{Decision: TriageSpec, Reason: "Stand-in triage: the issue asks for a spec."}
	case strings.Contains(text, "stand-in: park"):
		return Triage{Decision: TriagePark, Reason: "Stand-in triage: the issue asks to be parked.",
			Question: "Stand-in question: what should this do?"}
	}
	if m := standInDuplicate.FindStringSubmatch(text); m != nil {
		n, _ := strconv.Atoi(m[1])
		return Triage{Decision: TriageDuplicate, Duplicate: n, Reason: "Stand-in triage: the issue names its original."}
	}
	return Triage{Decision: TriageReady, Reason: "Stand-in triage: clear enough to build.",
		Criteria: []string{"The change does what the issue asks", "make check passes"}}
}

var standInDuplicate = regexp.MustCompile(`stand-in: duplicate of #(\d+)`)

// clip is s in at most n bytes, cut at a rune and ended with "…" when cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
