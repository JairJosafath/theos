---
name: handoff
description: Hand off the in-flight state of work on the factory as a comment on its GitHub issue (JairJosafath/c4factory), so a fresh session can pick it up; also how to pick an issue up, the rules while working one (agent name, branch, claims, one agent per checkout), and how to finish it or mark it needs-human. Use when taking or continuing a factory issue, when the session's context is getting large (around 140k tokens), when work stops mid-way, or when the user asks to hand off, pause, wrap up or continue later.
---

# Handoff

Work on the factory itself is tracked as GitHub issues on `JairJosafath/c4factory`,
one issue per piece of work. A handoff is a comment on that issue; the next
session reads the newest one and continues. Product work (the apps the factory
builds) stays in Gitea, where the Crews work it; this skill is not for that.

Standing facts (true next month) go in `AGENTS.md`, `docs/` or memory, never in
a handoff.

Every GitHub call goes through `hack/lib/github.sh` (run it alone for its
commands). It reads the token from the git credential helper and never prints it.

## Labels

- `status/proposed`: waits for the user to ready it. `status/ready`: a session
  may take it. `status/in-progress`: a session works on it.
  `status/needs-human`: blocked on the user; the newest comment says why.
  One status at a time: `hack/lib/github.sh status N <status>` swaps it.
- `priority/urgent` goes before other ready issues. `kind/bug|feature|chore|docs|improvement`
  says what it is. `source/evaluator` marks issues filed by `hack/evaluate.sh`.
- `needs/cluster`: the work or its "Done when" needs the Kind cluster
  (deploys, factory-ops, Crews, e2e). Set it when you file such an issue, or
  `hack/lib/github.sh label N needs/cluster` when you find out mid-way. On a
  device without a cluster (`C4_CLUSTER=none`) `pick` skips these and `take`
  refuses them: land what you can there (code, unit and envtest tests), label
  the issue and hand off with what is left. On a device with one, one checkout
  at a time works a `needs/cluster` issue; `take` and `label` exit 4 while
  another holds it.

## Pick one up

1. `hack/lib/github.sh flush` posts any handoff kept in `.relay/` while GitHub
   was away.
2. No issue named? `hack/lib/github.sh pick`: the oldest in-progress issue
   nobody holds, else the oldest ready + urgent one, else the oldest ready one.
   It skips issues another agent claims and issues whose `Depends on #M` is open.
3. `hack/lib/github.sh take N` claims it for this checkout (a comment naming
   you, e.g. `otter-23`, your host, branch and session) and swaps it to
   in-progress; it prints your name and branch. Exit 4: another agent holds it,
   so pick again. Exit 1 with "this checkout holds #M": another agent works in
   this checkout, so work from your own worktree (below); release it only if
   it is a leftover of your own session.
   `hack/relay.sh` has already done this for its sessions.
   `handoff`, `close` and `status N needs-human` release the claim;
   `hack/lib/github.sh claims` shows who holds what.
4. Switch to the branch `take` printed: `git fetch origin`, then
   `git switch BRANCH` if `origin/BRANCH` exists (an earlier agent pushed it;
   continue there), else `git switch -c BRANCH origin/main`.
5. `hack/lib/github.sh show N` prints the issue, its newest handoff comment and
   the comments after it (the user may have steered there). Check "Where it
   stands" against `git log` and the cluster, then do the first next step.

## While you work

- **Your name** is the one `take` printed (`jq -r .name .relay/claim` reads it
  again). Sign your traces with it: every commit message ends with an
  `Agent: otter-23` trailer, in the trailer block with `Co-Authored-By`
  (`Agent: otter-23` on the line above it, no blank line between), the pull
  request body has an `Agent: otter-23` line, and so does your handoff (its
  **Agent:** section). No `@` in front: GitHub would read it as a mention.
- **One agent per checkout.** The claim lives in this checkout's `.relay/claim`.
  A second agent on the same device works in its own git worktree
  (`claude --worktree`, or `git worktree add ../c4factory-NAME origin/main`
  and start there), never in a checkout another agent uses.
- **One branch per issue**, the one in the claim; never commit on main. Push
  after every commit (`git push -u origin HEAD`), so a dead session loses at
  most one commit and the next agent finds its branch.
- **Lost claim.** A hook beats your claim's heartbeat after tool calls. If it
  says you no longer hold the issue (your session slept past the 2-hour lease
  and another agent took over), stop working on it: push what you have to
  the issue's branch with your name appended, e.g.
  `git push origin HEAD:refs/heads/feat/23-claims-otter-23`, comment that
  branch on the issue (`hack/lib/github.sh comment N -`) so the new owner can
  take what it needs, and stop. Don't push to the issue's branch any more, and
  don't take the issue back.

## Write a handoff

1. Commit and push your work first, so the SHAs you name exist on the remote.
2. Write the comment to a scratch file. Its first line is `<!-- relay-handoff -->`
   (the helper adds it if missing). Sections, short:
   - **Agent:** your name, e.g. `otter-23`.
   - **Goal:** the outcome, in two sentences, and who asked for it.
   - **Where it stands:** done (with commit SHAs), in progress, not started; the
     branch and whether it is pushed.
   - **Live thread:** what you were in the middle of, what you learned that is
     written nowhere else, dead ends and why.
   - **Decisions:** made, and still open with your recommendation.
   - **Next steps:** numbered and concrete; the first doable in minutes.
   - **State outside git:** clusters, paused or running things, background
     processes, ports; where secrets live, never their values.
   - **References:** spec, plan, results, issues, pull requests, by path or URL.
     Point to them; never copy them.
   - **For the next session:** the skills and docs to reach for.
3. Keep it under about 150 lines. Redact secrets.
4. `hack/lib/github.sh handoff N FILE` posts it and prints the comment's URL.
   If GitHub can't be reached it exits 3 and keeps the text in
   `.relay/pending-handoff-N.md`; the next session or relay posts it first. Say
   so in your last message.
5. Give the user the comment's URL and the line to start the next session with:
   `Continue GitHub issue #N.`

## Finish

- **Landed:** the work's pull request to main says `Closes #N` and is merged
  (merge commit) once its checks are green; GitHub then closes N. If N is still
  open, `hack/lib/github.sh close N FILE`, FILE saying what landed: pull
  request, commit SHAs, evidence.
- **Can't go on without the user:** `hack/lib/github.sh status N needs-human`,
  and a comment (or a handoff) saying why and what you need.
- **Follow-up found on the way:** `hack/lib/github.sh create TITLE FILE status/proposed kind/<kind>`.
  Don't start it in this session. If it can only start once another issue has
  landed (a plan's step 3 after step 2), its body says `Depends on #M`;
  `pick` skips it while #M is open.
