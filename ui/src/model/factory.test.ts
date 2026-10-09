import { describe, expect, it } from 'vitest'
import { demoFactory, demoHealth } from '../demo/factoryFixture'
import type { FactoryHealth } from '../k8s/health'
import type { Agent, Brief, Container, Crew, Session, Task, TaskTransition } from '../k8s/types'
import { attention, briefOn, recentStall } from './attention'
import { activeCrewTasks, crewDoing, crewSummaries, crewSummary } from './crews'
import {
  agentBackAt,
  agentRole,
  boardColumns,
  currentStage,
  factoryStats,
  followUpLinks,
  stageOf,
  standardChangeLink,
  slotQueue,
  taskHeadline,
  taskRoles,
  taskSegments,
  timeByStage,
  timelineSteps,
} from './factory'
import { formatClock, formatDuration } from './format'

const T0 = Date.parse('2026-10-01T12:00:00Z')
const at = (min: number) => new Date(T0 + min * 60_000).toISOString()

function task(name: string, status: Task['status'] = {}, created = at(0)): Task {
  return {
    metadata: { name, namespace: 'apps', creationTimestamp: created },
    spec: { containerRef: { name: 'image-shrink' }, agentRef: { name: 'claude' }, goal: 'Build it' },
    status,
  }
}

/** A Task a Crew started from issue 3 of cel-language-server. */
function issueTask(status: Task['status'] = {}): Task {
  return {
    metadata: {
      name: 'cel-language-server-3',
      namespace: 'tools',
      creationTimestamp: at(0),
      labels: { 'c4.factory.io/crew': 'cel-language-server', 'c4.factory.io/issue': '3' },
    },
    spec: {
      containerRef: { name: 'cel-language-server' },
      agentRef: { name: 'claude-implementer' },
      reviewerRef: { name: 'claude-reviewer' },
      issue: 3,
    },
    status,
  }
}

function agent(name: string, spec: Partial<Agent['spec']> = {}, namespace = 'tools'): Agent {
  return { metadata: { name, namespace }, spec: { image: 'c4factory/agent-go:dev', ...spec } }
}

