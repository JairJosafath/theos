import { describe, expect, it } from 'vitest'
import type { Task, TaskTransition } from '../k8s/types'
import { changeSize, configKey, factoryMetrics, implementerConfig, NOT_RECORDED } from './metrics'

const NOW = Date.parse('2026-11-05T12:00:00Z')
const MIN = 60_000
const DAY = 24 * 60 * MIN
const at = (msAgo: number) => new Date(NOW - msAgo).toISOString()

/** A merged Task: started `start` ago, its pull request opened, reviewed and merged so many minutes after. */
function merged(
  name: string,
  opts: {
    endedAgo?: number
    round?: number
    model?: string
    harness?: string
    personCommits?: number
    size?: [number, number]
    cost?: number
    toPull?: number
    toReview?: number
    toMerge?: number
  } = {},
): Task {
  const { toPull = 10, toReview = 15, toMerge = 20 } = opts
  const end = opts.endedAgo ?? DAY
  const start = end + toMerge * MIN
  const history: TaskTransition[] = [
    { at: at(start), phase: 'Implementing', round: 1 },
    { at: at(start - toPull * MIN), phase: 'Checking', round: 1 },
    { at: at(start - toReview * MIN), phase: 'Reviewing', round: 1 },
    { at: at(end), phase: 'Merged', round: opts.round ?? 1 },
  ]
  return {
    metadata: { name, namespace: 'shop', creationTimestamp: at(start) },
    spec: { containerRef: { name: 'api' }, agentRef: { name: 'implementer' }, issue: 1 },
    status: {
      phase: 'Merged',
      round: opts.round ?? 1,
      pullRequest: 3,
      completedAt: at(end),
      history,
      costMilliUSD: opts.cost,
      personCommits: opts.personCommits,
      pullRequestSize: opts.size ? { additions: opts.size[0], deletions: opts.size[1] } : undefined,
      runs: opts.harness
        ? [
            {
              round: 1,
              role: 'implement',
              endedAt: at(start - 5 * MIN),
              agent: 'implementer',
              harness: opts.harness,
              model: opts.model,
            },
            { round: 1, role: 'review', endedAt: at(end), agent: 'reviewer', harness: 'claude-code', model: 'opus' },
          ]
        : undefined,
    },
  }
}

describe('How well it works (measure-and-improve, step 3)', () => {
  it('sizes a change as internal/model does', () => {
    expect([changeSize(60, 39), changeSize(60, 40), changeSize(499), changeSize(250, 250), changeSize(1000)]).toEqual([
      'S',
      'M',
      'M',
      'L',
      'XL',
    ])
  })

  it("groups a Task by its implementer's configuration, its newest implement run's", () => {
    const t = merged('a', { harness: 'claude-code', model: 'sonnet' })
    expect(implementerConfig(t)).toEqual({ harness: 'claude-code', model: 'sonnet' })
    expect(configKey(implementerConfig(t))).toBe('claude-code · sonnet')
    expect(configKey({ harness: 'claude-code', model: '' })).toBe('claude-code · default model')
    expect(implementerConfig(merged('b'))).toBeUndefined()
    expect(configKey(undefined)).toBe(NOT_RECORDED)
  })

  it('counts the Tasks merged in the period, and none outside it nor unmerged', () => {
    const tasks = [
      merged('in', { endedAgo: 2 * DAY }),
      merged('old', { endedAgo: 9 * DAY }),
      { ...merged('failed'), status: { ...merged('failed').status, phase: 'Failed' as const } },
      { ...merged('no-pr'), status: { ...merged('no-pr').status, pullRequest: undefined } },
    ]
    expect(factoryMetrics(tasks, NOW, 7).all.merged).toBe(1)
    expect(factoryMetrics(tasks, NOW, 30).all.merged).toBe(2)
  })

  it('counts autonomy over the Tasks whose commits were read, and says how many were', () => {
    const m = factoryMetrics(
      [
        merged('a', { personCommits: 0 }),
        merged('b', { personCommits: 0 }),
        merged('c', { personCommits: 2 }),
        merged('d'),
      ],
      NOW,
      7,
    )
    expect(m.all.autonomy).toBeCloseTo(2 / 3)
    expect(m.all.commitsRead).toBe(3)
    expect(factoryMetrics([merged('d')], NOW, 7).all.autonomy).toBeUndefined()
  })

  it('takes the medians from the first Implementing step to the pull request, the first review and the merge', () => {
    const m = factoryMetrics(
      [
        merged('a', { toPull: 10, toReview: 15, toMerge: 20 }),
        merged('b', { toPull: 30, toReview: 40, toMerge: 60 }),
        merged('c', { toPull: 20, toReview: 25, toMerge: 30, round: 2 }),
      ],
      NOW,
      7,
    )
    expect([m.all.toPullMs, m.all.toReviewMs, m.all.toMergeMs]).toEqual([20 * MIN, 25 * MIN, 30 * MIN])
    expect(m.all.firstRound).toBeCloseTo(2 / 3)
  })

  it('compares configurations, the most merged first and the unrecorded last', () => {
    const m = factoryMetrics(
      [
        merged('a', { harness: 'claude-code', model: 'opus', cost: 3000, personCommits: 0 }),
        merged('b', { harness: 'claude-code', model: 'sonnet', cost: 1000, personCommits: 1 }),
        merged('c', { harness: 'claude-code', model: 'sonnet', cost: 2000, personCommits: 0 }),
        merged('d', { cost: 500 }),
      ],
      NOW,
      7,
    )
    expect(m.byConfig.map((r) => [r.key, r.merged, r.costMilliUSD, r.autonomy])).toEqual([
      ['claude-code · sonnet', 2, 1500, 0.5],
      ['claude-code · opus', 1, 3000, 1],
      [NOT_RECORDED, 1, 500, undefined],
    ])
  })

  it('sets the cost of a merged Task within its size, and counts the ones not sized', () => {
    const m = factoryMetrics(
      [
        merged('s1', { size: [40, 10], cost: 800 }),
        merged('s2', { size: [20, 0], cost: 1200 }),
        merged('l', { size: [600, 100], cost: 9000 }),
        merged('unsized', { cost: 100 }),
        merged('free', { size: [5, 0] }),
      ],
      NOW,
      7,
    )
    expect(m.bySize).toEqual([
      { size: 'S', tasks: 3, costMilliUSD: 1000 },
      { size: 'M', tasks: 0, costMilliUSD: undefined },
      { size: 'L', tasks: 1, costMilliUSD: 9000 },
      { size: 'XL', tasks: 0, costMilliUSD: undefined },
    ])
    expect(m.withoutSize).toBe(1)
  })

  it('has nothing to say without merged Tasks', () => {
    const m = factoryMetrics([], NOW, 7)
    expect(m.all).toEqual({ key: 'all', merged: 0, commitsRead: 0 })
    expect(m.byConfig).toEqual([])
  })
})
