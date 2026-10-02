package model

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Issue labels the factory reads and writes. They share the exclusive scope
// "status/", so an issue is in one state at a time.
const (
	LabelProposed   = "status/proposed"
	LabelReady      = "status/ready"
	LabelInProgress = "status/in-progress"
	LabelNeedsHuman = "status/needs-human"
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
	}
}

// MaxIssueAttempts is how many Tasks a Crew starts for one issue before it asks a
// person: an issue that failed three times needs splitting or clarifying.
const MaxIssueAttempts = 3

// Issue is an open issue labelled ready, as a Crew sees it.
type Issue struct {
	Number int
	Title  string
	Body   string
	// OpenBlockers are the open issues this one depends on, as "#3" or "owner/repo#3".
	OpenBlockers []string
}

// CrewTask is a Task on the Crew's Container.
type CrewTask struct {
	Name string
	// Issue is the Task's issue; 0 for a Task with a written goal.
	Issue int
	// Final means Merged or Failed; Failed tells which.
	Final  bool
	Failed bool
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
// closed, one Task per issue.
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
			if !t.Failed {
				merged = t.Name
			}
			tried = append(tried, t.Name)
		}
		switch {
		case merged != "":
			step.NotReady = append(step.NotReady, IssueRejection{Issue: issue.Number, Reason: fmt.Sprintf(
				"Task %s already merged this issue; close it, or file the rest as a new issue", merged)})
		case len(tried) >= MaxIssueAttempts:
			step.NotReady = append(step.NotReady, IssueRejection{Issue: issue.Number, Reason: fmt.Sprintf(
				"Agents tried this issue %d times (Tasks %s) and failed each time; it needs a person: split it, "+
					"clarify it, or do it by hand", len(tried), strings.Join(tried, ", "))})
		case len(issue.OpenBlockers) > 0:
			step.Waiting = append(step.Waiting, issue.Number)
		default:
			candidates = append(candidates, issue.Number)
		}
	}

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

	free := max(f.Parallel, 1) - len(step.Active)
	var started []string
	for _, issue := range candidates {
		if free <= 0 {
			step.Ready = append(step.Ready, issue)
			continue
		}
		attempt := 1
		for _, t := range f.Tasks {
			if t.Issue == issue {
				attempt++
			}
		}
		name := IssueTaskName(container, issue, attempt)
		step.Start = append(step.Start, IssueStart{Issue: issue, Task: name, Attempt: attempt})
		started = append(started, fmt.Sprintf("Task %s for issue #%d", name, issue))
		free--
	}

	switch {
	case len(started) > 0:
		step.Phase, step.Message = CrewWorking, "Starting "+strings.Join(started, " and ")
	case len(step.Active) > 0:
		step.Phase, step.Message = CrewWorking, workingMessage(step.Active, activeIssues)
		if n := len(step.Ready); n > 0 {
			step.Message += fmt.Sprintf("; %d ready %s %s for a free place", n, plural(n, "issue", "issues"),
				plural(n, "waits", "wait"))
		}
	default:
		step.Phase, step.Message = CrewIdle, "No ready issues"
		if n := len(step.Waiting); n > 0 {
			step.Message += fmt.Sprintf("; %d %s on open dependencies", n, plural(n, "waits", "wait"))
		}
	}
	return step
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
	const fix = " Add one, then label it " + LabelReady + " again."
	if strings.TrimSpace(body) == "" {
		return "This issue has no description, so an agent would have to guess what to build." + fix
	}
	loc := criteriaHeading.FindStringIndex(body)
	if loc == nil {
		return `This issue has no "Acceptance criteria" section with a checklist ("- [ ] ..."), so a reviewer ` +
			"could not tell when it is done." + fix
	}
	section := body[loc[1]:]
	if next := anyHeading.FindStringIndex(section); next != nil {
		section = section[:next[0]]
	}
	if !checklistItem.MatchString(section) {
		return `The "Acceptance criteria" section has no checklist items ("- [ ] ..."), so a reviewer could ` +
			"not tell when it is done." + fix
	}
	return ""
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
