package model

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Issue labels the factory reads and writes. They share the exclusive scope
// "status/", so an issue is in one state at a time.
const (
	LabelProposed   = "status/proposed"
	LabelReady      = "status/ready"
	LabelInProgress = "status/in-progress"
	LabelNeedsHuman = "status/needs-human"
	// LabelNeedsSpec is an issue a triager found too big or unclear to build
	// as it stands: a spec comes first. LabelSpecReview is one whose spec
	// waits for its review (docs/plans/sdlc-loop.md).
	LabelNeedsSpec  = "status/needs-spec"
	LabelSpecReview = "status/spec-review"
)

// IssueLabel is a label the factory keeps in a repository.
type IssueLabel struct {
	Name        string
	Color       string
	Description string
	// Exclusive makes the label exclude the others of its scope.
	Exclusive bool
}

// StatusLabels are the labels a Crew keeps in its Container's repository.
func StatusLabels() []IssueLabel {
	return []IssueLabel{
		{LabelProposed, "#c5def5", "Filed, not yet triaged; agents don't pick it up", true},
		{LabelReady, "#0e8a16", "Clear and ready: the Crew picks it up when its dependencies are closed", true},
		{LabelInProgress, "#fbca04", "A Task is working on it", true},
		{LabelNeedsHuman, "#d93f0b", "Agents can't go on; a comment says why", true},
		{LabelNeedsSpec, "#5319e7", "Triaged: needs a spec before it is built", true},
		{LabelSpecReview, "#d4c5f9", "Its spec waits for review", true},
	}
}

// LabelUrgent asks a Crew to pick an issue up at once: it starts before every other
// ready issue, whatever its release, but never past the Crew's parallel count.
const LabelUrgent = "priority/urgent"

// UrgentLabel is the label a person adds to have an issue picked up at once.
func UrgentLabel() IssueLabel {
	return IssueLabel{LabelUrgent, "#b60205",
		"Pick up at once: goes next, whatever its release, within the Crew's parallel count", false}
}

// FollowUpMilestone is the milestone the factory files reviewers' follow-ups in,
// so they never look like release work. It is created with the description when
// a repository doesn't have it yet.
const (
	FollowUpMilestone            = "Later"
	FollowUpMilestoneDescription = "Not part of any release: reviewers' follow-ups and ideas. A person moves an issue " +
		"into a release milestone, with acceptance criteria, before marking it ready."
)

// MaxIssueAttempts is how many Tasks a Crew starts for one issue before it asks a
// person: an issue that failed three times needs splitting or clarifying. A
// Task that failed because CI never reported (NoCI) costs no try.
const MaxIssueAttempts = 3

// Issue is an open issue labelled ready, as a Crew sees it.
type Issue struct {
	Number int
	Title  string
	Body   string
	// OpenBlockers are the open issues this one depends on, as "#3" or "owner/repo#3".
	OpenBlockers []string
	// StandardChange is set for an issue labelled kind/standard-change. Only such an
	// issue may start with an allowance, and only once a Task names it.
	StandardChange bool
	// Release is the issue's milestone when it has a due date; nil otherwise.
	Release *Release
	// Urgent is set for an issue labelled priority/urgent.
	Urgent bool
	// Prepare is set for a release's prepare issue, labelled kind/release.
	Prepare bool
	// Labels are the issue's labels, which pick its Agents (Routes).
	Labels []string
}

// Release is a milestone with a due date. A Crew works releases one at a time,
// the one due first first; issues outside a release come after every release.
type Release struct {
	Title string
	Due   time.Time
}

// CrewTask is a Task on the Crew's Container.
type CrewTask struct {
	Name string
	// Issue is the Task's issue; 0 for a Task with a written goal.
	Issue int
	// Final means Merged or Failed; Failed tells which.
	Final  bool
	Failed bool
	// NoCI means it failed through its CI runner's fault (RunnerFault: CI
	// never reported, or the runner ran out of room), so it costs the issue no try.
	NoCI bool
	// Blocked means the Task waits on a Standard change; it keeps its issue but
	// takes no place from Parallel.
	Blocked bool
	// StandardChange is the Standard change issue the Task filed, from its status.
	StandardChange *IssueChange
	// Release is the release of the Task's issue; nil when it has none.
	Release *Release
	// Prepare means the Task's issue is a release's prepare issue.
	Prepare bool
}

// IssueChange is a Standard change issue a Task filed, and the protected paths it
// proposes to change.
type IssueChange struct {
	Issue int
	Paths []string
}

