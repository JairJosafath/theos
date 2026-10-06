# The Crew watches the release run and reports a failure (#116)

Kind cluster `c4factory-2`, operator built from `feat/116-the-crew-watches-the-release-run-and`
(`make deploy`), 2026-10-06 00:44–00:50 UTC. The setup is #78's
(`docs/screenshots/release-tags-hello.md`): Container `apps/hello-rel` whose
`gitea-release` layer runs `.gitea/workflows/release.yaml` on a `v*` tag, and its
Crew, which tagged `v0.1.0` at `2ea9c85` on 2026-10-05. That release run (run 5)
stayed `queued`: this cluster runs no `go` runner.

1. `kubectl -n apps patch crew hello-rel -p '{"spec":{"tagReleases":true}}'`: on
   its next poll the Crew read the tag's runs from Gitea and showed the queued one.
2. As a person would, the admin cancelled run 5 in Gitea's web UI (Gitea 1.27
   has no API to cancel a run). A cancelled run ended other than success, so the
   Crew reported the release run as failed.

```text
# after step 1
$ kubectl -n apps get crew hello-rel -o jsonpath={.status.lastRelease}
{"commit":"2ea9c85ce22efd94d2b4808986ae762dfb6bf963","run":"Running","runURL":"http://gitea.gitea.svc.cluster.local:3000/apps/hello-rel/actions/runs/5","tag":"v0.1.0","time":"2026-10-05T23:09:15Z"}

# after step 2
$ kubectl -n apps get crew hello-rel -o jsonpath={.status.lastRelease}
{"commit":"2ea9c85ce22efd94d2b4808986ae762dfb6bf963","run":"Failed","runURL":"http://gitea.gitea.svc.cluster.local:3000/apps/hello-rel/actions/runs/5","tag":"v0.1.0","time":"2026-10-05T23:09:15Z"}

$ kubectl -n apps get events.events.k8s.io --field-selector regarding.name=hello-rel,regarding.kind=Crew
TIME                          TYPE      REASON             ACTION            NOTE
2026-10-06T00:47:15.152511Z   Warning   ReleaseRunFailed   WatchReleaseRun   Release run release.yaml of v0.1.0 ended cancelled: http://gitea.gitea.svc.cluster.local:3000/apps/hello-rel/actions/runs/5

$ kubectl -n c4-factory-system logs deploy/c4-factory-controller-manager | grep "Release run"
2026-10-06T00:47:15Z	INFO	Release run failed	{"controller": "crew", ..., "name": "hello-rel", "tag": "v0.1.0", "workflow": "release.yaml", "conclusion": "cancelled", "url": "http://gitea.gitea.svc.cluster.local:3000/apps/hello-rel/actions/runs/5"}

# two minutes and two polls later (lastPolled 00:49:16): still one Warning, no series, one log line
$ kubectl -n apps get events.events.k8s.io --field-selector ...,reason=ReleaseRunFailed -o jsonpath='{.reason} series={.series.count}'
ReleaseRunFailed series=

# what the Crew asks Gitea; the check run on main at the same commit is left out by its path
$ Gitea: GET /repos/apps/hello-rel/actions/runs?event=push&head_sha=2ea9c85ce22efd94d2b4808986ae762dfb6bf963
{"id":6,"path":"check.yaml@refs/heads/main","event":"push","status":"completed","conclusion":"success"}
{"id":5,"path":"release.yaml@refs/tags/v0.1.0","event":"push","status":"completed","conclusion":"cancelled"}
```

Afterwards `tagReleases` was switched off again, as #78 left it. A run that
succeeds (Normal Event `ReleaseRunSucceeded`, `run: Succeeded`) and a re-run that
fixes a failure are covered by envtest (`internal/controller/crew_release_test.go`),
not shown live: this cluster has no runner that can run hello-rel's release job.
