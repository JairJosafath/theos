import { describe, expect, it } from 'vitest'
import type { FactoryHealth } from '../k8s/health'
import type { Container, Session, Task, TaskTransition } from '../k8s/types'
import {
  attention,
  boardColumns,
  currentStage,
  factoryStats,
  formatDuration,
  slotQueue,
  taskSegments,
  timeByStage,
  timelineSteps,
} from './factory'

const T0 = Date.parse('2026-10-01T12:00:00Z')
const at = (min: number) => new Date(T0 + min * 60_000).toISOString()

function task(name: string, status: Task['status'] = {}, created = at(0)): Task {
  return {
    metadata: { name, namespace: 'toolbox', creationTimestamp: created },
    spec: { containerRef: { name: 'image-shrink' }, agentRef: { name: 'claude' }, goal: 'Build it' },
    status,
  }
}

const step = (min: number, phase: TaskTransition['phase'], round: number, reason: string): TaskTransition => ({
  at: at(min),
  phase,
  round,
  reason,
})

/** One merged Task: 5 min for a slot, 20 implementing, 10 CI, 5 review, a second round, then merged at 70. */
const merged = task('image-shrink-v1', {
  phase: 'Merged',
  round: 2,
  completedAt: at(70),
  history: [
    step(0, 'Implementing', 1, 'WaitingForSlot'),
    step(5, 'Implementing', 1, 'Running'),
    step(6, 'Implementing', 1, 'Waiting'),
    step(25, 'Checking', 1, 'Checking'),
    step(35, 'Reviewing', 1, 'Running'),
    step(40, 'Implementing', 2, 'NextRound'),
    step(55, 'Checking', 2, 'Checking'),
    step(60, 'Reviewing', 2, 'Running'),
    step(70, 'Merged', 2, 'Merged'),
  ],
})

describe('taskSegments', () => {
  it('turns the timeline into stages that last until the next step', () => {
    const segments = taskSegments(merged, T0 + 999 * 60_000)
    expect(segments.map((s) => [s.stage, s.round, (s.end - s.start) / 60_000])).toEqual([
      ['slot', 1, 5],
      ['implement', 1, 1],
      ['implement', 1, 19],
      ['checks', 1, 10],
      ['review', 1, 5],
      ['implement', 2, 15],
      ['checks', 2, 5],
      ['review', 2, 10],
    ])
    expect(segments.some((s) => s.current)).toBe(false)
  })

  it('runs the current step of an active Task until now', () => {
    const active = task('bg-eraser-v1', {
      phase: 'Checking',
      round: 1,
      history: [step(0, 'Implementing', 1, 'Running'), step(30, 'Checking', 1, 'Checking')],
    })
    const segments = taskSegments(active, T0 + 42 * 60_000)
    expect(segments.at(-1)).toMatchObject({ stage: 'checks', current: true, end: T0 + 42 * 60_000 })
  })

  it('is empty for a Task from before timelines', () => {
    expect(taskSegments(task('old', { phase: 'Merged', round: 3 }), T0)).toEqual([])
  })
})

describe('timeByStage', () => {
  it('sums the time per stage', () => {
    const totals = timeByStage(taskSegments(merged, T0))
    expect(Object.fromEntries(Object.entries(totals).map(([k, v]) => [k, v / 60_000]))).toEqual({
      queued: 0,
      slot: 5,
      implement: 35,
      checks: 15,
      review: 15,
    })
  })
})

describe('currentStage and the board', () => {
  const waitingForSlot = task('doc-scanner-v1', { phase: 'Implementing', round: 2, slotWaitSince: at(10) })
  const queued = task('photo-editor-v1', {
    phase: 'Pending',
    conditions: [{ type: 'Progressing', status: 'True', reason: 'Waiting', message: 'Waiting for Task x' }],
  })
  const reviewing = task('qr-scanner-v2', { phase: 'Reviewing', round: 1, sandbox: 'qr-scanner-v2-r1-review-a1' })
  const failed = task('qr-scanner-v1', { phase: 'Failed', round: 4, completedAt: at(80) })

  it('puts each Task in its stage', () => {
    expect(currentStage(waitingForSlot)).toBe('slot')
    expect(currentStage(queued)).toBe('queued')
    expect(currentStage(reviewing)).toBe('review')
    expect(currentStage(failed)).toBe('done')
    expect(currentStage(task('new'))).toBe('queued')
  })

  it('shows the newest done Tasks first', () => {
    const columns = boardColumns([merged, failed, waitingForSlot, queued, reviewing])
    expect(columns.done.map((t) => t.metadata.name)).toEqual(['qr-scanner-v1', 'image-shrink-v1'])
    expect(columns.slot.map((t) => t.metadata.name)).toEqual(['doc-scanner-v1'])
    expect(columns.review.map((t) => t.metadata.name)).toEqual(['qr-scanner-v2'])
  })

  it('queues for a slot longest waiter first', () => {
    const later = task('later', { phase: 'Pending', slotWaitSince: at(20) })
    expect(slotQueue([later, waitingForSlot, merged]).map((t) => t.metadata.name)).toEqual(['doc-scanner-v1', 'later'])
  })
})

