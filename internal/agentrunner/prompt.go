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
- You are on branch %s, which starts from %s. Stay on it. Do not push, and do not
  change %s.
`, job.Branch, job.Base, strings.Join(protectedList(job), ", "))
	if job.Issue > 0 {
		b.WriteString("- Meet every acceptance criterion of the issue, each with a test that proves it.\n")
	}
	fmt.Fprintf(&b, `- Deliver the goal completely: working code, tests for it, and docs where users
  or developers need them. No placeholders, no TODOs, no dead code.
- Run %q and fix every failure until it passes. Fix lint findings instead of
  silencing them.
- Keep the change focused on the goal.
`, strings.Join(job.CheckCommand, " "))
	if job.Issue > 0 {
		b.WriteString("\nWhen you are done, reply with a summary of what you changed and, for each acceptance\n" +
			"criterion, where it is met and tested; say what you could not verify. At most fifteen lines.\n")
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
	fmt.Fprintf(&b, `
The change is on the current branch, %[1]s. See it with: git diff origin/%[2]s...HEAD
Read AGENTS.md and docs/style/ for the rules the code must follow. You may read
and run anything in the repository, including %[3]q, but do not change files.

Approve only if you would merge the change as it is:
- it achieves the goal completely, with nothing stubbed or left out;
- it is correct, handles empty and bad input, and has tests for its behaviour;
- it follows AGENTS.md and the style digests;
- it fits what the application is for.
`, job.Branch, job.Base, strings.Join(job.CheckCommand, " "))
	if job.Issue > 0 {
		b.WriteString(`
Check each acceptance criterion of the issue, one by one, against the code and its
tests. In the summary, list every criterion as met or not met, with where it is
tested. An unmet criterion is a required change.
`)
	}
	b.WriteString(`
Report every required change now: each round is reviewed afresh, and a change
found late costs a whole round. Require changes only for what would ship a
defect: the goal not met, a bug or crash, lost data, a privacy or security gap,
behaviour without tests, or a rule broken in a way users or maintainers would
notice. Put smaller points after "Non-blocking:" in the summary and approve.

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