// CrewFacts is everything a Crew's next step depends on.
type CrewFacts struct {
	Paused         bool
	ContainerReady bool
	// Parallel is how many Tasks the Crew may run at once; less than 1 means 1.
	Parallel int
	// Tasks are all Tasks on the Container.
	Tasks []CrewTask
	// Issues are the open issues labelled ready.
	Issues []Issue
	// Preparing is the release whose prepare issue waits for the Tasks at work
	// to end before it is filed (ReleasePrepareWaits); empty when none.
	Preparing string
	// Routes send issues with a label to other Agents, in the Crew's order.
	Routes []AgentRoute
}

// AgentRoute sends the issues labelled Label to other Agents than the Crew's:
// Implementer and Reviewer name them, empty for a role the route leaves to
// the Crew's own Agent.
type AgentRoute struct {
	Label       string
	Implementer string
	Reviewer    string
}

// RouteAgents is who works an issue with labels: for each role, the Agent of
// the first route whose label the issue has and that names one for the role,
// else empty for the Crew's own. An issue labelled kind/docs and security can
// so get the docs route's implementer and the security route's reviewer.
func RouteAgents(routes []AgentRoute, labels []string) (implementer, reviewer string) {
	for _, r := range routes {
		if !slices.Contains(labels, r.Label) {
			continue
		}
		if implementer == "" {
			implementer = r.Implementer
		}
		if reviewer == "" {
			reviewer = r.Reviewer
		}
	}
	return implementer, reviewer
}

// Crew phases, mirroring the API.
const (
	CrewWaiting = "Waiting"
	CrewIdle    = "Idle"
	CrewWorking = "Working"
	CrewPaused  = "Paused"
)

// IssueStart is a Task to create for an issue.
type IssueStart struct {
	Issue   int
	Task    string
	Attempt int
	// AllowedPaths are the protected paths the Task may change; set only for a
	// Standard change issue a Task named.
	AllowedPaths []string
	// Implementer and Reviewer are the Agents a route gave the issue; empty
	// for the Crew's own.
	Implementer, Reviewer string
}

// IssueRejection is a ready issue that agents must not take, and why: it goes
// back to a person with status/needs-human.
type IssueRejection struct {
	Issue  int
	Reason string
}

// CrewStep is what a Crew does next, and what its status shows.
type CrewStep struct {
	Phase string
	// Active are the Tasks working on the Container, before this step's starts.
	Active []string
	// Ready are the ready issues still waiting for a free place, in pick-up order.
	Ready []int
	// Waiting are the ready issues blocked by open dependencies.
	Waiting []int
	// Start are the Tasks to create.
	Start []IssueStart
	// NotReady are issues to hand back to a person.
	NotReady []IssueRejection
	Message  string
}

