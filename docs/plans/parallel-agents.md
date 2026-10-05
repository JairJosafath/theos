# Parallel agents on the factory's issues

**Goal:** Several agents, on one device or several, work the factory's GitHub
issues at the same time without taking the same issue twice, and an issue whose
agent died is picked up again within a couple of hours, on the same branch.

**Why:** `hack/lib/github.sh pick` hands every session the oldest
`status/in-progress` issue, and `hack/relay.sh` assumes one session at a time
(it even waits for the running session's handoff before it starts). Start two
agents today and both work the same issue in the same checkout.

## The rules

### Names

- An agent is named when it takes an issue: a word from a fixed list and the
  issue number, `otter-23`. The next agent on #23 is `heron-23`. The name says
  who and what at a glance; the claim comment adds the host and session.
- The name shows up wherever the agent leaves a trace: the claim comment, its
  commits (an `Agent: otter-23` trailer), its pull request body, its handoff
  and the relay log. No `@` in names: GitHub would read it as a mention.

### Claims and leases

- Taking an issue posts one claim comment (it replaces today's "took this
  issue" comment):

  ```
  <!-- c4-claim -->
  **otter-23** works on this issue · host `wsl` · branch `feat/23-claims` · `claude --resume <id>`
  Heartbeat: 2026-10-03T14:05Z
  ```

- A claim is live while its body is unreleased and the comment was edited in
  the last 2 hours (`C4_LEASE_TTL`). GitHub's `updated_at` is the heartbeat; no
  `stale` label, so nothing has to remember to remove one.
- A Claude Code `PostToolUse` hook (`.claude/settings.json`, shared in git)
  runs `hack/lib/github.sh beat`: it edits the heartbeat line at most every 15
  minutes. The agent cannot forget it, it covers relay and hand-started
  sessions alike, and it stops when the session stops. Without a claim, or
  within 15 minutes of the last beat, it exits at once; a GitHub error is
  ignored until the next tool call.
- `handoff`, `close` and `status N needs-human` release the claim: the comment
  is edited to `**otter-23** released this issue: handed off` (finished,
  needs-human), so the next agent can continue at once. The 2 hours only matter
  when a session dies or its device sleeps.
- The local half of a claim is `.relay/claim` in the agent's checkout (issue,
  comment id, name, branch, labels). One checkout holds one claim; `take`
  refuses a second.

### Taking an issue

`hack/lib/github.sh take N`:

1. A live claim by someone else: print who holds it and exit 4.
2. Edit claims past the TTL to `expired, taken over by <name>`.
3. Post the claim comment, wait 5 seconds and read the comments again. The live
   claim with the lowest comment id wins; a loser deletes its comment and exits 4.
   Two agents taking the same issue in the same second end with one owner.
4. Write `.relay/claim`, swap the issue to `status/in-progress`, print the
   name and branch.

A session whose claim was taken over (its laptop slept past the TTL) learns it
on its next beat: the hook exits 2 and tells it to push what it has to
`<branch>-<name>`, comment that branch on the issue and stop.

### Picking

`pick` offers, oldest first: in-progress issues without a live claim (handed
off or abandoned work), then ready + urgent, then ready. It skips an issue
that has a live claim, or whose body says `Depends on #M` while #M is open, so
a plan's step 3 waits for step 2. `pick` then `take`; on exit 4 pick again, up
to 5 times.

### Branches and checkouts

- One branch per issue, `<type>/<N>-<slug>` (type from the `kind/*` label: bug
  is `fix`, feature and improvement are `feat`, chore and docs keep their name),
  recorded in the claim. A takeover reads it from the newest claim or handoff
  and switches to it, or branches from `origin/main` if it was never pushed.
- Push after every commit, so a dead agent loses at most one commit.
- One agent per checkout. A second agent on a device works in a git worktree
  (`claude --worktree`, or the relay's workers below).

### Relay workers

`hack/relay.sh --workers N` starts N relays. Worker k runs from the main
checkout, logs to `.relay/relay-k.log` and starts its sessions in
`.claude/worktrees/relay-k`, switching that worktree to each issue's branch.
The relay no longer waits for a handoff before starting: claims keep it off
the issue you are working on. When a session ends still holding its claim, the
relay releases it (`session ended without a handoff`). The evaluator claims the
pinned "Factory evaluations" issue before it runs, so workers and devices never
evaluate twice; its 6-hour throttle reads that issue's newest evaluation.

### The Kind cluster

- **The label.** `needs/cluster` marks issues whose work or "Done when" needs
  the Kind cluster: deploys, factory-ops, Crews, e2e. An unlabelled issue needs
  none: code, docs and scripts, checked with `make check` (envtest needs no
  cluster). Whoever files an issue sets it; `labels` creates it, and
  `label N needs/cluster` adds it.
- **The cluster lease (#94).** A device with a cluster shares it between its
  checkouts, and any of them may work a `needs/cluster` issue. What they can't
  share is the cluster's state while one deploys and verifies (one operator
  Deployment and one set of CRDs, verifications that stop the node, and no
  memory for a second cluster), so a session holds the cluster only around
  those steps: `hack/lib/github.sh cluster acquire [--wait DURATION]` right
  before the first step that touches the cluster, then `make deploy` of its own
  branch, verify, `cluster release`. The lease is one file in the git common
  dir, beside every checkout, naming the agent, issue and worktree; `beat`
  refreshes it and it is stale after the TTL. `acquire` waits up to DURATION
  (default 30m) for another checkout's live lease, then exits 4 naming it, and
  the session hands off with only the cluster steps left. `release`,
  `handoff`, `close` and needs-human drop it; the relay drops one its session
  left behind; `hack/heavy-guard.sh` denies `make deploy`, `sample`, `e2e`,
  `test-e2e`, `up` and `down` while another checkout holds it. (Step 5 first
  had one `needs/cluster` issue per device, held for the claim's life; with 9
  workers that serialized most open issues, whose code and envtest work needs
  no cluster.)
- **Devices without a cluster.** `C4_CLUSTER=none` in the environment: `pick`
  skips `needs/cluster` issues, `take` refuses them, and the relay prompt says
  there is no cluster. A session that finds mid-way that its issue needs the
  cluster lands what it can (the code pull request, with unit and envtest
  tests), labels the issue `needs/cluster` and hands off with what is left
  (deploy and verify); a device with the cluster takes it from there.
- **The light devcontainer**, `.devcontainer/lite`, sets `C4_CLUSTER=none`:
  Go, Node, git, gh and Claude Code without Docker, Kind, kubectl or Helm. It
  and the full container empty Go's build cache on start past 4 GB.
  The README's "A device without the cluster" says how to set one up.

### Seeing who does what

`hack/lib/github.sh claims` lists the live and expired claims on open issues:
issue, name, host, heartbeat age, branch.

### Considered and dropped

- **A `stale` label agents remove while they work:** agents forget, and a
  forgotten label hands live work to a second agent. The hook can't forget.
- **Assignees:** every agent uses the same GitHub account.
- **"Has a branch, don't pick it":** a branch without a live claim is exactly
  the abandoned work that should be continued.

## Steps

Each step is one pull request to main and leaves the single-relay flow working.

1. **Claims.** `take` claims (comment, race check, expiry, `.relay/claim`);
   `handoff`, `close` and `needs-human` release; `release`, `claims` and `beat`
   commands; `pick` skips live claims and open `Depends on`. Names and branch
   names come from here.
2. **Heartbeat hook.** `.claude/settings.json` (un-ignored in `.gitignore`) runs
   `beat` after each tool call; the takeover message on exit 2.
3. **Agent rules.** The handoff skill, `AGENTS.md` and the relay prompt: the
   name and `Agent:` trailer, one agent per checkout, branch per issue pushed
   after every commit, `Depends on #M` in issue bodies, what to do when a claim
   is lost.
4. **Relay workers.** `--workers N`, a worktree per worker, release on session
   end, no handoff wait, the evaluator claims its issue.
5. **Cluster.** The `needs/cluster` label and the one-per-device rule, since
   replaced by the cluster lease (#94).

## Done when

- Steps 1 and 2: two checkouts run `take` on the same test issue at once and
  exactly one wins; `claims` shows its heartbeat moving while the session works
  and the claim expiring after the TTL once it stops; a third `take` then
  continues on the same branch.
- Step 4: two relay workers and one hand-started session run for an hour on
  three or more ready issues. No issue is taken twice, `claims` shows each with
  a fresh heartbeat, and killing a worker hands its issue to another after the
  TTL. The log and the `claims` output are saved in `docs/screenshots/`.
- Step 5: a relay with `C4_CLUSTER=none` takes only issues without
  `needs/cluster` while the cluster device works one; an issue it hands back
  with the label is finished on the cluster device; two checkouts on one device
  never both hold a `needs/cluster` issue; and the light devcontainer builds
  and passes `make check`. Evidence:
  `docs/screenshots/parallel-agents-lite-device.md` and
  `docs/screenshots/parallel-agents-cluster-device.txt`.
