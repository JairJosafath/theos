# The relay's pickup and handoff loop (#15)

Real relay runs on 2026-10-03 on the device without the cluster: started by the
user at 07:42 (2 workers) and 08:05 (3 workers), and by `hack/relay-watch.sh` at
17:18 and 18:35. The cluster device (host `e68e918e5f86`) ran its own relay and
sessions next to them. From `.relay/watch.log`, two seconds after #15 was readied:

```
18:35:02 ready: 15 | Agents pick up GitHub issues end to end: … | ready; starting the relay
18:35:02 relay: worker 1 started (pid 77024): tail -f /workspaces/c4factory/.relay/relay-1.log; worker 2 started (pid 77027): …
```

Each worker logs to `.relay/relay-k.log` (since #26; the issue's `.relay/relay.log`
is gone). Checked by otter-15, itself worker 1's session of 18:35. Times are UTC.

## Sessions in a row: the pick, why, the outcome, the next pick

The 12 sessions on this device ended 8 closed, 2 handed off, 2 needs-human.
Worker 3 at 08:14, urgent before ready (`.relay/relay-3.log`):

```
08:15:09 session 1 start: id b2cf0fed-…, otter-44 on #44 (ready-urgent) Relay check for #27: report the device's toolchain, …
08:15:38 session 1 end: exit 0 after 0m, 4 turns, error false; account in /workspaces/c4factory/.relay/session-3-1-20261003-081509.md
08:15:39 session 1 closed #44
08:15:53 session 2 start: id 56e5ac03-…, otter-45 on #45 (ready-urgent) Relay check for #27: report the Kind cluster's nodes, …
08:16:26 session 2 end: exit 0 after 0m, 6 turns, error false; account in …/session-3-2-20261003-081553.md
08:16:27 session 2 handed off #45
08:16:40 session 3 start: id f9e9908e-…, otter-13 on #13 (ready) A gated Task's reviewer asks for the proposal branch to be fixed too, …
08:26:11 session 3 end: exit 0 after 9m, 15 turns, error false; account in …/session-3-3-20261003-081640.md
08:26:12 released #13 (otter-13): finished
08:26:12 session 3 closed #13
08:26:12 stop file found; stopping
```

Worker 2 at 07:42, a needs-human session, then a finished one (`.relay/relay-2.log`):

```
07:43:18 session 1 start: id 38862b3e-…, otter-14 on #14 (ready) Keep the CEL Lens releases moving through the factory, …
07:45:01 session 1 end: exit 0 after 1m, 22 turns, error false; account in …/session-2-1-20261003-074318.md
07:45:01 session 1 left #14 waiting for the user (status/needs-human)
07:45:13 session 2 start: id f6e204c4-…, otter-18 on #18 (ready) Implementer guides allow wall-clock bounds in unit tests, …
07:52:57 session 2 end: exit 0 after 7m, 17 turns, error false; account in …/session-2-2-20261003-074513.md
07:52:58 released #18 (otter-18): finished
07:52:58 session 2 closed #18
07:53:01 no issue ready; the newest evaluation on #16 is 150m old, and the evaluator runs at most every 360m
07:53:01 no issue ready; stopping
```

