# The Crew prepares a release, then tags it (#117)

Kind cluster `c4factory`, operator built from `feat/117-the-crew-files-the-prepare-release-issue`
(`make deploy`), 2026-10-05 23:53–23:58 UTC. Setup, as a person would do it:

- Container `apps/hello-prep`: the hello Framework and `hello-standard`.
- Crew `apps/hello-prep` with `tagReleases: true` and `prepareReleases: true`, the
  token-free stand-in Agent as implementer and reviewer.
- In Gitea: milestone `v0.1.0` due 2026-10-20, and one `status/ready` issue in it (#1).

From then on no person acted. Task `hello-prep-1` merged #1; on the same poll
that saw v0.1.0 done the Crew filed #4 `Prepare v0.1.0` (`kind/release`,
`status/ready`) in the milestone, and started Task `hello-prep-4` on it. Once
that merged, the Crew tagged `v0.1.0` at the prepare issue's merge commit and
closed the milestone. The stand-in Agent only edits `STAND-IN.md`, so the
version and changelog files themselves are not changed in this run; a real
implementer gets the criteria below. #3 and #6 are the stand-in reviewer's
follow-ups, in Later.

```text
$ kubectl -n apps get crew hello-prep; kubectl -n apps get tasks
NAME         CONTAINER    IMPLEMENTER   REVIEWER   PHASE   PARALLEL   TASKS   AGE
hello-prep   hello-prep   stand-in      stand-in   Idle    1                  3m47s
NAME           CONTAINER    ISSUE   PHASE    ROUND   CHECKS    REVIEW    AGE
hello-prep-1   hello-prep   1       Merged   1       success   approve   2m47s
hello-prep-4   hello-prep   4       Merged   1       success   approve   2m8s

$ kubectl -n apps get task <each> -o jsonpath: name issue phase mergeCommit completedAt
hello-prep-1 #1 Merged 0298c57841097103d13915076d5d5dcc0521e106 2026-10-05T23:57:02Z
hello-prep-4 #4 Merged 463640455c2621a2f79964233820fb6c8cd9534b 2026-10-05T23:57:29Z

$ crew status.lastRelease + conditions
{"commit":"463640455c2621a2f79964233820fb6c8cd9534b","tag":"v0.1.0","time":"2026-10-05T23:57:29Z"}
Released=True Released: Released v0.1.0 at 463640455c2621a2f79964233820fb6c8cd9534b
Ready=True Idle: No ready issues

$ kubectl -n apps get events.events.k8s.io --field-selector regarding.name=hello-prep,regarding.kind=Crew
TIME                          TYPE     REASON             ACTION           NOTE
2026-10-05T23:57:02.235654Z   Normal   PreparingRelease   PrepareRelease   Filed #4 to prepare v0.1.0
2026-10-05T23:57:29.463327Z   Normal   Released           TagRelease       Released v0.1.0 at 463640455c2621a2f79964233820fb6c8cd9534b

$ kubectl -n c4-factory-system logs deploy/c4-factory-controller-manager | grep -E "Filed release preparation|Released milestone" | grep hello-prep
2026-10-05T23:57:02Z	INFO	Filed release preparation	{"controller": "crew", "Crew": {"name":"hello-prep","namespace":"apps"}, ..., "tag": "v0.1.0", "issue": 4}
2026-10-05T23:57:29Z	INFO	Released milestone	{"controller": "crew", "Crew": {"name":"hello-prep","namespace":"apps"}, ..., "tag": "v0.1.0", "commit": "463640455c2621a2f79964233820fb6c8cd9534b", "created": true}

$ Gitea: GET /repos/apps/hello-prep/issues?state=all&type=issues
{"number":6,"title":"Stand-in follow-up: add a third line","state":"open","labels":["status/proposed"],"milestone":"Later"}
{"number":4,"title":"Prepare v0.1.0","state":"closed","labels":["kind/release"],"milestone":"v0.1.0"}
{"number":3,"title":"Stand-in follow-up: add a third line","state":"open","labels":["status/proposed"],"milestone":"Later"}
{"number":1,"title":"Say hello in the stand-in file","state":"closed","labels":[],"milestone":"v0.1.0"}

$ Gitea: GET /repos/apps/hello-prep/issues/4 (body)
The work of release v0.1.0 is merged. This issue prepares it: once it merges, v0.1.0 is tagged at its merge commit, so the tag carries the version and the changelog.

## Merged in v0.1.0

- #1 Say hello in the stand-in file

## Acceptance criteria

- [ ] Every file that holds the version says 0.1.0, such as `version` in `package.json` and its lockfile; `make release-version VERSION=v0.1.0` passes when the Makefile has that target
- [ ] `CHANGELOG.md` has a 0.1.0 section that covers the issues above, with the "Unreleased" entries moved under it; the file is created in Keep a Changelog form when it is missing
- [ ] Nothing else changes: no code, test or check

$ Gitea: GET /repos/apps/hello-prep/pulls?state=all
{"number":5,"title":"Prepare v0.1.0","merged":true,"merge_commit_sha":"463640455c2621a2f79964233820fb6c8cd9534b"}
{"number":2,"title":"Say hello in the stand-in file","merged":true,"merge_commit_sha":"0298c57841097103d13915076d5d5dcc0521e106"}

$ Gitea: GET /repos/apps/hello-prep/tags/v0.1.0
{"name":"v0.1.0","commit":"463640455c2621a2f79964233820fb6c8cd9534b","message":"Release v0.1.0\n"}

$ Gitea: GET /repos/apps/hello-prep/milestones?state=all
{"title":"Later","state":"open","open_issues":2,"closed_issues":0}
{"title":"v0.1.0","state":"closed","open_issues":0,"closed_issues":2}

$ Gitea: GET /repos/apps/hello-prep/commits?sha=main
463640455c26 Prepare v0.1.0
0298c5784109 Say hello in the stand-in file
35e463cee4fd Scaffold hello-prep
```