const agents = [
  agent('claude-implementer', {
    displayName: 'Claude Code, Sonnet',
    harness: 'claude-code',
    model: 'claude-sonnet-5-5',
  }),
  agent('claude-reviewer', { displayName: 'Claude Code, Opus', model: 'claude-opus-5-5' }),
  agent('stand-in', { harness: 'stand-in' }),
  agent('claude', { displayName: 'Claude Code' }),
  agent('claude-implementer', { model: 'claude-haiku-5-5' }, 'elsewhere'),
]

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
      blocked: 0,
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
      ['warning', 'Task apps/stuck has been waiting for CI for 45 min'],
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
      'Task apps/qr-scanner-v1 failed',
      'Session webshop/s1 is stale',
    ])
    expect(items[1].detail).toBe('Failed after 4 rounds')
  })

  it('lists Tasks waiting for the agent instead of calling them stuck', () => {
    const now = new Date(2026, 9, 2, 14, 0).getTime()
    const waiting = issueTask({
      phase: 'Implementing',
      round: 2,
      agentBackAt: new Date(2026, 9, 2, 15, 4).toISOString(),
      history: [{ at: new Date(now - 5 * 60 * 60_000).toISOString(), phase: 'Implementing', round: 2 }],
      conditions: [
        {
          type: 'Progressing',
          status: 'True',
          reason: 'AgentUnavailable',
          message: 'The agent hit a usage limit; trying again at 2026-10-02T13:04:00Z',
        },
      ],
    })
    const items = attention({ tasks: [waiting], containers: [], sessions: [], now })
    expect(items).toEqual([
      {
        level: 'warning',
        title: 'Task tools/cel-language-server-3 is waiting for the agent until 15:04',
        detail: 'The agent hit a usage limit; trying again at 2026-10-02T13:04:00Z',
        task: 'tools/cel-language-server-3',
      },
    ])
  })

  it('lists Crews whose Ready condition is False, with its message', () => {
    const blocked = crew(
      'vscode-extension',
      {},
      {
        phase: 'Waiting',
        conditions: [
          {
            type: 'Ready',
            status: 'False',
            reason: 'ContainerNotReady',
            message: 'Waiting for Container vscode-extension to be Ready',
          },
        ],
      },
    )
    const fine = crew('cel-language-server', {}, { phase: 'Idle', conditions: [{ type: 'Ready', status: 'True' }] })
    const paused = crew('paused', { paused: true }, { phase: 'Paused' })
    const items = attention({ tasks: [], containers: [], sessions: [], crews: [fine, blocked, paused], now: T0 })
    expect(items).toEqual([
      {
        level: 'warning',
        title: 'Crew tools/vscode-extension can’t pick up issues',
        detail: 'Waiting for Container vscode-extension to be Ready',
      },
    ])
  })

  it('names the Brief that watches an item, and when it acts', () => {
    const now = new Date(2026, 9, 2, 12, 0).getTime()
    const watcher: Brief = {
      metadata: { name: 'cel-lens', namespace: 'tools' },
      spec: { idea: 'A CEL lens', autopilot: 'On' },
      status: {
        attention: [
          {
            kind: 'Task',
            name: 'cel-language-server-3',
            reason: 'Its agent is back after a usage limit.',
            actsAt: new Date(2026, 9, 2, 14, 0).toISOString(),
          },
          { kind: 'Container', name: 'quiet', reason: 'It acts once this changes.' },
        ],
        needsYou: [{ kind: 'Crew', name: 'vscode-extension', question: 'Its Agent is missing.' }],
      },
    }
    // Same names in another namespace are another Brief's business.
    const elsewhere: Brief = { ...watcher, metadata: { name: 'other', namespace: 'apps' } }
    const waiting = issueTask({
      phase: 'Implementing',
      agentBackAt: new Date(2026, 9, 2, 13, 0).toISOString(),
      conditions: [{ type: 'Progressing', status: 'True', reason: 'AgentUnavailable', message: 'Usage limit' }],
    })
    const blocked = crew(
      'vscode-extension',
      {},
      { conditions: [{ type: 'Ready', status: 'False', reason: 'AgentNotFound', message: 'No Agent' }] },
    )
    const items = attention({
      tasks: [waiting],
      containers: [],
      sessions: [],
      crews: [blocked],
      briefs: [elsewhere, watcher],
      now,
    })
    expect(items.map((i) => i.brief)).toEqual([
      { name: 'cel-lens', note: 'On Brief cel-lens’s Needs you list.' },
      { name: 'cel-lens', note: 'Brief cel-lens acts after 14:00 if this hasn’t healed.' },
    ])
    expect(briefOn([watcher], 'Container', 'tools', 'quiet', now)).toEqual({
      name: 'cel-lens',
      note: 'Brief cel-lens leaves it alone until it changes.',
    })
    // A Task by that name is not the Container; nothing names an item no Brief lists.
    expect(briefOn([watcher], 'Task', 'tools', 'quiet', now)).toBeUndefined()
    expect(attention({ tasks: [waiting], containers: [], sessions: [], now })[0].brief).toBeUndefined()
  })

  it('drops a failure a newer Task for the same issue answered, one line per issue', () => {
    const attempt = (name: string, created: number, status: Task['status']): Task => {
      const t = issueTask(status)
      return { ...t, metadata: { ...t.metadata, name, creationTimestamp: at(created) } }
    }
    const failed = (min: number): Task['status'] => ({ phase: 'Failed', completedAt: at(min) })
    const first = attempt('cel-language-server-3', 0, failed(10))
    const second = attempt('cel-language-server-3-2', 20, failed(30))
    const third = attempt('cel-language-server-3-3', 40, failed(50))
    const items = attention({ tasks: [third, first, second], containers: [], sessions: [], now: T0 + 60 * 60_000 })
    expect(items.map((i) => i.task)).toEqual(['tools/cel-language-server-3-3'])

    // A retry that is still running answers the failure, whatever its phase.
    const retry = attempt('cel-language-server-3-4', 55, { phase: 'Implementing' })
    const all = attention({ tasks: [first, second, third, retry], containers: [], sessions: [], now: T0 + 60 * 60_000 })
    expect(all.filter((i) => i.title.endsWith('failed'))).toEqual([])

    // Another issue, another Container or another namespace is not an answer.
    const otherIssue: Task = { ...retry, spec: { ...retry.spec, issue: 4 } }
    const otherNamespace: Task = { ...retry, metadata: { ...retry.metadata, namespace: 'apps' } }
    const goal = task('later', {}, at(55))
    expect(
      attention({
        tasks: [first, otherIssue, otherNamespace, goal],
        containers: [],
        sessions: [],
        now: T0 + 60 * 60_000,
      }).map((i) => i.task),
    ).toContain('tools/cel-language-server-3')
  })

  it('drops a failure a Brief answered with a retry or a close, and keeps an unanswered one', () => {
    const failed = issueTask({ phase: 'Failed', completedAt: at(10) })
    const tended = (answer: 'retry' | 'close' | 'wait' | 'ask', min: number): Brief => ({
      metadata: { name: 'cel-lens', namespace: 'tools' },
      spec: { idea: 'A CEL lens' },
      status: { tended: [{ container: 'cel-language-server', number: 3, answer, at: at(min), round: 2 }] },
    })
    const list = (briefs: Brief[]) =>
      attention({ tasks: [failed], containers: [], sessions: [], briefs, now: T0 + 60 * 60_000 }).map((i) => i.task)
    expect(list([tended('close', 20)])).toEqual([])
    expect(list([tended('retry', 20)])).toEqual([])
    // Waiting or asking still needs the person; an answer from before this failure was about an earlier one.
    expect(list([tended('wait', 20)])).toEqual(['tools/cel-language-server-3'])
    expect(list([tended('ask', 20)])).toEqual(['tools/cel-language-server-3'])
    expect(list([tended('close', 5)])).toEqual(['tools/cel-language-server-3'])
    expect(list([])).toEqual(['tools/cel-language-server-3'])
  })

  it('forgets failures older than a day', () => {
    const old = task('old', { phase: 'Failed', completedAt: at(0) })
    expect(attention({ tasks: [old], containers: [], sessions: [], now: T0 + 25 * 60 * 60_000 })).toEqual([])
  })
})

