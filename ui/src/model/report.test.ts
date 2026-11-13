// The factory's numbers as a report (#66, docs/plans/results-report.md): a
// command writes them from internal/model's rules, which are these rules
// written again in Go. This test holds the two together. testdata/report has
// Tasks as the API serves them and the report internal/model gives for them
// (its own test checks that, byte for byte); here the UI's rules read the same
// Tasks and must give the same numbers. A rule changed on one side alone fails
// the other side's test, for the rules the fixture has a Task for: the plan
// lists them ("What the fixture holds").

import { describe, expect, it } from 'vitest'
import type { Task } from '../k8s/types'
import { formatCost } from './brief'
import { factoryStats, median, STAGES, taskSegments, time, timeByStage } from './factory'
import { factoryMetrics } from './metrics'
import { scorerRates } from './scores'
import moneyJSON from './testdata/report/money.json?raw'
import reportJSON from './testdata/report/report.json?raw'
import tasksJSON from './testdata/report/tasks.json?raw'

interface Share {
  tasks: number
  of: number
  share?: number
}
interface Cost {
  tasks: number
  medianMilliUSD?: number
}
interface Time {
  key: string
  label: string
  tasks: number
  medianSeconds?: number
}
interface Verdicts {
  config?: string
  pass: number
  fail: number
  unclear: number
  rate?: number
}
/** internal/model's Report, as far as the UI has a number for it. */
interface Report {
  from: string
  to: string
  merged: number
  failed: number
  noPersonCommit: Share
  personApproved: Share
  firstRound: Share
  leadTimes: Time[]
  stageTimes: Time[]
  cost: Cost
  costBySize: { size: string; merged: number; cost: Cost }[]
  withoutSize: number
  byConfig: { key: string; merged: number; noPersonCommit: Share; cost: Cost; toMergeSeconds?: number }[]
  scorers: {
    namespace: string
    scorer: string
    scores: number
    configs: string[]
    criteria: { criterion: string; byConfig: Verdicts[] }[]
  }[]
  mergedTasks: { namespace: string; container: string; task: string; pullRequest: number; completed: string }[]
}

const every = (JSON.parse(tasksJSON) as { items: Task[] }).items
const report = JSON.parse(reportJSON) as Report
const money = JSON.parse(moneyJSON) as { milliUSD: number; shown: string }[]
const NOW = Date.parse(report.to)
const FROM = Date.parse(report.from)
const DAY = 24 * 60 * 60 * 1000

const seconds = (ms: number | undefined) => (ms === undefined ? undefined : ms / 1000)
const finished = (t: Task) => time(t.status?.completedAt) ?? NaN
/**
 * The one place the two differ on purpose (the plan's decision 4): a report's
 * period is open at its beginning, since the report before it ends there, and
 * the panel's window is closed at both ends. A Task that finished on that
 * instant is the panel's and not this report's, so the panel's rules are
 * given every other Task.
 */
const onTheBeginning = every.filter((t) => finished(t) === FROM)
const tasks = every.filter((t) => finished(t) !== FROM)
/** The Tasks that finished in the report's period: after its beginning, up to its end. */
const inPeriod = tasks.filter((t) => finished(t) > FROM && finished(t) <= NOW)
const merged = inPeriod.filter((t) => t.status?.phase === 'Merged' && t.status.pullRequest)
const named = (name: string) => {
  const task = merged.find((t) => t.metadata.name === name)
  if (!task) throw new Error(`the fixture has no merged Task ${name} in the period`)
  return task
}

