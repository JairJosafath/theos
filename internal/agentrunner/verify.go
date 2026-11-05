package agentrunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
)

// verify has the verifier build and start the change at the branch's head,
// the way the repository says, and check each acceptance criterion against
// what runs (docs/plans/sdlc-loop.md, "Verify"). It reports the verdict in
// the review's format, and commits and pushes nothing: the clone's push URL
// goes nowhere.
func verify(ctx context.Context, job Job, agent Agent, r *repo, ws Workspace, prog *runProgress,
	result Result) Result {
	head, err := r.head(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Commit = head
	ws.MakeVerify = hasVerifyTarget(r.dir)
	output, err := prog.session(ctx, agent, r.dir, VerifyPrompt(job, ws))
	if err != nil {
		return agentFailed(result, err)
	}
	verdict, err := parseVerdict(output, "verifier")
	if err != nil {
		result.Error = err.Error()
		return result
	}
	// A verifier suggests no follow-ups: that is the reviewer's part.
	result.Verdict, result.Summary, result.Open = verdict.Verdict, verdict.Summary, verdict.Open
	return result
}

// makefiles are the names GNU make reads, in its order.
var makefiles = []string{"GNUmakefile", "makefile", "Makefile"}

// hasVerifyTarget reports whether the Makefile in dir has a rule for
// model.VerifyTarget: a line, not in a recipe or a comment, naming it before
// its colon. A special target such as .PHONY only lists it, and an assignment
// such as "verify :=" is no rule.
func hasVerifyTarget(dir string) bool {
	for _, name := range makefiles {
		data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // the checkout's own Makefile
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if line == "" || line[0] == '\t' || line[0] == '#' {
				continue
			}
			names, rest, ok := strings.Cut(line, ":")
			assignment := strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ":=")
			if !ok || assignment || strings.ContainsAny(names, "=$") {
				continue
			}
			if slices.Contains(strings.Fields(names), model.VerifyTarget) {
				return true
			}
		}
		return false // make reads the first it finds
	}
	return false
}

// VerifyPrompt is what the verifier is asked to do: build and start the
// change the reviewer approved, the way the repository says, exercise each
// acceptance criterion against what runs, and report each with its evidence.
// It changes and pushes nothing.
func VerifyPrompt(job Job, ws Workspace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You verify changes to %q, one application (a C4 Container) of project %q: you build and "+
		"start a change the reviewer approved, and check what it does before it merges.\n", job.Container, job.Project)
	writeContext(&b, job)
	if job.Issue > 0 {
		fmt.Fprintf(&b, "\nThe change implements issue #%d of this repository. %s\n%s\n", job.Issue, issueIntro,
			issueBlock(job.Goal))
	} else {
		fmt.Fprintf(&b, "\nThe change should achieve this goal:\n%s\n", job.Goal)
	}
	if job.Feedback != "" {
		fmt.Fprintf(&b, "\nThis is round %d. The previous round was sent back with this feedback; check each point "+
			"was addressed in what runs:\n%s\n", job.Round, job.Feedback)
	}
	if job.MaxRounds > 0 && job.Round >= job.MaxRounds {
		b.WriteString("\nThis is the last round: asking for changes fails the task and nothing is merged.\n")
	}
	writeGuide(&b, ws, RoleVerify)
	writeRelated(&b, ws)
	start := fmt.Sprintf("Build and start it with `make %s`, which the repository's Makefile has for that.",
		model.VerifyTarget)
	if !ws.MakeVerify {
		start = fmt.Sprintf("The Makefile has no %q target: build and start it the way README.md says, or "+
			"the docs it points to.", model.VerifyTarget)
	}
	fmt.Fprintf(&b, `
The change is on the current branch, %[1]s; the reviewer read its code and
approved it. See it with: git diff origin/%[2]s...HEAD
Read AGENTS.md first. `+projectNotes+`
%[3]s
Then exercise each acceptance criterion of the issue, one by one, against what
runs: call its API, run its commands, read what it prints and logs, as someone
using it would. Reading the code was the reviewer's part; yours is what the
running change does. For each criterion, keep the command you ran and what it
printed.
Change, commit and push nothing: nothing in your workspace is pushed. Stop
what you started before you reply.

Approve only if every criterion is met by what runs, or open as below. In the
summary, list every criterion as met, not met or open, each with its
evidence: the command you ran and the lines it printed that show it.
- A criterion the running change doesn't meet is a required change: give the
  command and what it printed, so the implementer can see it fail too.
- A change that doesn't build or start the way the repository says is a
  required change, with the command and its output.
- A criterion no sandbox can meet stays open: it needs what no agent here has,
  such as a person's desktop or device, an account, a purchase or a service
  the sandbox can't reach. Never fake one: no output, log or picture you did
  not see is evidence. When you approve, list each in "open", with why it
  needs a person: at most `+strconv.Itoa(model.MaxFollowUpCriteria)+`.
A failure %[2]s has as well is not the change's: say so, and judge the
criteria the change is for.

End your reply with one line of JSON and nothing after it:
{"verdict": "approve" or "changes", "summary": "<each criterion: met, not met or open, with the command and what it printed>", "open": [{"criterion": "...", "why": "..."}]}
`, job.Branch, job.Base, start)
	return b.String()
}
