# A Standard update, live on the e2e hello Container

Issue [#80](https://github.com/JairJosafath/c4factory/issues/80), 2026-10-05, Kind
cluster `c4factory`, the operator built from branch
`chore/80-verify-a-standard-update-live-on-the-e2e` (`make deploy`). Agent: otter-80.

`hello-standard` v1.0.1 adds one line to its `Makefile` (`make check` says what it
checks). `make seed` pushed the tag to the templates' Gitea
(`Pushed template templates/hello-standard v1.0.1 (commit 0679eff)`), and
`make e2e-apps` moved the Standard's `ref` from `v1.0.0` to `v1.0.1` at 19:25:30Z.

## The Container, watched every 10s (`status.standard.ref | StandardUpdated | reason | pull request`)

```
19:25:35 v1.0.0|True|UpToDate|
19:26:05 v1.0.0|False|Updating|2
19:27:06 v1.0.1|True|UpToDate|
```

## After

```
$ kubectl -n apps get container hello -o yaml | yq ".status | {scaffoldedFrom, standard, conditions}"
{
  "scaffoldedFrom": [
    "hello@v1.0.0",
    "hello-standard@v1.0.0"
  ],
  "standard": {
    "name": "hello-standard",
    "ref": "v1.0.1"
  },
  "StandardUpdated": [
    {
      "lastTransitionTime": "2026-10-05T19:27:00Z",
      "message": "hello-standard is at v1.0.1",
      "observedGeneration": 1,
      "reason": "UpToDate",
      "status": "True",
      "type": "StandardUpdated"
    }
  ]
}
```

## The pull request in `apps/hello`, merged by the operator

```json
{
  "number": 2,
  "title": "Update the Standard hello-standard to v1.0.1",
  "state": "closed",
  "merged": true,
  "merged_at": "2026-10-05T19:27:00Z",
  "merged_by": "c4admin",
  "head": "refs/pull/2/head",
  "base": "main",
  "body": "The operator opened this pull request to bring the files of the Standard `hello-standard` from `v1.0.0` to `v1.0.1`.\n\n## Files\n\n- `Makefile`\n\nThe operator merges this pull request itself once CI passes. Close it to drop the update until the Standard's ref changes again.\n"
}
```

Files changed:

```json
[
  {
    "filename": "Makefile",
    "status": "changed",
    "additions": 1,
    "deletions": 0
  }
]
```

CI on its head commit:

```json
{
  "state": "success",
  "statuses": [
    {
      "context": "check / check (push)",
      "status": "skipped"
    },
    {
      "context": "check / check (pull_request)",
      "status": "success"
    }
  ]
}
```

Once merged, Gitea reports the head as `refs/pull/2/head`: the operator deleted the branch `standard-update/hello-standard-v1.0.1`.

## `main` of `apps/hello`

```
fadd7b7889 Update the Standard hello-standard to v1.0.1
12a0468cfa Prove the Task loop with the stand-in agent.
9a9cae22d9 Scaffold hello

$ cat Makefile
# The quality contract: `make check` is what CI and the agents run.
.PHONY: check
check:
	@echo 'make check: hello.sh must print "hello, world"'
	sh hello.sh | grep -qx 'hello, world'
```

## `make e2e` covers it

The Agent factory e2e now commits a change to `templates/hello-standard` (a new
branch off `main`), pins the Standard to that commit, waits for the merge, checks
the Makefile on `main` and the merged pull request, and restores the pin. Run
twice against this cluster, the second time starting from the restore the first
left in flight:

```
$ go test -tags=e2e ./test/e2e/ -ginkgo.focus "scaffolds the hello Container|carries a new commit"
  STEP: waiting until the Standard's files are at the ref the fixture pins @ 21:29:02 (UTC+2)
  STEP: committing a change to the Makefile of templates/hello-standard @ 21:30:02 (UTC+2)
  STEP: pinning the Standard to that commit @ 21:30:02 (UTC+2)
SUCCESS! -- 2 Passed | 0 Failed | 0 Pending | 13 Skipped
Ran 2 of 15 Specs in 131.541 seconds
```

No `mergeable=false` churn showed: each update merged at its first green CI.
