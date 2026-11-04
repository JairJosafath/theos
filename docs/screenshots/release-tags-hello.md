# The Crew tags a done release (#78)

Kind cluster `theos-2`, operator built from `feat/78-the-crew-tags-a-release-when-its`
(`make deploy`), 2026-10-05 23:07–23:10 UTC. Setup, as a person would do it:

- Container `apps/hello-rel`: the hello Framework, `hello-standard` and the
  `gitea-release` Deployment layer (its `.gitea/workflows/release.yaml` runs on a `v*` tag).
- Crew `apps/hello-rel` with `tagReleases: true`, the token-free stand-in Agent as implementer and reviewer.
- In Gitea: milestone `v0.1.0` due 2026-10-20, and one `status/ready` issue in it (#1).

From then on no person acted. The Crew started Task `hello-rel-1`, which merged
(`status.mergeCommit` from the pull request), and on its next poll the Crew
tagged `v0.1.0` at that commit through the Gitea API and closed the milestone.

**The API-made tag starts the release workflow:** run 5,
`release.yaml@refs/tags/v0.1.0`, event `push`. It stays `queued` here because
this cluster runs only the `base` CI runner, not the `go` one the gitea-release
layer asks for (`make toolchain-go runner-go`), and hello-standard has no `dist`
target; the files a release run attaches were not checked in this run.

```text
$ kubectl -n apps get crew hello-rel; kubectl -n apps get tasks
NAME        CONTAINER   IMPLEMENTER   REVIEWER   PHASE   PARALLEL   TASKS   AGE
hello-rel   hello-rel   stand-in      stand-in   Idle    1                  2m21s
NAME          CONTAINER   ISSUE   PHASE    ROUND   CHECKS    REVIEW    AGE
hello-rel-1   hello-rel   1       Merged   2       success   approve   78s

$ kubectl -n apps get task <merged> -o jsonpath={.status.phase} {.status.mergeCommit} {.status.completedAt}
hello-rel-1 1 Merged 2ea9c85ce22efd94d2b4808986ae762dfb6bf963 2026-10-05T23:09:15Z

$ kubectl -n apps get crew hello-rel -o jsonpath={.status.lastRelease} + conditions
true
{"commit":"2ea9c85ce22efd94d2b4808986ae762dfb6bf963","tag":"v0.1.0","time":"2026-10-05T23:09:15Z"}
Ready=True Idle: No ready issues
Released=True Released: Released v0.1.0 at 2ea9c85ce22efd94d2b4808986ae762dfb6bf963

$ kubectl -n apps get events.events.k8s.io --field-selector regarding.name=hello-rel,regarding.kind=Crew
TIME                          TYPE     REASON     ACTION       NOTE
2026-10-05T23:09:15.689901Z   Normal   Released   TagRelease   Released v0.1.0 at 2ea9c85ce22efd94d2b4808986ae762dfb6bf963

$ kubectl -n theos-system logs deploy/theos-controller-manager | grep "Released milestone"
2026-10-05T23:09:15Z	INFO	Released milestone	{"controller": "crew", "controllerGroup": "theos.factory.io", "controllerKind": "Crew", "Crew": {"name":"hello-rel","namespace":"apps"}, "namespace": "apps", "name": "hello-rel", "reconcileID": "bf772ae4-dc6e-4153-ab09-11ddcc1b4d05", "tag": "v0.1.0", "commit": "2ea9c85ce22efd94d2b4808986ae762dfb6bf963", "created": true}

$ Gitea: GET /repos/apps/hello-rel/tags/v0.1.0
{"name":"v0.1.0","commit":"2ea9c85ce22efd94d2b4808986ae762dfb6bf963","message":"Release v0.1.0\n"}

$ Gitea: GET /repos/apps/hello-rel/milestones?state=all
{"title":"v0.1.0","state":"closed","open_issues":0,"closed_issues":1,"closed_at":"2026-10-05T23:09:15Z"}

$ Gitea: GET /repos/apps/hello-rel/commits?sha=main (newest 3)
2ea9c85ce22e Say hello in the stand-in file
6354869179b5 Scaffold hello-rel

$ Gitea: GET /repos/apps/hello-rel/actions/tasks (runs)
{"name":"check","event":"push","head_branch":"main","status":"success","display_title":"Say hello in the stand-in file"}
{"name":"check","event":"pull_request","head_branch":"#2","status":"success","display_title":"Issue #1: Say hello in the stand-in file"}
{"name":"check","event":"pull_request","head_branch":"#2","status":"success","display_title":"Issue #1: Say hello in the stand-in file"}
{"name":"check","event":"push","head_branch":"main","status":"success","display_title":"Scaffold hello-rel"}

$ Gitea: GET /repos/apps/hello-rel/actions/runs
{"id":6,"path":"check.yaml@refs/heads/main","event":"push","status":"completed","conclusion":"success"}
{"id":5,"path":"release.yaml@refs/tags/v0.1.0","event":"push","head_branch":null,"status":"queued","conclusion":null}
{"id":4,"path":"check.yaml@refs/pull/2/head","event":"pull_request","status":"completed","conclusion":"success"}
```

## Switched off

`kubectl -n apps patch crew hello-rel --type merge -p '{"spec":{"tagReleases":false}}'`
drops the `Released` condition on the next poll; the Crew's conditions are then
only `Ready=True`, as for a Crew that never had the switch.
