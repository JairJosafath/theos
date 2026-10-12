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
	// FailedByHand is a by-hand issue: criteria only a person can do.
	FailedByHand = "byHand"
)

// Answers to a failed issue: try it again or close it, or leave it handed
// back, waiting until something changed or asking the person
// (plans/done/brief-autopilot.md, decision 6).
const (
	AnswerRetry = "retry"
	AnswerClose = "close"
	AnswerWait  = "wait"
	AnswerAsk   = "ask"
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
	// WorkspaceTendedFile lists what rounds answered, so a prompt can overrule it.
	WorkspaceTendedFile = "model/tended.yaml"
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
	// Release is a release step's milestone as it is now; nil for another
	// issue.
	Release *FailedRelease
	// ProposalGone is the proposal branch of a Standard change whose branch is
	// gone from the Git host, which no retry can merge; "" for another issue.
	ProposalGone string
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
	// NoCI means it failed through its CI runner's fault (model.RunnerFault); it costs no try.
	NoCI bool
	// Message is the Task's status message.
	Message string
	Created time.Time
	// Completed is when it ended; zero while it works.
	Completed time.Time
}

// FailedReason decides why issue number, with labels and body, was handed
// back, from its Container's Tasks, the first that holds: labelled kind/by-hand
// (byHand, with the whys its body gives); no acceptance criteria (unclear); a
// Task merged it (merged); MaxIssueAttempts Tasks failed, not counting those
// CI never reported on (exhausted); its
// newest failed Task's message (blocked, when its agent said it can't start
// the issue; else failed); else a person (person). Tries counts the issue's
// Tasks.
func FailedReason(number int, labels []string, body string, tasks []IssueTask) (kind, reason string, tries int) {
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
		case t.Failed && !t.NoCI:
			failed = append(failed, t.Name)
		}
	}
	if IsByHand(labels) {
		return FailedByHand, ByHandReason(body), tries
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
	// Recommend is what an ask would do if the person agrees; ask only.
	Recommend *Recommendation `yaml:"recommend,omitempty"`
}

// Recommendation is the answer an ask recommends: retry, close or wait, why,
// and a retry's clarification.
type Recommendation struct {
	Answer  string `yaml:"answer"`
	Why     string `yaml:"why"`
	Clarify string `yaml:"clarify,omitempty"`
}

// Retries reports whether the answer retries the issue, or recommends it.
func (a FailedAnswer) Retries() bool {
	return a.Answer == AnswerRetry || a.Answer == AnswerAsk && a.Recommend != nil && a.Recommend.Answer == AnswerRetry
}

// Closes reports whether the answer closes the issue, or recommends it.
func (a FailedAnswer) Closes() bool {
	return a.Answer == AnswerClose || a.Answer == AnswerAsk && a.Recommend != nil && a.Recommend.Answer == AnswerClose
}

// Applies reports whether the answer changes the issue on the Git host: a
// retry or a close. A wait or an ask leaves it handed back.
func (a FailedAnswer) Applies() bool { return a.Answer == AnswerRetry || a.Answer == AnswerClose }

// clarifiable reports whether some clarify could give body acceptance
// criteria. The Crew reads only the first "Acceptance criteria" section, so a
// body whose own section has no checklist can't be fixed by one appended below.
func clarifiable(body string) bool {
	return missingCriteria(ClarifiedBody(body, 0, "## Acceptance criteria\n\n- [ ] done\n")) == ""
}

// ClarifiedBody is a retried issue's body: body with clarify appended under a
// heading naming the Brief's round. The Crew reads the whole of it, so an
// unclear issue's acceptance criteria are checked here. A body that has the
// round's heading already is left as it is, so a try that stops part way
// doesn't append it twice.
func ClarifiedBody(body string, round int, clarify string) string {
	clarify = strings.TrimSpace(clarify)
	heading := fmt.Sprintf("## Clarification (Brief round %d)", round)
	if clarify == "" || round > 0 && strings.Contains(body, heading) {
		return body
	}
	return fmt.Sprintf("%s\n\n%s\n\n%s\n", strings.TrimRight(body, "\n"), heading, clarify)
}

