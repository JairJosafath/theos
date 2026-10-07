package model

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// A failed issue is one of a Brief's Containers' issues handed back to a
// person (status/needs-human): by a failed Task, by a Crew refusing it, or by
// a person. The Brief lists it with why, and the person answers it in a prompt:
// try it again, or close it (plans/brief-failed-issues.md).

// The kinds of failed issue, by why it was handed back.
const (
	// FailedUnclear has no acceptance criteria, so the Crew refused it.
	FailedUnclear = "unclear"
	// FailedMerged was merged by a Task already.
	FailedMerged = "merged"
	// FailedExhausted failed MaxIssueAttempts Tasks.
	FailedExhausted = "exhausted"
	// FailedTask is a failed Task's: its newest failed Task's message.
	FailedTask = "failed"
	// FailedBlocked is a blocked Task's: its agent said it can't start the
	// issue yet, and why. Retry it once its blocker lands.
	FailedBlocked = "blocked"
	// FailedPerson was handed back by a person.
	FailedPerson = "person"
)

// Answers to a failed issue.
const (
	AnswerRetry = "retry"
	AnswerClose = "close"
)

// MaxFailedIssues is how many waiting failed issues a Brief lists, in its
// status and its workspace.
const MaxFailedIssues = 50

// MaxClarify is how long a retry's clarification may be.
const MaxClarify = 4000

// PlanFailedFile is where a plan answers the failed issues its round's prompts
// speak to, and WorkspaceFailedFile where the planner reads them.
const (
	PlanFailedFile      = "plan/failed.yaml"
	WorkspaceFailedFile = "model/failed.yaml"
)

// IsFailedIssue reports whether an open issue is handed back to a person.
func IsFailedIssue(labels []string) bool { return slices.Contains(labels, LabelNeedsHuman) }

// FailedIssue is a failed issue waiting in a Container's repository.
type FailedIssue struct {
	Container string
	Number    int
	Title     string
	URL       string
	Body      string
	// Kind and Reason say why it was handed back (FailedReason).
	Kind   string
	Reason string
	// Tries is how many Tasks the issue has had.
	Tries int
}

// IssueTask is a Task on a failed issue's Container, as FailedReason sees it.
type IssueTask struct {
	Name string
	// Issue is the Task's issue; 0 for a Task with a written goal.
	Issue int
	// Merged and Failed say how a finished Task ended; neither while it works.
	Merged bool
	Failed bool
	// Blocked means it failed because its agent said it can't start the issue.
	Blocked bool
	// Message is the Task's status message.
	Message string
	Created time.Time
}

// FailedReason decides why issue number, with body, was handed back, from its
// Container's Tasks, the first that holds: no acceptance criteria (unclear); a
// Task merged it (merged); MaxIssueAttempts Tasks failed (exhausted); its
// newest failed Task's message (blocked, when its agent said it can't start
// the issue; else failed); else a person (person). Tries counts the issue's
// Tasks.
func FailedReason(number int, body string, tasks []IssueTask) (kind, reason string, tries int) {
	var mine []IssueTask
	for _, t := range tasks {
		if t.Issue == number {
			mine = append(mine, t)
		}
	}
	slices.SortStableFunc(mine, func(a, b IssueTask) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	tries = len(mine)
	var failed []string
	merged := ""
	for _, t := range mine {
		switch {
		case t.Merged:
			merged = t.Name
		case t.Failed:
			failed = append(failed, t.Name)
		}
	}
	if r := missingCriteria(body); r != "" {
		return FailedUnclear, r + " Say in a prompt what it should do, so it can be tried again with acceptance criteria.",
			tries
	}
	if merged != "" {
		return FailedMerged, fmt.Sprintf("Task `%s` already merged it; close it, or file the rest as a new issue.",
			merged), tries
	}
	if len(failed) >= MaxIssueAttempts {
		return FailedExhausted, fmt.Sprintf("Agents tried it %d times (Tasks %s) and failed each time; "+
			"say in a prompt to file it again, clarified or split.", len(failed), quoted(failed)), tries
	}
	for _, t := range slices.Backward(mine) {
		if !t.Failed {
			continue
		}
		kind, reason := FailedTask, fmt.Sprintf("Task `%s` failed", t.Name)
		if t.Blocked {
			kind, reason = FailedBlocked, fmt.Sprintf("Task `%s` is blocked", t.Name)
		}
		if msg := oneParagraph(t.Message); msg != "" {
			reason += ": " + msg
		}
		return kind, reason, tries
	}
	return FailedPerson, "Handed back by a person; see the issue.", tries
}

// FailedMessage is the sentence a Brief says its failed issues in: the first
// of listed and its reason, how many more of waiting there are, and that a
// prompt answers them. It is empty when none waits.
func FailedMessage(listed []FailedIssue, waiting int) string {
	if waiting <= 0 || len(listed) == 0 {
		return ""
	}
	first := listed[0]
	msg := fmt.Sprintf("Issue %s#%d %q was handed back: %s", first.Container, first.Number, oneParagraph(first.Title),
		strings.TrimRight(oneParagraph(first.Reason), "."))
	if more := waiting - 1; more > 0 {
		msg += fmt.Sprintf("; and %d more", more)
	}
	return msg + ". Say in a prompt whether to try again."
}

// quoted lists names as `a`, `b`.
func quoted(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "`" + n + "`"
	}
	return strings.Join(out, ", ")
}