describe("The report's numbers are the UI's (#66)", () => {
  const m = factoryMetrics(tasks, NOW, 30)

  it('reads a fixture whose period is one the panel offers, with Tasks on both sides of it', () => {
    expect(NOW - FROM).toBe(30 * DAY)
    expect(report.merged).toBeGreaterThan(0)
    expect(tasks.length).toBeGreaterThan(inPeriod.length)
    expect(finished(named('catalog-17'))).toBe(NOW)
  })

  it('counts a Task that finished on the period’s first instant in the panel, and not in the report', () => {
    expect(onTheBeginning.map((t) => t.metadata.name)).toEqual(['edge-26'])
    expect(factoryMetrics(every, NOW, 30).all.merged).toBe(report.merged + 1)
    expect(report.mergedTasks.map((t) => t.task)).not.toContain('edge-26')
  })

  it('counts the same Tasks as merged and as failed', () => {
    expect(m.all.merged).toBe(report.merged)
    // A Scorer's judge runs and a Benchmark's runs are no failed Tasks.
    expect(factoryStats(inPeriod, NOW).failed).toBe(report.failed)
  })

  it('names the merged Tasks the panel counts, each with its pull request', () => {
    const key = (namespace: string | undefined, task: string, pull: number | undefined) =>
      `${namespace ?? ''}/${task} #${pull ?? 0}`
    expect(report.mergedTasks.map((t) => key(t.namespace, t.task, t.pullRequest)).sort()).toEqual(
      merged.map((t) => key(t.metadata.namespace, t.metadata.name, t.status?.pullRequest)).sort(),
    )
    expect(report.mergedTasks.map((t) => Date.parse(t.completed))).toEqual(
      report.mergedTasks.map((t) => finished(named(t.task))),
    )
  })

  it('gives the same shares: no person’s commit, a person’s approval, the first round', () => {
    expect(m.all.commitsRead).toBe(report.noPersonCommit.of)
    expect(m.all.autonomy).toBeCloseTo(report.noPersonCommit.share ?? NaN, 12)
    expect(m.all.personApproved).toBeCloseTo(report.personApproved.share ?? NaN, 12)
    expect(m.all.firstRound).toBeCloseTo(report.firstRound.share ?? NaN, 12)
  })

  it('takes the same medians to the pull request, the first review and the merge', () => {
    expect(Object.fromEntries(report.leadTimes.map((t) => [t.key, t.medianSeconds]))).toEqual({
      toPullRequest: seconds(m.all.toPullMs),
      toFirstReview: seconds(m.all.toReviewMs),
      toMerge: seconds(m.all.toMergeMs),
    })
  })

  it('measures the lead times from the first Implementing step, to the steps after it', () => {
    // adopted-25 was in CI and in review before it was first implemented.
    const phases = (named('adopted-25').status?.history ?? []).map((s) => s.phase)
    expect(phases.indexOf('Checking')).toBeLessThan(phases.indexOf('Implementing'))
    expect(phases.indexOf('Reviewing')).toBeLessThan(phases.indexOf('Implementing'))
  })

  it('puts a merged Task’s time in the same stages, under the same names', () => {
    expect(merged).toHaveLength(report.merged)
    expect(report.stageTimes).toHaveLength(STAGES.length)
    // hotfix-22 left Pending in the second it entered it: queued, for no time.
    expect(timeByStage(taskSegments(named('hotfix-22'), NOW)).queued).toBe(0)
    expect(taskSegments(named('hotfix-22'), NOW).map((s) => s.stage)).toContain('queued')
    STAGES.forEach((stage, i) => {
      // A stage a Task never was in is no observation of it; one it was in for no time is, of zero.
      const spent = merged
        .map((t) => taskSegments(t, NOW))
        .filter((segments) => segments.some((s) => s.stage === stage.key))
        .map((segments) => timeByStage(segments)[stage.key])
      const row = report.stageTimes[i]
      expect([row.key, row.label, row.tasks, row.medianSeconds]).toEqual([
        stage.key,
        stage.label,
        spent.length,
        seconds(median(spent)),
      ])
    })
  })

  it('sets the same cost on a merged Task, over all and within each size', () => {
    // checkout-18 says a cost of 0, which is none reported, as when it says nothing.
    expect(named('checkout-18').status?.costMilliUSD).toBe(0)
    expect(m.all.costMilliUSD).toBe(report.cost.medianMilliUSD)
    expect(m.bySize).toEqual(
      report.costBySize.map((s) => ({ size: s.size, tasks: s.merged, costMilliUSD: s.cost.medianMilliUSD })),
    )
    expect(m.withoutSize).toBe(report.withoutSize)
  })

  it('compares the same configurations, in the same order', () => {
    expect(
      m.byConfig.map((r) => [r.key, r.merged, r.commitsRead, r.autonomy, r.costMilliUSD, seconds(r.toMergeMs)]),
    ).toEqual(
      report.byConfig.map((g) => [
        g.key,
        g.merged,
        g.noPersonCommit.of,
        g.noPersonCommit.share,
        g.cost.medianMilliUSD,
        g.toMergeSeconds,
      ]),
    )
    expect(m.byConfig.length).toBeGreaterThan(2)
    // Three configurations merged one Task each: their names' code points decide, a capital first.
    const one = report.byConfig.filter((g) => g.merged === 1 && g.key !== 'not recorded').map((g) => g.key)
    expect(one).toEqual(['claude-code · Opus', 'claude-code · default model', 'claude-code · haiku'])
    // catalog-17 began on sonnet and merged on opus: its newest implement run is the one it counts under.
    const implemented = (named('catalog-17').status?.runs ?? []).filter((r) => r.role === 'implement')
    expect([implemented[0].model, implemented[implemented.length - 1].model]).toEqual(['sonnet', 'opus'])
  })

  it('gives each Scorer the same pass rates, per criterion and configuration', () => {
    const rates = scorerRates(inPeriod)
    expect(rates.map((s) => [s.namespace, s.scorer, s.scored, s.configs, s.criteria])).toEqual(
      report.scorers.map((s) => [s.namespace, s.scorer, s.scores, s.configs, s.criteria.map((c) => c.criterion)]),
    )
    rates.forEach((s, n) => {
      s.rates.forEach((byConfig, i) => {
        // The UI has no entry for a configuration no verdict was given under; the report counts it as none.
        const ui = s.configs.map((config) => ({
          config,
          ...(byConfig[config] ?? { pass: 0, fail: 0, unclear: 0, rate: undefined }),
        }))
        expect(ui).toEqual(report.scorers[n].criteria[i].byConfig)
      })
    })
    expect(rates.length).toBeGreaterThan(1)
    // Three configurations were judged once each: the same order of names.
    expect(report.scorers[1].configs.slice(1, 4)).toEqual([
      'claude-code · Opus',
      'claude-code · default model',
      'claude-code · opus',
    ])
  })

  it('writes money as the report’s page does: the nearest cent, half a cent up', () => {
    // internal/model's reportMoney is held to the same file (TestReportMoneyIsTheUIs).
    expect(money.length).toBeGreaterThan(9)
    for (const { milliUSD, shown } of money) expect([milliUSD, formatCost(milliUSD)]).toEqual([milliUSD, shown])
    expect(formatCost(125)).toBe('$0.13')
    expect(formatCost(1125)).toBe('$1.13')
  })
})
