---
name: handoff
description: Pick up, claim, hand off or finish a GitHub issue on JairJosafath/c4factory with hack/lib/github.sh - pick and take, write the handoff comment, close or escalate to the maintainer (needs-maintainer), file a follow-up. Use when taking or continuing a factory issue, when the context grows past about 100k tokens between tasks, when work stops mid-way, or when the user asks to hand off, pause, wrap up or continue later.
---

# Handoff

The procedure for working a factory issue. AGENTS.md ("How to Work", "Working
in Sessions") has the standing rules; they are not repeated here. Every GitHub
call goes through `hack/lib/github.sh` (run it alone for its commands; it never
prints the token). Standing facts go in AGENTS.md, `docs/` or memory, never in
a handoff.

## Pick one up

Issues are the relay's: pick one up only in a relay session, or when the user
said to work it in this session; otherwise leave it to the relay.

1. `hack/lib/github.sh flush` posts any handoff kept in `.relay/` while GitHub
   was away.
2. No issue named? `hack/lib/github.sh pick`: the oldest in-progress issue
   nobody holds, else the oldest ready + urgent one, else the oldest ready one.
3. `hack/lib/github.sh take N` claims it, sets `status/in-progress` and prints
   your name (`otter-23`) and branch. Exit 4: another agent holds it, pick
   again. Exit 1 "this checkout holds #M": work from your own worktree. The
   relay has already done this for its sessions.
4. `git fetch origin`; `git switch BRANCH` if `origin/BRANCH` exists, else
   `git switch -c BRANCH origin/main`.
5. `hack/lib/github.sh show N` prints the issue, its newest handoff and the
   comments after it. Check "Where it stands" against `git log`, then do the
   first next step.


## Labels

One `status/*` at a time, swapped with `hack/lib/github.sh status N <status>`.
AGENTS.md ("Working in Sessions") says what each status means. `waiting` is a
plan's parent waiting on its steps, not on an agent. `waiting`,
`needs-maintainer` and `needs-human` release your claim.

## No cluster

An agent never leases, creates or deploys to a Kind cluster, and never runs
`make e2e`, `make deploy`, `make up`/`down` or factory-ops against one.
`make check` (Go tests, `make validate-yaml`, envtest) is the gate, with
`make test-gitea` and `make seed-local` where Go alone can't see the
behaviour ([docs/dev/testenv.md](../../../docs/dev/testenv.md#test-tiers)).
When only a real node would show something, land the code with the tests the
lower tiers can give and say in the pull request what a human run of Kind
would check. Kind is a tier only a human runs: `cluster acquire` refuses
without `C4_HUMAN_CLUSTER=1`, which no agent sets. A human who needs a Kind
cluster follows
[docs/dev/testenv.md](../../../docs/dev/testenv.md#sharing-the-kind-clusters-between-checkouts)
("Sharing the Kind clusters between checkouts").

## Sign your work

`Agent: otter-23` goes on the line above `Co-Authored-By` in every commit
(same trailer block, no blank line), and as a line in the pull request body
and the handoff. No `@`: GitHub would read it as a mention.

Lost claim (the heartbeat hook says so):
`git push origin HEAD:refs/heads/<branch>-otter-23`, then
`hack/lib/github.sh comment N -` naming that branch, and stop.

## Write a handoff

1. Commit and push first, so the SHAs you name exist on the remote.
2. Write it to a scratch file, first line `<!-- relay-handoff -->`, under 150
   lines, secrets redacted. Short sections:
   - **Agent:** your name.
   - **Goal:** the outcome in two sentences, and who asked for it.
   - **Where it stands:** done (commit SHAs), in progress, not started; the
     branch and whether it is pushed.
   - **Live thread:** what you were in the middle of; what you learned that is
     written nowhere else; dead ends and why.
   - **Decisions:** made, and open ones with your recommendation.
   - **Next steps:** numbered and concrete; the first doable in minutes.
   - **State outside git:** clusters, running things, ports; where secrets
     live, never their values.
   - **References:** spec, plan, results, issues, pull requests by path or
     URL, never copied; and the skills and docs the next session needs.
3. `hack/lib/github.sh handoff N FILE` posts it and prints the URL. Exit 3
   (GitHub away) keeps it in `.relay/pending-handoff-N.md`; say so in your
   last message.
4. Give the user the URL and the line to start the next session with:
   `Continue GitHub issue #N.`

## Review before landing

A pull request that changes more than 400 lines in hand-written files gets a
review pass before it lands; `land` refuses it until it has one, and
`hack/lib/github.sh review-check PR` tells you whether it does. Generated files
(`zz_generated.*`, `config/crd/bases/`, `config/rbac/role.yaml`), lockfiles
and `docs/screenshots/` don't count. Smaller pull requests land as before.

1. Run `/code-review` in a subagent (the Agent tool, model named: sonnet or
   opus), not in your own context: the author reads past its own mistakes.
   Give it the branch against `origin/main` and ask for its findings back.
2. Post them on the pull request as one comment (`gh pr comment PR
   --body-file FILE`) that starts with `Review:` and has an `Agent: <name>`
   line, each finding one top-level list item:
   ```
   Review: /code-review (opus subagent) of <head SHA>
   Agent: otter-23

   - <finding, file:line>
   ```
   No findings: `Review: no findings` and the `Agent:` line.
3. Fix what it found, commit and push; then edit the comment from a file
   (`gh api --method PATCH repos/OWNER/REPO/issues/comments/ID -F body=@FILE`)
   so every finding starts with its verdict: `- Fixed in <SHA>: <finding>` or
   `- Dismissed (<reason>): <finding>`. Only top-level list items outside a
   code fence are findings; a sub-item may explain.
4. `land`. It takes the newest `Review:` comment posted through the owner's
   account with an `Agent:` line, and refuses while any finding in it starts
   with neither Fixed nor Dismissed, quoting the first.

## Finish

- **Landed:** the pull request says `Closes #N` and is merged as AGENTS.md
  ("How to Work") says, with `hack/lib/github.sh land PR` (a merge commit),
  run from the branch's checkout with everything committed and pushed, and
  over 400 changed lines, reviewed first ("Review before landing"). Two
  pull requests that each passed `make check` alone can still break main
  together, so `merge` refuses a branch behind main, and `land` takes the
  merge turn: it waits until the pull requests queued before it have landed
  or failed, then merges `origin/main` in, reruns `make check`, pushes and
  merges, and gives the turn up. The wait and the check can take an hour:
  run it in the background and follow its output (it says its place in the
  queue every few minutes; `make check`'s output goes to
  `.relay/land-PR.log`). A conflict or a red check ends the turn with exit 1
  and says so: fix it on the branch, push and `land` again. Don't merge main
  in and `merge` by hand: `merge` refuses while another pull request holds the
  turn. If N is still open: `hack/lib/github.sh close N FILE`,
  FILE naming the pull request, SHAs and evidence. An issue with no plan is
  finished by its own pull request: its body says `Closes #N`, and neither the
  body nor a commit message says `Refs #N` (that is only a plan's), or `merge`
  refuses it, or merges it leaving N open. When a pushed commit already says
  `Refs #N`, don't force-push: reword the history onto a new branch
  `<branch>-2` (`git switch -c <branch>-2 origin/main`, cherry-pick the
  commits and `git commit --amend` or `git rebase` to reword the message),
  push it, open a fresh pull request saying `Closes #N`, and close the old one
  with a comment naming the new one (`gh pr close OLD --comment ...`).
- **Plan only:** when N is split into a plan and step issues, the plan's
  pull request says `Refs #N`, never `Closes #N`: nothing N asked for has
  shipped. Nor may a close, fix or resolve word (closes, fixed, resolves...)
  stand next to `#N` anywhere in its body or commit messages, even in prose:
  GitHub reads "#S (its PR closes #N)" as closing N. Write "#S finishes #N"
  instead. `hack/lib/github.sh merge PR` refuses such a pull request and names
  the phrase to reword. Once it merges, add a `Depends on #S` line per step to N's body
  (`gh issue edit N --body-file FILE`, the rest kept), post a handoff naming
  the plan and the steps, then `hack/lib/github.sh status N waiting`: N stays
  open, waiting on its steps rather than on an agent, and `pick` skips it.
  When its last step closes, N closes itself with a comment naming the steps
  (`close`, the *Issue closed* workflow and the relay watch run
  `hack/lib/github.sh parents --apply`; the watch also moves a parent you left
  in progress to waiting). Each step's body names N. Steps never say
  `Depends on #N` (that is a cycle). Never close the parent of a plan by
  hand, even when "its work was the design": it closes with its last step.
  `hack/close-guard.sh` (a hook) denies a `gh pr create`, `edit`, `merge` or
  `git commit` that would close it while a step is open, naming the line, and
  `hack/lib/github.sh close N` refuses it, naming the open step.
- **Plan finished:** when the issue you close finishes a plan's last step,
  move the plan to `docs/plans/done/` in the same pull request (`git mv`),
  fix the relative links inside it, and update every link to it
  (`grep -rn <plan>.md` finds none left at the old path). The evidence
  AGENTS.md asks for ("Show, don't claim") goes in `docs/screenshots/` in
  the same pull request.
- **Stuck on a decision:** escalate to the maintainer,
  `hack/lib/github.sh status N needs-maintainer`, plus a comment saying what
  you need: something only the owner can give (access, credentials, money;
  a tag is the maintainer's to cut), or a direction you're unsure of, with
  the choice you'd make and why.
  The relay's watch starts the maintainer within a minute; it decides, or
  sends the issue on to the owner (`status/needs-human`, which only it sets).
  How to build it is ours to decide (AGENTS.md, "Designing is our job"),
  never a question for the user.
- **Follow-up found:** `hack/lib/github.sh create TITLE FILE status/proposed
  kind/<kind>` (a body line `Depends on #M` if it waits on another issue). Don't start it in this
  session. Write it so the relay's triage can ready it: one piece of work, a
  checkable "Done when", and what it waits on.

## Refused

Auto mode may refuse a command (`land`, `release`, editing an issue you didn't
file). That is not a failure of the command: say which command was refused,
once, in the pull request body and the handoff, and carry on. When it blocks
landing, `hack/lib/github.sh status N needs-maintainer` with a comment naming
the command. The allow rules in `.claude/settings.json` are the owner's: never
edit them, and never do the refused thing through another command.
