# Parallel agents step 2: heartbeat hook (#24), 2026-10-03

## The hook beats a working session's claim (this session, #24)

Local beat reset to 0, then one tool call later (no manual beat):

```
07:11:51 #24 otter-24 live 36fc77da978f heartbeat 4m ago feat/24-parallel-agents-2-5-heartbeat-hook-keeps
next call  #24 otter-24 live 36fc77da978f heartbeat 0m ago feat/24-parallel-agents-2-5-heartbeat-hook-keeps
```

A no-op beat (within 15 minutes) takes about 60 ms; without a claim about 6 ms.

## A claim expires once beats stop, and a takeover stops the old holder (C4_LEASE_TTL=60, test issue #33)

```
== test issue #33
== 07:12:15 agent A takes it
otter-33	chore/33-test-claim-expiry-for-24
take rc=0
#33	otter-33	live	36fc77da978f	heartbeat 0m ago	chore/33-test-claim-expiry-for-24
== 07:13:43 75s later, no beats from A
#33	otter-33	stale	36fc77da978f	heartbeat 1m ago	chore/33-test-claim-expiry-for-24
== 07:13:44 agent B takes it
heron-33	chore/33-test-claim-expiry-for-24
take rc=0
#33	heron-33	live	36fc77da978f	heartbeat 0m ago	chore/33-test-claim-expiry-for-24
== 07:13:54 A's next beat
github: otter-33 no longer holds #33: another agent took it over (or it was released). Stop working on it:
push what you have to chore/33-test-claim-expiry-for-24-otter-33 (git push origin HEAD:refs/heads/chore/33-test-claim-expiry-for-24-otter-33),
comment that branch on #33 (hack/lib/github.sh comment 33 -), and stop.
beat rc=2
== claim comments
<!-- c4-claim -->
**otter-33** expired, taken over by heron-33 · branch `chore/33-test-claim-expiry-for-24`

<!-- c4-claim -->
**heron-33** works on this issue · host `36fc77da978f` · branch `chore/33-test-claim-expiry-for-24` · `claude --resume f0b9a4fa-9469-47f7-b069-e42e08ea2026`
Heartbeat: 2026-10-03T07:13Z
== cleanup
https://github.com/JairJosafath/c4factory/issues/33#issuecomment-5966669034
closed #33
close rc=0
```