describe('taskHeadline', () => {
  it('names the issue with its title, linking to it', () => {
    const t = issueTask({
      issueTitle: 'Map positions inside YAML scalars',
      issueURL: 'http://gitea/tools/x/issues/3',
    })
    expect(taskHeadline(t)).toEqual({
      text: '#3 Map positions inside YAML scalars',
      issue: 3,
      url: 'http://gitea/tools/x/issues/3',
    })
  })

  it('names the issue by number before the operator has read it', () => {
    expect(taskHeadline(issueTask())).toEqual({ text: 'Issue #3', issue: 3, url: undefined })
  })

  it("takes a goal's first line", () => {
    const t = task('t')
    t.spec.goal = 'Build version 1 of Image Shrink\n- Pick photos'
    expect(taskHeadline(t)).toEqual({ text: 'Build version 1 of Image Shrink' })
  })
})

describe('taskRoles', () => {
  it('names the implementer and the reviewer with their models', () => {
    const { implementer, reviewer } = taskRoles(issueTask(), agents)
    expect(implementer).toEqual({
      name: 'claude-implementer',
      title: 'Claude Code, Sonnet',
      model: 'claude-sonnet-5-5',
      found: true,
    })
    expect(reviewer).toMatchObject({ title: 'Claude Code, Opus', model: 'claude-opus-5-5' })
  })

  it('lets the implementer review when no reviewer is named', () => {
    const t = task('t')
    t.metadata.namespace = 'tools'
    t.spec.agentRef = { name: 'claude' }
    const { implementer, reviewer } = taskRoles(t, agents)
    expect(reviewer).toEqual(implementer)
    // No model: the harness's default.
    expect(reviewer).toEqual({ name: 'claude', title: 'Claude Code', model: undefined, found: true })
  })

  it("says stand-in for the scripted agent, and keeps a missing Agent's name", () => {
    expect(agentRole(agents, 'tools', 'stand-in').model).toBe('stand-in')
    expect(agentRole(agents, 'tools', 'gone')).toEqual({ name: 'gone', title: 'gone', found: false })
  })

  it("looks in the Task's own namespace", () => {
    expect(agentRole(agents, 'elsewhere', 'claude-implementer').model).toBe('claude-haiku-5-5')
  })
})

