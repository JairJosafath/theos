package agentrunner

import (
	"fmt"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
)

// guideSpec keys the spec writer's role guide in roleGuides: a Spec Task's
// implement run writes a spec, not code.
const guideSpec = "spec"

// maxSpecCriteriaAsked is how many acceptance criteria the spec writer is
// asked for at most.
const maxSpecCriteriaAsked = 10

// guideFor is the role guide a run reads: the spec writer's for a Spec Task's
// implement run, else its role's.
func guideFor(job Job) string {
	if job.SpecPath != "" && job.Role == RoleImplement {
		return guideSpec
	}
	return job.Role
}

// implementPrompt is what an implement run is asked to do: write the spec for
// a Spec Task, else implement the goal.
func implementPrompt(job Job, ws Workspace) string {
	if job.SpecPath != "" {
		return SpecPrompt(job, ws)
	}
	return ImplementPrompt(job, ws)
}

// reviewPrompt is what a review run is asked to do: review the spec for a
// Spec Task, else the change.
func reviewPrompt(job Job, ws Workspace) string {
	if job.SpecPath != "" {
		return SpecReviewPrompt(job, ws)
	}
	return ReviewPrompt(job, ws)
}

// specScope refuses a Spec Task's round that changed more than its spec
// file, or returns "".
func specScope(job Job, changed []string) string {
	if job.SpecPath == "" {
		return ""
	}
	if stray := model.SpecStray(job.SpecPath, changed); len(stray) > 0 {
		return model.SpecScopeRefusal(job.SpecPath, stray)
	}
	return ""
}

// SpecPrompt is what a Spec Task's implement run is asked to do: write the
// issue's spec in job.SpecPath, and change nothing else.
func SpecPrompt(job Job, ws Workspace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You write specs for %q, one application (a C4 Container) of project %q: the plan that "+
		"settles how an issue is built, before anyone builds it.\n", job.Container, job.Project)
	writeContext(&b, job)
	fmt.Fprintf(&b, "\nYour task is the spec of issue #%d of this repository. %s\n%s\n", job.Issue, issueIntro,
		issueBlock(job.Goal))
	if job.Feedback != "" {
		fmt.Fprintf(&b, "\nThis is round %d. The previous round's spec was not accepted. Address all of this "+
			"feedback:\n%s\n", job.Round, job.Feedback)
	}
	if job.PullRequest > 0 {
		fmt.Fprintf(&b, "\nYour branch already has pull request #%d, from an earlier round or try: build on the "+
			"spec it has.\n", job.PullRequest)
	}
	if len(ws.Conflicts) > 0 {
		fmt.Fprintf(&b, "\nMerging %s into your branch left conflicts in %s. Resolve them first, then finish the "+
			"merge (git add, git commit).\n", job.Base, strings.Join(ws.Conflicts, ", "))
	}
	writeGuide(&b, ws, guideSpec)
	writeRelated(&b, ws)
	fmt.Fprintf(&b, `
How to work:
- Read AGENTS.md, the code and the docs as much as you need to plan the issue.
- `+projectNotes+`
- You are on branch %[1]s, which starts from %[2]s. Stay on it. Do not push.
- Write the spec in %[3]s, and change no other file: no code, test or doc
  beside it. The runner refuses a round that changes anything else.
- The spec has three parts, each under its own Markdown heading:
  ## Problem: what is missing or wrong today, and for whom.
  ## Approach: how the change will do it: the parts of the code it touches,
  each choice the issue leaves open made, with why, and what it leaves out.
  ## Acceptance criteria: a checklist ("- [ ] ...") of one to %[4]d checks a
  reviewer can verify in the pull request that implements the spec, each one
  sentence of at most %[5]d characters. They become the issue's acceptance
  criteria, so each is a check of the finished work, not of the spec.
- Make every decision the issue leaves open yourself and say why: the spec is
  where the approach is settled, so nobody has to ask the issue's author.
- Run %[6]q and fix what your spec breaks, such as a Markdown check.

If the spec can't be written yet (it waits on other work, or on an answer only
a person can give), stop on purpose: change nothing, and end your summary with
a line %[7]q followed by what it waits on and why, in at most five sentences.

When you are done, reply with a summary of the approach the spec takes, at most
ten lines.
`, job.Branch, job.Base, job.SpecPath, maxSpecCriteriaAsked, maxCriterionChars,
		strings.Join(job.CheckCommand, " "), model.BlockedPrefix)
	return b.String()
}

// SpecReviewPrompt is what the reviewer of a Spec Task's round is asked to do:
// judge the spec in job.SpecPath as the plan the issue is built from.
func SpecReviewPrompt(job Job, ws Workspace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a strict senior reviewer for %q, one application (a C4 Container) of project %q. "+
		"You review a spec: the plan an issue will be built from.\n", job.Container, job.Project)
	writeContext(&b, job)
	fmt.Fprintf(&b, "\nThe spec under review is for issue #%d of this repository. %s\n%s\n", job.Issue, issueIntro,
		issueBlock(job.Goal))
	if job.Feedback != "" {
		fmt.Fprintf(&b, "\nThis is round %d. The previous round was sent back with this feedback; check each point "+
			"was addressed:\n%s\n", job.Round, job.Feedback)
	}
	if job.MaxRounds > 0 && job.Round >= job.MaxRounds {
		b.WriteString("\nThis is the last round: asking for changes fails the task and nothing is merged.\n")
	}
	writeGuide(&b, ws, RoleReview)
	writeRelated(&b, ws)
	fmt.Fprintf(&b, `
The spec is %[1]s on the current branch, %[2]s. See the change with:
git diff origin/%[3]s...HEAD
Read AGENTS.md and the code it plans to change. `+projectNotes+`
You may read and run anything in the repository, but do not change files.

Approve only if the issue could be built from the spec as it is:
- it changes %[1]s and nothing else;
- its Problem says what the issue needs, and its Approach settles how, fitting
  the code and AGENTS.md, with every choice the issue leaves open made;
- its "Acceptance criteria" checklist has one to %[4]d checks a reviewer can
  verify in the implementing pull request, which together mean the issue is done;
- it asks for nothing the application isn't for.
A person approves the spec after you, as its review point: say in the summary
what they should look at first.

Require changes only for what would build the wrong thing, leave the issue
half done, or break a rule; put smaller points after "Non-blocking:" in the
summary and approve.

End your reply with one line of JSON and nothing after it:
{"verdict": "approve" or "changes", "summary": "<what is good, and every change required, concretely>"}
`, job.SpecPath, job.Branch, job.Base, maxSpecCriteriaAsked)
	return b.String()
}
