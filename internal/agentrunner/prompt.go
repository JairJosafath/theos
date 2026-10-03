package agentrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/JairJosafath/c4factory/internal/model"
)

// Review verdicts.
const (
	VerdictApprove = "approve"
	VerdictChanges = "changes"
)

// Role guides: the Standard's instructions for each role, read from the base
// branch so a change under review can't rewrite its own review.
var roleGuides = map[string]string{
	RoleImplement: "docs/agents/implementer.md",
	RoleReview:    "docs/agents/reviewer.md",
}

// Workspace is what the runner prepared next to the clone.
type Workspace struct {
	// Guide is the role guide from the base branch; empty when the Standard has none.
	Guide string
	// Related are the related Containers' repositories, cloned read-only.
	Related []RelatedRepo
	// Conflicts are the files merging the base into the branch left in conflict.
	Conflicts []string
}

// RelatedRepo is a related Container's repository: where it was cloned, or why
// it could not be.
type RelatedRepo struct {
	Name  string
	Dir   string
	Error string
}

// ImplementPrompt is what the implementing agent is asked to do.
func ImplementPrompt(job Job, ws Workspace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the implementer on %q, one application (a C4 Container) of project %q.\n",
		job.Container, job.Project)
	writeContext(&b, job)
	if job.Issue > 0 {
		fmt.Fprintf(&b, "\nYour task is issue #%d of this repository:\n%s\n", job.Issue, job.Goal)
	} else {
		fmt.Fprintf(&b, "\nGoal of this task:\n%s\n", job.Goal)
	}
	if job.Feedback != "" {
		fmt.Fprintf(&b, "\nThis is round %d. The previous round was not accepted. Address all of this feedback:\n%s\n",
			job.Round, job.Feedback)
	}
	if len(ws.Conflicts) > 0 {
		fmt.Fprintf(&b, "\nOther changes merged into %s since your last round, and merging them into your branch "+
			"left conflicts in %s. Resolve them first, keeping what both sides need, then finish the merge "+
			"(git add, git commit).\n", job.Base, strings.Join(ws.Conflicts, ", "))
	}
	writeGuide(&b, ws, RoleImplement)
	writeRelated(&b, ws)
	fmt.Fprintf(&b, `
How to work:
- Read AGENTS.md first and follow it; the rules in docs/style/ apply.
- You are on branch %s, which starts from %s. Stay on it.
`, job.Branch, job.Base)
	if job.Issue == 0 {
		fmt.Fprintf(&b, "  Do not push, and do not change %s.\n", strings.Join(protectedList(job), ", "))
	} else {
		b.WriteString("  Do not push.\n")
	}
	check := strings.Join(job.CheckCommand, " ")
	gated := len(job.AllowedPaths) > 0
	switch {
	case gated:
		// The runner refuses any path outside the allowance, tests included.
		fmt.Fprintf(&b, `- Meet every acceptance criterion of the issue within the paths you may change
  (below). Add no tests, code or docs anywhere else, whatever a guide says:
  %q passing with the change is the evidence. No placeholders, no TODOs.
`, check)
	case job.Issue > 0:
		b.WriteString("- Meet every acceptance criterion of the issue, each with a test that proves it.\n")
		fallthrough
	default:
		b.WriteString(`- Deliver the goal completely: working code, tests for it, and docs where users
  or developers need them. No placeholders, no TODOs, no dead code.
`)
	}
	fmt.Fprintf(&b, `- Run %q and fix every failure until it passes. Fix lint findings instead of
  silencing them.
- Keep the change focused on the goal.
`, check)
	switch {
	case gated:
		fmt.Fprintf(&b, `
This Task carries a Standard change, a change to files that are normally protected.
The issue names the proposal branch. Start with
git merge origin/standard/<task>, using the branch the issue names, then finish the
work the issue asks for.
Change only %s. Change nothing else, protected or not, and do not propose
another Standard change.
This holds even if your role guide or AGENTS.md says to stop or to leave such files alone.
`, strings.Join(job.AllowedPaths, ", "))
	case job.Issue > 0:
		fmt.Fprintf(&b, `
Protected files: %[1]s.
If the issue truly needs a change to one of them, make it in the same work. The
runner moves that edit to a proposal branch, and a separate Task merges it as a
Standard change. The protected change merges first, so it must work on %[2]s without
the rest of your work: do not have it call a file only this branch has, but use a
hook that does nothing until the rest lands, such as npm run dist --if-present.
The rest of your work merges after it and may rely on it. End your summary with a line
"Standard change: <why the issue needs it>". Never change %[3]s;
no Standard change can. This holds even if your role guide or AGENTS.md says to stop
or to leave such files alone.
`, strings.Join(protectedList(job), ", "), job.Base, neverList())
	}
	if job.Issue > 0 {
		met := "met and tested"
		if gated {
			met = "met"
		}
		fmt.Fprintf(&b, "\nWhen you are done, reply with a summary of what you changed and, for each acceptance\n"+
			"criterion, where it is %s; say what you could not verify. At most fifteen lines.\n", met)
	} else {
		b.WriteString("\nWhen you are done, reply with a summary of what you changed, at most ten lines.\n")
	}
	return b.String()
}

