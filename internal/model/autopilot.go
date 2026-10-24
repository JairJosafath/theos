package model

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
)

// A Brief looks after its own handed-back work (plans/done/brief-autopilot.md):
// each reconcile it classifies what it made as healing, left alone until a
// time, or due. A due failed issue is the planner's to decide in a tending
// round; anything else due is a fact for the person, listed under Needs you
// with what the Brief would do.

// The autopilot modes of a Brief (decision 1).
const (
	// AutopilotOff tends nothing: nothing happens without a prompt.
	AutopilotOff = "Off"
	// AutopilotAsk tends, and turns every retry and close into an ask.
	AutopilotAsk = "Ask"
	// AutopilotOn tends and applies the answers.
	AutopilotOn = "On"
)

// DefaultAutopilot is the mode of a Brief whose spec sets none: it tends, and
// asks before it applies anything (plans/done/brief-autopilot.md, decision 1).
const DefaultAutopilot = AutopilotAsk

// AutopilotMode is a Brief's mode: mode, or DefaultAutopilot when unset.
func AutopilotMode(mode string) string {
	if mode == "" {
		return DefaultAutopilot
	}
	return mode
}

// TendInterval is the least time between two tending rounds' starts.
const TendInterval = time.Hour

// WaitLapse is how long a wait answer holds when nothing changes.
const WaitLapse = 24 * time.Hour

// MaxNeedsYou is how many Needs you entries a Brief lists.
const MaxNeedsYou = 50

// The stages of a Task that is not final, as the UI shows them (STAGES).
const (
	StageQueued    = "queued"
	StageSlot      = "slot"
	StageImplement = "implement"
	StageChecks    = "checks"
	StageReview    = "review"
	StageBlocked   = "blocked"
)

// StuckAfter is how long a Task may stay in a stage before the Factory view
// says it needs a look; the UI's STUCK_AFTER_MS.
var StuckAfter = map[string]time.Duration{
	StageQueued:    time.Hour,
	StageSlot:      time.Hour,
	StageImplement: 2 * time.Hour,
	StageChecks:    30 * time.Minute,
	StageReview:    time.Hour,
	StageBlocked:   24 * time.Hour,
}

// The heal windows (decision 3), from when the item began.
const (
	// AgentBackGrace is how long past its agentBackAt a Task waits for its agent.
	AgentBackGrace = time.Hour
	// SlotWindow is how long a Task waits for an agent slot.
	SlotWindow = 4 * time.Hour
	// StandardChangeGrace is how long past the end of its Standard change's
	// Task a Task stays blocked on it.
	StandardChangeGrace = time.Hour
	// CrewWindow is how long a Crew waits for its Git host or Container.
	CrewWindow = time.Hour
)

// The kinds of a watched item.
const (
	AttentionIssue     = "Issue"
	AttentionTask      = "Task"
	AttentionCrew      = "Crew"
	AttentionContainer = "Container"
)

// The reasons BriefAttention reads.
const (
	reasonWaitingForSlot           = "WaitingForSlot"
	reasonWaitingForStandardChange = "WaitingForStandardChange"
	containerFailed                = "Failed"
)

// WatchedTask is a Task on one of the Brief's Containers that is not final.
type WatchedTask struct {
	Name      string
	Container string
	// Stage is where the Task is (Stage*), and Since when it got there.
	Stage string
	Since time.Time
	// Reason and Message are its Progressing condition's.
	Reason  string
	Message string
	// AgentBackAt is when its agent is back, while it waits for one.
	AgentBackAt time.Time
	// StandardChange is the issue of the Standard change it waits on, and
	// StandardChangeEnded when that change's Task became final; zero while it
	// works.
	StandardChange      int
	StandardChangeEnded time.Time
}

// WatchedCrew is one of the Brief's Crews that is not Ready.
type WatchedCrew struct {
	Name string
	// Reason and Message are its Ready condition's, and Since when it turned False.
	Reason  string
	Message string
	Since   time.Time
}

// WatchedContainer is one of the Brief's Containers.
type WatchedContainer struct {
	Name    string
	Phase   string
	Message string
	Since   time.Time
}

