# Plan: agents never pass off made-up evidence

GitHub issue #467, reported by gray-lenovo on v0.5.0. Agent: otter-467.

## Goal

An implementer never ships a picture, recording or message presented as the
product's that the product didn't make. When an acceptance criterion needs what
an agent's sandbox lacks (a desktop editor, a browser to record, a phone, a
real account), the implementer leaves it open and says why; the reviewer
refuses a made-up picture and accepts an honestly open criterion of that kind;
and the open criterion goes to a person as its own issue, so the work merges
without a lie and nothing is quietly dropped.

Done when the four steps below are merged: the prompts say all this, the
reviewer is shown every image the change adds with its size, an approved
review's open criteria become a `kind/by-hand` issue for a person (also on a
Brief's card), and the planner stops writing criteria agents can't meet.

## What happened

In cel-lens's VS Code extension, an issue asked for "a short GIF or screenshot
list of errors, hover and completion" in the README. Round 1's implementer said
it could not run VS Code. The reviewer counted the criterion as unmet, and an
unmet criterion is a required change (`ReviewPrompt`). With no way to meet it
and no way to leave it open, round 3 drew three PNGs in a pixel font
(936×189, about 3 KB each), with an error message the product never gives
(`no such key: replica (cel-lens)` where the engine says `undefined field
'replica'`) and a hover on the wrong word, and the reviewer approved.

So the factory pushed the agent to fake it: the only ways out of an
impossible criterion were failing the Task or making something up. Nothing told
either agent that made-up evidence is worse than an unmet criterion, and the
reviewer was never pointed at the pictures.

## Decisions, and why

1. **The rule lives in the platform's prompts, not the Standards' guides.**
   `ImplementPrompt` and `ReviewPrompt` (`internal/agentrunner/prompt.go`) are
   the operator's and hold for every Standard; `docs/agents/*.md` are each
   Standard's own and would need every template bumped. The implementer is
   told: never make up evidence, no drawn or generated image, recording or
   sample output presented as the product's, and no message, log or output
   the code doesn't give; a screenshot or recording must come from running the
   product in the sandbox. A criterion that needs what the sandbox can't do is
   left open, never faked: say so under a line `Open: <criterion>: <why>`.
2. **The reviewer checks evidence, and knows an honest gap from a defect.**
   Made-up evidence is a required change, whatever round it is: a lie in the
   README is a defect users notice. The reviewer opens every image the change
   adds (the agents' harnesses read images) and checks it shows what running
   the product shows; every quoted message, output or log is checked against
   the code. A criterion the implementer left open is not a required change
   only when it truly needs what no agent here has (a person's desktop or
   device, an account, a purchase); one an agent could meet, or meet in part
   (a text sample instead of a screenshot when the issue allows it), still is.
3. **The verdict carries the open criteria.** The JSON verdict gains `"open":
   [{"criterion": "...", "why": "..."}]`, at most `MaxFollowUpCriteria` items,
   each trimmed like the follow-ups' criteria, kept only on `approve` (on
   `changes` the summary says it all). It is separate from `followUps`: a
   follow-up is optional later work that waits in Later as `status/proposed`;
   an open criterion is work the issue asked for that only a person can do.
4. **An open criterion becomes a person's issue.** On merge, when the review
   had open criteria, the operator files one issue in the Container's
   repository, `By hand: <issue title>`, labelled `kind/by-hand` and
   `status/needs-human`, in the Later milestone so it never holds a release.
   Its body names the merged pull request and issue and lists each criterion
   with its why as an "Acceptance criteria" checklist. The merged issue still
   closes (its pull request did the agent's part), and the operator's closing
   comment links the new issue. `status/needs-human` because no Crew may pick
   it: it is not for agents. The Task records the issue number
   (`status.byHand`) so a rerun of the merge step files it once, like a
   follow-up, and a marker in the body finds it again.
5. **A Brief shows it, and can't retry it.** A Brief's app's open
   `kind/by-hand` issue is listed in `status.failed` like any handed-back
   issue, with kind `byHand` and the reason "needs a person: <why>". The
   planner may only `close` it (the person did it, or dropped it); a `retry`
   is refused, as for an issue whose tries are used, because agents still
   can't do it.
6. **The reviewer is shown the pictures.** The runner lists the images the
   change adds or changes (`git diff --name-only --diff-filter=AM` on
   `.png .jpg .jpeg .gif .webp .svg`), with each one's pixel size (Go's
   `image.DecodeConfig`; SVG by its size only) and bytes, in the review
   prompt, telling the reviewer to open each. A 936×189 picture of 2.6 KB
   claiming to be an editor window stands out once someone looks; nothing
   decides automatically, since a small honest diagram is fine.
7. **The planner doesn't ask agents for a person's work.** `planner.md` says
   a criterion must be one an agent can meet and a reviewer can check in the
   repository: a screenshot of a desktop app, a recording, or a run on a
   device goes in a `kind/by-hand` issue for the person, filed with the plan,
   not in an agent's criteria. Where a text sample (a command's real output in
   a code block) serves, the planner asks for that.

## Steps

Each step is its own issue and pull request, and leaves main working.

1. **Prompts: no made-up evidence, open criteria in the verdict** (#472, decisions
   1-3). Tier 1: `ImplementPrompt` and `ReviewPrompt` tests check the rules
   are there; `ParseVerdict` table tests keep `open` on approve, drop it on
   changes, trim and cap it, and `run.go` carries it in the result.
2. **The reviewer is shown the pictures** (#473, decision 6). Tier 1: a test repo
   with an added PNG, a changed JPEG and an unchanged one; the prompt lists
   the first two with sizes, and nothing when no image changed.
3. **Open criteria become a `kind/by-hand` issue on merge** (#474, decisions 4-5).
   envtest: a Task whose approved review has open criteria files the issue
   once, labelled and in Later, the closing comment links it, a rerun files no
   second; a Brief lists it in `status.failed` as `byHand`, and a plan's
   `retry` of it is refused while `close` closes it. Depends on step 1.
4. **Planner guide: criteria agents can meet** (#475, decision 7). Tier 1: the
   guide test finds the rule; the plan check accepts a plan issue labelled
   `kind/by-hand` and files it `status/needs-human`. Depends on step 3.

## Not done here

The cel-lens README itself: that repository is the reporter's, and the
reporter fixes it or files it there. A Kind run of an extension's Task, which
only a human runs, would show a real reviewer refusing such pictures.