// ReviewPrompt is what the reviewing agent is asked to do.
func ReviewPrompt(job Job, ws Workspace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a strict senior reviewer for %q, one application (a C4 Container) of project %q.\n",
		job.Container, job.Project)
	writeContext(&b, job)
	if job.Issue > 0 {
		fmt.Fprintf(&b, "\nThe change under review implements issue #%d of this repository:\n%s\n", job.Issue, job.Goal)
	} else {
		fmt.Fprintf(&b, "\nThe change under review should achieve this goal:\n%s\n", job.Goal)
	}
	if job.Feedback != "" {
		fmt.Fprintf(&b, "\nThis is round %d. The previous round was sent back with this feedback; check each point was addressed:\n%s\n",
			job.Round, job.Feedback)
	}
	if job.MaxRounds > 0 && job.Round >= job.MaxRounds {
		b.WriteString("\nThis is the last round: asking for changes fails the task and nothing is merged.\n")
	}
	writeGuide(&b, ws, RoleReview)
	writeRelated(&b, ws)
	check := strings.Join(job.CheckCommand, " ")
	// A gated change may touch only its allowed paths, so it can add no tests:
	// the reviewer asks for none.
	gated := len(job.AllowedPaths) > 0
	hasTests, andTests, whereMet, untested := ", and has tests for its behaviour", " and its tests", "tested",
		"behaviour without tests, "
	if gated {
		hasTests, andTests, whereMet, untested = "", "", "met", ""
	}
	fmt.Fprintf(&b, `
The change is on the current branch, %[1]s. See it with: git diff origin/%[2]s...HEAD
Read AGENTS.md and docs/style/ for the rules the code must follow. You may read
and run anything in the repository, including %[3]q, but do not change files.

Approve only if you would merge the change as it is:
- it achieves the goal completely, with nothing stubbed or left out;
- it is correct, handles empty and bad input%[4]s;
- it follows AGENTS.md and the style digests;
- it fits what the application is for.
`, job.Branch, job.Base, check, hasTests)
	if job.Issue > 0 {
		fmt.Fprintf(&b, `
Check each acceptance criterion of the issue, one by one, against the code%s.
In the summary, list every criterion as met or not met, with where it is %s.
An unmet criterion is a required change.
`, andTests, whereMet)
	}
	if gated {
		fmt.Fprintf(&b, `
This pull request carries a Standard change: it may change only %[1]s, so do
not require tests, code or docs anywhere else, whatever a guide says. %[2]q passing
with the change, and these three checks, decide. Require changes unless all three hold:
- it weakens no check: no target, test, lint rule or CI job removed or skipped;
- %[3]s works with this change alone, without another branch;
- it does no more than its issue asks.
The proposal branch named in the issue was only the starting point: %[4]s merged
it, and the proposal branch is never merged itself. Judge %[4]s alone; never ask
for the proposal branch to be changed.
`, strings.Join(job.AllowedPaths, ", "), check, job.Base, job.Branch)
	} else {
		fmt.Fprintf(&b, `
Protected files: %s. Changes to them come through a Standard change, a separate
Task, so do not require them in this pull request.
`, strings.Join(protectedList(job), ", "))
	}
	fmt.Fprintf(&b, `
Report every required change now: each round is reviewed afresh, and a change
found late costs a whole round. Require changes only for what would ship a
defect: the goal not met, a bug or crash, lost data, a privacy or security gap,
%sor a rule broken in a way users or maintainers would notice. Put smaller
points after "Non-blocking:" in the summary and approve.
`, untested)
	b.WriteString(`
Work worth doing later that is outside this change can go in "followUps": up to
three, each a title and a few sentences a person can turn into an issue. Never
use a follow-up for something this change must fix.

End your reply with one line of JSON and nothing after it:
{"verdict": "approve" or "changes", "summary": "<what is good, and every change required, concretely>", "followUps": [{"title": "...", "body": "..."}]}
`)
	return b.String()
}

