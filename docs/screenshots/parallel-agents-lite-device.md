# Parallel agents 5/5: the light device (#27)

The light side of #27's "Done when", run on 2026-10-03 in the lite dev container
(`.devcontainer/lite`, host `1aba59e9ec44`) while the cluster device (host
`e68e918e5f86`) worked `needs/cluster` issues.

## The container has no cluster tools

```
$ hostname
1aba59e9ec44
$ echo C4_CLUSTER=$C4_CLUSTER
C4_CLUSTER=none
$ go version
go version go1.26.8 linux/amd64
$ node --version
v24.21.0
$ gh --version | head -1
gh version 2.102.0 (2026-09-30)
$ claude --version
2.1.288 (Claude Code)
$ kubectl version --client
bash: line 1: kubectl: command not found
$ kind version
bash: line 1: kind: command not found
$ docker version
bash: line 1: docker: command not found
$ helm version
bash: line 1: helm: command not found
```

(#44's report came from the full container with `C4_CLUSTER=none` set by the relay,
so kubectl and kind ran there; this one replaces it.)

## `make check` passes without a cluster

At f52de1e, 4m19s on 8 CPUs; `golangci-lint` reported 0 issues.

```
ok  	github.com/JairJosafath/c4factory/features	0.007s	coverage: 92.0% of statements
ok  	github.com/JairJosafath/c4factory/internal/agentrunner	9.279s	coverage: 87.3% of statements
ok  	github.com/JairJosafath/c4factory/internal/controller	103.789s	coverage: 84.3% of statements
ok  	github.com/JairJosafath/c4factory/internal/gitprovider	0.420s	coverage: 79.6% of statements
ok  	github.com/JairJosafath/c4factory/internal/model	0.012s	coverage: 97.9% of statements
ok  	github.com/JairJosafath/c4factory/internal/scaffold	0.022s	coverage: 91.1% of statements
ok  	github.com/JairJosafath/c4factory/internal/sessionclient	15.760s	coverage: 73.2% of statements
real	4m19.096s
```

## `pick` skips `needs/cluster` here, and offers it to a cluster device

Ready at the time: #12 (`needs/cluster`, older) and #38 (no label).

```
== pick with C4_CLUSTER=none (this lite container):
38	Release the claim when a merged pull request closes the issue	…/issues/38	ready
== pick as a device with the cluster would (C4_CLUSTER unset):
12	go-standard and ts-standard v1.1.0 guides state the Standard-change hook rule backwards	…/issues/12	ready
```

## The lite relay takes only the unlabelled issue

`.relay/relay-1.log`, while `claims` showed lynx-14 on `needs/cluster` issue #14 on
the cluster device:

```
08:34:55 worker 1 started: pid 25946, C4_CLUSTER=none, worktree …/.claude/worktrees/relay-1, HEAD f52de1e, at most 8 sessions of 6h
08:35:07 session 1 start: id 0ce589a7-…, otter-38 on #38 (ready) Release the claim when a merged pull request closes the issue
08:41:02 session 1 end: exit 0 after 5m, 16 turns, error false
08:41:02 backup-releases: failed with exit 2; see …/.relay/backup-releases.out
08:41:04 released #38 (otter-38): finished
08:41:04 session 1 closed #38
08:41:07 no issue ready; the evaluator reads the cluster, and this device has none (C4_CLUSTER=none)
08:41:07 no issue ready; stopping
```

```
$ hack/lib/github.sh claims        # 08:41
#14	lynx-14	live	e68e918e5f86	…	chore/14-keep-the-cel-lens-releases-moving
#27	heron-27	live	e68e918e5f86	…	feat/27-parallel-agents-5-5-needs-cluster-and
#54	otter-54	live	1aba59e9ec44	…	chore/54-lite-devcontainer-commit-its-lock-file
```

#12 stayed ready for the cluster device. `backup-releases` still runs, and fails,
on a device with `C4_CLUSTER=none`.

## A handed-back issue is finished on the cluster device

#45 ("report the Kind cluster's nodes") had no `needs/cluster` label. A relay with
`C4_CLUSTER=none` took it, found it needs the cluster, labelled it and handed it
back; the cluster device closed it two and a half minutes later. Issue events:

```
08:15:50Z unlabeled status/ready
08:15:51Z labeled   status/in-progress
08:16:17Z labeled   needs/cluster
08:18:40Z closed
```

## Not shown here

Two checkouts on the cluster device refusing a second `needs/cluster` issue is a
check for that device.

## Found on the way

A fresh lite container had no GitHub access (`git fetch`: "could not read
Username"; `gh` not logged in); `gh auth login` and `gh auth setup-git` fixed it.
The README now says so (#54).
