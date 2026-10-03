---
name: handoff
description: Hand off the in-flight state of work on the factory as a comment on its GitHub issue (JairJosafath/c4factory), so a fresh session can pick it up; also how to pick an issue up, finish it or mark it needs-human. Use when the session's context is getting large (around 140k tokens), when work stops mid-way, or when the user asks to hand off, pause, wrap up or continue later.
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

## Pick one up

1. `hack/lib/github.sh flush` posts any handoff kept in `.relay/` while GitHub
   was away.
2. No issue named? `hack/lib/github.sh pick`: the open in-progress issue, else
   the oldest ready + urgent one, else the oldest ready one.
3. `hack/lib/github.sh take N` swaps it to in-progress and comments that a
   session took it. `hack/relay.sh` has already done this for its sessions.
4. `hack/lib/github.sh show N` prints the issue, its newest handoff comment and
   the comments after it (the user may have steered there). Check "Where it
   stands" against `git log` and the cluster, then do the first next step.

## Write a handoff

1. Commit and push your work first, so the SHAs you name exist on the remote.
2. Write the comment to a scratch file. Its first line is `<!-- relay-handoff -->`
   (the helper adds it if missing). Sections, short:
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

- **Landed:** `hack/lib/github.sh close N FILE`, FILE saying what landed: commit
  SHAs, branch, pull request, evidence. Commits land on the integration branch,
  so GitHub's auto-close never fires; close it yourself.
- **Can't go on without the user:** `hack/lib/github.sh status N needs-human`,
  and a comment (or a handoff) saying why and what you need.
- **Follow-up found on the way:** `hack/lib/github.sh create TITLE FILE status/proposed kind/<kind>`.
  Don't start it in this session.
