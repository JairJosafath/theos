# Development speed first

Parent issue: #1279. Written 2026-10-16, the day the owner made the speed of
development itself the first priority: an issue that makes the factory's
development faster is worked before every other issue, because it pays on
every issue after it.

## What was found

At 09:20 UTC on 2026-10-16 the tracker held 101 open issues besides the
pinned report: 71 ready, 11 in progress, 11 proposed, 8 plan parents. The
relay, at 6 workers on two devices, closed 2 to 5 issues an hour; agents
filed 3 to 14 follow-ups an hour. On the device 3st8JJ a real session ran
68 minutes at the median and 140 at p90 (26 sessions in 24 hours), of which
the model's own time was 4 minutes: 93 percent of a session waits on tools,
nearly all of it `make check` and `land`. Pull requests waited one to four
hours between opening and merging. A 7.5-hour login outage idled four
workers the same night.

Where the hours go, heaviest first:

1. **`land` serializes the whole project.** `gh_land_turn` takes the merge
   turn, one across every device, merges `origin/main`, and only then runs
   `make check`, holding the turn while the check runs. In the 24 hours to
   11:35 on 2026-10-16, 51 pull requests landed; the check under the turn
   took 14.9 minutes at the median and 17.7 at p90, 11.3 hours of the 24 in
   all, and 11 branches needed a second or third merge-in because main moved
   during their check, 14 extra checks, 3.5 more hours of turn. Merges ran
   back to back at one per check: the turn is the project's ceiling, about
   96 a day at best, whatever the worker count. The check is needed so that
   main stays green; holding the turn during it is not.
2. **Every check pays the full price, twice.** `make test` passes
   `-coverprofile`, which is not among Go's cacheable test flags, so Go's
   test cache never answers: a pull request that changes only `hack/`, a
   template or a doc still runs the controller envtest suite (286 specs one
   after another, about 730 s of a 15-minute check), and the UI's eslint and
   `kubectl-validate` run too. Each issue runs the check at least twice,
   once on the branch and once under `land`; a `make check` took 15 minutes
   at the median and 52 at p90, because the device is CPU-bound: load 20 on
   8 cores with two checks running, and that load is what makes the
   "under load" flakes.
3. **Follow-ups lose their finder.** A second problem an agent finds is filed
   `status/proposed`, waits for triage (every 30 minutes, then a triage
   session) and for any free worker, who starts from nothing. The agent that
   found it, diff in hand, could have fixed it in minutes.
4. **Flakes cost a full check each.** The controller spec of #1202 and the
   shell tests of #1192 and #1208 fail under load on CI and in `land`; every
   failure is a rerun of the whole check, often under the merge turn.

The mechanics otherwise hold: `pick` and `take` with the claim heartbeat, the
handoff comment, the plan parent, the watch's admission gate. Nothing below
changes how they work.

## Decisions

1. **`priority/velocity`, picked after urgent and before the rest.** It
   marks an issue that makes development itself faster: the relay, `land`,
   `make check`, flakes, the device, the scripts and skills agents work with.
   `priority/urgent` keeps its one meaning, something is broken now, and
   stays first: a red main or a dead relay blocks the velocity work too. The
   label was created on GitHub and put on the 21 open issues it fits before
   the pick order changed, so the order applies the moment #1280 lands.
   Triage gives it to a proposed issue of that kind.
2. **`land` is optimistic.** The check runs on the merged tree before the
   turn is queued for; the turn is taken to confirm `origin/main` is still
   the SHA that was merged, and the merge follows at once. Only when main
   moved does a second check run under the turn, as today, at most twice.
   The turn is held for seconds in the common case, and checks run in
   parallel across workers and devices, bounded by `HEAVY_JOBS` instead of
   by one turn. With that, **`land`'s check is the check**: an agent runs
   the targeted tests while it works and then lands; it runs `make check`
   by hand only when `land` failed. One full check per pull request instead
   of two or three.
