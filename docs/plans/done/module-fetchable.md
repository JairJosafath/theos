# Plan: `ModulePublished` means Go can fetch the version

GitHub issue #1001, reported from a device running factory 0.21.1. Agent:
otter-1001.

## Goal

After cel-lens-core released v0.15.1, its Crew said `ModulePublished=True`
(`Published github.com/JairJosafath/cel-lens/engine v0.15.1`) beside
`Published=False Unauthorized`. The version was in the factory's Gitea
registry, but the module path names GitHub, so `go get` asks
proxy.golang.org, which fetches from GitHub, where no `engine/v0.15.1` tag
was ever pushed: `unknown revision engine/v0.15.1`. Anyone reading the green
condition would tell people to `go install ...@v0.15.1`, and it would fail.

`ModulePublished` turns True only once the version can be fetched at the
path `go.mod` declares, and otherwise says what is missing; a refused GitHub
release never sits beside a module step that reports success for the same
version; and a module nobody can fetch shows in *Needs you*.

Done when the steps below are merged, and design.md, *A released Go app is a
module others can fetch*, says what they built (this plan's pull request
writes it there already, as the target).

## What is true today

- The operator uploads every release's module to Gitea's Go registry under
  the path `go.mod` declares, whatever host the path names, and calls that
  `Published` (`crew_gomodule.go`).
- Only `theos.local/` paths are ever read from that registry: the agent
  pods' proxy (`agentrunner.GoProxy`) sends every other path to
  proxy.golang.org, and the go-standard CI reads the registry only for
  `theos.local/` modules. An upload of a `github.com/...` path is read by
  nobody.
- `publishReleases.source` puts the app's source in a folder of the GitHub
  repository and tags it `<folder>/vX.Y.Z` (`release-source.md`). That tag is
  what `go install github.com/<repo>/<folder>@vX.Y.Z` needs, and it is made
  only by the publish steps, which a refused token stops before they write
  anything.

## Decisions

1. **`ModulePublished` is about the reader of the path, not the upload.**
   True means `go get <path>@<version>` finds the version for whoever reads
   that path. A path under `theos.local/` is read by the factory's apps,
   from the registry: the upload is the publish, as today. Any other path is
   read by the public go command, through proxy.golang.org: the upload counts
   for nothing there. Why: the condition's reader acts on it by telling
   someone to `go install`; a meaning that depends on which proxy they use is
   the bug.

2. **The registry upload stays for every path.** It is harmless, a version is
   immutable there either way, and dropping it changes nothing anyone reads.
   For a path outside the factory the message says it was uploaded and what
   Go still lacks.

