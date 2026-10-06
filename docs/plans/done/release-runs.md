# The Crew watches the release run

**Goal:** When the Crew tags a release (#78), it watches the release workflow runs
on that tag and reports how they went; a failure is a Warning Event, a log line
and `Failed` in the Crew's `status.lastRelease`. Today a person checks that the
release run attached its files. GitHub issue #116, filed as a follow-up of
`docs/plans/done/release-tags.md`.

## The rule

- The **release runs** are the CI workflow runs a push of the tag started: in
  Gitea, the repository's runs whose `path` ends in `@refs/tags/<tag>`, at the
  tag's commit.
- `status.lastRelease.run` sums them up: `Waiting` until one starts, `Running`
  while one hasn't ended, `Failed` as soon as one ended other than `success` or
  `skipped`, `Succeeded` once all ended well, counting only each workflow's
  newest run (a tag pushed again at the same commit runs it again).
  `status.lastRelease.runURL` links to the failed run, else to the newest one.
- The Crew watches `status.lastRelease` each poll while `spec.tagReleases` is
  on, until its runs `Succeeded`. A `Failed` release keeps being watched, so a
  run a person re-runs and fixes turns it `Succeeded`.
- **Reporting.** On becoming `Failed`: a Warning Event `ReleaseRunFailed` and
  the log line `Release run failed` with the tag, workflow, conclusion and URL.
  On becoming `Succeeded`: a Normal Event `ReleaseRunSucceeded` and its log
  line. Each once per change, remembered like the held-release Warning since
  the cached Crew can lag behind its status.
- An error reading the runs (Actions off, an old Gitea) is logged once per
  error, not each poll.
- **Known limit.** Only the last release is watched: a release tagged before
  the previous one's runs ended leaves those runs unwatched. A run that never
  starts (no runner for its labels) stays `Waiting` or `Running`, unreported.

## Steps (one pull request)

1. This plan.
2. `internal/model`: `ReleaseRun`, `ReleaseRunResult`; table tests.
3. `internal/gitprovider`: `WorkflowRun`, `TagRuns` (`GET
   /repos/{owner}/{repo}/actions/runs?event=push&head_sha=<commit>`, filtered
   by path); test with a fake Gitea.
4. API and controller: `CrewRelease.Run`, `CrewRelease.RunURL`; the Crew
   controller watches the last release before it tags the next. Envtest for a
   failing then fixed run. `make manifests generate`.
5. `docs/design.md` Crew section; `make check`; on the Kind cluster, a tagged
   release whose run fails shows up in the Crew. Evidence in `docs/screenshots/`.

## Done when

- `make check` passes.
- In the Kind cluster, a release run that fails shows as a Warning Event, the
  log line and `run: Failed` in the Crew's status; saved in `docs/screenshots/`.
