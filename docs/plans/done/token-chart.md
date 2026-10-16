# What agent runs use: a chart in the Work area

Oct 16, 2026 · #1086 · otter-1086. One step, done in one pull request.

## Goal

A reporter can judge an Agent's `sessionTokenLimit` from the Work area: how
large each agent run got, in each round of its Task, against the limit, set
against the rounds the Task used and whether it failed.
[session-tokens.md](session-tokens.md) left this chart for when the numbers had
been collected for a while; every Task has recorded its runs since #658 and #659.

## Decisions

| # | Topic | Decision |
| --- | --- | --- |
| 1 | What is charted | Each recorded agent run (`status.runs`) by its `tokens.largestSession`, the prompt of its biggest turn: that is what the limit caps. A run's total tokens are in its tooltip and the table, not on the axis: they are mostly cache reads and grow with turns, which the limit doesn't decide. |
| 2 | One Agent at a time | The limit is an Agent's, and an implementer's and a reviewer's runs differ in size, so the chart shows one Agent's runs against its own limit, with a picker when several Agents ran. It opens on the Agent with the most runs on the chart. A run is its role's Agent's: review is the `reviewerRef` (else the `agentRef`); implement, plan and a run that named no role are the `agentRef`'s, as on the Task page. |
| 3 | The form | A dot plot: round across (1 to the highest round charted), largest session up, the limit a dashed line. Rounds are few, so a band per round spreads its runs side by side instead of piling them up. |
| 4 | Outcome and handoffs | The mark is the Task's outcome, by shape and the status colour: a dot for merged, a cross for failed, a ring for still going. A run that handed off gets a halo. Colour is never the only cue. |
| 5 | Rounds used and failures, in numbers | A table under the chart, per outcome: Tasks, the rounds they used on average, runs, the median largest run, tokens per run, runs past the limit and runs that handed off. It is also what a screen reader reads in place of the dots. |
| 6 | Runs without figures | A run that reported no largest session (the stand-in's, an unreported call) is left off and counted under the chart. A Task from before runs were recorded adds nothing: its Task-wide figures can't be put in a round. |
| 7 | Where | The Work area's main column, under *Where the time goes*. Each run links to its Task's page and says, when pointed at, its Task, round, role, size, total, handoffs and how the Task ended. |

## Done when

- `ui/src/model/tokens.ts` shapes the data: `agentTokens`, `outcomeRows`,
  `tokenAxis`, `spreadInRounds` and `describeRun`, with table tests in
  `tokens.test.ts`, the demo fixture's runs included.
- `ui/src/components/factory/TokensPanel.tsx` draws it; `TokensPanel.test.ts`
  renders the demo: the chosen Agent and its limit, the marks by outcome, the
  links, the table and the empty states.
- The demo fixture's Tasks record runs with tokens, so the demo shows the chart.
- `make ui-test` and `make check` pass.

Evidence: [ui-work-agent-runs.png](../../screenshots/ui-work-agent-runs.png)
and [the dark one](../../screenshots/ui-work-agent-runs-dark.png), from the demo;
axe found nothing in the panel in either scheme.