// FailedComment is what the operator writes on a failed issue it answers for
// Brief brief in round: the answer, the planner's why and the round's
// prompts, quoted; a close also names the issues the round filed, which may
// carry its work on. A tending round has no prompt and says so.
func FailedComment(a FailedAnswer, brief string, round int, tending bool, prompts, filed []string) string {
	var b strings.Builder
	by := fmt.Sprintf("for Brief `%s` in round %d", brief, round)
	if tending {
		by = fmt.Sprintf("by Brief `%s` in tending round %d, with no prompt: its autopilot is On", brief, round)
	}
	if a.Answer == AnswerRetry {
		fmt.Fprintf(&b, "Tried again %s. The Crew picks it up as %s.", by, LabelReady)
		if strings.TrimSpace(a.Clarify) != "" {
			fmt.Fprintf(&b, " The issue's body has a clarification for round %d.", round)
		}
	} else {
		fmt.Fprintf(&b, "Closed %s.", by)
	}
	fmt.Fprintf(&b, "\n\nThe planner: %s", oneParagraph(a.Why))
	if a.Answer == AnswerClose && len(filed) > 0 {
		b.WriteString("\n\nThe round filed:\n")
		for _, f := range filed {
			fmt.Fprintf(&b, "\n- %s", f)
		}
	}
	if len(prompts) > 0 {
		b.WriteString("\n\nThe round's prompts:\n")
		for _, p := range prompts {
			fmt.Fprintf(&b, "\n> %s\n", oneParagraph(p))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// CloseRefusedComment is what the operator writes on a failed issue Brief
// brief's round answered with a close the Git host refused: the issue waits on
// open issues, blockers, the round didn't close. It stays handed back.
func CloseRefusedComment(a FailedAnswer, brief string, round int, tending bool, blockers []string) string {
	by := fmt.Sprintf("Brief `%s`'s round %d", brief, round)
	if tending {
		by = fmt.Sprintf("Brief `%s`'s tending round %d", brief, round)
	}
	return fmt.Sprintf("%s answered this with close, but it can't be closed: it waits on %s, which the round "+
		"didn't close. It stays handed back: close those, or answer it again in a prompt.\n\nThe planner: %s",
		by, strings.Join(blockers, ", "), oneParagraph(a.Why))
}

// CloseRefused says, for a round's message, which failed issues it answered
// with a close the Git host refused, each as "container#3 (waits on #4)".
func CloseRefused(refused []string) string {
	if len(refused) == 0 {
		return ""
	}
	return fmt.Sprintf("; could not close %s, which %s handed back: close what %s on first",
		strings.Join(refused, ", "), plural(len(refused), "stays", "stay"), plural(len(refused), "it waits", "they wait"))
}

// parseFailedAnswers reads plan/failed.yaml: a list of answers.
func parseFailedAnswers(s string) ([]FailedAnswer, error) {
	var answers []FailedAnswer
	dec := yaml.NewDecoder(strings.NewReader(s))
	dec.KnownFields(true)
	if err := dec.Decode(&answers); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("want a list of {container, issue, answer, why, clarify, recommend}: %w", err)
	}
	if err := checkCutText(s); err != nil {
		return nil, err
	}
	return answers, nil
}

// checkCutText says which text field, a why or a clarify, YAML cuts short:
// in an unquoted value, " #" starts a comment, so `why: Its only change, #136,
// shipped.` reads "Its only change,", and so does a value wrapped before
// "#136". Malformed YAML is the decoder's to say.
func checkCutText(s string) error {
	var doc yaml.Node
	if yaml.Unmarshal([]byte(s), &doc) != nil {
		return nil
	}
	lines := strings.Split(s, "\n")
	var walk func(n *yaml.Node) error
	walk = func(n *yaml.Node) error {
		for i := 0; n.Kind == yaml.MappingNode && i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if (k.Value != "why" && k.Value != "clarify") || v.Kind != yaml.ScalarNode || v.Style != 0 {
				continue
			}
			comment := v.LineComment
			// A plain value goes on over the lines indented past its key; a
			// comment among them cuts it.
			for _, line := range lines[min(v.Line, len(lines)):] {
				text := strings.TrimLeft(line, " ")
				if text == "" {
					continue
				}
				if len(line)-len(text) < k.Column {
					break
				}
				if strings.HasPrefix(text, "#") {
					comment = text
					break
				}
			}
			if comment != "" {
				return fmt.Errorf("line %d: %s: reads only %q, since YAML takes %q for a comment; "+
					"quote the text, or write it as a block (%s: |)", v.Line, k.Value, v.Value, comment, k.Value)
			}
		}
		for _, c := range n.Content {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(&doc)
}

// CheckFailedAnswers returns every reason a plan's answers to failed issues
// can't be applied: each answers a waiting failed issue once, retries, closes,
// waits on or asks about it and says why, and retries only one the Crew would
// take: not merged, not exhausted, not by hand, and an unclear one only with a
// clarification that has acceptance criteria. It closes no release step whose
// milestone gained merged work after its last try stopped. An ask recommends one of the
// other answers, checked the same way.
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
		case AnswerRetry, AnswerClose, AnswerWait:
			for _, r := range checkAnswer(a.Answer, a.Clarify, waiting[i], subject) {
				refuse("%s", r)
			}
			for _, r := range checkReleaseClose(a.Answer, waiting[i], subject) {
				refuse("%s", r)
			}
			if a.Recommend != nil {
				refuse("%s %s with a recommend; only an ask recommends", verb(a.Answer), subject)
			}
		case AnswerAsk:
			if strings.TrimSpace(a.Clarify) != "" {
				refuse("asks about %s with a clarify; put it in the recommend", subject)
			}
			r := a.Recommend
			switch {
			case r == nil:
				refuse("asks about %s with no recommend: say what you would do (%s, %s or %s) and why",
					subject, AnswerRetry, AnswerClose, AnswerWait)
			case r.Answer != AnswerRetry && r.Answer != AnswerClose && r.Answer != AnswerWait:
				refuse("asks about %s recommending %q; recommend %s, %s or %s", subject, r.Answer,
					AnswerRetry, AnswerClose, AnswerWait)
			default:
				for _, reason := range checkAnswer(r.Answer, r.Clarify, waiting[i], subject) {
					refuse("recommends: %s", reason)
				}
				for _, reason := range checkReleaseClose(r.Answer, waiting[i], subject) {
					refuse("recommends: %s", reason)
				}
				checkWhy(refuse, "recommends for", subject, r.Why)
			}
		default:
			refuse("answers %s %q; an answer is %s, %s, %s or %s", subject, a.Answer,
				AnswerRetry, AnswerClose, AnswerWait, AnswerAsk)
		}
		checkWhy(refuse, "answers", subject, a.Why)
	}
	return out
}

