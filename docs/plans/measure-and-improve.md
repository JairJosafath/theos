# Measure and improve: know whether a change to the factory made it better

Written 2026-11-05, after the owner compared theos with Warp Factories
(docs.warp.dev/factories) and chose this as the first thing to take from it.
No parent issue yet: step 1 files one per step, as the relay does.

## Goal

When someone changes how the factory works (an Agent's model, a route, a
Standard's agent guide, `docs/project-notes.md`), they can tell whether it
helped, with numbers the factory keeps itself, and the factory proposes the
next change where its own work keeps failing:

1. **Metrics.** The Work area shows, per period and per Agent
   configuration, how much merged, how much merged with no person's code,
   how long each stage took and what a merged Task cost, by its size.
2. **Scorers.** A Scorer is criteria in plain words that a judge Agent
   checks every finished Task against (or a sample of them), each verdict a
   `Score` with its reasons.
3. **Benchmarks.** A Benchmark runs the same goals from the same base commit
   under two or more Agent configurations, never merging, and compares what
   came out, with the uncertainty said.
4. **Self-improvement.** A Scorer whose criterion keeps failing files an
   issue that proposes the fix, which goes through the normal loop like any
   other change.

Done when every step below is merged with `make check`, and the evidence
in *Done when* is in `docs/screenshots/`.

## Where we stand (main 019e38f)

- **Tasks keep most of what metrics need.** `status.history` holds each
  phase change with its time, round and reason; `status.runs[]` each agent
  run's round, role, end time, cost and tokens; the Task its
  `costMilliUSD`, `round`, `completedAt`, review verdict and follow-ups.
  Tasks are never deleted by the operator, so the history stays.
- **The UI already reads some of it.** `ui/src/model/factory.ts`
  `factoryStats` gives the success rate, mean rounds, the first-round rate,
  median lead time and time per stage (*Where the time goes*); the token
  chart ([done/token-chart.md](done/token-chart.md)) plots runs against an
  Agent's `sessionTokenLimit`.
- **What is missing.**
  - A run records its role but not the Agent, harness or model that ran it
    (token-chart decision 2 names this), so nothing can be compared by
    configuration, and a changed Agent rewrites the past.
  - Nothing records whether a person pushed to a Task's branch, so autonomy
    can't be counted.
  - The size of a merged change isn't kept, so cost can't be set against it.
  - Nothing judges a Task beyond its own reviewer, whose verdict decides the
    merge; nothing compares configurations on the same work.
  - The operator exports no metrics of its own (no Prometheus collectors).

## Decisions, and why

1. **The CRDs stay the truth, and the record is on the Task.** Metrics are
   computed from Tasks, Scores and Benchmarks, which are kept, not from a
   metrics store theos would have to run, back up and keep in sync. The UI
   computes the views (as `factoryStats` does); a Prometheus exporter can
   come later from the same `internal/model` functions, so the two never
   disagree.
2. **A run records what ran it.** Each `status.runs[]` entry gains `agent`
   (the Agent's name), `harness` and `model`, copied when the run starts, so
   a later change to the Agent doesn't rewrite it. A configuration is the
   triple (harness, model, and the Standard's ref, which the Container's
   status already pins); the views group by it, and say "not recorded" for
   runs from before.
3. **Autonomy is "merged with no person's commit".** The operator knows each
   repository's agent bot (`AgentAccess`) and its own signature; any other
   author on the Task's pull request is a person. `status.personCommits`
   counts them, read once when the pull request ends through a new GitHost
   read, `PullRequestCommits` (author and SHA), with a contract case.
   Approving a spec, answering a question or approving a deploy is a
   decision, not code, and doesn't count against autonomy; those are the
   factory's designed human points, shown on their own.
4. **Size is the merged diff.** `status.pullRequestSize` (lines added and
   removed, files) is read with the commits. The views bin it as Warp does
   (S under 100 changed lines, M under 500, L under 1000, XL beyond), so the
   cost of a merged Task is compared within its size.
5. **A Scorer is a new kind; a Score is its record.** `kubebuilder create
   api` for both. `Scorer.spec`: `criteria` (plain words, up to 20 items),
   `judgeRef` (an Agent, so the judge's harness, model and limits are set the
   way every Agent's are), `select` (Task kinds, Containers by name or
   label; empty means every Implement Task), `samplePercent` (0 to 100,
   default 100; 0 stops it) and `maxPerDay` (default 50), with `budgetUSD`
   per day. A `Score` names its Scorer and Task in `spec` (children name
   their parent), is owned by the Scorer, and holds one verdict per
   criterion (`Pass`, `Fail`, `Unclear`) with the judge's reason, plus the
   run's cost and the configuration of decision 2 copied from the Task.
   Scores are immutable once written (CEL); a Scorer whose criteria change
   gets a new `generation` on its Scores, and the views count only the
   current one.
6. **Sampling is deterministic.** A Task is scored when the hash of
   `<scorer UID>/<task name>` falls under `samplePercent`, so a restart, a
   second replica or a re-run picks the same Tasks and never scores one twice
   (the Score's name is `<scorer>-<task>`, so a second create fails).
7. **The judge sees the evidence, read only.** It runs in an Agent Sandbox
   like a reviewer, with a read-only token and the fence, on the Task's
   finished pull request: the issue (between the data markers, as every
   prompt does), the diff, the commits, the CI result, the reviewer's verdict
   and follow-ups, the Task's history and runs, and the implementer's final
   summary. It writes nothing to the Git host. The agents' transcripts are
   not kept after a run, so criteria about how the agent worked ("ran the
   tests before pushing") are judged from what it left; keeping transcripts
   is its own plan, not this one. The reviewer's verdict still alone decides
   a merge: a Score is advisory and changes nothing about the Task.
8. **A Benchmark never touches the real work.** `Benchmark.spec`:
   `containerRef`, `base` (a commit, never a branch, like every pinned ref),
   `goals` (each a goal text or an issue number, read once at the start and
   kept in status), `variants` (each a name and an implementer, reviewer and
   verifier Agent), `repetitions` (1 to 5, default 3), `scorerRefs` and
   `parallel` (default 1). It makes one Task per goal, variant and
   repetition with `spec.benchmark` set (immutable): the Task branches from
   `base` as `bench/<benchmark>/<variant>/<goal>-<n>`, opens a draft pull
   request so CI runs, and ends in a new phase `Approved` when checks are
   green and its reviewer approves, instead of merging. It labels and closes
   no issue, files no follow-ups, and its branch and pull request are closed
   and deleted when it ends. Benchmark Tasks take no Crew slot: the
   Benchmark's own `parallel` caps them, so a benchmark never delays an
   issue.
9. **A difference is said with its uncertainty.** `Benchmark.status` gives
   each variant's approval rate, mean rounds, median duration and cost, and
   each Scorer's pass rate, each rate with its 95% Wilson interval. When two
   variants' intervals overlap, the status and the UI say "no clear
   difference at N runs" rather than a winner. `internal/model` computes
   it, with table tests.
10. **Self-improvement files an issue, never a change.** A Scorer with
    `improve.after` (for example 3 failures of one criterion among the last
    10 Scores of one configuration) files one issue, `status/proposed`,
    `kind/improvement`, with the failing Scores' reasons and links, and
    files no other while it is open. It goes where the fix would live: the
    Container's repository, for a change to `docs/project-notes.md` (the
    project's own word to its agents) or its code; the Standard's template
    repository when it is on the same GitProvider and the criterion is about
    how the agents work (the agent guides), so the change ships as a Standard
    bump through the usual pull request. Triage and a person decide; nothing
    is changed without the loop's review. It is the factory's own word, so
    the issue says which Scorer and Scores filed it.
11. **Judging costs money, and says so.** Every judge run's cost counts into
    its Score and the Scorer's `status.costTodayMilliUSD`; at `budgetUSD` or
    `maxPerDay` the Scorer stops for the day with `Ready=False`, reason
    `BudgetReached`, a needs-you item. Benchmarks cost real runs:
    `Benchmark.spec.budgetUSD` is required, and it stops starting Tasks at
    the cap, `OverBudget`, as a Crew's `taskBudgetUSD` does.

## Steps

Each step is one issue, lands on its own with `make check`, and adds its
part to `docs/design.md` (a new section, *Measuring the factory*, after
*Crews and releases*). Steps 2, 3 and 6 need only step 1; 4 needs 3; 5
needs 3; 7 needs 3 and 6.

| Step | Depends on | Done when |
| --- | --- | --- |
| 1. This plan and the design section | | landed; issues filed for steps 2 to 7 |
| 2. A run records its configuration; a Task its person commits and size | | `status.runs[].agent`, `harness`, `model`; `status.personCommits`, `status.pullRequestSize`; `GitHost.PullRequestCommits` in the port, the fake, Gitea and the contract suite; envtest: a run records its Agent's harness and model, and keeps them after the Agent changes; a pull request with a person's commit counts it, the bot's and the operator's don't |
| 3. Metrics in the Work area | 2 | `ui/src/model/metrics.ts`: per period (7 and 30 days) and per configuration, merged, autonomy (decision 3), first-round rate, cycle time to pull request, first review and merge (medians from `status.history`), cost per merged Task by size; `metrics.test.ts` cases for each, Tasks from before step 2 shown as "not recorded"; a panel beside *Where the time goes*; `make ui-test` |
| 4. `Scorer` and `Score` | 2 | `kubebuilder create api` for both; CEL: criteria count and length, `samplePercent` 0..100, a Score immutable; `internal/model`: selection, sampling (decision 6), the judge's prompt with the data markers, parsing its verdicts (`Unclear` for anything unparsable); the Scorer controller makes a judge run on each selected finished Task and writes its Score; envtest: a sampled Task gets one Score after two passes and a restart, an unsampled one none, a judge run that fails writes `Unclear` with why, `maxPerDay` and `budgetUSD` stop it with `BudgetReached`; the reconciler runs under the generated role |
| 5. Scores in the UI | 4 | the Task page's Scores (each criterion, verdict and reason); the Work area's pass rate per Scorer and configuration, with the counts; `make ui-test` |
| 6. `Benchmark` | 4 | `kubebuilder create api`; CEL: `base` a 40-hex commit, 1..5 repetitions, at least two variants, `budgetUSD` required; Task `spec.benchmark` (immutable) and phase `Approved`; `internal/model`: the Task matrix, the Wilson interval and the "no clear difference" rule (decision 9) in table tests; envtest on the faked Git host: a Benchmark of two variants makes its Tasks from `base`, they open draft pull requests, end `Approved` without a merge, touch no issue and leave no branch, take no Crew slot, and the status compares the variants; `OverBudget` at the cap |
| 7. Self-improvement and the Benchmark page | 3, 6 | `Scorer.spec.improve`; `internal/model`: when to file (decision 10) and the issue's text, table tests; envtest: three failures of one criterion in one configuration file one issue in the Container's repository, a fourth files none while it is open, a criterion about the agent guide files in the Standard's repository; the UI's Benchmark page compares its variants with their intervals; `make ui-test` |

## Done when

- `make check` passes after each step.
- Table tests in `internal/model` for sampling, the judge's prompt and
  verdicts, the Wilson interval and the comparison rule, and when
  self-improvement files; envtest for the Scorer, the Benchmark and the
  person-commit count; `ui/src/model/metrics.test.ts` for every metric.
- Evidence in `docs/screenshots/`: the test output of each step, and the
  Work area's metrics, a Task's Scores and a Benchmark's comparison from
  the demo, in both themes.

## Not in this plan

- **Keeping agents' transcripts,** which criteria about how an agent worked
  would need (decision 7).
- **A Prometheus exporter** of the same numbers (decision 1).
- **Chat posts about scores:** a Scorer at `BudgetReached` is a needs-you
  item, which the Chat's `alerts` room can carry once this lands.
- **Changing what merges.** Scores and Benchmarks advise; the reviewer, the
  checks and the fence still decide.
