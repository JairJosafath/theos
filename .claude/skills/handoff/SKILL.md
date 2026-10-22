---
name: handoff
description: Pick up, claim, hand off or finish a GitHub issue on JairJosafath/c4factory with hack/lib/github.sh - pick and take, write the handoff comment, close or escalate to the maintainer (needs-maintainer), file a follow-up. Use when taking or continuing a factory issue, when the context grows past about 80k tokens (mid-task too; never past 120k), when work stops mid-way, or when the user asks to hand off, pause, wrap up or continue later.
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
2. No issue named? `hack/lib/github.sh pick`: by `priority/N`, 1 first, no
   label counting as 5; within a number, the oldest in-progress issue nobody
   holds, else the oldest ready one.
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

## Scratch

Scratch goes under `$TMPDIR`: make it with `mktemp -d` (or `mktemp` for a
file), never a literal `/tmp/...` path, and remove it before you land.
`/tmp` is a small tmpfs every session on the device shares, and one
session's copies and caches filled it and failed every `make check` (#1269).
The relay points `$TMPDIR` at `~/.cache/c4factory/tmp/<worktree>`, outside any
git checkout (#1307), and removes it after the session.

## Write a handoff

Hand off when the context reaches about 80k tokens, mid-task if need be, and
never go on past 120k; the context guard hook (`hack/context-guard.sh`) says
when. Then end the session: the next one continues from the handoff.

1. Commit and push first, so the SHAs you name exist on the remote.
2. Write it to a scratch file (`mktemp`), first line `<!-- relay-handoff -->`,
   under 150 lines, secrets redacted. Short sections:
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

While yolo mode is on (`hack/lib/github.sh yolo status`), none of this
applies: `land` merges the pull request at once, with no `make check`, no
review pass, no merge turn and no bug-fix-test gate; only the closing-keyword
refusals stay (AGENTS.md, "Small pull requests to main"). Run the targeted
tests before you land anyway: nothing else runs before the merge, and the
Tests run on main is the first thing that sees your change.

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
  together, so `merge` refuses a branch behind main, and `land` merges
  `origin/main` in, runs `make check` and pushes, then queues for the merge
  turn: it waits until the pull requests queued before it have landed or
  failed, and when main is still what it checked, merges at once, holding
  the turn for seconds; when main moved, it merges it in and checks again
  under the turn. A docs-only pull request (`*.md`, `docs/`,
  `.claude/skills/`; under 200 lines) takes the simple path instead: no
  heavy-job slot, no merge turn, `make test-hack` only when a file of it is
  one `test-hack` reads. `land`'s check is the check: while you work, run the
  targeted tests (`make test-hack`, `go test ./internal/...`,
  `make ui-test`) and land; run `make check` by hand only when `land` failed
  and you need the whole output. The check takes 15 to 25 minutes, more
  when main moved: run it in the background and follow its output (it says
  its place in the queue every few minutes; `make check`'s output, the path
  taken and how long the turn was held go to `.relay/land-PR.log`). Never
  end your turn while `land` (or any background command you started) still
  runs: a headless session ends when its turn ends, and nothing wakes it for
  the result; follow it, or poll its log with a short command, until it ends
  (#1327). A conflict or a red check ends the turn with exit 1
  and says so: fix it on the branch, push and `land` again. Don't merge main
  in and `merge` by hand: `merge` refuses while another pull request holds the
  turn. If N is still open: `hack/lib/github.sh close N FILE`,
  FILE naming the pull request, SHAs and evidence. An issue with no plan is
  finished by its own pull request: its body says `Closes #N` and not
  `Refs #N` (that is only a plan's), or `merge` refuses it. A pull request's
  intent is its body: a commit message saying `Refs #N` closes nothing and
  `merge` lets it through, so leave pushed commits as they are; if the body
  says `Refs #N`, edit the body (`gh pr edit PR --body-file FILE`).
- **Plan only:** when N is split into a plan and step issues, the plan's
  pull request says `Refs #N`, never `Closes #N`: nothing N asked for has
  shipped. Nor may a close, fix or resolve word (closes, fixed, resolves...)
  stand next to `#N` anywhere in its body or commit messages, even in prose:
  GitHub reads "#S (its PR closes #N)" as closing N. Write "#S finishes #N"
  instead. `hack/lib/github.sh merge PR` refuses such a pull request and names
  the phrase to reword. When the phrase is in a pushed commit, don't
  force-push: reword the history onto a new branch `<branch>-2`
  (`git switch -c <branch>-2 origin/main`, cherry-pick the commits and
  `git commit --amend` or `git rebase` to reword the message), push it, open
  a fresh pull request saying `Refs #N`, and close the old one with a comment
  naming the new one (`gh pr close OLD --comment ...`). The steps of a plan
  for a decided issue (N ready or in progress) are decided design work
  (AGENTS.md, "Designing is our job"): file each `status/ready`, with
  `hack/lib/github.sh create TITLE FILE status/ready kind/<kind> source/dev`,
  so the relay picks them without a second triage pass. A step is filed
  `status/proposed` only when it needs a decision the plan did not make,
  and then its body says which; the relay's triage (the `triage` skill,
  "Ready it as design work") readies it or sends it on.
  Each step's body names N, has a checkable *Done when*, and has a
  `Depends on #S` line per earlier step it waits on, so they run in order.
  Steps never say `Depends on #N` (that is a cycle). Once the plan's pull
  request merges, add a `Depends on #S` line per step to N's body
  (`gh issue edit N --body-file FILE`, the rest kept), post a handoff naming
  the plan and the steps, then `hack/lib/github.sh status N waiting`: N stays
  open, waiting on its steps rather than on an agent, and `pick` skips it.
  When its last step closes, N closes itself with a comment naming the steps
  (`close`, the *Issue closed* workflow and the relay watch run
  `hack/lib/github.sh parents --apply`; the watch also moves a parent you left
  in progress to waiting). Never close the parent of a plan by hand, even
  when "its work was the design": it closes with its last step.
  `hack/close-guard.sh` (a hook) denies a `gh pr create`, `edit`, `merge` or
  `git commit` that would close it while a step is open, naming the line, and
  a `gh issue close`, a `gh api` call setting `state=closed` or a
  `hack/lib/github.sh duplicate` that would close it at once, naming the
  open step; `hack/lib/github.sh close N` and `duplicate N M` refuse it too.
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
- **Follow-up found:** work you find outside the issue and its plan (a
  plan's own steps are filed as *Plan only* says). One rule, three cases.
  Don't start a filed one in this session.
  - *Already there:* under about 30 changed lines, in a file your pull
    request already changes, inside the issue's scope: fix it in the same
    pull request and name it in the body under `Also fixes:`.
  - *Decided:* a `kind/bug` whose body names its failing test, or a
    `kind/chore` or `kind/docs` whose Done-when you can state:
    `hack/lib/github.sh create --next TITLE FILE kind/<bug|chore|docs>`. It is
    filed `status/ready` and, in a relay worker's session, reserved for your worktree:
    the relay starts your worker's next session on it (`.relay/next`, read
    before `pick`), and every other worker's `pick` skips it for
    `C4_LEASE_TTL` (2h), after which anyone picks it. `--next` refuses
    another kind, a status other than ready, or no kind.
  - *Needs a decision* (a design question, a feature, anything the reporter
    must elaborate): `hack/lib/github.sh create TITLE FILE status/proposed
    kind/<kind>`, written so the relay's triage can ready it: one piece of
    work, a checkable "Done when", and what it waits on.

  Either way, a body line `Depends on #M` if it waits on another issue.

## Refused

Auto mode may refuse a command (`land`, `release`, editing an issue you didn't
file). That is not a failure of the command: say which command was refused,
once, in the pull request body and the handoff, and carry on. When it blocks
landing, `hack/lib/github.sh status N needs-maintainer` with a comment naming
the command. The allow rules in `.claude/settings.json` are the owner's: never
edit them, and never do the refused thing through another command.