// NextCrewStep decides what a Crew does: it keeps up to Parallel Tasks working,
// starting them for the lowest-numbered ready issues whose dependencies are
// closed, one Task per issue. Parallel is a hard cap. An urgent issue starts
// first, then a Standard change a Task waits on.
func NextCrewStep(container string, f CrewFacts) CrewStep {
	var step CrewStep
	working := map[int]bool{}
	var activeIssues []int
	for _, t := range f.Tasks {
		if !t.Final {
			step.Active = append(step.Active, t.Name)
			if t.Issue > 0 {
				working[t.Issue] = true
				activeIssues = append(activeIssues, t.Issue)
			}
		}
	}
	issues := slices.Clone(f.Issues)
	slices.SortFunc(issues, func(a, b Issue) int { return a.Number - b.Number })
	var candidates []int
	allowances := map[int][]string{}
	urgent := map[int]bool{}
	prepare := map[int]bool{}
	labels := map[int][]string{}
	for _, issue := range issues {
		if working[issue.Number] {
			// Taken a moment ago; its label changes when its Task starts.
			continue
		}
		if reason := MissingAcceptanceCriteria(issue.Body); reason != "" {
			step.NotReady = append(step.NotReady, IssueRejection{Issue: issue.Number, Reason: reason})
			continue
		}
		var tried []string
		merged := ""
		for _, t := range f.Tasks {
			if t.Issue != issue.Number || !t.Final {
				continue
			}
			switch {
			case !t.Failed:
				merged = t.Name
			case t.NoCI:
				continue
			}
			tried = append(tried, t.Name)
		}
		allowance := standardAllowance(f.Tasks, issue)
		switch {
		case merged != "":
			step.NotReady = append(step.NotReady, IssueRejection{Issue: issue.Number, Reason: fmt.Sprintf(
				"Task %s already merged this issue; close it, or file the rest as a new issue", merged)})
		case len(tried) >= MaxIssueAttempts:
			step.NotReady = append(step.NotReady, IssueRejection{Issue: issue.Number, Reason: fmt.Sprintf(
				"Agents tried this issue %d times (Tasks %s) and failed each time; it needs a person: split it, "+
					"clarify it, or do it by hand", len(tried), strings.Join(tried, ", "))})
		case len(issue.OpenBlockers) > 0, issue.StandardChange && len(allowance) == 0:
			// A Standard change waits until a Task's status names it, so it never
			// starts without its allowance.
			step.Waiting = append(step.Waiting, issue.Number)
		default:
			candidates = append(candidates, issue.Number)
			allowances[issue.Number] = allowance
			urgent[issue.Number] = issue.Urgent
			prepare[issue.Number] = issue.Prepare
			labels[issue.Number] = issue.Labels
		}
	}
	// An urgent issue goes first: a person asked for it at once. Then a Standard
	// change with its allowance: a Blocked Task waits on it, and that Task's
	// branch ages behind every older ready issue otherwise. Then the releases, due
	// first first: the release in progress has first claim on a free place, and
	// what it can't use goes to the next releases rather than sit empty. Tags
	// still go out in order (NextRelease).
	order := releaseOrder(f.Tasks, issues, candidates, step.Waiting)
	slices.SortStableFunc(candidates, func(a, b int) int {
		if c := startRank(urgent, allowances, a) - startRank(urgent, allowances, b); c != 0 {
			return c
		}
		return compareReleases(order[a], order[b])
	})
	onDependencies := len(step.Waiting)

	switch {
	case f.Paused:
		step.Phase, step.Ready, step.NotReady = CrewPaused, candidates, nil
		step.Message = "Paused; not picking up issues"
		if len(step.Active) > 0 {
			step.Message += "; " + workingMessage(step.Active, activeIssues) + " until done"
		}
		return step
	case !f.ContainerReady:
		step.Phase, step.Ready, step.NotReady = CrewWaiting, candidates, nil
		step.Message = fmt.Sprintf("Waiting for Container %s to be Ready", container)
		return step
	}

	free := max(f.Parallel, 1) - (len(step.Active) - blockedTasks(f.Tasks))
	// A release's prepare step is built on the main every other Task's work
	// merged into, and lists that work (#956): while a release is being
	// prepared, no other issue starts but an urgent one, within parallel, and its prepare issue
	// starts once the Tasks at work, Blocked ones aside, have ended.
	preparing, others := preparingRelease(f, candidates, prepare)
	held := 0
	var started []string
	for _, issue := range candidates {
		if heldForRelease(preparing, others, urgent[issue] || len(allowances[issue]) > 0, prepare[issue]) {
			step.Ready = append(step.Ready, issue)
			held++
			continue
		}
		if free <= 0 {
			step.Ready = append(step.Ready, issue)
			continue
		}
		others = true // started now, it merges before a prepare issue
		attempt := 1
		for _, t := range f.Tasks {
			if t.Issue == issue {
				attempt++
			}
		}
		name := IssueTaskName(container, issue, attempt)
		implementer, reviewer := RouteAgents(f.Routes, labels[issue])
		step.Start = append(step.Start, IssueStart{Issue: issue, Task: name, Attempt: attempt,
			AllowedPaths: allowances[issue], Implementer: implementer, Reviewer: reviewer})
		kind := "issue"
		if urgent[issue] {
			kind = "urgent issue"
		}
		started = append(started, fmt.Sprintf("Task %s for %s #%d", name, kind, issue))
		free--
	}

	step.Phase, step.Message = crewProgress(step, started, activeIssues, held, onDependencies)
	return step
}

// crewProgress is a Crew's phase and message after its step: what it starts,
// else what works, and what waits, held ready issues for a release to be
// prepared.
func crewProgress(step CrewStep, started []string, activeIssues []int, held, onDependencies int) (string, string) {
	switch {
	case len(started) > 0:
		return CrewWorking, "Starting " + strings.Join(started, " and ")
	case len(step.Active) > 0:
		msg := workingMessage(step.Active, activeIssues)
		if n := len(step.Ready) - held; n > 0 {
			msg += fmt.Sprintf("; %d ready %s %s for a free place", n, plural(n, "issue", "issues"),
				plural(n, "waits", "wait"))
		}
		return CrewWorking, msg + preparingMessage(held) + dependenciesMessage(onDependencies)
	default:
		return CrewIdle, "No ready issues" + preparingMessage(held) + dependenciesMessage(onDependencies)
	}
}