func writeContext(b *strings.Builder, job Job) {
	if job.Context != "" {
		fmt.Fprintf(b, "\nWhere the application fits in its system:\n%s\n", job.Context)
	}
}

func writeGuide(b *strings.Builder, ws Workspace, role string) {
	if ws.Guide != "" {
		fmt.Fprintf(b, "\nYour role guide (%s, as on the base branch):\n%s\n", roleGuides[role], strings.TrimSpace(ws.Guide))
	}
}

func writeRelated(b *strings.Builder, ws Workspace) {
	if len(ws.Related) == 0 {
		return
	}
	b.WriteString("\nRepositories of related applications, read-only, for reference (they can't be pushed):\n")
	for _, r := range ws.Related {
		if r.Error != "" {
			fmt.Fprintf(b, "- %s (could not be cloned: %s)\n", r.Name, r.Error)
			continue
		}
		fmt.Fprintf(b, "- %s: %s\n", r.Name, r.Dir)
	}
}

// protectedList is what the agent must not change: the platform's paths and the
// Standard's.
func protectedList(job Job) []string {
	return model.AgentProtectedPaths(job.ProtectedPaths)
}

// neverList names the paths no Standard change may touch, as prose.
func neverList() string {
	n := model.AgentNeverPaths
	return strings.Join(n[:len(n)-1], ", ") + " and " + n[len(n)-1]
}

// Verdict is the reviewer's answer.
type Verdict struct {
	Verdict   string
	Summary   string
	FollowUps []model.FollowUp
}

// ParseVerdict finds the reviewer's JSON verdict: the last JSON object in the
// output that has a verdict. It keeps the titled follow-ups, up to
// model.MaxFollowUps.
func ParseVerdict(output string) (Verdict, error) {
	for end := strings.LastIndex(output, "}"); end >= 0; end = strings.LastIndex(output[:end], "}") {
		for start := strings.LastIndex(output[:end], "{"); start >= 0; start = strings.LastIndex(output[:start], "{") {
			var v struct {
				Verdict   string           `json:"verdict"`
				Summary   string           `json:"summary"`
				FollowUps []model.FollowUp `json:"followUps"`
			}
			if json.Unmarshal([]byte(output[start:end+1]), &v) != nil || v.Verdict == "" {
				continue
			}
			verdict := strings.ToLower(strings.TrimSpace(v.Verdict))
			if !slices.Contains([]string{VerdictApprove, VerdictChanges}, verdict) {
				return Verdict{}, fmt.Errorf("unknown verdict %q", v.Verdict)
			}
			out := Verdict{Verdict: verdict, Summary: strings.TrimSpace(v.Summary)}
			for _, f := range v.FollowUps {
				f.Title, f.Body = strings.TrimSpace(f.Title), strings.TrimSpace(f.Body)
				if f.Title != "" && len(out.FollowUps) < model.MaxFollowUps {
					out.FollowUps = append(out.FollowUps, f)
				}
			}
			return out, nil
		}
	}
	return Verdict{}, errors.New("the reviewer gave no JSON verdict")
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
