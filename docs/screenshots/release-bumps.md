# A release files a bump issue in the Container that bundles it (#118)

Kind cluster `theos`, operator built from `feat/118-a-release-files-bump-issues-in-the`
(`make deploy`), 2026-10-06 00:32–00:34 UTC. Setup, as a person would do it:

- Containers `apps/bump-srv`, `apps/bump-ext` and `apps/bump-web`: the hello
  Framework and `hello-standard`.
- Relationship `bump-ext-bundles-srv` (bump-ext → bump-srv, `bundles: true`) and
  `bump-web-calls-srv` (bump-web → bump-srv, no `bundles`).
- Crew `apps/bump-srv` with `tagReleases: true`, the token-free stand-in Agent as
  implementer and reviewer.
- In Gitea: milestone `v0.1.0` in bump-srv with one `status/ready` issue (#1);
  milestones `v0.2.0` (due 2026-10-27) and `v0.3.0` (due 2026-11-03) in bump-ext.
- The admission check refuses `bundles` on a link that isn't Container to Container:
  `The Relationship "bad" is invalid: spec: Invalid value: only a Container to Container link can bundle`.

From then on no person acted. The Crew merged #1 through Task `bump-srv-1`, tagged
`v0.1.0` and closed its milestone, and in the same poll filed the bump issue in
bump-ext's first open release, `v0.2.0`. bump-web, which calls the server but
doesn't bundle it, got nothing.

```text
$ kubectl -n apps get crew bump-srv; kubectl -n apps get task bump-srv-1
NAME       CONTAINER   IMPLEMENTER   REVIEWER   PHASE   PARALLEL   TASKS   AGE
bump-srv   bump-srv    stand-in      stand-in   Idle    1                  80s
NAME         CONTAINER   ISSUE   PHASE    ROUND   CHECKS    REVIEW    AGE
bump-srv-1   bump-srv    1       Merged   2       success   approve   77s

$ kubectl -n apps get crew bump-srv -o jsonpath={.status.lastRelease}
{
  "bumps": [
    {
      "container": "bump-ext",
      "issueURL": "http://gitea.gitea.svc.cluster.local:3000/apps/bump-ext/issues/1"
    }
  ],
  "commit": "c6f1ec9d3ea72ae3e54e5d765e6b6a1369047526",
  "tag": "v0.1.0",
  "time": "2026-10-06T00:33:26Z"
}

$ kubectl -n apps get events.events.k8s.io --field-selector regarding.name=bump-srv,regarding.kind=Crew
TYPE     REASON      ACTION       NOTE
Normal   Released    TagRelease   Released v0.1.0 at c6f1ec9d3ea72ae3e54e5d765e6b6a1369047526
Normal   BumpFiled   FileBump     Filed http://gitea.gitea.svc.cluster.local:3000/apps/bump-ext/issues/1 to bump bump-srv to v0.1.0 in bump-ext

$ Gitea: GET /repos/apps/bump-srv/tags/v0.1.0
{"name":"v0.1.0","commit":"c6f1ec9d3ea72ae3e54e5d765e6b6a1369047526"}

$ Gitea: GET /repos/apps/bump-ext/issues?state=all  (the bundler)
{"number":1,"title":"Bump bump-srv to v0.1.0","state":"open","labels":["kind/bump","status/ready"],"milestone":"v0.2.0"}

$ Gitea: issue bump-ext#1 body
bump-srv released v0.1.0 at `c6f1ec9d3ea72ae3e54e5d765e6b6a1369047526`: http://gitea.gitea.svc.cluster.local:3000/apps/bump-srv/src/tag/v0.1.0

bump-ext bundles bump-srv (Relationship `bump-ext-bundles-srv`: Ships the server in server.json), so it ships a pinned release of it.

## Goal

bump-ext ships bump-srv v0.1.0.

## Acceptance criteria

- [ ] Every place this repository pins a release of bump-srv names v0.1.0
- [ ] What bump-srv's changes up to v0.1.0 ask of the Containers that use it is done here, or filed as an issue
- [ ] `make check` passes

<!-- theos:bump bump-srv v0.1.0 -->


$ Gitea: GET /repos/apps/bump-web/issues?state=all  (calls the server, does not bundle it)
[]
$ kubectl -n theos-system logs deploy/theos-controller-manager | grep -E "Released milestone|bump issue" (trimmed)
2026-10-06T00:33:26Z	INFO	Released milestone	{"tag": "v0.1.0", "commit": "c6f1ec9d3ea72ae3e54e5d765e6b6a1369047526", "created": true}
2026-10-06T00:33:27Z	INFO	Filed a bump issue	{"bundler": "bump-ext", "tag": "v0.1.0", "issue": "http://gitea.gitea.svc.cluster.local:3000/apps/bump-ext/issues/1"}
```

Later polls file nothing more: at lastPolled 2026-10-06T00:36:33Z bump-ext still has 1 issue.
