# CI on the pull request is the merge gate

Parent issue: #1379. Agent: otter-1379. Written 2026-10-16.

## Goal

Merging a pull request waits on its own CI run, and on nothing else: no local
`make check` in `land`, no merge turn shared across devices. A green pull
request merges within a minute of its last run finishing.

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

1. **The pull request's CI is the check.** `test.yml` and `ui.yml` run on
   pull requests with their push path filters, one cancel-in-progress group
   per pull request. The jobs get distinct names, listed in one place that
   `land` reads.
2. **`land` waits on CI, then merges.** It merges `origin/main` in, pushes,
   waits for the required checks on that head SHA and merges. It runs no
   local check and takes no turn. A red check fails it, and a hand merge
   finishes it (#1369).
3. **Main may move between the run and the merge.** Two pull requests green
   alone can make main red together. This needs two small pull requests that
   conflict in meaning but not in text, which is rare. The run on main after
   every merge and `github.sh red` (a red main is `priority/1`) catch it
   within one run. Today every pull request pays about an hour to rule it
   out. GitHub's merge queue would close this gap, but it isn't available on
   a personal repository.
4. **Device CPU drops.** Per pull request there is one CI run plus main's
   run, and no local check or recheck. The runs are spread over the runners
   of every device, not pinned to the worker's device.
5. **Owner acts.** A main ruleset requiring the pull-request checks makes a
   hand merge wait for them too. The repository's auto-merge setting lets a
   later step have `land` arm GitHub's auto-merge and exit at once, instead
   of waiting. Both are repository settings, so they are the owner's to set.

This supersedes decision 2 of [velocity.md](velocity.md) and its "Not done:
skipping the land check when CI is green". That note's reason, a merge ref
tested at an earlier main, is accepted as the cost in decision 3.

## Steps

| Step | Issue | How we know |
| --- | --- | --- |
| This plan | #1379 | merged |
| Tests and UI run on pull requests, stable job names | #1380 | `make test-hack` (`check-workflows.sh`); the step's own pull request shows a Tests run |
| `land` waits on the pull request's CI, no check, no turn | #1381 | `make test-hack`: green merges without make or a turn; red names the check; a stale SHA isn't counted; a hand merge exits 0 |

## Done when

Both steps are closed; a green pull request merges within a minute of its
last run; no doc says `land` runs `make check`. Record the median time from
a pull request's last push to its merge over ten landings, before (1 to 4
hours on 2026-10-16, `velocity.md`) and after, here.