// verb is how a refusal says answer: "retries", "closes", "waits on".
func verb(answer string) string {
	switch answer {
	case AnswerRetry:
		return "retries"
	case AnswerClose:
		return "closes"
	case AnswerWait:
		return "waits on"
	}
	return "asks about"
}

// checkWhy refuses a missing or overlong why.
func checkWhy(refuse func(string, ...any), what, subject, why string) {
	if strings.TrimSpace(why) == "" {
		refuse("%s %s with no why: say in plain words why", what, subject)
	} else if len(why) > MaxAnswerWhy {
		refuse("%s %s with a why of %d bytes; keep it under %d", what, subject, len(why), MaxAnswerWhy)
	}
}

// checkAnswer returns why answer (retry, close or wait) with clarify can't be
// done to issue f: only a retry clarifies, and only one the Crew would take.
func checkAnswer(answer, clarify string, f FailedIssue, subject string) []string {
	var out []string
	if answer != AnswerRetry {
		if strings.TrimSpace(clarify) != "" {
			out = append(out, fmt.Sprintf("%s %s with a clarify; only a retry clarifies", verb(answer), subject))
		}
		return out
	}
	if f.ProposalGone != "" {
		out = append(out, fmt.Sprintf("retries %s, a Standard change whose proposal branch `%s` is gone, so its "+
			"Task would have nothing to merge: close it, and retry the issue that waits on it", subject, f.ProposalGone))
	}
	switch f.Kind {
	case FailedMerged:
		out = append(out, fmt.Sprintf("retries %s, which a Task merged already: close it, and file the rest "+
			"as a new issue", subject))
	case FailedExhausted:
		out = append(out, fmt.Sprintf("retries %s, which agents tried %d times: close it, and file it again as "+
			"a new issue, clarified or split", subject, MaxIssueAttempts))
	case FailedByHand:
		out = append(out, fmt.Sprintf("retries %s, which needs a person, not agents: close it once the person "+
			"did it or dropped it", subject))
	case FailedUnclear:
		switch {
		case !clarifiable(f.Body):
			out = append(out, fmt.Sprintf("retries %s, whose own \"Acceptance criteria\" section has no "+
				"checklist, so no clarify can add one: close it, and file it again as a new issue with criteria",
				subject))
		case strings.TrimSpace(clarify) == "" || missingCriteria(ClarifiedBody(f.Body, 0, clarify)) != "":
			out = append(out, fmt.Sprintf("retries %s, which has no acceptance criteria, without adding them: "+
				"clarify it with an \"## Acceptance criteria\" checklist (\"- [ ] ...\")", subject))
		}
	}
	if len(clarify) > MaxClarify {
		out = append(out, fmt.Sprintf("clarifies %s with %d bytes; keep it under %d", subject, len(clarify), MaxClarify))
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
	// Asked and Recommend are an ask's question and what the Brief would do,
	// so a prompt saying "do what you suggest" is enough.
	Asked     string          `yaml:"asked,omitempty"`
	Recommend *Recommendation `yaml:"recommend,omitempty"`
	// Release is a release step's milestone as it is now, so the planner
	// judges it by what merged since its last try, not only by why it stopped.
	Release *releaseOut `yaml:"release,omitempty"`
}

// workspaceFailed renders model/failed.yaml: the first MaxFailedIssues
// waiting failed issues, by Container and number, each asked one with its
// recommendation from tended.
func workspaceFailed(failed []FailedIssue, tended []TendedIssue) []failedOut {
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
		entry := failedOut{Container: f.Container, Issue: f.Number, Title: oneParagraph(f.Title), Kind: f.Kind,
			Reason: oneParagraph(f.Reason), Tries: f.Tries, Body: strings.TrimSpace(f.Body),
			Release: workspaceRelease(f.Release)}
		if i := slices.IndexFunc(tended, func(t TendedIssue) bool {
			return t.Container == f.Container && t.Issue == f.Number && t.Answer == AnswerAsk
		}); i >= 0 {
			entry.Asked = oneParagraph(tended[i].Why)
			entry.Recommend = tended[i].Recommend
		}
		out = append(out, entry)
	}
	return out
}

type tendedOut struct {
	Container string          `yaml:"container"`
	Issue     int             `yaml:"issue"`
	Round     int             `yaml:"round"`
	Answer    string          `yaml:"answer"`
	Why       string          `yaml:"why"`
	Recommend *Recommendation `yaml:"recommend,omitempty"`
	At        string          `yaml:"at"`
}

// workspaceTended renders model/tended.yaml: what rounds answered the failed
// issues still handed back, by Container and number, so the next prompt
// round can overrule it.
func workspaceTended(tended []TendedIssue) []tendedOut {
	sorted := slices.Clone(tended)
	slices.SortFunc(sorted, func(a, b TendedIssue) int {
		if c := strings.Compare(a.Container, b.Container); c != 0 {
			return c
		}
		return a.Issue - b.Issue
	})
	sorted = sorted[:min(len(sorted), MaxFailedIssues)]
	out := make([]tendedOut, 0, len(sorted))
	for _, t := range sorted {
		out = append(out, tendedOut{t.Container, t.Issue, t.Round, t.Answer, oneParagraph(t.Why), t.Recommend,
			t.At.UTC().Format(time.RFC3339)})
	}
	return out
}
