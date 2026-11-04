# One app uses another app's released Go module

GitHub issue #430, reported by gray-lenovo on v0.5.0. Agent: otter-430.
Related: #428 (a Brief's issues keep the plan's order, `after:`, steps
#440–#442) orders issues by merged work; this plan makes one app's code
something another app can fetch, and orders by release.

## Goal

In gray-lenovo's `cel-lens`, the engine (`cel-lens-core`) and the CLI
(`cel-lens-cli`) stay two Go apps, and the CLI depends on a released version
of the engine's module: `go get theos.local/cel-lens/cel-lens-core@v0.1.0`
works in the CLI's agent pod and in its CI. Today nothing can fetch that name
(the issue's probe log), so a plan that asks for it sets the issue up to fail.

Done when a Brief round whose CLI issue says it uses the core files that issue
only once the core's release is published, with the version in its body, and
the CLI's pod and CI fetch that version from Gitea; and a plan that uses an
app with no release planned is refused before the round starts.

## Decisions, and why

The owner asked for the industry's way of sharing between repositories
(the comments on #430): one app publishes a versioned release, the other
depends on that version. The maintainer settled the direction; these are the
choices under it.

1. **One repository per Container stays.** Scaffold-once, layers, Standards,
   per-app Crews and releases are all built on it. A monorepo split by path
   would rework all of that for one case.
2. **Gitea's Go package registry is the module proxy; module names stay.**
   Gitea (1.27 in the chart) serves a Go module proxy per owner,
   `<gitea>/api/packages/<owner>/go`, and takes uploads of module zips
   (`PUT .../go/upload`, 201; a version that exists is refused, so a version
   is immutable). A proxy serves any module path, so the name the template
   already gives, `theos.local/<project>/<container>`, works as is: no
   template change, no rename of apps already scaffolded. The other way, a
   module named after its Gitea repository and fetched straight from Git,
   doesn't work here: a module path can't carry a port
   (`gitea.<ns>.svc.cluster.local:3000`), the in-cluster name isn't one the
   user's own machine resolves, and it would fetch branches, not releases.
   A proxy is also the shape npm and PyPI take in Gitea's registry, so other
   languages follow the same plan later.
3. **The operator publishes, right after it tags.** The Crew already tags a
   Container's release (`tagRelease`, `crew_release.go`). Once the tag
   exists, if the repository has a `go.mod` at its root at that tag, the
   operator downloads the tag's archive (`/repos/<owner>/<repo>/archive/<tag>.zip`),
   repacks it as a module zip with `golang.org/x/mod/zip` (already in
   `go.mod`) under the module path `go.mod` declares, and uploads it. A
   repeat finds the version there and does nothing. Not a workflow step: a
   step in the template's release workflow would need a runner and couldn't
   be tested below Kind, and the operator already holds the Gitea credentials
   and the moment of release. A major version of 2 or more whose module path
   lacks the `/vN` suffix Go requires isn't published; the Crew's
   `Progressing` message says why.
4. **Pods fetch through the proxy, never directly.** Agent pods get
   `GOPROXY=<gitea>/api/packages/<owner>/go,https://proxy.golang.org,direct`
   and `GONOSUMDB=theos.local` (the checksum database has never heard of
   it), and the agent runner writes a `~/.netrc` for the Gitea host from the
   credentials it already has (`THEOS_GIT_USERNAME`/`THEOS_GIT_TOKEN`), which Go
   uses for proxy auth. Gitea comes first so our module names never go to
   proxy.golang.org; a module Gitea lacks returns 404 and falls through to the
   public proxy (wrong, see #518 below). Not `GOPRIVATE`: it would also set `GONOPROXY` and send
   `theos.local` straight to a host that doesn't exist. The fence already
   lets an agent pod reach Gitea's namespace. The go-standard Standard's CI
   (`check.yaml`) sets the same, with the job's `gitea.token` in `~/.netrc`,
   so a pull request that uses the module passes CI.

   What step 2 found (#459, `make test-gitea`): the go command sends `.netrc`
   credentials over https only, on the first request; over plain http, which
   the in-cluster Gitea is, it fetches without them, and a public owner's
   packages allow that. Over https, a token without a package scope gets a
   401 (`reqPackageAccess`) for every module, which stops the go command where
   a 404 lets it go on. So the agent bot's token is made with
   `write:repository,read:package` (`gitprovider.AgentTokenScopes`), and the
   agent's Secret keeps the scopes, so a token made before is made again on the
   next run. The proxy URL is on `gitprovider.Repository` (`GoProxy`), built
   from the GitProvider's URL and owner; the planner's pod doesn't get it, as
   it builds no code.

   What #518 corrected: "Gitea comes first so our module names never go to
   proxy.golang.org" was wrong. With a comma list the go command asks the next
   proxy after a 404, so a module Gitea lacks (not released yet, or a typo)
   went to proxy.golang.org with the project's and Container's names, and Go
   has no per-path proxy. Agent pods now get
   `GOPROXY=http://127.0.0.1:3127,direct` and `THEOS_GO_REGISTRY=<gitea>/api/packages/<owner>/go`:
   theos-agent serves a proxy on loopback (`agentrunner.GoProxy`) that sends
   `theos.local/` paths to the registry alone, adding the bot's
   credentials over https, and the rest to proxy.golang.org. A factory module
   the registry lacks is a 404, the go command tries direct, and
   `theos.local` resolves nowhere. go-standard v1.1.5's CI can't run
   theos-agent, and needs no new modules: a step downloads the `theos.local/`
   modules `go.sum` lists (`go mod tidy` loads dependencies' test imports too,
   which only `go.sum` has) with `GOPROXY` set to the registry alone, and
   `make check` runs with `GOPRIVATE=theos.local`, so it finds them in the
   module cache and asks no proxy for them.
5. **The plan says who uses whom.** An issue file's front matter takes
   `uses:`, the Containers whose released code it builds on:

   ```markdown plan/issues/03-cli.md
   ---
   title: Check a file from the command line
   labels: [kind/feature]
   uses: [cel-lens-core]
   ---
   ```

   A Relationship alone is too coarse, as in #428: it says the CLI uses the
   core, not which of the CLI's issues need the core's release.
6. **The plan check refuses what can't be met.** A `uses:` naming the issue's
   own Container, a Container not in the model, one with no Relationship from
   the issue's Container to it (in the plan or the cluster), or one with
   neither a release planned (`plan/releases.yaml`) nor one made before, is
   refused with the file and the reason, and the planner's next round gets it.
   The check can't see a repository's files, so a used app that turns out not
   to be Go is caught at release: no module is published, and the waiting
   issue says so (decision 7).
7. **Filed once the release is published.** An issue with `uses:` is filed
   without `status/ready`, like an ordered one in #442, and readied once each
   Container it uses has a published module version: the newest at that
   moment, which for a library released in the same round is the round's
   release. Its body gets a line per used module above the Brief's marker,
   `Uses theos.local/cel-lens/cel-lens-core v0.1.0: go get it at that
   version.` so the implementer pins a release, never a branch. While it
   waits, the Brief stays `Applying` and its message names the issue and the
   app it waits for, and why if the release had no `go.mod`.
8. **Off until the planner is told.** The planner guide documents `uses:`
   (and that the old way, reusing packages of another app without it, can't
   work) in the last step, so everything before it does nothing until a
   planner writes it.
9. **Go only, for now.** npm and PyPI through Gitea's registry follow the
   same pattern (publish at tag, a registry URL in the pod's config) and are
   a follow-up issue once a Brief needs them.

## Steps

Each step is a GitHub issue, `status/proposed`, one pull request each.

1. **Publish a Go module to Gitea** (#458, `internal/gitprovider`).
   `PublishGoModule(ctx, repo, tag)`: reads `go.mod` at the tag, downloads
   the tag's archive, repacks it as a module zip, uploads it; reports "no
   go.mod" and "exists" as results, not errors. Done when the unit tests
   cover the zip (prefix, excluded files, a nested module left out) and
   `make test-gitea` publishes a tagged repository's module and then
   `go mod download theos.local/<owner>/<repo>@v0.1.0` with only
   `GOPROXY` set to the local Gitea succeeds, and a repeat publishes nothing.
2. **Pods and CI fetch through Gitea** (#459, `internal/controller` pod spec,
   `internal/agentrunner`, `templates/go-standard`). Decision 4's env on the
   agent's pod and its `~/.netrc`; the same in go-standard's `check.yaml`.
   Done when the pod-spec unit tests show the env for an in-cluster and an
   external GitProvider URL, the runner's test shows the netrc written with
   mode 0600, and `make validate-yaml` passes the template; `make test-gitea`
   fetches a published module as a restricted agent account with the netrc.
3. **The Crew publishes its releases** (#460, `internal/controller/crew_release.go`;
   depends on #458). After `tagRelease` tags, the Crew publishes; a failure
   is retried on the next reconcile and shown in `Progressing`; `status.apps`
   (or the release status it already keeps) records the module and version.
   Done when envtest, on the fake Git, shows a released Go Container's module
   published once, a non-Go one skipped, and a `v2.0.0` without `/v2`
   reported.
   As built: `status.lastRelease.goModule` (state `Published`, `NotGo`,
   `Invalid` or `Failed`, path, version, message) and a Crew condition
   `ModulePublished` in place of `Progressing`, which only Tasks have; step 4
   reads both.
4. **Plans say `uses:`, and the round waits for the release**
   (#461, `internal/model`, `internal/controller`, the planner guide; depends on
   #459, #460 and #442). `PlanIssue.Uses`, decision 6's refusals in
   `CheckPlan`, filing without ready and readying with the body line once
   the module is published, the Brief's message while it waits;
   `internal/model/planner.md` documents `uses:` with the cel-lens example.
   Done when table tests show each refusal and a valid plan passing, and
   envtest applies a two-Container round whose CLI issue uses the core,
   showing the CLI issue unready until the core's module is published, then
   ready with `Uses ... v0.1.0` in its body; and a restart in between
   finishes it.
   As built: a used app with no release made must be in `releases.yaml`
   *and* have an issue of the round, since a release with no work is never
   made (R7). For an app the round releases, "the round's release" is the
   one tagged with the title of the round's release milestone in its
   repository; any other app gives its newest. The issue is filed at once
   without `status/ready` and recorded in the Brief's status only once
   readied, like an ordered one, so a restart finds it by its marker
   (`brief_uses.go`).

## What only Kind would show

That the CLI's agent, on a real node with the real agent-go image, runs
`go get theos.local/cel-lens/cel-lens-core@v0.1.0` and builds, and that
its CI run on the Gitea Actions runner does the same. `make test-gitea`
covers the publish and the fetch through the proxy with a real Gitea and the
real `go` command; envtest covers the pod's env and the round's order.
