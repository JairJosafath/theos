# CI on the pull request is the merge gate

Superseded 2026-10-19 by [fresh-start.md](../fresh-start.md): abandoned; main stays the only branch.

Parent issue: #1379. Agent: otter-1379. Written 2026-10-16.

## Goal

Merging a pull request waits on nothing: no local
`make check` in `land`, no merge turn, no CI run. The heavy checks run once,
on a batch from `dev` to `main`.

## What was found

- A pull request green on GitHub still waits 20 to 60 minutes or more for
  `land`. The optimistic `land` (`velocity.md`, decision 2) runs a full
  `make check` on the merged tree before it queues. On 3st8JJ that check
  first waits for a heavy-job slot at load 20 to 44 on 8 cores. Then `land`
  waits for the one merge turn, and checks again under the turn when main
  moved, as it does most of the time with six workers.
- "Green" did not mean tested. On a pull request only Lint, govulncheck,
  the shell tests of `hack/` and GitGuardian run. `test.yml`, with the unit
  and envtest suites, runs only on main after the merge. The owner merges
  green pull requests by hand to go faster, so those merges skip the Go tests.
- Each pull request paid for at least two full runs on the devices:
  `land`'s check and main's `test.yml`, plus a recheck for each move of
  main. The runners are self-hosted on the same devices (`RUNS_ON`), so all
  of them share the same CPUs.

## Decisions

Revised 2026-10-16 by the owner: waiting on a pull request's CI is still too
slow. Merges go optimistic onto a `dev` branch, and only the batch from `dev`
to `main` pays for the heavy checks. A red `main` gets fixed after the fact;
the owner takes that risk for speed.

1. **Work merges into `dev`, at once.** `take` branches from `origin/dev`,
   pull requests target `dev`, and `land` merges `origin/dev` in, pushes and
   merges with a merge commit. It runs no `make check`, takes no turn and
   waits on no CI. The bug-fix-needs-a-test, review and closes refusals stay.
2. **Workers run no heavy checks.** No `make check`, `make test`, `make lint`
   or `vulncheck` in a worker session; the targeted tests of what the change
   touches (`go test ./internal/<pkg>/...`, `make test-hack`, the UI's tests)
   are still the author's to run. The heavy-job slots stay for humans and CI.
3. **`dev` gets one light check.** A `Build` workflow (`go build ./...`) runs
   on each push to `dev`. Nobody waits on it; a red one is a `priority/1`
   issue, because every new branch starts from `dev`. GitGuardian stays on
   every pull request; it runs on GitHub, not on the devices.
4. **A batch from `dev` to `main` is the gate.** `github.sh promote` opens or
   refreshes the one pull request `dev` → `main` when `dev` is ahead, and
   merges it (merge commit, never squash) when the required checks of its
   head SHA are green. The maintainer session runs it each cycle; a human may
   run it too. The heavy checks (Lint, Tests, UI, Vulnerabilities, Shell
   tests: #1380's list in `.github/required-checks`) run only on pull
   requests to `main` and, as today, on `main` after the merge. They do not
   run on pull requests to `dev`, so nothing runs twice before `main`.
5. **A red batch is fixed on `dev`.** When the promotion's checks fail,
   `promote` files one `priority/1` issue naming the failed check and its run
   (or comments on the open one), and the batch waits. The fix lands on `dev`
   like any other work and the next `promote` picks it up. A red `main` is
   handled by `github.sh red` as today.
6. **Releases stay on `main`.** Tags are cut from a green `main`; `dev` is
   never tagged. `hack/pull.sh` fast-forwards local `dev` as it does `main`.
7. **Owner acts.** Make `dev` the default branch, so a hand-opened pull
   request targets it, and require #1380's checks on `main` in a ruleset.
   Both are repository settings.

This supersedes decision 2 of [velocity.md](velocity.md) and this plan's
first version (`land` waiting on the pull request's CI).

## Steps

| Step | Issue | How we know |
| --- | --- | --- |
| This plan | #1379 | merged |
| Heavy checks run on pull requests to `main`, stable job names | #1380 | `make test-hack` (`check-workflows.sh`); the promotion pull request shows a Tests run |
| `dev` branch; `take`, `land` and `pull.sh` work against it; `land` merges at once | #1381 | `make test-hack`: land merges into dev without make, a turn or a CI wait; take branches from dev |
| `promote` and the `Build` workflow on `dev` | #1388 | `make test-hack`: promote opens, refreshes, merges green, files one priority/1 on red |
| Docs: no worker runs `make check` | #1389 | no doc tells an agent to run `make check` or that `land` runs it |

## Done when

Every step is closed; a worker's pull request merges into `dev` within a
minute of `land`; `main` moves by promotion only. Record the median time from
a pull request's last push to its merge into `dev` over ten landings, before
(1 to 4 hours on 2026-10-16, `velocity.md`) and after, here.