// preparingRelease reports whether a release is being prepared, its prepare
// issue waiting or at work, and whether a Task is at work, a Blocked one
// aside: a prepare issue starts only when none is.
func preparingRelease(f CrewFacts, candidates []int, prepare map[int]bool) (preparing, others bool) {
	preparing = f.Preparing != "" || slices.ContainsFunc(candidates, func(n int) bool { return prepare[n] })
	for _, t := range f.Tasks {
		if !t.Final {
			preparing = preparing || t.Prepare
			others = others || !t.Blocked
		}
	}
	return preparing, others
}

// heldForRelease reports whether a ready issue waits while a release is being
// prepared: an urgent one never does, nor a Standard change a Blocked Task
// (maybe the prepare issue's) waits on (exempt); the prepare issue only while
// another Task is at work.
func heldForRelease(preparing, others, exempt, prepare bool) bool {
	return preparing && !exempt && (!prepare || others)
}

// preparingMessage tells how many ready issues wait for a release to be
// prepared.
func preparingMessage(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("; %d ready %s %s for a release to be prepared", n, plural(n, "issue", "issues"),
		plural(n, "waits", "wait"))
}

// dependenciesMessage tells how many ready issues wait on open dependencies.
func dependenciesMessage(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("; %d %s on open dependencies", n, plural(n, "waits", "wait"))
}

// releaseOrder is the release each candidate starts in turn with: its own, or
// the current release's for an issue the current release waits on, so the
// release in progress isn't held up behind its own blockers.
func releaseOrder(tasks []CrewTask, issues []Issue, candidates, waiting []int) map[int]*Release {
	current := currentRelease(tasks, issues, candidates, waiting)
	needed := neededBy(issues, waiting, current)
	order := map[int]*Release{}
	for _, issue := range issues {
		order[issue.Number] = issue.Release
		if needed[issue.Number] && compareReleases(current, issue.Release) < 0 {
			order[issue.Number] = current
		}
	}
	return order
}

// neededBy is every issue of this repository that the current release's waiting
// issues wait on, directly or through other waiting issues.
func neededBy(issues []Issue, waiting []int, current *Release) map[int]bool {
	byNumber := map[int]Issue{}
	for _, issue := range issues {
		byNumber[issue.Number] = issue
	}
	var queue []int
	for _, n := range waiting {
		if compareReleases(byNumber[n].Release, current) == 0 {
			queue = append(queue, n)
		}
	}
	needed := map[int]bool{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, ref := range byNumber[n].OpenBlockers {
			blocker, err := strconv.Atoi(strings.TrimPrefix(ref, "#"))
			if !strings.HasPrefix(ref, "#") || err != nil || needed[blocker] {
				continue // another repository's issue, which this Crew doesn't start
			}
			needed[blocker] = true
			if slices.Contains(waiting, blocker) {
				queue = append(queue, blocker)
			}
		}
	}
	return needed
}

// currentRelease is the release a Crew works on: the first, in release order, that
// has a ready issue (startable or waiting on dependencies) or an active Task. It
// is nil when that is no release, or when nothing has work.
func currentRelease(tasks []CrewTask, issues []Issue, candidates, waiting []int) *Release {
	var current *Release
	found := false
	consider := func(r *Release) {
		if !found || compareReleases(r, current) < 0 {
			current, found = r, true
		}
	}
	for _, t := range tasks {
		if !t.Final {
			consider(t.Release)
		}
	}
	for _, issue := range issues {
		if slices.Contains(candidates, issue.Number) || slices.Contains(waiting, issue.Number) {
			consider(issue.Release)
		}
	}
	return current
}

// compareReleases orders releases by due date, then title; nil, outside any
// release, comes last.
func compareReleases(a, b *Release) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	if c := a.Due.Compare(b.Due); c != 0 {
		return c
	}
	return strings.Compare(a.Title, b.Title)
}

// startRank sorts the issues a Crew may start: urgent ones (0), then those with
// an allowance (1), then the rest (2).
func startRank(urgent map[int]bool, allowances map[int][]string, issue int) int {
	switch {
	case urgent[issue]:
		return 0
	case len(allowances[issue]) > 0:
		return 1
	}
	return 2
}