// TendedIssue is a failed issue a round answered, as the Brief remembers it
// (status.tended): the answer, with the tries the issue had then, so it is
// due again only when that grows or a wait lapses (decision 7).
type TendedIssue struct {
	Container string
	Issue     int
	Answer    string
	Why       string
	Recommend *Recommendation
	Tries     int
	At        time.Time
	// Round is the round that answered it.
	Round int
}

// AttentionFacts is what BriefAttention classifies.
type AttentionFacts struct {
	Failed     []FailedIssue
	Tended     []TendedIssue
	Tasks      []WatchedTask
	Crews      []WatchedCrew
	Containers []WatchedContainer
	// Changed is when something last changed that a wait waits for: a Task on
	// one of the Brief's Containers merged, or one of its apps made a release.
	Changed time.Time
}

// Healing is an item left alone: why, and when the Brief acts if it hasn't
// healed. A zero ActsAt means it acts only when the item changes.
type Healing struct {
	Kind   string
	Name   string
	Reason string
	ActsAt time.Time
}

// NeedsYou is an item only the person can decide: the question in one
// sentence, what the Brief would do and why, and since when.
type NeedsYou struct {
	Kind      string
	Name      string
	Question  string
	Recommend string
	Why       string
	Since     time.Time
}

// Attention is a Brief's watched items, classified.
type Attention struct {
	Healing  []Healing
	NeedsYou []NeedsYou
	// Due are the failed issues a tending round decides: due and unanswered.
	Due []FailedIssue
	// Waiting counts the Needs you entries, those past the MaxNeedsYou listed too.
	Waiting int
}

// BriefAttention classifies a Brief's watched items at now (decisions 3, 4,
// 7 and 11). A by-hand failed issue is the person's, under Needs you. Any
// other failed issue is due unless a round answered it and it hasn't
// changed since: it waits (healing until the wait lapses), it asks (Needs you),
// or it came back with no new try (Needs you). A Task, Crew or Container
// heals inside its window and is listed under Needs you past it.
func BriefAttention(f AttentionFacts, now time.Time) Attention {
	var a Attention
	for _, issue := range f.Failed {
		if issue.Kind == FailedByHand {
			// A person's by design (decision 11): never tended.
			a.NeedsYou = append(a.NeedsYou, NeedsYou{AttentionIssue, fmt.Sprintf("%s#%d", issue.Container, issue.Number),
				fmt.Sprintf("Issue %s#%d %q needs a person: %s", issue.Container, issue.Number,
					oneParagraph(issue.Title), sentence(issue.Reason, "see the issue")),
				"Do it by hand and close it, or say in a prompt to drop it.", "Agents can't do what it asks.",
				time.Time{}})
			continue
		}
		i := slices.IndexFunc(f.Tended, func(t TendedIssue) bool {
			return t.Container == issue.Container && t.Issue == issue.Number
		})
		if i < 0 || issue.Tries > f.Tended[i].Tries {
			a.Due = append(a.Due, issue)
			continue
		}
		t := f.Tended[i]
		name := fmt.Sprintf("%s#%d", issue.Container, issue.Number)
		switch t.Answer {
		case AnswerWait:
			if WaitLapsed(t, f.Changed, now) {
				a.Due = append(a.Due, issue)
				continue
			}
			a.Healing = append(a.Healing, Healing{AttentionIssue, name,
				"Waits until a Task merges or an app releases: " + oneParagraph(t.Why), t.At.Add(WaitLapse)})
		case AnswerAsk:
			a.NeedsYou = append(a.NeedsYou, askEntry(name, issue, t))
		default:
			a.NeedsYou = append(a.NeedsYou, NeedsYou{AttentionIssue, name,
				fmt.Sprintf("Issue %s %q was handed back again after round %d's %s, with no new try.",
					name, oneParagraph(issue.Title), t.Round, t.Answer),
				"See the issue, and say in a prompt what to do with it.", oneParagraph(issue.Reason), t.At})
		}
	}
	for _, t := range f.Tasks {
		h, entry := taskAttention(t, now)
		a.add(h, entry)
	}
	for _, c := range f.Crews {
		h, entry := crewAttention(c, now)
		a.add(h, entry)
	}
	for _, c := range f.Containers {
		if c.Phase != containerFailed {
			continue
		}
		a.NeedsYou = append(a.NeedsYou, NeedsYou{AttentionContainer, c.Name,
			fmt.Sprintf("Container `%s` failed: %s", c.Name, sentence(c.Message, "see the Container")),
			"Fix what it says, or delete it and plan it again in a prompt.",
			"A failed Container doesn't heal by itself.", c.Since})
	}
	slices.SortStableFunc(a.NeedsYou, func(x, y NeedsYou) int { return x.Since.Compare(y.Since) })
	a.Waiting = len(a.NeedsYou)
	a.NeedsYou = a.NeedsYou[:min(len(a.NeedsYou), MaxNeedsYou)]
	return a
}

