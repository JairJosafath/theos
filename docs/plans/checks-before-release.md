# Plan: an app can run its full check only when a release is prepared

GitHub issue #879 (reported by gray-lenovo). Agent: otter-879.

## Goal

An app's owner chooses, per Container, when CI runs the app's full check:

- `EveryChange` (the default, what happens today): every pull request and
  every push to main runs `make check`.
- `BeforeRelease`: each change runs a light check, and the full check runs on
  the exact commit a release will be tagged at, before the tag. The agent
  already runs `make check` in its workspace before it pushes; CI no longer
  repeats the slow part of it on every change, and nothing is tagged without
  the full check passing.

The choice is a field the owner can change at any time, not something each
app builds into its own CI. Changing it reaches the app's repository through
the operator's own pull request, the way a Standard's fixes do.

Done when the five steps below have landed, each with its own "Done when".
`make check` passes on each.

## What happened

cel-lens-core runs its full CI check on every change: 3 to 18 minutes, plus
up to 19 minutes waiting for the runner its sibling app shares. A change to
only CHANGELOG.md ("Prepare v0.14.0", cel-lens-core #358) took 18 minutes.
The agent ran `make check` in its workspace before it pushed, so the change
was checked twice, and the slowest part runs on every change though it
matters most right before a release. The owner can only rearrange the CI by
prompt, change by change, since `.gitea/` is a protected path the Standard
owns.

## Decisions

- **The setting lives on the Container,** `spec.checks`, enum `EveryChange` |
  `BeforeRelease`, default `EveryChange`, mutable. The CI workflow is a file of
  the Container's repository, written by its Standard, so the Container is
  where it is chosen; the issue asks for a choice per app. Not the Standard:
  two apps on one Standard choose differently. Not the Crew: a Container with
  no Crew, whose releases a person tags, still chooses its CI.
- **A Standard says whether it can do it,** `StandardSpec.checksBeforeRelease:
  true`: its templates render a light check per change when asked and run the
  full check on a release-check branch (below). A Standard that doesn't say
  so keeps the full check on every change whatever the Container asks, and
  the Container's condition `ChecksApplied` is False, reason
  `StandardRunsFullChecks`, naming the Standard. Otherwise a Container could
  ask for `BeforeRelease`, have a Standard that ignores it, and its releases
  would wait for a full check that never runs.
- **What reaches the templates** is `scaffold.Input.Checks`: `BeforeRelease`
  only when the Container asks for it and its Standard can do it, else
  `EveryChange`. One pure function in `internal/model` decides it, and the
  scaffold, the Standard update and the Crew all call it.
- **Full is `make check`; light is `make check-light`.** `make check` keeps
  its meaning everywhere, so the agent's workspace check, the full CI check
  and the docs agree. Each Standard's Makefile adds `check-light`: what is
  quick and catches a broken merge (the build, vet or type check, lint), no
  test suites, no build for every platform. A Standard whose check is
  already quick may make `check-light` depend on `check`. The changelog, docs
  and images rule (only those changed: the job passes without running) stays
  in both modes.
- **The workflow file is a template.** Each Standard's
  `.gitea/workflows/check.yaml` becomes `check.yaml.tmpl`. With `EveryChange`
  it renders as today, byte for byte, so a Standard bump changes nothing for
  an app that didn't choose. With `BeforeRelease` its `check` job runs
  `make check-light` on pull requests and main, and a second job, `full`,
  runs `make check` on a push to a branch `release-check/**` only. Its status
  context is `check / full (push)`; branch protection keeps requiring
  `check*`, which the light job satisfies on pull requests.
- **The full check runs on the commit the tag will name.** When a Crew with
  `tagReleases` finds a release done and its Container's checks are
  `BeforeRelease`, it does not tag yet: it creates the branch
  `release-check/vX.Y.Z` at the commit it would tag (the gate is idempotent:
  an existing branch at that commit is reused), and reads the runs at that
  commit (`CommitRuns`, as for a Task). The `full` job succeeding tags the
  release as today and deletes the branch. Running, the Crew's `Released`
  condition says `FullCheckRunning` with the run's URL. Failing, it does not
  tag: `Released` False, reason `FullCheckFailed`, a Warning Event once, and
  the Crew files `Full check fails for vX.Y.Z` in the release's milestone,
  `kind/bug` and `status/ready`, with the failed job's URL and a marker so it
  is filed once per commit. That issue reopens the release; its merge is the
  release's newest, and the next time the release is done the gate runs on
  that commit. No run within the hour a release's runs get (no runner, or a
  Standard that renders no `full` job) leaves the release to a person:
  `NoFullCheck`.