3. **The public proxy is asked, after our tag is out.** For a path outside
   the factory, the operator asks the Go proxy for
   `<escaped path>/@v/<version>.info` (the go command's own request, so it
   also makes proxy.golang.org fetch and cache the version, and pkg.go.dev
   list it). A 200 is True; a 404 or 410 is False with the proxy's own line.
   The proxy caches a miss for up to about 30 minutes, so asking before the
   tag exists would hide the version after it is pushed: when the path is
   the Crew's own GitHub source folder (`github.com/<repository>/<folder>`,
   `model.GoInstallProblem` finds nothing wrong), the operator does not ask
   until `status.lastRelease.publish.source.tag` is the version's tag, and
   says `WaitsForRelease` meanwhile, with the `Published` condition's reason
   and message when it is False. Any other path (a module published by
   hand, or a path that doesn't match the folder) is asked at once. It is
   asked at most every 5 minutes (`goModule.checkedTime`), until it is True
   or the next release replaces it; it is no network load worth bounding
   further. Why not trust our own tag alone: the user asked for "can really
   be fetched", a path we don't publish to can only be confirmed by asking,
   and one rule for both is simpler to explain.

4. **Where the proxy can't look, the tag is the answer.** The operator's flag
   `--go-proxy` (Helm `goProxy`, default `https://proxy.golang.org`) names
   the proxy; `off` asks none, for an install without that egress. A GitHub
   target that GitHub reports private can't be read by the public proxy
   either. In both cases the Crew's own source folder turns True once the
   source tag is out (the message says to fetch a private one with
   `GOPRIVATE`), and any other path stays False `NotFetchable`, saying the
   factory can't tell.

5. **Reasons.** `ModulePublished` True: `ModulePublished`, `NotGo`. False:
   `InvalidModule`, `GitHostUnavailable` (as today), `WaitsForRelease`
   (outside path, the Crew's source tag not out yet), `NotFetchable` (the
   proxy says no, or the factory can't tell; the message says what Go
   needs, from `GoInstallProblem` when the path is near the Crew's folder,
   else that nothing in this Crew publishes to that host), and
   `ProxyUnavailable` (the proxy couldn't be reached; asked again next
   time). `status.lastRelease.goModule.state` gains `Waiting` and
   `NotFetchable`; `Published` keeps its name with the new meaning. A
   Warning Event `ModuleNotFetchable`, once per release, when it first turns
   `NotFetchable`; the Normal Event `ModulePublished` moves, for an outside
   path, to the moment the proxy first answers 200.

6. **Old statuses are checked again.** A `goModule` already `Published` for
   a path outside the factory and with no `checkedTime` was never checked;
   the Crew checks it as above, so v0.15.1 turns `WaitsForRelease` after the
   upgrade instead of staying green.

7. **The rule is the model's.** `internal/model` gets the decision as a pure
   function of the facts (the path, the version, the registry outcome, the
   Crew's target and source folder, the published source tag, the target's
   visibility, the proxy's last answer and when): what to report and
   whether to ask the proxy now. The controller does the asking and the
   writing.

8. **Needs you shows it.** The UI's *Needs you* lists a Crew whose
   `ModulePublished` is False with `InvalidModule` or `NotFetchable`, or
   `WaitsForRelease` while `Published` is False, with the condition's
   message (which names the GitHub failure). The Crew page shows the new
   states, and a Brief's `status.apps[].lastRelease` and card carry the
   module's state and message, so the Brief points at it too.

## Steps

Each step is its own pull request and leaves main working; nothing changes
what the Crew says before step 2.

1. **The rule.** `internal/model`: the module decision of decisions 1 to 7
   (factory path or not, wait for the source tag, ask the proxy or not, the
   state, reason and message for each answer, the old-status recheck), the
   proxy's escaped-path request URL, with table tests for every reason and
   for the issue's case (source tag missing, `Published` False
   `Unauthorized`: `WaitsForRelease` naming it).
   Done when `make check` passes with those tests.
2. **The Crew follows it.** The controller asks the proxy as step 1 says
   (an `--go-proxy` flag and Helm value, `off` honoured), reads the GitHub
   target's visibility where it already reads the repository, records
   `checkedTime`, sets the condition, the new states (CRD enum) and Events,
   and design.md's text is checked against it. envtest with a fake proxy:
   the issue's case is `WaitsForRelease` and turns True after the source
   tag and a 200; a 404 is `NotFetchable` with one Warning Event; a
   `theos.local/` path is True on upload as before; an old `Published`
   status with no `checkedTime` is checked again.
   Done when `make check` passes with those specs.
3. **People see it.** The UI's *Needs you*, the Crew page's module line and
   the Brief (`status.apps[].lastRelease` module state and message, its
   card) as decision 8 says, with UI tests and a demo fixture of the
   issue's case. Done when `make check` passes and a screenshot of the demo
   UI's *Needs you* item is in `docs/screenshots/`.

What a human run of Kind would check, after step 2: a release of an app
whose module path names its GitHub folder, with a refused token, stays
`WaitsForRelease`; after fixing the token it turns True once
`go list -m <path>@<version>` works from a laptop.