// add files an item healing or under Needs you, whichever it is.
func (a *Attention) add(h *Healing, entry *NeedsYou) {
	switch {
	case h != nil:
		a.Healing = append(a.Healing, *h)
	case entry != nil:
		a.NeedsYou = append(a.NeedsYou, *entry)
	}
}

// askEntry is the Needs you entry of an issue answered ask.
func askEntry(name string, issue FailedIssue, t TendedIssue) NeedsYou {
	entry := NeedsYou{Kind: AttentionIssue, Name: name, Since: t.At,
		Question: fmt.Sprintf("Issue %s %q: %s", name, oneParagraph(issue.Title), sentence(t.Why, "it needs you"))}
	if r := t.Recommend; r != nil {
		entry.Recommend = RecommendText(*r)
		entry.Why = oneParagraph(r.Why)
	} else {
		entry.Recommend = "Say in a prompt whether to try it again, close it or wait."
		entry.Why = oneParagraph(issue.Reason)
	}
	return entry
}

// RecommendText says a recommendation as an action.
func RecommendText(r Recommendation) string {
	switch r.Answer {
	case AnswerRetry:
		if strings.TrimSpace(r.Clarify) != "" {
			return "Try it again, with a clarification."
		}
		return "Try it again."
	case AnswerClose:
		return "Close it."
	case AnswerWait:
		return "Wait until something changed."
	}
	return "Say in a prompt what to do with it."
}

// WaitLapsed reports whether a wait answered at t.At lapsed by now: something
// changed since, or WaitLapse passed.
func WaitLapsed(t TendedIssue, changed, now time.Time) bool {
	return changed.After(t.At) || !now.Before(t.At.Add(WaitLapse))
}

// taskAttention classifies a Task by its row of the heal-window table.
func taskAttention(t WatchedTask, now time.Time) (*Healing, *NeedsYou) {
	window := func(reason string, acts time.Time, question, recommend, why string) (*Healing, *NeedsYou) {
		if acts.IsZero() || now.Before(acts) {
			return &Healing{AttentionTask, t.Name, reason, acts}, nil
		}
		return nil, &NeedsYou{AttentionTask, t.Name, question, recommend, why, t.Since}
	}
	waited := formatHours(now.Sub(t.Since))
	switch {
	case !t.AgentBackAt.IsZero():
		return window("Its agent is back at "+t.AgentBackAt.UTC().Format(time.RFC3339)+".",
			t.AgentBackAt.Add(AgentBackGrace),
			fmt.Sprintf("Task `%s` has waited %s for its agent, past when it was due back.", t.Name, waited),
			"Check the Agent's credentials and its plan's usage limit.",
			sentence(t.Message, "its agent's service is unavailable"))
	case t.Stage == StageBlocked && t.Reason == reasonWaitingForStandardChange:
		// Generous, but not forever: a change nobody reviews is due after
		// the blocked stage's stuck window twice over.
		acts := t.Since.Add(2 * StuckAfter[StageBlocked])
		if !t.StandardChangeEnded.IsZero() {
			acts = minTime(acts, t.StandardChangeEnded.Add(StandardChangeGrace))
		}
		return window(fmt.Sprintf("Its Standard change #%d is in review.", t.StandardChange), acts,
			fmt.Sprintf("Task `%s` has waited %s for Standard change #%d.", t.Name, waited, t.StandardChange),
			"Review the change, or close it.", "Blocked until the Standard change is merged or closed.")
	case t.Reason == ReasonBaseRed:
		// No window: only a person mends main. The item goes when the Task starts.
		return nil, &NeedsYou{AttentionTask, t.Name,
			fmt.Sprintf("Task `%s` has waited %s for main to turn green.", t.Name, waited),
			"Fix main's checks, or revert the change that broke them.",
			sentence(t.Message, "round 1 starts only on a green base"), t.Since}
	case t.Stage == StageChecks:
		return &Healing{AttentionTask, t.Name, ChecksBound(t.Message),
			time.Time{}}, nil
	case t.Stage == StageSlot || t.Reason == reasonWaitingForSlot:
		return window("A slot frees, longest waiter first.", t.Since.Add(SlotWindow),
			fmt.Sprintf("Task `%s` has waited %s for an agent slot.", t.Name, waited),
			"Give the factory more agent slots, or stop a Crew so fewer Tasks wait.",
			"Every slot was taken for longer than a slot is.")
	}
	stuck, ok := StuckAfter[t.Stage]
	if !ok {
		stuck = StuckAfter[StageQueued]
	}
	return window("It is "+t.Stage+".", t.Since.Add(2*stuck),
		fmt.Sprintf("Task `%s` has been %s for %s.", t.Name, t.Stage, waited),
		"Look at its agent, or delete the Task so the Crew starts it again.",
		sentence(t.Message, "it stayed in one stage twice as long as it should"))
}