describe('agentBackAt', () => {
  const waiting = (status: Task['status']) =>
    issueTask({
      phase: 'Implementing',
      round: 2,
      agentBackAt: at(38),
      conditions: [{ type: 'Progressing', status: 'True', reason: 'AgentUnavailable', message: 'Usage limit' }],
      ...status,
    })

  it('says when the agent is tried again', () => {
    expect(agentBackAt(waiting({}))).toBe(T0 + 38 * 60_000)
  })

  it('is undefined once the Task does something else, or is done', () => {
    const slot = waiting({
      conditions: [{ type: 'Progressing', status: 'True', reason: 'WaitingForSlot', message: '' }],
    })
    expect(agentBackAt(slot)).toBeUndefined()
    expect(agentBackAt(waiting({ phase: 'Failed' }))).toBeUndefined()
    expect(agentBackAt(task('t'))).toBeUndefined()
  })
})

describe('followUpLinks', () => {
  it('labels issue addresses by number', () => {
    const t = issueTask({
      followUps: ['http://gitea/tools/cel-language-server/issues/15', 'http://gitea/somewhere/else'],
    })
    expect(followUpLinks(t)).toEqual([
      { url: 'http://gitea/tools/cel-language-server/issues/15', label: '#15' },
      { url: 'http://gitea/somewhere/else', label: 'http://gitea/somewhere/else' },
    ])
    expect(followUpLinks(task('t'))).toEqual([])
  })
})

function crew(name: string, spec: Partial<Crew['spec']> = {}, status: Crew['status'] = {}): Crew {
  return {
    metadata: { name, namespace: 'tools' },
    spec: {
      containerRef: { name },
      implementer: { name: 'claude-implementer' },
      reviewer: { name: 'claude-reviewer' },
      ...spec,
    },
    status,
  }
}

describe('crewSummary', () => {
  const working = crew(
    'cel-language-server',
    { displayName: 'Language server crew' },
    {
      phase: 'Working',
      activeTasks: ['cel-language-server-3'],
      ready: [5],
      waiting: [6, 7],
      lastPolled: at(-1),
      conditions: [
        { type: 'Ready', status: 'True', reason: 'Working', message: 'Task cel-language-server-3 is working' },
      ],
    },
  )

  it('names its Agents with their models, and the issue it works on', () => {
    const tasks = [issueTask({ issueTitle: 'Map positions inside YAML scalars' })]
    expect(crewSummary(working, agents, tasks)).toEqual({
      key: 'tools/cel-language-server',
      namespace: 'tools',
      name: 'cel-language-server',
      title: 'Language server crew',
      description: undefined,
      container: 'cel-language-server',
      phase: 'Working',
      paused: false,
      notReady: undefined,
      implementer: {
        name: 'claude-implementer',
        title: 'Claude Code, Sonnet',
        model: 'claude-sonnet-5-5',
        found: true,
      },
      reviewer: { name: 'claude-reviewer', title: 'Claude Code, Opus', model: 'claude-opus-5-5', found: true },
      parallel: 1,
      active: [
        {
          name: 'cel-language-server-3',
          key: 'tools/cel-language-server-3',
          issue: 3,
          headline: '#3 Map positions inside YAML scalars',
          found: true,
        },
      ],
      ready: [5],
      waiting: [6, 7],
      lastPolled: at(-1),
    })
    expect(crewDoing(crewSummary(working, agents, tasks))).toBe('working on #3')
  })

  it('keeps the active Task by name before it is seen', () => {
    const summary = crewSummary(working, agents, [])
    expect(summary.active).toEqual([
      {
        name: 'cel-language-server-3',
        key: 'tools/cel-language-server-3',
        issue: undefined,
        headline: undefined,
        found: false,
      },
    ])
    expect(crewDoing(summary)).toBe('working on cel-language-server-3')
  })

  it('lists every active Task of a parallel Crew', () => {
    const parallel = crew(
      'cel-language-server',
      { parallel: 2 },
      { phase: 'Working', activeTasks: ['cel-language-server-3', 'cel-language-server-4'] },
    )
    const four = { ...issueTask(), metadata: { ...issueTask().metadata, name: 'cel-language-server-4' } }
    four.spec.issue = 4
    const summary = crewSummary(parallel, agents, [issueTask(), four])
    expect(summary.parallel).toBe(2)
    expect(summary.active.map((a) => a.issue)).toEqual([3, 4])
    expect(crewDoing(summary)).toBe('working on #3, #4')
    expect(crewSummary(crew('x', { parallel: 0 }), agents, []).parallel).toBe(1)
  })

  it('marks a Crew that is paused or not Ready', () => {
    const paused = crewSummary(crew('vscode-extension', { paused: true }, { phase: 'Paused' }), agents, [])
    expect(paused).toMatchObject({ paused: true, notReady: undefined, active: [], parallel: 1, ready: [], waiting: [] })
    expect(crewDoing(paused)).toBe('paused')

    const blocked = crewSummary(
      crew(
        'vscode-extension',
        { reviewer: { name: 'gone' } },
        {
          phase: 'Waiting',
          conditions: [{ type: 'Ready', status: 'False', reason: 'AgentNotFound', message: 'Agent gone not found' }],
        },
      ),
      agents,
      [],
    )
    expect(blocked.notReady).toEqual({ reason: 'AgentNotFound', message: 'Agent gone not found' })
    expect(blocked.reviewer).toEqual({ name: 'gone', title: 'gone', found: false })
    expect(crewDoing(blocked)).toBe('can’t pick up issues')
  })

  it('says why an idle Crew has nothing to do', () => {
    const idle = (waiting: number[]) => crewSummary(crew('x', {}, { phase: 'Idle', waiting }), agents, [])
    expect(crewDoing(idle([]))).toBe('no ready issues')
    expect(crewDoing(idle([4]))).toBe('no ready issues; 1 waits on open dependencies')
    expect(crewDoing(idle([4, 5]))).toBe('no ready issues; 2 wait on open dependencies')
    expect(crewDoing(crewSummary(crew('x'), agents, []))).toBe('not started yet')
  })

  it('sorts Crews by project, then by title', () => {
    const other = crew('a-crew')
    other.metadata.namespace = 'zz'
    const names = crewSummaries([other, crew('vscode-extension'), working], agents, []).map((c) => c.key)
    expect(names).toEqual(['tools/cel-language-server', 'tools/vscode-extension', 'zz/a-crew'])
  })
})

