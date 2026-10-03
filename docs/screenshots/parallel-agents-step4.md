# Parallel agents step 4: relay workers (#26)

Evidence for [docs/plans/parallel-agents.md](../plans/parallel-agents.md), step 4,
taken 2026-10-03 on one device (host `36fc77da978f`) with a second device's agent
(`e68e918e5f86`, otter-36 on #36) working at the same time.

**Spike, not the full hour.** The plan's "Done when" asks for an hour and a killed
worker taken over after the 2-hour TTL. Instead: `MAX_SESSIONS=3 hack/relay.sh
--workers 2` ran on the real backlog, and the takeover was shown with a short
`C4_LEASE_TTL` on #26's own claim rather than by killing a worker mid-work. The
hour-long run is left to normal use; the relay logs keep accruing in `.relay/`.

What it shows:
- Two workers start 20 s apart, each creates its own worktree and claims a
  different issue (#27, #14); with otter-36 on #36 that is three agents on three
  issues, none taken twice.
- Worker 2's session on #14 ended at `status/needs-human` (the Kind cluster was
  gone after a devcontainer restart), which released its claim; the worker read
  the outcome and moved on to #18 within 12 s.
- A live claim (heartbeat 3 min old, TTL 5 min) refuses a second `take` (exit 4).
- Treated as dead (TTL 60 s), it is taken over by `heron-26` on the same branch,
  and the old holder's next `beat` exits 2 with the push-and-stop instructions.

## Relay logs

```
2026-10-03T07:42:46+00:00 worker 1 started: pid 88751, worktree /workspaces/c4factory/.claude/worktrees/relay-1, HEAD a508e46, at most 3 sessions of 6h, issues on JairJosafath/c4factory
2026-10-03T07:42:46+00:00 worker 2 started: pid 88754, worktree /workspaces/c4factory/.claude/worktrees/relay-2, HEAD a508e46, at most 3 sessions of 6h, issues on JairJosafath/c4factory
2026-10-03T07:42:49+00:00 created the worktree /workspaces/c4factory/.claude/worktrees/relay-1
2026-10-03T07:43:00+00:00 session 1 start: id adca15c9-4f2e-409f-8015-8436192e473e, otter-27 on #27 (ready-urgent) Parallel agents 5/5: needs/cluster, and devices without a cluster, branch feat/27-parallel-agents-5-5-needs-cluster
2026-10-03T07:43:07+00:00 created the worktree /workspaces/c4factory/.claude/worktrees/relay-2
2026-10-03T07:43:18+00:00 session 1 start: id 38862b3e-0d02-4a49-9694-08585d36bf74, otter-14 on #14 (ready) Keep the CEL Lens releases moving through the factory, branch chore/14-keep-the-cel-lens-releases-moving, HEAD a508e46
2026-10-03T07:45:01+00:00 backup-releases: failed with exit 2; see /workspaces/c4factory/.relay/backup-releases.out
2026-10-03T07:45:01+00:00 session 1 end: exit 0 after 1m, 22 turns, error false; account in /workspaces/c4factory/.relay/session-2-1-20261003-074318.md
2026-10-03T07:45:01+00:00 session 1 left #14 waiting for the user (status/needs-human)
2026-10-03T07:45:13+00:00 session 2 start: id f6e204c4-e073-4d46-8560-1ff79aac97d4, otter-18 on #18 (ready) Implementer guides allow wall-clock bounds in unit tests, which fail on the shared CI runner, branch docs/18-implementer-g
```

## Claims and the takeover

```
## claims before
#18	otter-18	live	36fc77da978f	heartbeat 3m ago	docs/18-implementer-guides-allow-wall-clock
#26	otter-26	live	36fc77da978f	heartbeat 3m ago	feat/26-parallel-agents-4-5-relay-workers-in
#27	otter-27	live	36fc77da978f	heartbeat 5m ago	feat/27-parallel-agents-5-5-needs-cluster-and
#36	otter-36	live	e68e918e5f86	heartbeat 25m ago	chore/36-keep-every-checkout-s-main-up-to-date-vs

## takeover: C4_LEASE_TTL=5m hack/lib/github.sh take 26 (from a second checkout)
github: #26 is claimed by otter-26 on host 36fc77da978f, heartbeat 3m ago
exit 4

## claims after (C4_LEASE_TTL=5m)
#18	otter-18	live	36fc77da978f	heartbeat 3m ago	docs/18-implementer-guides-allow-wall-clock
#26	otter-26	live	36fc77da978f	heartbeat 3m ago	feat/26-parallel-agents-4-5-relay-workers-in
#27	otter-27	stale	36fc77da978f	heartbeat 5m ago	feat/27-parallel-agents-5-5-needs-cluster-and
#36	otter-36	stale	e68e918e5f86	heartbeat 26m ago	chore/36-keep-every-checkout-s-main-up-to-date-vs

## takeover: C4_LEASE_TTL=60s hack/lib/github.sh take 26 (second checkout; otter-26 treated as dead)
heron-26	feat/26-parallel-agents-4-5-relay-workers-in
exit 0

## what otter-26 hears on its next beat
github: otter-26 no longer holds #26: another agent took it over (or it was released). Stop working on it:
push what you have to feat/26-parallel-agents-4-5-relay-workers-in-otter-26 (git push origin HEAD:refs/heads/feat/26-parallel-agents-4-5-relay-workers-in-otter-26),
comment that branch on #26 (hack/lib/github.sh comment 26 -), and stop.
exit 2

## claims after
#18	otter-18	live	36fc77da978f	heartbeat 3m ago	docs/18-implementer-guides-allow-wall-clock
#26	heron-26	live	36fc77da978f	heartbeat 0m ago	feat/26-parallel-agents-4-5-relay-workers-in
#27	otter-27	live	36fc77da978f	heartbeat 5m ago	feat/27-parallel-agents-5-5-needs-cluster-and
#36	otter-36	live	e68e918e5f86	heartbeat 26m ago	chore/36-keep-every-checkout-s-main-up-to-date-vs
```
