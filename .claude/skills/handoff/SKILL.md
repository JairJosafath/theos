---
name: handoff
description: Pick up, claim, hand off or finish a GitHub issue on JairJosafath/c4factory with hack/lib/github.sh - pick and take, lease a Kind cluster, write the handoff comment, close or mark needs-human, file a follow-up. Use when taking or continuing a factory issue, when the context grows past about 100k tokens between tasks, when work stops mid-way, or when the user asks to hand off, pause, wrap up or continue later.
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

One `status/*` at a time, swapped with `hack/lib/github.sh status N <status>`:
`proposed` (waits for the user), `ready`, `in-progress`, `needs-human` (the
newest comment says why). `needs/cluster` marks work whose "Done when" needs a
real Kubernetes node: Agent Sandbox pods, node or kubelet behaviour, RBAC as
deployed, Gitea Actions runners (`hack/lib/github.sh label N needs/cluster`
when you find out mid-way). Gitea work, YAML, templates and controller logic
don't qualify: `make test-gitea`, `make seed-local`, `make validate-yaml` and
envtest verify them on any device ([docs/dev/testenv.md](../../../docs/dev/testenv.md#test-tiers)).
With `C4_CLUSTER=none`, land the code and tests, label it and hand off the
rest.

## Lease a cluster

Only around the steps that touch one (deploys, samples, e2e, factory-ops,
`make up`/`down`), never for the whole issue:

1. Do the cluster-free work first: code, `make check`, envtest.
2. `hack/lib/github.sh cluster acquire` prints the cluster leased to this
   checkout (waits up to 30m, `--wait 10m` for less). Exit 4 names the
   holders: hand off with only the cluster steps left. A pool cluster
   belongs to nobody between leases, so an earlier session's install may
   still be on it: when the "Done when" asks for a fresh cluster, or what is
   installed isn't yours, pass `--fresh`. It prefers a cluster not created
   yet, else deletes and recreates the one it leased, and only that one;
   never `kind delete cluster` or `make cluster-delete` by hand. To install
   a release (`docs/getting-started.md`, the chart), pass `--fresh --bare`:
   the same, with nothing installed, since the chart brings its own Gitea
   and helm can't take over the testenv's.
3. Make targets now use it; kubectl takes `--context kind-<name>` on every
   call, never `kubectl config use-context` (every checkout shares the
   kubeconfig). `make deploy` this branch first: never trust what is deployed.
4. Verify, then `hack/lib/github.sh cluster release`. A handoff names the
   cluster under **State outside git** only while the work still holds it.

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

## Finish

- **Landed:** the pull request says `Closes #N` and is merged as AGENTS.md
  ("How to Work") says. If N is still open: `hack/lib/github.sh close N FILE`,
  FILE naming the pull request, SHAs and evidence.
- **Blocked on the user:** `hack/lib/github.sh status N needs-human` plus a
  comment saying what you need.
- **Follow-up found:** `hack/lib/github.sh create TITLE FILE status/proposed
  kind/<kind>` (add `needs/cluster` if it needs a real node; a body line
  `Depends on #M` if it waits on another issue). Don't start it in this
  session. Write it so the relay's triage can ready it: one piece of work, a
  checkable "Done when", and what it waits on.
