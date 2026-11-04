# Self-hosted CI: N GitHub Actions runners on a device (#395)

**Goal.** `make gh-runner RUNNERS=N` starts N self-hosted runners on a device's
Docker, every workflow job runs on them while `RUNS_ON` is `self-hosted`, and
the jobs that make a Kind cluster run one at a time per device.

## Decisions

- **Names and volumes.** Runner *i* is container `theos-gh-runner-i`,
  `theos-<hostname>-i` on GitHub, registration volume
  `theos-gh-runner-i-config`, tool cache `theos-gh-runner-i-toolcache`,
  work directory `work/i`. `up` finds the runners a device has by their
  containers and registration volumes, and removes those above N.
- **Shared and own caches.** The Go module and build caches, golangci-lint's
  and npm's are one shared volume: each tool locks what it writes. The tool
  cache is per runner: setup-go and setup-node unpack into it without a lock.
- **The old single runner** (container `theos-gh-runner`, name
  `theos-<hostname>`) is removed and registered again as runner 1: it
  needs the new name and the Kind label, both of which take a new
  registration, and its registration volume's name doesn't fit the scheme.
- **One Kind job per device: a label.** Only runner 1 carries
  `theos-kind`; *Test Chart*, *E2E Tests* and *Verify Release*'s Kind job
  ask for it when `RUNS_ON` is `self-hosted`. A `concurrency` group spans the
  repository, not a device, and cancels all but the newest pending run;
  per-runner cluster names would still put two clusters in one device's
  memory. A runner 1 registered without the label registers again on `up`.
- **The check** is `hack/check-workflows.sh`, run by its test in
  `make test-hack` (so `make check`): every job's `runs-on` uses
  `vars.RUNS_ON`, and a job with `kind create cluster` or the e2e target asks
  for `theos-kind`.
- **The image** already has what the workflows use beyond `actions/*`
  (gh, jq, yq, kubectl, kind, helm, docker, sudo, build tools); nothing added.

## Done when

- `hack/gh-runner/gh-runner_test.sh` (fake docker, gh, stat): `RUNNERS=3` up
  makes three named containers and registration volumes; `RUNNERS=1` after 3
  removes two; `down` removes all; the old runner migrates.
- `hack/check-workflows_test.sh`: the repository's workflows pass; a hosted
  `runs-on`, a missing one and a Kind job without the label fail.
- `make check` passes.

A human run on a device checks what the fakes can't: three runners online on
GitHub, two jobs running at once, and a second Kind job waiting for runner 1.