describe('factoryStats', () => {
  const failed = task('qr-scanner-v1', { phase: 'Failed', round: 4, completedAt: at(240) })
  const firstRound = task('qr-scanner-v2', { phase: 'Merged', round: 1, completedAt: at(20) })
  const active = task('bg-eraser-v1', { phase: 'Implementing', round: 1 })

  it('counts outcomes, rounds and lead time', () => {
    const stats = factoryStats([merged, failed, firstRound, active], T0)
    expect(stats).toMatchObject({ total: 4, active: 1, merged: 2, failed: 1, meanRounds: 1.5, firstRoundRate: 0.5 })
    expect(stats.successRate).toBeCloseTo(2 / 3)
    expect(stats.medianLeadMs).toBe(70 * 60_000)
    expect(stats.withTimeline).toBe(1)
    expect(stats.timeByStage.implement).toBe(35 * 60_000)
  })

  it('has no rates before anything finished', () => {
    const stats = factoryStats([active], T0)
    expect(stats.successRate).toBeUndefined()
    expect(stats.medianLeadMs).toBeUndefined()
  })
})

describe('attention', () => {
  const health = (patch: Partial<FactoryHealth> = {}): FactoryHealth => ({
    generatedAt: at(0),
    state: 'ok',
    metrics: true,
    nodes: [
      {
        name: 'kind',
        ready: true,
        pressure: [],
        cpuAllocatableMilli: 12000,
        memoryAllocatableBytes: 16 * 2 ** 30,
        cpuMilli: 150,
        memoryBytes: 2 ** 30,
      },
    ],
    components: [],
    agents: { maxSandboxes: 1, sandboxes: [] },
    errors: [],
    ...patch,
  })

  it('is empty when all is well', () => {
    expect(attention({ tasks: [merged], containers: [], sessions: [], health: health(), now: T0 })).toEqual([])
  })

  it('names a component that is down, errors first', () => {
    const items = attention({
      tasks: [task('stuck', { phase: 'Checking', history: [step(0, 'Checking', 1, 'Checking')] })],
      containers: [],
      sessions: [],
      health: health({
        components: [
          {
            key: 'runner',
            name: 'CI runner',
            role: 'Runs make check on pull requests',
            namespace: 'gitea',
            optional: false,
            state: 'down',
            pods: [
              {
                name: 'r',
                namespace: 'gitea',
                phase: 'Running',
                ready: false,
                restarts: 9,
                restartedRecently: true,
                waiting: 'CrashLoopBackOff',
              },
            ],
          },
        ],
      }),
      now: T0 + 45 * 60_000,
    })
    expect(items.map((i) => [i.level, i.title])).toEqual([
      ['error', 'CI runner is down'],
      ['warning', 'Task toolbox/stuck has been waiting for CI for 45 min'],
    ])
    expect(items[0].detail).toContain('CrashLoopBackOff')
  })

  it('warns when the node runs out of memory', () => {
    const full = health()
    full.nodes[0].memoryBytes = 15 * 2 ** 30
    expect(attention({ tasks: [], containers: [], sessions: [], health: full, now: T0 })[0].title).toBe(
      'Node memory at 94%',
    )
  })

  it('lists recent failures, failed Containers and stale Sessions', () => {
    const failed = task('qr-scanner-v1', {
      phase: 'Failed',
      completedAt: at(0),
      conditions: [{ type: 'Succeeded', status: 'False', reason: 'RoundsExhausted', message: 'Failed after 4 rounds' }],
    })
    const container = {
      metadata: { name: 'web', namespace: 'webshop' },
      spec: {},
      status: {
        phase: 'Failed',
        conditions: [{ type: 'ChoicesValid', status: 'False', message: 'python-fastapi does not support static-site' }],
      },
    } as unknown as Container
    const session = {
      metadata: { name: 's1', namespace: 'webshop' },
      spec: { containerRef: { name: 'orders-api' }, actor: 'human' },
      status: { phase: 'Stale', lastHeartbeat: at(-5) },
    } as Session
    const items = attention({ tasks: [failed], containers: [container], sessions: [session], now: T0 + 60_000 })
    expect(items.map((i) => i.title)).toEqual([
      'Container webshop/web failed',
      'Task toolbox/qr-scanner-v1 failed',
      'Session webshop/s1 is stale',
    ])
    expect(items[1].detail).toBe('Failed after 4 rounds')
  })

  it('forgets failures older than a day', () => {
    const old = task('old', { phase: 'Failed', completedAt: at(0) })
    expect(attention({ tasks: [old], containers: [], sessions: [], now: T0 + 25 * 60 * 60_000 })).toEqual([])
  })
})

describe('formatDuration', () => {
  it.each([
    [45_000, '45 s'],
    [3 * 60_000, '3 min'],
    [125 * 60_000, '2 h 5 min'],
    [120 * 60_000, '2 h'],
    [76 * 60 * 60_000, '3 d 4 h'],
  ])('formats %d ms as %s', (ms, want) => expect(formatDuration(ms)).toBe(want))
})

describe('timelineSteps', () => {
  it('joins steps of the same stage and round', () => {
    const steps = timelineSteps(
      taskSegments(
        task('t', {
          phase: 'Checking',
          history: [
            { ...step(0, 'Implementing', 1, 'Running'), message: 'Starting round 1' },
            { ...step(1, 'Implementing', 1, 'Waiting'), message: 'The agent is working on round 1' },
            step(20, 'Checking', 1, 'Checking'),
          ],
        }),
        T0 + 30 * 60_000,
      ),
    )
    expect(steps.map((s) => [s.stage, (s.end - s.start) / 60_000, s.messages])).toEqual([
      ['implement', 20, ['Starting round 1', 'The agent is working on round 1']],
      ['checks', 10, []],
    ])
    expect(steps[1].current).toBe(true)
  })
})
