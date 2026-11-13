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
   A rule changed on one side alone fails the other side's test, as far as
   the fixture has a Task the rule decides for: "What the fixture holds",
   below, lists them. A third language both could run (CEL, WebAssembly)
   would be more machinery than the dozen rules are worth.
2. **The fixture lives under `ui/`, in a `testdata` directory.** The UI's
   check runs only when a file under `ui/` changed (`hack/check-cache.sh`,
   and the UI workflow's paths), and `make test` tests every Go package when
   a file under any `testdata/` changed (`hack/test-packages.sh`). So that
   one place makes both sides run when the fixture or the report changes.
   Under `internal/model/testdata/` a Go rule could change, with its report,
   and the UI's check would be skipped as unchanged. The *Tests* workflow
   leaves `ui/` out of its paths, so it takes `ui/src/model/testdata/` back
   in: without that, a pull request that changed a UI rule and wrote
   `report.json` to match ran the UI's test and not Go's, and merged with the
   two apart. `hack/check-workflows.sh` holds the workflow to it.
3. **What the UI has no number for is Go's alone,** and table-tested there
   (`internal/model/report_test.go`): the 90th percentile
   (`TestMedianAndPercentile`, and each row's in `TestFactoryReportTimes`),
   how many Tasks a lead time is over, the failed Tasks by reason and the
   Tasks that ended with no change
   (`TestFactoryReportCountsWhatFinishedInThePeriod`), the count beside each
   share (`TestFactoryReportShares`), how many Tasks reported a cost, over
   all, in a size and in a row, and the rows by Agent
   (`TestFactoryReportCost`), a Scorer's generation and each criterion's
   verdicts over all configurations (`TestFactoryReportScorers`), and the
   merged Tasks by name (`TestFactoryReportNamesTheMergedTasks`). The page
   is Go's alone too: its durations, percentages, escapes and names
   (`TestReportPage`, `TestReportPathsRefuseANameThatIsNoName`). The UI's
   test checks every number the UI does have: the counts, the three shares,
   the lead medians, the stage of every step and each stage's median, cost
   over all, by size and by configuration, each Scorer's verdicts, and how a
   cost is written.
4. **A period is after its beginning, up to and including its end.** A
   release's report runs from the release before, so two in a row count no
   Task twice. The UI's window is closed at both ends; it slides with the
   clock and has no neighbour, and the two differ only for a Task that
   finished on the first second. The fixture has one, `edge-26`: the UI's
   test gives the panel's rules every other Task, and says of this one that
   the panel counts it and the report doesn't.
5. **A Task counts by when it finished** (`status.completedAt`). Merged is a
   Task in phase Merged with a pull request, as in the UI; a Task whose agent
   found nothing to do (#72) is counted apart, as in the Prometheus series. A
   Scorer's judge runs and a Benchmark's runs are no failed Tasks, as in the
   UI's totals.
6. **Time per stage is over the Tasks that were in the stage,** however
   briefly. Most Tasks are never blocked and never wait for a person;
   counting their zeros would make every such median zero. A Task that left
   a stage in the second it entered it was in it, for zero seconds, and
   counts: of four Tasks that were queued, three for no time, the median is
   0s over 4 Tasks, not 10m over 1. Each row says how many Tasks it is over.
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
10. **Reports live in `<dir>/<name>.md` and `<name>.json`,** where name is
    the release's tag and dir is the repository's to say, `docs/results`
    unless it does (`model.ReportPaths(dir, name)`). Another project's
    factory writes its reports where that project keeps its pages, so the
    directory is the command's setting and not a fixed pick. The page links
    its JSON by file name, so the two move together wherever they lie.
11. **A report's name is a name and nothing else:** letters, digits and
    `. _ + -`, beginning with a letter or a digit, which every release tag
    is. It names two files and goes into the page's heading and its link, so
    `../../README`, `a/b` and an empty name are errors, from `ReportPaths`
    before a file is written and from `Report.Markdown`.
12. **Text from the cluster is printed as text.** A failure's reason, a
    harness and model, an Agent's name, a Scorer's name and its criteria are
    what someone typed into a resource, and the page is published. In a
    table cell or a heading each is one line, with a backslash before `\`,
    `|`, a backtick, `*`, `_`, `~`, `[` and `]`, with `&` and `<` as their
    HTML entities, and with a dollar sign as `<span>$</span>`: GitHub reads
    what two dollar signs enclose as mathematics, and the span is what its
    documentation gives for one outside a formula (a backslash is its
    escape inside one). The backslash comes first so the text's own can't
    undo an escape (`a\|b`). The backtick is escaped because a code span
    shows backslashes as they are; once it is, escaping the emphasis and link
    characters is the same rule, and a criterion that says
    `[see here](https://...)` or `<img src=...>` prints as typed, links
    nowhere and loads nothing. A bare address is left alone: GitHub links
    it, to the address it shows.
13. **Money is written as the UI writes it** (`formatCost`): dollars and the
    nearest cent, half a cent going up, and `<$0.01` for a cost under a
    cent. The nearest cent is to the number as the machine holds it, which
    is what JavaScript's `toFixed` does: $1.125 prints $1.13, and $1.115,
    held a hair under, prints $1.11. Go's `%.2f` rounds an exact half to the
    even cent and printed $1.12 where the UI shows $1.13. `money.json` in
    the fixture has the cases, and both sides' tests read it.
14. **Rows with as many Tasks are in the order of their names' code
    points:** `Opus` before `haiku`, `m-1` before `m_1`. Go's string
    comparison gives it and the UI's `compareText` does. The UI used
    `localeCompare`, the order of the reader's locale, so two readers of one
    cluster could see the rows in different orders, and no report could
    match both. The UI changed; a capital now sorts before a small letter
    there.
15. **The JSON names every merged Task:** `mergedTasks` has each one's
    namespace, Container, name, pull request number and when it merged, in
    the order they merged, and the page says so in a line. That is the
    issue's "anyone can check a number against the pull requests": the
    numbers are over these Tasks, and a pull request names its Task. The
    page doesn't list them, since a release has hundreds. The failed Tasks
    are counted by reason and not named: a failed Task needn't have a pull
    request. A link to each pull request needs its Container's repository,
    which the rules here don't know; the command reads it (step 2).

## Steps

1. **The rules and the fixture** (done). `model.FactoryReport` turns
   a period and Tasks into a `Report`; `Report.JSON` and `Report.Markdown`
   write it. Done when the table tests pass, the fixture gives its report
   and page byte for byte, and the UI's test agrees with the report.
2. **`theos-session report`** (done). Reads the Tasks of the cluster the session
   talks to, maps each to `model.ReportTask`, and writes the two files of
   `model.ReportPaths(dir, name)`: the name is the release's tag, and the
   directory a flag, `docs/results` when it isn't given. It asks for the
   paths first, so a name that is none fails before anything is read or
   written. Its mapping is proven on the same fixture: decode `tasks.json`
   into the typed TaskList, and the report must be `report.json`. It reads
   each Container's repository and adds it to that Container's
   `mergedTasks`, so a reader has the pull request's address and not only
   its number (decision 15). `hack/release-notes.sh` links the period's
   page when the file exists. `docs/design.md`, "Measuring the factory",
   gets its paragraph with the command. "What step 2 found", below, has
   what it settled.
3. **The first report,** from the owner's cluster, run by a person.
4. **README's first screen** carries three numbers with the date: Tasks
   merged, the share merged with no person's commit, and the median cost of
   a merged Task. They are `merged`, `noPersonCommit` and `cost` in the JSON.

## What step 2 found

- **The command is `theos-session report --name <tag>`,** with `--from` and
  `--to` (RFC 3339), `--dir` (`docs/results`), `--namespace` and
  `--kubeconfig`. With `--namespace` it lists the Tasks and the Containers
  of that namespace; without, it makes one list of each over the whole
  cluster, so the person must be allowed to list both in every namespace:
  one who may list them in some namespaces only is refused, and names one
  with `--namespace`. The list of Containers is asked for either way,
  though a merged Task's `repository` may be absent. `--to` is now
  when it isn't given and `--from` the first Task, so the first report needs
  only its name; each later one takes the report before's `to` as its
  `--from`. It reads and never writes to the cluster: it registers no
  Session, so it needs no `.theos.yaml`. A word left after the flags
  (`report v0.5.0`) is refused, with the word.
- **Checked before the cluster is opened, in this order:** the name
  (`model.ReportPaths`), the two times and that the period ends after it
  begins and no later than now, and the directory: it, or the nearest of
  its parents that exists, must be a directory, and no directory may have
  the name of either file. Nothing is made to find that out; the directory
  is made after the cluster was read, and both files are rendered before
  either is written. `TestReportChecksBeforeItReadsOrWrites` holds each to a
  cluster never opened and a folder with nothing new in it.
- **The mapping is the command's, and is the operator's written again.**
  `reportTask` (`cmd/theos-session/report.go`) reads the metric facts as
  `taskMetricFacts` does in `internal/controller`, which a command of a
  devcontainer shouldn't link (the whole operator would come with it).
  `TestReportOfTheFixture` decodes `tasks.json` into `c4v1beta1.TaskList`,
  refusing a field the API's types don't have, runs the command on a fake
  cluster holding it, and compares the two files it wrote with
  `report.json` and `report.md` byte for byte. Tried: a size always read,
  and a person's approval of a held change left out, each failed it.
- **What the fixture can't hold of the mapping is table-tested beside it**
  (`TestReportTask`). The fixture has one Task with a `Succeeded` condition,
  the one that ended with no change, so reading any `Succeeded` condition as
  "no change" passed the fixture; the table has a merged Task whose reason
  is another, one that only `Progressing` says it of, and a failed one.
  `Unreviewed` is read by the Prometheus series and by no number of the
  report, so no report can show it wrong; the table holds it to the
  operator's rule all the same. `status.pullRequestSize` is a struct the API
  leaves out when it is empty: one that says nothing was not read, and one
  that names only files (a rename) was read, as a change of no lines.
- **A merged Task's `repository` is its Container's `status.repositoryURL`**
  (decision 15), by namespace and name, read in one list of Containers. It
  is absent, not empty, for a Container deleted since or not scaffolded
  yet, and the page doesn't print it, so `report.json` and `report.md` of
  the fixture, which has no Containers, are as step 1 wrote them.
- **The release notes look in the tag's tree,** not in the working copy:
  `git cat-file -e <tag>:<RESULTS>/<tag>.md`. The notes' links point at the
  tag, so a report committed after it would be a dead link, and a draft
  from `HEAD` (version `X.Y.Z`) links none. `RESULTS` is the directory, as
  `--dir` is the command's; the line stands between the changes and the
  images. So the report is written and committed before the release is
  tagged, with `--to` at that moment; what merges between then and the tag
  is the next report's.
- **From the last review of step 1:** a cost under zero or one that is no
  number prints as none (`reportMoney` printed `$-1.-49` for the first and
  panicked on NaN and on an infinity; the UI's `formatCost` prints `$-1.50`
  and `$NaN`, which no Task can give it, so `money.json` has no such case
  and the rule is Go's alone, in `TestReportPage`); a dollar sign in a
  cell or a heading takes a backslash, since GitHub reads what lies between
  two as mathematics (decision 12's list, one more; the review of step 2
  made it a span); and the directory is checked first, as above.
- **From the review of step 2:**
  - *A failed write leaves the report that was there.* The two files were
    written in a map's order, so a page that couldn't be written left the
    new document beside the old page in most runs. Each is now written
    whole under another name in the directory (`.<name>.json.tmp`,
    `.<name>.md.tmp`), and only when both are, and no directory has taken
    either's name since the first check, is each moved to its place, the
    document first and the page last, since the page is what a release
    links. What is left to fail is the second move after the first, which
    two files can't rule out; its error names the file that is new and the
    one that is not (`TestReportLeavesNoHalfAReport`).
  - *A period can't end after now.* A `--to` in the future was taken, and
    a Task that finished between the run and that end was after no
    report's beginning: in none. It is refused. The same gap is there,
    smaller, when `--to` isn't given and the person's clock runs ahead of
    the cluster's, whose clock `completedAt` is; the command doesn't ask
    the cluster the time, so a person who doubts their clock gives `--to`.
  - *A dollar sign in a cell is `<span>$</span>`,* which is what GitHub's
    documentation gives for a dollar sign outside a formula, where the
    backslash of step 1 is its escape inside one. The page is read on
    GitHub, where the release notes link it; the repository has no site
    that renders its Markdown another way, and the UI never reads the
    page, so nothing of ours prints the span as text. The table test holds
    the string; the rendering itself has not been looked at by a person, on
    GitHub or on another project's Git host.

## What the fixture holds

`tasks.json` is 35 Tasks; 13 merged in the period. A rule is held on both
sides only while its Task is there, so both tests name the Tasks they lean
on and fail when one is gone. The period is called `report`, so the
directory holds it as a repository holds a release's: `report.md`, and the
`report.json` its page links (`hack/check-doc-links.sh` follows the link).

| Rule | Task |
| --- | --- |
| A period is after its beginning | `edge-26` merged on the first instant; `old-20` a day before |
| and up to and including its end | `catalog-17` merged on the last instant; `late-21` an hour after |
| A zero-second stay in a stage counts | `hotfix-22` left Pending in the second it entered |
| Waiting for a slot is a stage of its own | `checkout-11` |
| A Task counts under its newest implement run | `catalog-17` began on sonnet and merged on opus |
| No recorded harness is "not recorded" | `legacy-14`, and `spec-16`, which has no implement run |
| Rows with as many Tasks go by code point | `pricing-23` on `Opus`, `pricing-24` on `haiku`, `search-15` on the default model, and the three Scores of those configurations |
| A cost of 0 is no cost reported | `checkout-18` says 0; `legacy-14` says nothing |
| Half a cent goes up | the median cost is `pricing-23`'s 1125, which the page prints as $1.13 |
| Lead times run from the first Implementing step to the steps after it | `adopted-25` was in CI and in review before it |
| The stages of several rounds add up | `checkout-12`, `catalog-17` |
| The 90th percentile is not the slowest | 13 merged Tasks: it is the second slowest (Go's alone) |
| A person's approval, of a spec or a held change | `spec-16`, `payments-13` |
| The sizes' bounds | `spec-16` at 99 lines and `payments-13` at 100, `checkout-18` at 499 and `search-15` at 500, `catalog-17` at 1000 |
| A Benchmark's run and a judge run are no failed Task | `bench-36`, `score-quality-17` |
| Only a Scorer's newest generation counts | `score-quality-19` is of generation 1 |
| Text is printed as text | `score-tone-19`'s criterion has a pipe, a tag, backticks and a link |

Not held by the fixture, and why: the timeline's 64 steps (the operator
cuts it, not these rules), a step with no time (the API always writes one),
and the order of two Scorers whose names differ only in what a locale
ignores (their names are Kubernetes names; `compareText` has its own test).

## How we know it's done

- `go test ./internal/model -run 'Report|MedianAndPercentile'` and
  `make ui-test` pass.
- Step 2: `go test ./cmd/theos-session -run Report` and
  `hack/release-notes_test.sh` pass.
- Changing a rule the table above lists, on either side, fails a test.
  Tried on 2026-11-10, one change at a time. In Go, each of these failed
  Go's test and, once the fixture was written again with `-update`, the
  UI's: dropping a zero-second stay, the oldest implement run instead of
  the newest, a period closed at its beginning, a period open at its end,
  a cost of 0 counted, the first Checking step and the first Reviewing
  step from any time, names in the other order. In the UI, each of these
  failed `report.test.ts`: `localeCompare` in either table, the oldest
  implement run, a window open at either end, a cost of 0 counted in the
  rows or in the sizes, the first Checking and the first Reviewing step
  from any time, a cost cut to the cent below, a zero-length step dropped
  from the timeline, a size bound moved by one line. Go's own (the 90th
  percentile as the slowest, `%.2f` for money, an escape or a name's check
  taken out) each failed a table test.
- After a rule changes on purpose:
  `go test ./internal/model -run TestFactoryReportFixture -update`, then
  `make ui-test`.