// crewAttention classifies a Crew that is not Ready: a missing object doesn't
// heal; anything else may, inside CrewWindow.
func crewAttention(c WatchedCrew, now time.Time) (*Healing, *NeedsYou) {
	if strings.HasSuffix(c.Reason, "NotFound") {
		return nil, &NeedsYou{AttentionCrew, c.Name,
			fmt.Sprintf("Crew `%s` can't start: %s", c.Name, sentence(c.Message, c.Reason)),
			"Create it, or name another in a prompt.", "A missing object doesn't heal by itself.", c.Since}
	}
	acts := c.Since.Add(CrewWindow)
	if now.Before(acts) {
		return &Healing{AttentionCrew, c.Name, "Its Git host or Container may come back.", acts}, nil
	}
	recommend := "Check the GitProvider, its Secret and the Git host."
	if c.Reason == "ContainerNotReady" {
		recommend = "See its Container."
	}
	return nil, &NeedsYou{AttentionCrew, c.Name,
		fmt.Sprintf("Crew `%s` has not been ready for %s: %s", c.Name, formatHours(now.Sub(c.Since)),
			sentence(c.Message, c.Reason)),
		recommend, "It didn't come back inside an hour.", c.Since}
}

// TendFacts is what a tending round's start depends on (decisions 5 and 7).
type TendFacts struct {
	Mode string
	// Round is the last round; 0 before the first, which plans the idea.
	Round int
	// RoundOver says the last round is in the history.
	RoundOver bool
	// PromptWaiting says a prompt waits for a round; it goes first.
	PromptWaiting bool
	// OperatorNeedsInput says the operator waits for the person to make
	// something, which stops every round.
	OperatorNeedsInput bool
	// LastTend is when the last tending round started; zero if none did.
	LastTend time.Time
	// Due is how many failed issues are due and unanswered (Attention.Due).
	Due int
	// Paused says the Brief's spec.paused is set: it tends nothing.
	Paused bool
}

// StartsTend reports whether the Brief starts a tending round at now: its
// autopilot is Ask or On, it isn't paused, the last round is over, no prompt waits, at least
// one failed issue is due and unanswered, and the last tending round started
// at least TendInterval ago.
func StartsTend(f TendFacts, now time.Time) bool {
	mode := AutopilotMode(f.Mode)
	switch {
	case mode != AutopilotAsk && mode != AutopilotOn:
		return false
	case f.Paused || f.Round == 0 || !f.RoundOver || f.PromptWaiting || f.OperatorNeedsInput || f.Due == 0:
		return false
	}
	return f.LastTend.IsZero() || !now.Before(f.LastTend.Add(TendInterval))
}