describe('formatClock', () => {
  // Local times, as the browser shows them.
  const now = new Date(2026, 9, 2, 12, 0).getTime()

  it('shows a time today as hours and minutes', () => {
    expect(formatClock(new Date(2026, 9, 2, 15, 4).getTime(), now)).toBe('15:04')
    expect(formatClock(new Date(2026, 9, 2, 9, 5).getTime(), now)).toBe('09:05')
  })

  it('adds the weekday on another day', () => {
    expect(formatClock(new Date(2026, 9, 3, 3, 0).getTime(), now)).toBe('Sat 03:00')
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

  it.each([
    [2, '2 ms'],
    [999, '999 ms'],
    [1500, '1.5 s'],
    [90_000, '90.0 s'],
  ])('formats a fine %d ms as %s', (ms, want) => expect(formatDuration(ms, { fine: true })).toBe(want))
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

describe('recentStall', () => {
  it('finds the worst stall in the last 15 minutes', () => {
    const history = [
      { at: at(-30), apiMs: 9000 },
      { at: at(-10), apiMs: 6000 },
      { at: at(-5), apiMs: 10000, apiFailed: true },
      { at: at(-1), apiMs: 12 },
    ]
    expect(recentStall(history, T0)).toEqual({ at: at(-5), apiMs: 10000, failed: true })
    expect(recentStall([{ at: at(-1), apiMs: 40 }], T0)).toBeUndefined()
  })

  it('is on the attention list', () => {
    const items = attention({
      tasks: [],
      containers: [],
      sessions: [],
      health: {
        generatedAt: at(0),
        state: 'ok',
        metrics: false,
        nodes: [],
        components: [],
        agents: { maxSandboxes: 1, sandboxes: [] },
        errors: [],
        history: [{ at: at(-3), apiMs: 8000 }],
      },
      now: T0,
    })
    expect(items.map((i) => i.title)).toEqual(['The API server stalled for 8 s 3 min ago'])
  })
})

describe('the demo', () => {
  const data = demoFactory(T0)
  const crews = crewSummaries(data.crews, data.agents, data.tasks)

  it('has Crews working, paused and not Ready, with Sonnet and Opus', () => {
    expect(crews.map((c) => [c.key, c.phase, c.paused, Boolean(c.notReady)])).toEqual([
      ['webshop/catalog-api', 'Waiting', false, true],
      ['webshop/orders-api', 'Working', false, false],
      ['webshop/storefront', 'Paused', true, false],
    ])
    expect(crews[1]).toMatchObject({
      implementer: { model: 'claude-sonnet-5-5' },
      reviewer: { model: 'claude-opus-5-5' },
      parallel: 2,
      active: [
        { issue: 14, found: true },
        { issue: 15, found: true },
      ],
    })
  })

  it('has issue Tasks in several phases, with follow-ups and an agent wait', () => {
    const issueTasks = data.tasks.filter((t) => t.spec.issue)
    expect(new Set(issueTasks.map((t) => t.status?.phase))).toEqual(
      new Set(['Merged', 'Failed', 'Implementing', 'Checking']),
    )
    expect(issueTasks.some((t) => followUpLinks(t).length)).toBe(true)
    expect(issueTasks.filter((t) => agentBackAt(t) !== undefined).map((t) => t.metadata.name)).toEqual([
      'orders-api-14',
    ])
  })

  it('needs a look at the agent wait and the Crew that is not Ready', () => {
    const titles = attention({ ...data, health: demoHealth(T0), now: T0 }).map((i) => i.title)
    expect(titles).toContainEqual(expect.stringMatching(/^Task webshop\/orders-api-14 is waiting for the agent until /))
    expect(titles).toContain('Crew webshop/catalog-api can’t pick up issues')
  })

  it('names the webshop Brief on the agent wait', () => {
    const items = attention({ ...data, health: demoHealth(T0), now: T0 })
    const wait = items.find((i) => i.task === 'webshop/orders-api-14')
    expect(wait?.brief?.note).toMatch(/^Brief webshop acts after .* if this hasn’t healed\.$/)
  })
})

describe('activeCrewTasks', () => {
  const working = crew(
    'cel-language-server',
    {},
    { phase: 'Working', activeTasks: ['cel-language-server-4', 'cel-language-server-3', 'gone'] },
  )
  const issue = (n: number, status: Task['status']): Task => {
    const t = issueTask(status)
    t.metadata.name = `cel-language-server-${n}`
    t.spec.issue = n
    return t
  }
  const tasks = [
    issue(3, { phase: 'Implementing', issueTitle: 'Map positions' }),
    issue(4, { phase: 'Reviewing', issueTitle: 'Report diagnostics' }),
    issue(2, { phase: 'Merged' }),
  ]

  it('names the implementer while implementing and the reviewer while reviewing', () => {
    const found = activeCrewTasks([working], tasks, agents, ['cel-language-server'])
    expect(found.map((t) => [t.issue, t.issueTitle, t.phase, t.agent.name, t.agent.model])).toEqual([
      [3, 'Map positions', 'Implementing', 'claude-implementer', 'claude-sonnet-5-5'],
      [4, 'Report diagnostics', 'Reviewing', 'claude-reviewer', 'claude-opus-5-5'],
    ])
  })

  it('skips Tasks that are not active or not seen, and Containers out of scope', () => {
    expect(activeCrewTasks([working], tasks, agents, ['other'])).toEqual([])
    expect(activeCrewTasks([working], [], agents, ['cel-language-server'])).toEqual([])
    expect(activeCrewTasks([crew('cel-language-server')], tasks, agents, ['cel-language-server'])).toEqual([])
  })
})

describe('Blocked Tasks', () => {
  it('shows a Blocked Task in its own column, with the issue it waits on', () => {
    const change = {
      issue: 31,
      issueURL: 'http://gitea/tools/vscode-extension/issues/31',
      paths: ['Makefile'],
      count: 1,
    }
    const blocked = task('blocked-task', { phase: 'Blocked', standardChange: change })
    expect(stageOf({ phase: 'Blocked' })).toBe('blocked')
    expect(boardColumns([blocked]).blocked).toEqual([blocked])
    expect(standardChangeLink(blocked)).toEqual({ label: '#31', url: change.issueURL, paths: ['Makefile'] })
    expect(standardChangeLink({ ...blocked, status: { ...blocked.status, phase: 'Implementing' } })).toBeUndefined()
  })
})
