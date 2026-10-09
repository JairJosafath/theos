import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Brief, Crew, Relationship, Task, TaskTransition } from '../../k8s/types'
import { agentRuns, briefTimeline, crewTasks, relationshipsOf, taskTimeline } from './rows'

describe('briefTimeline', () => {
  it('turns each round into an entry, with the question when it asked one', () => {
    const brief = {
      metadata: { name: 'shop' },
      spec: { idea: 'a shop' },
      status: {
        history: [
          { round: 1, outcome: 'Applied', summary: 'Planned the shop' },
          { round: 2, outcome: 'PlannerNeedsInput', summary: 'Asked', needsInput: 'Which payment provider?' },
          { round: 3, outcome: 'RoundFailed', summary: 'The plan did not validate' },
        ],
      },
    } as Brief
    expect(briefTimeline(brief)).toEqual([
      { key: '1', title: 'Round 1 applied', detail: 'Planned the shop', tone: 'ok' },
      { key: '2', title: 'Round 2 asked you', detail: 'Which payment provider?', tone: 'waiting' },
      { key: '3', title: 'Round 3 failed', detail: 'The plan did not validate', tone: 'error' },
    ])
  })
})

describe('relationshipsOf', () => {
  const rel = (name: string, source: string, target: string, applied?: [string, string]): Relationship => ({
    metadata: { name },
    spec: { source: { kind: 'Container', name: source }, target: { kind: 'DataStore', name: target } },
    status: applied ? { conditions: [{ type: 'Applied', status: applied[0] as 'True', reason: applied[1] }] } : {},
  })

  it('lists the Container’s Relationships with their Applied status and reason', () => {
    const rows = relationshipsOf(
      [
        rel('api-db', 'api', 'db', ['True', 'AppliedAtScaffold']),
        rel('api-cache', 'api', 'cache', ['False', 'AddedAfterScaffold']),
        rel('web-db', 'web', 'db'),
      ],
      'api',
    )
    expect(rows).toEqual([
      { kind: 'Relationship', name: 'api-db', note: 'to DataStore db · Applied: True, AppliedAtScaffold' },
      { kind: 'Relationship', name: 'api-cache', note: 'to DataStore cache · Applied: False, AddedAfterScaffold' },
    ])
  })
})

const step = (phase: TaskTransition['phase'], round: number, reason?: string, message?: string): TaskTransition => ({
  at: `2026-06-01T10:${String(round).padStart(2, '0')}:00Z`,
  phase,
  round,
  reason,
  message,
})

function task(name: string, status: Task['status'], labels?: Record<string, string>): Task {
  return {
    metadata: { name, namespace: 'shop', labels, creationTimestamp: status?.history?.[0]?.at },
    spec: { containerRef: { name: 'api' }, agentRef: { name: 'dev' }, reviewerRef: { name: 'rev' }, issue: 7 },
    status: { issueTitle: 'Refunds', ...status },
  }
}

describe('taskTimeline', () => {
  it('turns each history step into an entry, with its round, reason and message', () => {
    const t = task('api-7', {
      history: [
        step('Pending', 0, 'WaitingForSlot'),
        step('Implementing', 1, 'Running', 'Starting round 1'),
        step('Blocked', 1, 'StandardChange', 'Waits for #4'),
        step('Failed', 2, 'RoundsExhausted', 'Failed after 2 rounds'),
      ],
    })
    expect(taskTimeline(t).map(({ title, detail, tone }) => [title, detail, tone])).toEqual([
      ['Queued · WaitingForSlot', undefined, 'waiting'],
      ['Implementing, round 1 · Running', 'Starting round 1', 'ok'],
      ['Blocked, round 1 · StandardChange', 'Waits for #4', 'waiting'],
      ['Failed, round 2 · RoundsExhausted', 'Failed after 2 rounds', 'error'],
    ])
    expect(taskTimeline(t)[1].at).toBe('2026-06-01T10:01:00Z')
  })
})

describe('agentRuns', () => {
  it('starts a run at each implement or review step of a round, not at its later steps or a wait', () => {
    const t = task('api-7', {
      phase: 'Merged',
      history: [
        step('Implementing', 1, 'WaitingForSlot'),
        step('Implementing', 1, 'Running'),
        step('Implementing', 1, 'Waiting'),
        step('Checking', 1, 'Checking'),
        step('Reviewing', 1, 'Running'),
        step('Implementing', 2, 'NextRound'),
        step('Reviewing', 2, 'Running'),
        step('Merged', 2, 'Merged'),
      ],
    })
    expect(agentRuns(t).map((r) => [r.n, r.round, r.role, r.agent, r.current])).toEqual([
      [1, 1, 'implement', 'dev', false],
      [2, 1, 'review', 'rev', false],
      [3, 2, 'implement', 'dev', false],
      [4, 2, 'review', 'rev', false],
    ])
  })

  it('marks the run of the newest step as running while the Task does', () => {
    const t = task('api-7', {
      phase: 'Implementing',
      history: [step('Implementing', 1, 'Running'), step('Implementing', 1, 'ResolvingConflicts')],
    })
    expect(agentRuns(t)).toEqual([
      { n: 1, round: 1, role: 'implement', agent: 'dev', at: '2026-06-01T10:01:00Z', current: true },
    ])
    const checking = task('api-7', {
      phase: 'Checking',
      history: [step('Implementing', 1, 'Running'), step('Checking', 1, 'Checking')],
    })
    expect(agentRuns(checking)[0].current).toBe(false)
  })

  it('has the implementer review when the Task has no reviewer', () => {
    const t = task('api-7', { history: [step('Reviewing', 1, 'Running')] })
    delete t.spec.reviewerRef
    expect(agentRuns(t)[0].agent).toBe('dev')
  })
})

describe('crewTasks', () => {
  beforeEach(() => {
    vi.stubGlobal('window', { location: { pathname: '/', search: '?shell=next', hash: '' } })
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('lists the Crew’s Tasks in its namespace, newest first, each linking to its page', () => {
    const crew = {
      metadata: { name: 'api', namespace: 'shop' },
      spec: { containerRef: { name: 'api' }, implementer: { name: 'dev' }, reviewer: { name: 'rev' } },
    } as Crew
    const label = { 'c4.factory.io/crew': 'api' }
    const tasks = [
      task('api-6', { phase: 'Merged', history: [step('Implementing', 1)] }, label),
      task('api-7', { phase: 'Checking', history: [step('Implementing', 2)] }, label),
      task('web-1', { history: [step('Implementing', 3)] }, { 'c4.factory.io/crew': 'web' }),
      { ...task('api-8', {}, label), metadata: { name: 'api-8', namespace: 'other', labels: label } },
    ]
    expect(crewTasks(crew, tasks)).toEqual([
      {
        kind: 'Task',
        name: 'api-7',
        href: '/?shell=next&ns=shop&kind=Task&name=api-7',
        note: '#7 Refunds · Checking',
      },
      {
        kind: 'Task',
        name: 'api-6',
        href: '/?shell=next&ns=shop&kind=Task&name=api-6',
        note: '#7 Refunds · Merged',
      },
    ])
  })
})