// blockedTasks counts the Tasks that wait on a Standard change; they take no place.
func blockedTasks(tasks []CrewTask) int {
	n := 0
	for _, t := range tasks {
		if t.Blocked && !t.Final {
			n++
		}
	}
	return n
}

// standardAllowance is the protected paths a Standard change issue may change:
// those the first Task (by name) that filed it proposed, minus the paths no agent
// may touch. A label alone grants nothing; nil when no Task names the issue.
// Two filers can't occur: a Task reuses only an issue carrying its own proposal
// branch's marker, and that branch is named after the Task, so the order by name
// only keeps the choice stable.
func standardAllowance(tasks []CrewTask, issue Issue) []string {
	if !issue.StandardChange {
		return nil
	}
	var filer *CrewTask
	for i, t := range tasks {
		if t.StandardChange != nil && t.StandardChange.Issue == issue.Number &&
			(filer == nil || t.Name < filer.Name) {
			filer = &tasks[i]
		}
	}
	if filer == nil {
		return nil
	}
	return EligibleAllowance(filer.StandardChange.Paths)
}

// workingMessage says which Tasks work, and on which issues.
func workingMessage(tasks []string, issues []int) string {
	msg := plural(len(tasks), "Task ", "Tasks ") + joinAnd(tasks) + plural(len(tasks), " is working", " are working")
	if len(issues) > 0 {
		refs := make([]string, 0, len(issues))
		for _, n := range issues {
			refs = append(refs, "#"+strconv.Itoa(n))
		}
		msg += " on " + plural(len(issues), "issue ", "issues ") + joinAnd(refs)
	}
	return msg
}

var (
	criteriaHeading = regexp.MustCompile(`(?im)^#{1,6}\s*acceptance criteria\s*$`)
	anyHeading      = regexp.MustCompile(`(?m)^#{1,6}\s`)
	checklistItem   = regexp.MustCompile(`(?m)^\s*[-*]\s+\[[ xX]\]\s+\S`)
)

// MissingAcceptanceCriteria says why an issue isn't clear enough for an agent,
// or returns "" when it is: its body needs an "Acceptance criteria" heading with
// at least one checklist item under it, so a reviewer can tell when it is done.
func MissingAcceptanceCriteria(body string) string {
	const fix = " Add one, then label it " + LabelReady + " again. For example:\n\n" +
		"```markdown\n## Acceptance criteria\n\n" +
		"- [ ] An empty order is rejected with a message naming the missing field\n" +
		"- [ ] A test covers the empty order\n```\n\n" +
		"Each item is one check a reviewer can verify in the pull request."
	if reason := missingCriteria(body); reason != "" {
		return reason + fix
	}
	return ""
}

// missingCriteria is MissingAcceptanceCriteria's reason without the fix.
func missingCriteria(body string) string {
	if strings.TrimSpace(body) == "" {
		return "This issue has no description, so an agent would have to guess what to build."
	}
	section, ok := criteriaSection(body)
	if !ok {
		return `This issue has no "Acceptance criteria" section with a checklist ("- [ ] ..."), so a reviewer ` +
			"could not tell when it is done."
	}
	if !checklistItem.MatchString(section) {
		return `The "Acceptance criteria" section has no checklist items ("- [ ] ..."), so a reviewer could ` +
			"not tell when it is done."
	}
	return ""
}

// criteriaSection is the text under the body's first "Acceptance criteria"
// heading, up to the next heading, and whether it has that heading.
func criteriaSection(body string) (string, bool) {
	loc := criteriaHeading.FindStringIndex(body)
	if loc == nil {
		return "", false
	}
	section := body[loc[1]:]
	if next := anyHeading.FindStringIndex(section); next != nil {
		section = section[:next[0]]
	}
	return section, true
}

// IssueTaskName names the Task for an issue: <container>-<issue>, with the
// attempt from the second one on.
func IssueTaskName(container string, issue, attempt int) string {
	name := container + "-" + strconv.Itoa(issue)
	if attempt > 1 {
		name += "-" + strconv.Itoa(attempt)
	}
	return name
}

// IssueGoal is the goal an agent gets from an issue.
func IssueGoal(number int, title, body string) string {
	return fmt.Sprintf("Issue #%d: %s\n\n%s", number, strings.TrimSpace(title), strings.TrimSpace(body))
}