No real pick here had the reason `in-progress`: the two in-progress issues left
without a claim (#45, #66) carry `needs/cluster`, which this device skips.
`hack/lib/github_test.sh` now pins the order: in-progress, then ready + urgent,
then ready; never proposed, needs-human, or "Depends on" an open issue.

## A handoff, and the session that continues it

- #45: otter-45 found it needed the cluster, labeled it `needs/cluster` and
  [handed off](https://github.com/JairJosafath/c4factory/issues/45#issuecomment-5967096555)
  with the skill's sections (Agent, Goal, Where it stands, Live thread, Decisions,
  Next steps, State outside git, References, For the next session). Two minutes
  later heron-45 on the cluster device posted "continuing otter-45's handoff",
  ran next step 1 (the report) and step 2 (closed it with the deliverable).
- #14: otter-14's [handoff](https://github.com/JairJosafath/c4factory/issues/14#issuecomment-5966881979)
  at 07:44 → heron-14 continued and [handed off](https://github.com/JairJosafath/c4factory/issues/14#issuecomment-5967070869)
  at 08:12 → lynx-14 continued; PR #75 closed it at 18:38.
- #27: otter-27's [handoff](https://github.com/JairJosafath/c4factory/issues/27#issuecomment-5966946617)
  → otter-54 (the light device) and heron-27 (the cluster device) did its next
  steps; PR #58 closed it.
- #66: otter-66 [handed off](https://github.com/JairJosafath/c4factory/issues/66#issuecomment-5971698748)
  at 17:33 with `needs/cluster`; it waits for the cluster device.

## Finished sessions close their issue

Each finished session's pull request says `Closes #N` and `Agent: otter-N`, and
was merged with a merge commit; GitHub closed the issue and the relay released
the claim (`released #10 (otter-10): finished`).

| Issue | Pull request | Merge commit |
| --- | --- | --- |
| #18 | #42 | 5c9d937 |
| #11 | #48 | 5cb459d |
| #35 | #51 | 8cb2bc9 |
| #10 | #49 | 9a9d8ba |
| #13 | #50 | f52de1e |
| #38 | #53 | 5890c5e |
| #60 | #69 | 255d10a |

#44 needed no code: its session posted the report and a
[closing comment](https://github.com/JairJosafath/c4factory/issues/44) naming it.

## Needs-human is left alone

otter-14 (#14, 07:45) and otter-27 (#27, 07:55) swapped their issues to
`status/needs-human` after a handoff saying why. The 08:05 run's six sessions took
#10, #11, #44, #45, #13 and #35, never #14 or #27; both went back to in-progress
only through `take` by sessions on the cluster device (08:00 and 08:07).

## The evaluator, and backup-releases

- The 6-hour gate: the 07:53 line above, and at 07:55 "the newest evaluation on
  #16 is 152m old". With `C4_CLUSTER=none` the worker skips it: "the evaluator
  reads the cluster, and this device has none" (08:41, 17:33, 17:42).
- The run, on the cluster device (it reads etcd and the node), read from GitHub:
  a relay worker claimed #16 as otter-16 at 17:09:50, posted the
  [evaluation](https://github.com/JairJosafath/c4factory/issues/16) at 17:19:04
  (it filed #65 and #66 as ready) and released the claim "evaluated" at 17:19:16.
  otter-62 claimed #62 four seconds later, while this device's workers were busy
  or waiting: by its timing, that worker's pick once more. #16 has evaluations at
  05:22 and 17:19 only. The "still nothing after the evaluator; stopping" branch
  didn't come up, and that device's relay log isn't on this one.
- `make backup-releases` ran after every session. On this device it first failed
  (`kubectl: command not found`, exit 2), which #56 fixed (PR #64): since 17:42
  "backup-releases: skipped; it reads Gitea in the cluster".

## A handoff that can't reach GitHub

From relay-1 at 18:38, with the claim on #15 held:

```
$ GH_TOKEN=bad hack/lib/github.sh handoff 15 test-handoff.md
gh: Bad credentials (HTTP 401)
github: could not post the handoff; kept it in /workspaces/c4factory/.claude/worktrees/relay-1/.relay/pending-handoff-15.md for the next session (hack/lib/github.sh flush)
exit=3
$ jq -r .name .relay/claim
otter-15
$ hack/lib/github.sh flush
posted the pending handoff on #15
exit=0
```

The [posted comment](https://github.com/JairJosafath/c4factory/issues/15#issuecomment-5972255376).
The relay flushes the worker's worktree before each pick and after each session.
`flush` leaves the claim live, though, which strands a hand-started session's
issue for two hours: #76.

## No token in the relay's traces

Patterns `gho_`, `ghp_`, `github_pat_`, at 18:40:

```
.relay dirs: .relay .claude/worktrees/relay-{1,2,3}/.relay
files: 62 (12 session accounts), with a match: 0
issue comments: 83 on 34 issues, with a match: 0
issue and pull request bodies: 75, with a match: 1 (#15's own text naming the three prefixes)
```

## What broke, and its issue

- A relay session on a device without the cluster tried cluster work (#14,
  07:43): `C4_CLUSTER=none` and `needs/cluster`, #27 (PR #43).
- backup-releases failed without the cluster: #56 (PR #64).
- The relay kept the `github.sh` it started with: #35 (PR #51); 17:42's worker
  restarted on new code ("the relay's code changed (HEAD 255d10a); restarting").
- A merged pull request left the claim live: #38 (PR #53).
- `flush` leaves the claim live: #76, proposed.
