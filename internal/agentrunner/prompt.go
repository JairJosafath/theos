package agentrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Review verdicts.
const (
	VerdictApprove = "approve"
	VerdictChanges = "changes"
)

// ImplementPrompt is what the implementing agent is asked to do.
func ImplementPrompt(job Job) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are an engineer working on %q, one application (a C4 Container) of project %q.\n",
		job.Container, job.Project)
	if job.Context != "" {
		fmt.Fprintf(&b, "\nWhat the application is for:\n%s\n", job.Context)
	}
	fmt.Fprintf(&b, "\nGoal of this task:\n%s\n", job.Goal)
	if job.Feedback != "" {
		fmt.Fprintf(&b, "\nThis is round %d. The previous round was not accepted. Address all of this feedback:\n%s\n",
			job.Round, job.Feedback)
	}
	fmt.Fprintf(&b, `
How to work:
- Read AGENTS.md first and follow it; the rules in docs/style/ apply.
- You are on branch %s, which starts from %s. Stay on it. Do not push, and do not
  change .devcontainer/, .c4factory.yaml or .gitea/.
- Deliver the goal completely: working code, tests for it, and docs where users
  or developers need them. No placeholders, no TODOs, no dead code.
- Run %q and fix every failure until it passes. Fix lint findings instead of
  silencing them.
- Keep the change focused on the goal.

When you are done, reply with a summary of what you changed, at most ten lines.
`, job.Branch, job.Base, strings.Join(job.CheckCommand, " "))
	return b.String()
}

// ReviewPrompt is what the reviewing agent is asked to do.
func ReviewPrompt(job Job) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a strict senior reviewer for %q, one application (a C4 Container) of project %q.\n",
		job.Container, job.Project)
	if job.Context != "" {
		fmt.Fprintf(&b, "\nWhat the application is for:\n%s\n", job.Context)
	}
	fmt.Fprintf(&b, "\nThe change under review should achieve this goal:\n%s\n", job.Goal)
	if job.Feedback != "" {
		fmt.Fprintf(&b, "\nThis is round %d. The previous round was sent back with this feedback; check each point was addressed:\n%s\n",
			job.Round, job.Feedback)
	}
	if job.MaxRounds > 0 && job.Round >= job.MaxRounds {
		b.WriteString("\nThis is the last round: asking for changes fails the task and nothing is merged.\n")
	}
	fmt.Fprintf(&b, `
The change is on the current branch, %[1]s. See it with: git diff origin/%[2]s...HEAD
Read AGENTS.md and docs/style/ for the rules the code must follow. You may read
and run anything in the repository, including %[3]q, but do not change files.

Approve only if you would merge the change as it is:
- it achieves the goal completely, with nothing stubbed or left out;
- it is correct, handles empty and bad input, and has tests for its behaviour;
- it follows AGENTS.md and the style digests;
- it fits what the application is for.

Report every required change now: each round is reviewed afresh, and a change
found late costs a whole round. Require changes only for what would ship a
defect: the goal not met, a bug or crash, lost data, a privacy or security gap,
behaviour without tests, or a rule broken in a way users or maintainers would
notice. Put smaller points after "Non-blocking:" in the summary and approve.

End your reply with one line of JSON and nothing after it:
{"verdict": "approve" or "changes", "summary": "<what is good, and every change required, concretely>"}
`, job.Branch, job.Base, strings.Join(job.CheckCommand, " "))
	return b.String()
}

// ParseVerdict finds the reviewer's JSON verdict: the last JSON object in the
// output that has a verdict.
func ParseVerdict(output string) (verdict, summary string, err error) {
	for end := strings.LastIndex(output, "}"); end >= 0; end = strings.LastIndex(output[:end], "}") {
		for start := strings.LastIndex(output[:end], "{"); start >= 0; start = strings.LastIndex(output[:start], "{") {
			var v struct {
				Verdict string `json:"verdict"`
				Summary string `json:"summary"`
			}
			if json.Unmarshal([]byte(output[start:end+1]), &v) != nil || v.Verdict == "" {
				continue
			}
			verdict = strings.ToLower(strings.TrimSpace(v.Verdict))
			if !slices.Contains([]string{VerdictApprove, VerdictChanges}, verdict) {
				return "", "", fmt.Errorf("unknown verdict %q", v.Verdict)
			}
			return verdict, strings.TrimSpace(v.Summary), nil
		}
	}
	return "", "", errors.New("the reviewer gave no JSON verdict")
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