// FailedAnswer is a plan's answer to one failed issue.
type FailedAnswer struct {
	Container string `yaml:"container"`
	Issue     int    `yaml:"issue"`
	Answer    string `yaml:"answer"`
	// Why says, in plain words, which prompt asked for it and how.
	Why string `yaml:"why"`
	// Clarify is appended to a retried issue's body; retry only, optional.
	Clarify string `yaml:"clarify,omitempty"`
}

// ClarifiedBody is a retried issue's body: body with clarify appended under a
// heading naming the Brief's round. The Crew reads the whole of it, so an
// unclear issue's acceptance criteria are checked here.
func ClarifiedBody(body string, round int, clarify string) string {
	clarify = strings.TrimSpace(clarify)
	if clarify == "" {
		return body
	}
	return fmt.Sprintf("%s\n\n## Clarification (Brief round %d)\n\n%s\n", strings.TrimRight(body, "\n"), round, clarify)
}

// parseFailedAnswers reads plan/failed.yaml: a list of answers.
func parseFailedAnswers(s string) ([]FailedAnswer, error) {
	var answers []FailedAnswer
	dec := yaml.NewDecoder(strings.NewReader(s))
	dec.KnownFields(true)
	if err := dec.Decode(&answers); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("want a list of {container, issue, answer, why, clarify}: %w", err)
	}
	return answers, nil
}

// CheckFailedAnswers returns every reason a plan's answers to failed issues
// can't be applied: each answers a waiting failed issue once, retries or closes
// it and says why, and retries only one the Crew would take: not merged, not
// exhausted, and an unclear one only with a clarification that has acceptance
// criteria.
func CheckFailedAnswers(answers []FailedAnswer, waiting []FailedIssue) []Refusal {
	var out []Refusal
	refuse := func(format string, args ...any) {
		out = append(out, Refusal{PlanFailedFile, fmt.Sprintf(format, args...)})
	}
	seen := map[string]bool{}
	for _, a := range answers {
		subject := fmt.Sprintf("%s#%d", a.Container, a.Issue)
		i := slices.IndexFunc(waiting, func(f FailedIssue) bool { return f.Container == a.Container && f.Number == a.Issue })
		switch {
		case seen[subject]:
			refuse("answers %s twice; answer each failed issue once", subject)
			continue
		case i < 0:
			refuse("answers %s, which is not a waiting failed issue; answer only those %s lists",
				subject, WorkspaceFailedFile)
			continue
		}
		seen[subject] = true
		switch a.Answer {
		case AnswerRetry:
			switch waiting[i].Kind {
			case FailedMerged:
				refuse("retries %s, which a Task merged already: close it, and file the rest as a new issue", subject)
			case FailedExhausted:
				refuse("retries %s, which agents tried %d times: close it, and file it again as a new issue, "+
					"clarified or split", subject, MaxIssueAttempts)
			case FailedUnclear:
				if strings.TrimSpace(a.Clarify) == "" || missingCriteria(ClarifiedBody(waiting[i].Body, 0, a.Clarify)) != "" {
					refuse("retries %s, which has no acceptance criteria, without adding them: clarify it with an "+
						"\"## Acceptance criteria\" checklist (\"- [ ] ...\")", subject)
				}
			}
			if len(a.Clarify) > MaxClarify {
				refuse("clarifies %s with %d bytes; keep it under %d", subject, len(a.Clarify), MaxClarify)
			}
		case AnswerClose:
			if strings.TrimSpace(a.Clarify) != "" {
				refuse("closes %s with a clarify; only a retry clarifies", subject)
			}
		default:
			refuse("answers %s %q; an answer is %s or %s", subject, a.Answer, AnswerRetry, AnswerClose)
		}
		if strings.TrimSpace(a.Why) == "" {
			refuse("answers %s with no why: say in plain words which prompt asked for it", subject)
		} else if len(a.Why) > MaxAnswerWhy {
			refuse("answers %s with a why of %d bytes; keep it under %d", subject, len(a.Why), MaxAnswerWhy)
		}
	}
	return out
}

type failedOut struct {
	Container string `yaml:"container"`
	Issue     int    `yaml:"issue"`
	Title     string `yaml:"title"`
	Kind      string `yaml:"kind"`
	Reason    string `yaml:"reason"`
	Tries     int    `yaml:"tries"`
	Body      string `yaml:"body"`
}

// workspaceFailed renders model/failed.yaml: the first MaxFailedIssues
// waiting failed issues, by Container and number.
func workspaceFailed(failed []FailedIssue) []failedOut {
	sorted := slices.Clone(failed)
	slices.SortFunc(sorted, func(a, b FailedIssue) int {
		if c := strings.Compare(a.Container, b.Container); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	sorted = sorted[:min(len(sorted), MaxFailedIssues)]
	out := make([]failedOut, 0, len(sorted))
	for _, f := range sorted {
		out = append(out, failedOut{f.Container, f.Number, oneParagraph(f.Title), f.Kind, oneParagraph(f.Reason),
			f.Tries, strings.TrimSpace(f.Body)})
	}
	return out
}
