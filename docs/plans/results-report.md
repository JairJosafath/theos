# The factory's numbers, published with every release

GitHub issue #66, a step of [open-source-launch.md](open-source-launch.md)
(#46). Written 2026-11-10 in a session the owner ran.

## Goal

A reader can check that the factory works without taking our word: each
release links one page, and one JSON document, of what the factory's Tasks
recorded since the release before. Until now the numbers were in one
cluster's UI, and `docs/results.md` holds screenshots from October.

## Where we stand

- The UI computes the numbers from Tasks: `factoryMetrics` in
  `ui/src/model/metrics.ts` (merged, the share with no person's commit, the
  share a person approved, medians to the pull request, the first review and
  the merge, cost by size and by harness and model), `scorerRates` in
  `scores.ts`, and the stages of a timeline in `factory.ts`.
- The operator computes Prometheus series from the same Tasks in Go:
  `model.FactoryMetrics` over `model.TaskMetricFacts` (#59). Those are sums
  and histograms for a scrape, not medians over a period.
- The issue asks for "the functions the UI already uses, moved to where both
  can call them". They can't be moved: the UI's are TypeScript in a browser,
  and the command is Go.

## Decisions, and why

1. **The rules are written again in Go, and one fixture holds the two
   together.** `internal/model/report.go` has the rules, building on
   `TaskMetricFacts`. `ui/src/model/testdata/report/` has `tasks.json`, Tasks
   as the API serves them, and `report.json`, the report they give.
   `internal/model`'s test renders the report from the Tasks and compares it
   byte for byte; `ui/src/model/report.test.ts` gives the same Tasks to the
   UI's own functions and compares their numbers with the same `report.json`.
   A rule changed on one side alone fails the other side's test. A third
   language both could run (CEL, WebAssembly) would be more machinery than
   the dozen rules are worth.
2. **The fixture lives under `ui/`, in a `testdata` directory.** The UI's
   check runs only when a file under `ui/` changed (`hack/check-cache.sh`,
   and the UI workflow's paths), and `make test` tests every Go package when
   a file under any `testdata/` changed (`hack/test-packages.sh`). So that
   one place makes both sides run when the fixture or the report changes.
   Under `internal/model/testdata/` a Go rule could change, with its report,
   and the UI's check would be skipped as unchanged.
3. **What the UI has no number for is Go's alone,** and table-tested there:
   the 90th percentile, the failed Tasks by reason, the Tasks that ended with
   no change, the rows by Agent. The UI's test checks every number the UI
   does have: the counts, the three shares, the lead medians, the stage of
   every step and each stage's median, cost over all, by size and by
   configuration, and each Scorer's verdicts.
4. **A period is after its beginning, up to and including its end.** A
   release's report runs from the release before, so two in a row count no
   Task twice. The UI's window is closed at both ends; it slides with the
   clock and has no neighbour, and the two differ only for a Task that
   finished on the first second.
5. **A Task counts by when it finished** (`status.completedAt`). Merged is a
   Task in phase Merged with a pull request, as in the UI; a Task whose agent
   found nothing to do (#72) is counted apart, as in the Prometheus series. A
   Scorer's judge runs and a Benchmark's runs are no failed Tasks, as in the
   UI's totals.
6. **Time per stage is over the Tasks that were in the stage.** Most Tasks
   are never blocked and never wait for a person; counting their zeros would
   make every such median zero. Each row says how many Tasks it is over.
7. **The 90th percentile is by nearest rank:** the smallest value that nine
   in ten are at or under. It is always one Task's own time, so a reader can
   go and find the Task; an interpolated value belongs to none.
8. **Failed Tasks are counted by reason, not listed with their message.**
   The reason is the one the Task's timeline recorded (`NoCIResult`,
   `BaseRed`, or `Failed` when it gave none). A message can quote a log, and
   the page is published.
9. **What the numbers don't show is said on every page, and in the JSON:**
   what people decided, why a Task failed, what the failures cost, what a
   Task no longer records (a deleted Task, a timeline past its 64 steps), and
   that it is one project. A number quoted from either carries it along.
10. **Reports live in `docs/results/<name>.md` and `<name>.json`,** where
    name is the release's tag (`model.ReportPaths`). The page links its JSON
    by file name, so the two move together. Nothing about the path is a
    setting: it is this repository's own page, not something a user runs.

## Steps

1. **The rules and the fixture** (this change). `model.FactoryReport` turns
   a period and Tasks into a `Report`; `Report.JSON` and `Report.Markdown`
   write it. Done when the table tests pass, the fixture gives its report
   and page byte for byte, and the UI's test agrees with the report.
2. **`theos-session report`.** Reads the Tasks of the cluster the session
   talks to, maps each to `model.ReportTask`, and writes the two files of
   `model.ReportPaths`. Its mapping is proven on the same fixture: decode
   `tasks.json` into the typed TaskList, and the report must be
   `report.json`. `hack/release-notes.sh` links the period's page when the
   file exists. `docs/design.md`, "Measuring the factory", gets its
   paragraph with the command.
3. **The first report,** from the owner's cluster, run by a person.
4. **README's first screen** carries three numbers with the date: Tasks
   merged, the share merged with no person's commit, and the median cost of
   a merged Task. They are `merged`, `noPersonCommit` and `cost` in the JSON.

## How we know it's done

- `go test ./internal/model -run 'Report|MedianAndPercentile'` and
  `make ui-test` pass; changing a rule on either side fails one of them
  (tried: the UI's size bound and its slot stage, each caught).
- After a rule changes on purpose:
  `go test ./internal/model -run TestFactoryReportFixture -update`, then
  `make ui-test`.
