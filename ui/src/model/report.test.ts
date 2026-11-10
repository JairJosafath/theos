// The factory's numbers as a report (#66, docs/plans/results-report.md): a
// command writes them from internal/model's rules, which are these rules
// written again in Go. This test holds the two together. testdata/report has
// Tasks as the API serves them and the report internal/model gives for them
// (its own test checks that, byte for byte); here the UI's rules read the same
// Tasks and must give the same numbers. A rule changed on one side alone fails
// the other side's test.

import { describe, expect, it } from 'vitest'
import type { Task } from '../k8s/types'
import { factoryStats, median, STAGES, taskSegments, time, timeByStage } from './factory'
import { factoryMetrics } from './metrics'
import { scorerRates } from './scores'
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
}

const tasks = (JSON.parse(tasksJSON) as { items: Task[] }).items
const report = JSON.parse(reportJSON) as Report
const NOW = Date.parse(report.to)
const FROM = Date.parse(report.from)
const DAY = 24 * 60 * 60 * 1000

const seconds = (ms: number | undefined) => (ms === undefined ? undefined : ms / 1000)
const finished = (t: Task) => time(t.status?.completedAt) ?? NaN
/** The Tasks that finished in the report's period: after its beginning, up to its end. */
const inPeriod = tasks.filter((t) => finished(t) > FROM && finished(t) <= NOW)

describe("The report's numbers are the UI's (#66)", () => {
  const m = factoryMetrics(tasks, NOW, 30)

  it('reads a fixture whose period is one the panel offers, with Tasks on both sides of it', () => {
    expect(NOW - FROM).toBe(30 * DAY)
    expect(report.merged).toBeGreaterThan(0)
    expect(tasks.length).toBeGreaterThan(inPeriod.length)
  })

  it('counts the same Tasks as merged and as failed', () => {
    expect(m.all.merged).toBe(report.merged)
    // A Scorer's judge runs and a Benchmark's runs are no failed Tasks.
    expect(factoryStats(inPeriod, NOW).failed).toBe(report.failed)
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

  it('puts a merged Task’s time in the same stages, under the same names', () => {
    const merged = inPeriod.filter((t) => t.status?.phase === 'Merged' && t.status.pullRequest)
    expect(merged).toHaveLength(report.merged)
    const byStage = merged.map((t) => timeByStage(taskSegments(t, NOW)))
    expect(report.stageTimes).toHaveLength(STAGES.length)
    STAGES.forEach((stage, i) => {
      // A stage a Task never was in is no observation of it.
      const spent = byStage.map((s) => s[stage.key]).filter((ms) => ms > 0)
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
  })
})