// TendAnswers are the answers a round leaves for the Brief to apply. A prompt
// round's are the person's, applied as they are. A tending round's, in Ask,
// keep each retry and close as an ask recommending it, and every due issue it
// didn't answer counts as an ask recommending nothing, so it isn't raised
// again on its own (decisions 6 and 7). Check the planner's answers with
// CheckFailedAnswers before this: the asks it makes need no recommend.
func TendAnswers(mode string, answers []FailedAnswer, due []FailedIssue, tending bool) []FailedAnswer {
	out := make([]FailedAnswer, 0, len(answers))
	for _, a := range answers {
		if tending && AutopilotMode(mode) == AutopilotAsk && a.Applies() {
			action := "close it"
			if a.Answer == AnswerRetry {
				action = "try it again"
			}
			a = FailedAnswer{Container: a.Container, Issue: a.Issue, Answer: AnswerAsk,
				Why:       "Should the Brief " + action + "? Autopilot is Ask, so it waits for your answer.",
				Recommend: &Recommendation{Answer: a.Answer, Why: a.Why, Clarify: a.Clarify}}
		}
		out = append(out, a)
	}
	if !tending {
		return out
	}
	for _, f := range due {
		if !slices.ContainsFunc(answers, func(a FailedAnswer) bool { return a.Container == f.Container && a.Issue == f.Number }) {
			out = append(out, FailedAnswer{Container: f.Container, Issue: f.Number, Answer: AnswerAsk,
				Why: "The tending round didn't decide it."})
		}
	}
	return out
}

// Tended is the Brief's memory of answered issues after a round answered
// answers at at: each answer replaces the issue's earlier one, with the tries
// the issue has now; an issue no longer failed is forgotten.
func Tended(earlier []TendedIssue, answers []FailedAnswer, failed []FailedIssue, round int, at time.Time) []TendedIssue {
	var out []TendedIssue
	for _, t := range earlier {
		if slices.ContainsFunc(failed, func(f FailedIssue) bool { return f.Container == t.Container && f.Number == t.Issue }) &&
			!slices.ContainsFunc(answers, func(a FailedAnswer) bool { return a.Container == t.Container && a.Issue == t.Issue }) {
			out = append(out, t)
		}
	}
	for _, a := range answers {
		i := slices.IndexFunc(failed, func(f FailedIssue) bool { return f.Container == a.Container && f.Number == a.Issue })
		if i < 0 {
			continue
		}
		out = append(out, TendedIssue{Container: a.Container, Issue: a.Issue, Answer: a.Answer, Why: a.Why,
			Recommend: a.Recommend, Tries: failed[i].Tries, At: at, Round: round})
	}
	slices.SortFunc(out, func(a, b TendedIssue) int {
		if c := strings.Compare(a.Container, b.Container); c != 0 {
			return c
		}
		return a.Issue - b.Issue
	})
	return out[:min(len(out), MaxFailedIssues)]
}

// TendingHeading heads the section of a tending round's brief.md.
const TendingHeading = "## Tending round"

// IsTendingBrief reports whether brief.md is a tending round's.
func IsTendingBrief(brief string) bool {
	return slices.Contains(strings.Split(brief, "\n"), TendingHeading)
}

// ReasonTended is the outcome of a tending round that applied.
const ReasonTended = "Tended"

// ParseTendPlan reads a tending round's plan (decision 5): plan/failed.yaml,
// plan/summary.md and new issues in plan/issues/, nothing else. A tending
// round has no say on the story, so any other file refuses it, a
// plan/needs-input.md too: it asks the person with an ask answer instead.
func ParseTendPlan(files map[string]string) (Plan, error) {
	var other []string
	for p := range files {
		switch base := path.Base(p); {
		case !strings.HasPrefix(p, PlanDir+"/"):
		case p == PlanFailedFile, p == PlanSummaryFile, strings.HasPrefix(base, "."):
		case strings.HasPrefix(p, planIssuesDir) && !strings.Contains(strings.TrimPrefix(p, planIssuesDir), "/"):
		default:
			other = append(other, p)
		}
	}
	if len(other) > 0 {
		slices.Sort(other)
		return Plan{}, fmt.Errorf("a tending round writes only %s, %s and new issues in %s; it has no say on the "+
			"story, so leave out %s; to ask the person, answer an issue %s", PlanFailedFile, PlanSummaryFile,
			planIssuesDir, strings.Join(other, ", "), AnswerAsk)
	}
	return parsePlan(files, false)
}

