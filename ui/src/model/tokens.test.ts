import { describe, expect, it } from 'vitest'
import { demoStory } from '../demo/fixture'
import type { Agent, Task, TaskAgentRun } from '../k8s/types'
import { agentTokens, describeRun, outcomeRows, spreadInRounds, tokenAxis, totalTokens, type RunPoint } from './tokens'

const agent = (name: string, sessionTokenLimit?: number, namespace = 'shop'): Agent => ({
  apiVersion: 'theos.factory.io/v1beta1',
  kind: 'Agent',
  metadata: { name, namespace },
  spec: { displayName: `${name} (display)`, image: 'agent:dev', sessionTokenLimit },
})

const run = (round: number, role: string | undefined, largestSession?: number, more: Partial<TaskAgentRun> = {}) => ({
  round,
  role,
  endedAt: '2026-10-16T01:00:00Z',
  tokens: largestSession === undefined ? undefined : { largestSession, output: 1_000, cacheRead: 9_000 },
  ...more,
})

const task = (name: string, created: string, spec: Partial<Task['spec']>, status: Task['status']): Task => ({
  apiVersion: 'theos.factory.io/v1beta1',
  kind: 'Task',
  metadata: { name, namespace: 'shop', creationTimestamp: created },
  spec: { agentRef: { name: 'impl' }, reviewerRef: { name: 'rev' }, goal: `Goal of ${name}`, ...spec },
  status,
})

describe('agentTokens', () => {
  const agents = [agent('impl', 130_000), agent('rev', 100_000), agent('impl', 50_000, 'other')]

  it('puts each run on the chart of the Agent that ran it, by its role', () => {
    const tasks = [
      task(
        't1',
        '2026-10-16T00:00:00Z',
        {},
        {
          phase: 'Merged',
          round: 1,
          runs: [run(1, 'implement', 90_000), run(1, 'review', 40_000)],
        },
      ),
      // Without a reviewerRef the implementer reviews; a run that names no role is the implementer's.
      task(
        't2',
        '2026-10-16T00:10:00Z',
        { reviewerRef: undefined },
        {
          phase: 'Implementing',
          round: 2,
          runs: [run(1, 'review', 30_000), run(2, undefined, 70_000)],
        },
      ),
      // A Plan Task's runs are its planner's.
      task(
        'plan',
        '2026-10-16T00:20:00Z',
        { kind: 'Plan', agentRef: { name: 'planner' }, reviewerRef: undefined },
        {
          phase: 'Merged',
          round: 1,
          runs: [run(1, undefined, 60_000)],
        },
      ),
    ]
    const charts = agentTokens(tasks, agents)
    expect(charts.map((c) => [c.key, c.points.map((p) => `${p.name}:${p.round}:${p.role}`)])).toEqual([
      ['shop/impl', ['t1:1:implement', 't2:1:review', 't2:2:implement']],
      ['shop/planner', ['plan:1:plan']],
      ['shop/rev', ['t1:1:review']],
    ])
  })

  it('takes the limit from the Agent in the Task’s namespace, and only the name of one that is gone', () => {
    const tasks = [
      task(
        't1',
        '2026-10-16T00:00:00Z',
        { agentRef: { name: 'gone' } },
        {
          phase: 'Merged',
          runs: [run(1, 'implement', 90_000), run(1, 'review', 40_000)],
        },
      ),
    ]
    const [gone, rev] = agentTokens(tasks, agents)
    expect(gone).toMatchObject({ key: 'shop/gone', title: 'gone', found: false, limit: undefined })
    expect(rev).toMatchObject({ key: 'shop/rev', title: 'rev (display)', found: true, limit: 100_000 })
    // impl exists in two namespaces; the Task's own is shop.
    const [impl] = agentTokens([{ ...tasks[0], spec: { ...tasks[0].spec, agentRef: { name: 'impl' } } }], agents)
    expect(impl).toMatchObject({ key: 'shop/impl', limit: 130_000 })
  })

  it('counts a run that reported no largest session as unreported, and charts nothing of a Task without runs', () => {
    const tasks = [
      task(
        'stand-in',
        '2026-10-16T00:00:00Z',
        {},
        {
          phase: 'Merged',
          tokens: { largestSession: 50_000 },
          runs: [run(1, 'implement'), run(1, 'review', 0, { tokens: { runsWithout: 1 } })],
        },
      ),
      task('old', '2026-10-16T00:10:00Z', {}, { phase: 'Failed', round: 3, tokens: { largestSession: 99_000 } }),
    ]
    const charts = agentTokens(tasks, agents)
    expect(charts.map((c) => [c.key, c.points.length, c.unreported])).toEqual([
      ['shop/impl', 0, 1],
      ['shop/rev', 0, 1],
    ])
  })

  it('gives each point its Task’s outcome and rounds used, what the run used and when it handed off', () => {
    const tasks = [
      task(
        't1',
        '2026-10-16T00:00:00Z',
        { issue: 13 },
        {
          phase: 'Failed',
          round: 3,
          issueTitle: 'Refund an order',
          runs: [
            run(2, 'implement', 142_000, {
              tokens: { input: 1_000, output: 2_000, cacheRead: 3_000, cacheWrite: 4_000, largestSession: 142_000 },
              sessionHandoffs: [131_000, 138_400],
            }),
          ],
        },
      ),
    ]
    const [impl] = agentTokens(tasks, agents)
    expect(impl.points).toEqual([
      {
        task: 'shop/t1',
        name: 't1',
        namespace: 'shop',
        headline: '#13 Refund an order',
        round: 2,
        role: 'implement',
        largest: 142_000,
        total: 10_000,
        handoffs: [131_000, 138_400],
        outcome: 'failed',
        rounds: 3,
      },
    ])
    expect(impl.maxRound).toBe(2)
  })

  it('orders the points oldest Task first, and the Agents by how many points they have', () => {
    const tasks = [
      task('newer', '2026-10-16T02:00:00Z', {}, { round: 1, runs: [run(1, 'implement', 10_000)] }),
      task(
        'older',
        '2026-10-16T01:00:00Z',
        {},
        {
          round: 1,
          runs: [run(1, 'implement', 20_000), run(1, 'review', 5_000), run(2, 'review', 5_000)],
        },
      ),
    ]
    const charts = agentTokens(tasks, agents)
    expect(charts.map((c) => c.key)).toEqual(['shop/impl', 'shop/rev'])
    expect(charts[0].points.map((p) => p.name)).toEqual(['older', 'newer'])
    // A Task still in round 1 whose runs went on into round 2 used two rounds.
    expect(charts[1].points.map((p) => p.rounds)).toEqual([2, 2])
  })

  it('charts the demo’s implementer and reviewer, with the failed Task’s run past the limit (#1086)', () => {
    const story = demoStory(Date.parse('2026-10-16T04:00:00Z'))
    const charts = agentTokens(story.tasks, story.agents)
    expect(charts.map((c) => [c.name, c.points.length, c.limit])).toEqual([
      ['claude-implementer', 7, 130_000],
      ['claude-reviewer', 5, 130_000],
    ])
    expect(charts[0].byOutcome.map((r) => [r.outcome, r.tasks, r.runs, r.pastLimit, r.handedOff])).toEqual([
      ['merged', 2, 3, 0, 0],
      ['failed', 1, 3, 1, 1],
      ['active', 1, 1, 0, 0],
    ])
  })
})