3. **Coverage only when asked.** `make test` runs `go test` with cacheable
   flags; `COVER=1` (CI) adds the profile. Go tracks the files and
   environment a test reads, so a changed CRD or `KUBEBUILDER_ASSETS` still
   reruns the suite; an untouched package answers from the cache. The
   check-cache stays for the whole-tree case.
4. **The envtest suite runs in parallel procs,** as many as the device's
   memory carries (default 2, `C4_TEST_PROCS`), each with its own control
   plane. Memory, not CPU, is the device's limit, so the number is measured
   and written down next to the `HEAVY_JOBS` numbers, not guessed.
5. **A follow-up stays with its finder when it is decided.** Three cases,
   one rule in AGENTS.md: under about 30 lines in a file the pull request
   already changes, fix it there and name it under `Also fixes:`; a bug
   whose failing test you can name, or a chore or doc whose Done-when you
   can state, file with `create --next`, which readies it and reserves it
   for this worker's next fresh session (a claim other pickers skip for one
   lease); anything that needs a decision stays `proposed`. One issue per
   session still holds: the next session is the small-context session that
   does the small work. Triage runs every 10 minutes instead of 30 for the
   rest.
6. **Each tier of `make check` is cached on its own inputs.** The UI check,
   the shell tests and `validate-yaml` run on every check, CPU-heavy, when
   nothing under `ui/`, `hack/` or the manifests changed. `check-cache.sh`
   records a pass per tier keyed on that tier's tree, as Go's cache does per
   package.
7. **The worker count stays.** Six workers are already bounded by the
   device's CPU and by the two heavy-job slots per device; the gain is in
   what each session and each landing costs, not in more of them. Count
   again once the steps are in.

Not done, and why: a cheaper model for small issues (quality on this code
base is not measured per model; revisit when the velocity set is drained);
skipping the land check when CI is green (CI tests the pull request's merge
ref at an earlier main; the optimistic land gets the same parallelism
without trusting a stale merge).

## Steps

| Step | Issue | How we know |
| --- | --- | --- |
| This plan, the label in `GH_LABELS`, the pick order, the docs, the watch's flake filing | #1280 | `make test-hack`: urgent beats velocity, velocity beats older ready; no doc says "in-progress first" |
| Optimistic `land` | #1281 | `make test-hack`: no second check when main did not move; the turn-held time in `.relay/land-*.log` |
| Coverage only when asked | #1282 | `touch hack/lib/github.sh; make check` shows `internal/controller` `(cached)` |
| The envtest suite in parallel procs | #1283 | the suite's wall time and peak RSS at 1, 2, 4 procs in `docs/dev/testenv.md` |
| Follow-ups the finder works, triage every 10 min | #1284 | `make test-hack`: `--next` readies, reserves, is skipped by others, expires |
| Each check tier cached on its own inputs | #1285 | `touch hack/x.sh; make check` skips the UI check and `validate-yaml` |

## Measurements to record

Each step's pull request writes its numbers here, same device, before and
after: the median turn-held time of ten landings; the wall time of a
`hack/`-only `make check`; the controller suite's time at the chosen procs;
issues closed per hour over a day, from `gh issue list --search closed:`.

| Measure | 2026-10-16 before | After |
| --- | --- | --- |
| Landings a day, whole project | 51 | |
| Turn held per landing | 14.9 min median, 17.7 p90 | |
| Checks retried because main moved, a day | 14 | |
| `make check`, any change | 15 min median, 52 p90 | |
| `make check`, `hack/`-only change, warm device | the same | |
| `internal/controller` suite | about 730 s; 834 s at 1 proc on gray-lenovo, 2026-10-16 | 484 s at 2 procs (the default), 242 s at 4, same device and day (#1283) |
| Session length, 3st8JJ | 68 min median, 140 p90 | |
| Issues closed an hour, 6 workers | 2 to 5 | |

## Done when

Every step is closed; a velocity issue filed while the relay runs is the
next issue a worker starts; the turn is held for seconds when main did not
move; a `hack/`-only check finishes in minutes on a warm device; the table
above has its After column; the open-issue count falls day over day.