// CheckTendPlan checks a tending round's plan against the namespace: the
// story stays as it is, so only its issues are checked, as CheckPlan checks a
// plan's.
func CheckTendPlan(plan Plan, current CurrentModel) []Refusal {
	plan.Objects = make([]PlanObject, 0, len(current.Objects))
	for _, o := range current.Objects {
		o.APIVersion = PlanAPIVersion
		plan.Objects = append(plan.Objects, o)
	}
	plan.Choices = []string{"Tended the failed issues"}
	return CheckPlan(plan, current)
}

// TendSummary is what a Brief's history says a tending round did, given
// the answers TendAnswers left: the planner's summary.md, or else one
// sentence per answer. In Ask, a round that proposes a retry or a close says
// it in sentences of its own, since the planner's summary.md reads as if the
// Brief acted while the proposal waits for the person (#872).
func TendSummary(mode, summary string, answers []FailedAnswer) string {
	proposes := AutopilotMode(mode) == AutopilotAsk && slices.ContainsFunc(answers, proposal)
	if s := strings.Join(strings.Fields(summary), " "); s != "" && !proposes {
		return ClipSentences(s, maxRoundSummary)
	}
	if len(answers) == 0 {
		return "Decided nothing."
	}
	lines := make([]string, 0, len(answers)+1)
	if proposes {
		// First, so clipping a long summary keeps it.
		lines = append(lines, "Autopilot is Ask, so each proposal waits for your answer.")
	}
	for _, a := range answers {
		name := fmt.Sprintf("%s#%d", a.Container, a.Issue)
		why := a.Why
		var what string
		switch {
		case a.Answer == AnswerRetry:
			what = "Tried " + name + " again"
		case a.Answer == AnswerClose:
			what = "Closed " + name
		case a.Answer == AnswerWait:
			what = "Waits on " + name
		case proposes && proposal(a) && a.Recommend.Answer == AnswerRetry:
			what, why = "Proposes trying "+name+" again", a.Recommend.Why
		case proposes && proposal(a):
			what, why = "Proposes closing "+name, a.Recommend.Why
		default:
			what = "Asks you about " + name
		}
		lines = append(lines, what+": "+sentence(why, "no reason given"))
	}
	return ClipSentences(strings.Join(lines, " "), maxRoundSummary)
}

// proposal reports whether a is an ask recommending a retry or a close.
func proposal(a FailedAnswer) bool {
	return a.Answer == AnswerAsk && a.Recommend != nil &&
		(a.Recommend.Answer == AnswerRetry || a.Recommend.Answer == AnswerClose)
}

// TendCounts counts a tending round's answers: retried, closed, waited on and
// asked about.
func TendCounts(answers []FailedAnswer) (retried, closed, waited, asked int) {
	for _, a := range answers {
		switch a.Answer {
		case AnswerRetry:
			retried++
		case AnswerClose:
			closed++
		case AnswerWait:
			waited++
		default:
			asked++
		}
	}
	return retried, closed, waited, asked
}

// NeedsYouMessage is the NeedsYou condition's message: the first entry's
// question, and how many more wait.
func NeedsYouMessage(listed []NeedsYou, waiting int) string {
	if len(listed) == 0 {
		return "Nothing needs you"
	}
	msg := listed[0].Question
	if listed[0].Recommend != "" {
		msg += " " + listed[0].Recommend
	}
	if more := waiting - 1; more > 0 {
		msg += fmt.Sprintf(" (%d more)", more)
	}
	return msg
}

// sentence is s on one line ending in a period, or fallback's when s is empty.
func sentence(s, fallback string) string {
	s = strings.TrimRight(oneParagraph(s), ".")
	if s == "" {
		s = fallback
	}
	return s + "."
}

// formatHours says d in whole hours, or minutes under an hour.
func formatHours(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