describe('outcomeRows', () => {
  const point = (task: string, outcome: RunPoint['outcome'], rounds: number, largest: number, more = {}): RunPoint => ({
    task,
    name: task,
    namespace: 'shop',
    headline: task,
    round: 1,
    role: 'implement',
    largest,
    total: largest * 10,
    handoffs: [],
    outcome,
    rounds,
    ...more,
  })

  it('sets what the runs used against the rounds their Tasks used, per outcome, in a fixed order', () => {
    const points = [
      point('a', 'failed', 3, 140_000, { handoffs: [131_000] }),
      point('a', 'failed', 3, 130_000),
      point('b', 'merged', 1, 60_000),
      point('c', 'merged', 2, 80_000, { total: undefined }),
      point('c', 'merged', 2, 100_000),
    ]
    expect(outcomeRows(points, 130_000)).toEqual([
      {
        outcome: 'merged',
        tasks: 2,
        meanRounds: 1.5,
        runs: 3,
        medianLargest: 80_000,
        meanTotal: 800_000,
        pastLimit: 0,
        handedOff: 0,
      },
      // A run at the limit is not past it.
      {
        outcome: 'failed',
        tasks: 1,
        meanRounds: 3,
        runs: 2,
        medianLargest: 135_000,
        meanTotal: 1_350_000,
        pastLimit: 1,
        handedOff: 1,
      },
    ])
  })

  it('counts none past a limit the Agent doesn’t have, and no mean of totals nobody reported', () => {
    const [row] = outcomeRows([point('a', 'active', 1, 900_000, { total: undefined })], undefined)
    expect(row).toMatchObject({ pastLimit: 0, meanTotal: undefined })
  })
})

describe('tokenAxis', () => {
  it.each([
    // largest, limit: top, gridlines
    [142_000, 130_000, 150_000, [0, 50_000, 100_000, 150_000]],
    // The limit's label keeps room above it when every run is small.
    [20_000, 130_000, 150_000, [0, 50_000, 100_000, 150_000]],
    [900_000, 130_000, 1_000_000, [0, 250_000, 500_000, 750_000, 1_000_000]],
    [41_000, undefined, 60_000, [0, 20_000, 40_000, 60_000]],
    [800, undefined, 800, [0, 200, 400, 600, 800]],
  ])('fits %d under a %s limit in %d', (largest, limit, max, ticks) => {
    expect(tokenAxis(largest, limit)).toEqual({ max, ticks })
  })
})

describe('spreadInRounds', () => {
  it('spreads each round’s points evenly over its band, in their order', () => {
    expect(spreadInRounds([{ round: 1 }, { round: 2 }, { round: 1 }, { round: 1 }])).toEqual([0.25, 0.5, 0.5, 0.75])
  })
})

describe('totalTokens', () => {
  it('adds up the four kinds a run reported, and is undefined when it reported none', () => {
    expect(totalTokens({ input: 1, output: 2, cacheRead: 3, cacheWrite: 4, largestSession: 100 })).toBe(10)
    expect(totalTokens({ largestSession: 100 })).toBeUndefined()
    expect(totalTokens(undefined)).toBeUndefined()
  })
})

describe('describeRun', () => {
  const point: RunPoint = {
    task: 'shop/t',
    name: 't',
    namespace: 'shop',
    headline: 'Add a cart',
    round: 2,
    role: 'review',
    largest: 40_000,
    handoffs: [],
    outcome: 'active',
    rounds: 2,
  }

  it('says the run, its size and its Task, without a limit or a total it doesn’t know', () => {
    expect(describeRun(point, undefined)).toBe(
      'Add a cart (t) · round 2, review · largest 40k · the Task is still going, in round 2',
    )
    expect(describeRun({ ...point, headline: '', outcome: 'merged', total: 1_200_000 }, 40_000)).toBe(
      't · round 2, review · largest 40k · 1.2M tokens in all · the Task merged after 2 rounds',
    )
  })
})