- **A fix of the full check doesn't count as merged after the prepare
  issue.** Work merged after the prepare issue leaves a release to a person
  (`MergedAfterPrepare`), since its changelog doesn't list it. The issue the
  gate files is the release's own: its merge doesn't count, the changelog
  stays the prepare issue's. Any other merge still does.
- **A person tags without the Crew.** Without `tagReleases`, nobody waits:
  the Container's docs (the Standard's `docs/agents/` and the Crew notes)
  say to push `release-check/vX.Y.Z` at the commit and wait for `full`
  before tagging. The operator can't stop a person's tag; the setting only
  removes the full check from each change.
- **Changing the setting reaches existing apps** through the Standard update
  (`internal/controller/container_standard.go`). Today it re-renders when the
  Standard's `ref` differs from `status.standard.ref`; it also re-renders when
  the effective checks differ from `status.standard.checks` (new, empty read
  as `EveryChange`). The plan is the same three-way one: the old render
  (old ref, old checks) against the new (new ref, new checks), files the
  repository changed since left out and named. A Container whose
  `check.yaml` was changed by hand gets `StandardUpdated` False naming it, and
  its checks stay as they are until a person updates it; the Crew's gate
  reads `status.standard.checks`, what the repository runs, not the spec, so
  it never waits for a `full` job the repository doesn't have.
- **A new Container** is scaffolded with its effective checks and records them
  in `status.standard.checks` and in `.c4factory.yaml`, like the refs.
- **No Kind evidence.** Everything above is seen by `internal/model` and
  `internal/scaffold` table tests, envtest with a faked Gitea, and
  `make test-gitea` for the new Gitea call. Whether a real Gitea runner picks
  up a `release-check/**` push and reports `check / full (push)` is what a
  human run of Kind would check; the step's pull request says so.

## Steps

Each step is its own issue, filed `status/proposed`, naming #879. Steps 2 to 5
depend on step 1; the setting does nothing visible until steps 2 and 3 have
landed, and nothing waits on a full check until step 4.

1. **The setting and who decides it.** `ContainerSpec.checks` (enum, default
   `EveryChange`, mutable), `StandardSpec.checksBeforeRelease`,
   `ContainerStandardStatus.checks`, `scaffold.Input.Checks`, the
   `internal/model` function that decides the effective checks, and the
   Container's `ChecksApplied` condition (`StandardRunsFullChecks`). Samples
   and CRDs regenerated; `docs/design.md` already says it.
   Done when: model table tests cover the four combinations (asked or not,
   Standard can or can't); envtest shows the default, a change of the field
   accepted, an unknown value refused, and `ChecksApplied` False on a
   Standard that can't; `make check` passes.
2. **The Standards render a light and a full check.** go-standard,
   go-operator-standard, ts-standard and python-standard: `check.yaml.tmpl`
   and `make check-light`, their catalog entries `checksBeforeRelease: true`,
   new tags pinned in `templates/versions.txt`. hello-standard and
   hello-html-standard stay as they are (stand-ins).
   Done when: `internal/scaffold` tests render each Standard with
   `EveryChange` byte for byte as before and with `BeforeRelease` to a
   `check` job running `make check-light` and a `full` job on
   `release-check/**` running `make check`; `make validate-yaml` and
   `make check` pass.
3. **A change of the setting reaches the repository.** The Standard update
   re-renders when the effective checks differ from `status.standard.checks`,
   and a new Container records them at scaffold.
   Done when: `internal/scaffold` tests plan an update between checks modes
   at one ref, with a hand-changed `check.yaml` left out; envtest shows the
   operator's pull request after the field changes, `status.standard.checks`
   set once it merges, and nothing opened when only an unsupporting
   Standard's Container changes; `make check` passes.
4. **The Crew runs the full check before it tags.** The gitprovider call to
   create a branch at a commit; the gate in `crew_release.go` with
   `FullCheckRunning`, `FullCheckFailed` (and its issue), `NoFullCheck`; the
   gate's issue not counting as `MergedAfterPrepare`.
   Done when: model table tests decide tag, wait, file or leave to a person
   from faked runs; envtest shows no tag while `full` runs, the tag after it
   succeeds and the branch deleted, one issue filed on failure, and a release
   of an `EveryChange` Container tagged as today; `make test-gitea` creates
   and deletes the branch at a commit; `make check` passes.
5. **The owner sees and sets it.** The UI's Container page shows the checks
   and lets the owner change them, with `ChecksApplied` when False; the
   Standards' `docs/agents/` notes and `docs/getting-started.md` say what the
   choice does and how a person tags with `BeforeRelease`.
   Done when: the UI's tests show the setting read and written and the
   condition shown; `make check` passes.
