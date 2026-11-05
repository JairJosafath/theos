import { describe, expect, it } from 'vitest'
import type { Brief, Condition, Container, ContainerDeployStatus, Crew, Task } from '../k8s/types'
import { demoStory } from '../demo/fixture'
import { needs } from './needs'
import { defaultStage, loopStages, selectedStage, taskStage, type LoopInput, type StageKey } from './loop'

const T = '2026-06-01T10:00:00Z'

const brief = (name: string, phase?: NonNullable<Brief['status']>['phase'], round?: number): Brief => ({
  metadata: { name, namespace: 'shop' },
  spec: { idea: `Idea ${name}` },
  status: phase ? { phase, round } : undefined,
})

const task = (name: string, kind: Task['spec']['kind'], phase?: NonNullable<Task['status']>['phase']): Task => ({
  metadata: { name, namespace: 'shop' },
  spec: { kind, containerRef: { name: 'api' }, agentRef: { name: 'claude' }, issue: 7 },
  status: phase ? { phase, issueTitle: `Title of ${name}` } : undefined,
})

const container = (
  deploys: ContainerDeployStatus[] = [],
  entries: Container['spec']['deploy'] = deploys.map((d) => ({ target: d.target })),
  conditions: Condition[] = [],
): Container => ({
  metadata: { name: 'api', namespace: 'shop' },
  spec: {
    softwareSystemRef: { name: 'store' },
    type: 'service',
    gitProviderRef: { name: 'gitea' },
    frameworkRef: { name: 'go' },
    deploy: entries,
  },
  status: { deploys, conditions },
})

const crew = (run?: 'Waiting' | 'Running' | 'Succeeded' | 'Failed'): Crew => ({
  metadata: { name: 'api', namespace: 'shop' },
  spec: { containerRef: { name: 'api' }, implementer: { name: 'claude' }, reviewer: { name: 'claude' } },
  status: run ? { lastRelease: { tag: 'v1.2.0', commit: 'abc', time: T, run, runURL: 'https://git/run/1' } } : {},
})

const none: LoopInput = { briefs: [], tasks: [], containers: [], crews: [] }
const names = (input: Partial<LoopInput>, key: StageKey) =>
  loopStages({ ...none, ...input })
    .find((s) => s.key === key)!
    .items.map((i) => i.name)

describe('loopStages', () => {
  it('has the ten stages in the order of the cycle, the two reviews of a person', () => {
    const stages = loopStages(none)
    expect(stages.map((s) => s.label)).toEqual([
      'Idea or bug',
      'Triage',
      'Spec',
      'Reviewing the spec',
      'Implement',
      'Reviewing the code',
      'Verify',
      'Reviewing the product',
      'Ship',
      'Monitor',
    ])
    expect(stages.map((s) => s.number)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10])
    expect(stages.filter((s) => s.person).map((s) => s.key)).toEqual(['specReview', 'productReview'])
  })

  it('shows an empty project as ten stages with nothing in them', () => {
    const stages = loopStages(none)
    expect(stages.every((s) => s.count === 0 && s.items.length === 0)).toBe(true)
    expect(stages.at(-1)?.healthy).toBe(0)
  })

  it('puts Briefs that are Pending or Planning at Idea or bug, and no others', () => {
    const briefs = [
      brief('queued'),
      brief('pending', 'Pending'),
      brief('planning', 'Planning', 2),
      brief('applying', 'Applying'),
      brief('planned', 'Planned'),
      brief('asks', 'NeedsInput'),
      brief('failed', 'Failed'),
    ]
    expect(names({ briefs }, 'idea')).toEqual(['pending', 'planning', 'queued'])
    const item = loopStages({ ...none, briefs }).find((s) => s.key === 'idea')!.items[1]
    expect(item).toMatchObject({ kind: 'Brief', title: 'Idea planning', detail: 'Planning, round 2' })
    expect(item.page).toEqual({ ns: 'shop', kind: 'Brief', name: 'planning' })
  })

  it('puts Triage Tasks that have not finished at Triage', () => {
    const tasks = [
      task('t-pending', 'Triage', 'Pending'),
      task('t-running', 'Triage', 'Implementing'),
      task('t-new', 'Triage'),
      task('t-merged', 'Triage', 'Merged'),
      task('t-failed', 'Triage', 'Failed'),
    ]
    expect(names({ tasks }, 'triage')).toEqual(['t-new', 't-pending', 't-running'])
  })

  it('puts Spec Tasks before AwaitingApproval at Spec and those in it at Reviewing the spec', () => {
    const tasks = [
      task('s-new', 'Spec'),
      task('s-writing', 'Spec', 'Implementing'),
      task('s-checking', 'Spec', 'Checking'),
      task('s-review', 'Spec', 'AwaitingApproval'),
      task('s-merged', 'Spec', 'Merged'),
      task('s-failed', 'Spec', 'Failed'),
    ]
    expect(names({ tasks }, 'spec')).toEqual(['s-checking', 's-new', 's-writing'])
    const review = loopStages({ ...none, tasks }).find((s) => s.key === 'specReview')!
    expect(review.items.map((i) => i.name)).toEqual(['s-review'])
    expect(review.items[0].page).toEqual({ ns: 'shop', kind: 'Task', name: 's-review' })
    expect(review.items[0].title).toBe('Title of s-review')
  })

  it('splits Implement Tasks, or Tasks with no kind, by phase', () => {
    const tasks = [
      task('i-new', undefined),
      task('i-pending', 'Implement', 'Pending'),
      task('i-writing', undefined, 'Implementing'),
      task('i-checking', 'Implement', 'Checking'),
      task('i-blocked', 'Implement', 'Blocked'),
      task('i-review', undefined, 'Reviewing'),
      task('i-verify', 'Implement', 'Verifying'),
      task('i-merged', 'Implement', 'Merged'),
      task('i-failed', undefined, 'Failed'),
      task('plan', 'Plan', 'Implementing'),
    ]
    expect(names({ tasks }, 'implement')).toEqual(['i-blocked', 'i-checking', 'i-new', 'i-pending', 'i-writing'])
    expect(names({ tasks }, 'codeReview')).toEqual(['i-review'])
    expect(names({ tasks }, 'verify')).toEqual(['i-verify'])
    expect(taskStage(tasks.at(-1)!)).toBeUndefined()
  })

  it('puts a Container deploy waiting for approval at Reviewing the product, as Needs you does', () => {
    const waiting = container(
      [
        { target: 'staging', phase: 'Deployed', health: 'Healthy', version: 'v1', wantedVersion: 'v1' },
        { target: 'prod', phase: 'Waiting', reason: 'WaitingForApproval', wantedVersion: 'v2', version: 'v1' },
      ],
      [{ target: 'staging' }, { target: 'prod', after: 'staging' }],
    )
    const stages = loopStages({ ...none, containers: [waiting] })
    const review = stages.find((s) => s.key === 'productReview')!
    expect(review.items).toHaveLength(1)
    expect(review.items[0]).toMatchObject({ kind: 'Container', name: 'api' })
    expect(review.items[0].title).toContain('v2 waits for approval at prod')
    // The entry that waits is not also shipping.
    expect(stages.find((s) => s.key === 'ship')!.items).toEqual([])
    // Needs you lists the same approval, from the same logic.
    const approvals = needs({ projects: [], briefs: [], crews: [], tasks: [], containers: [waiting] }).filter((n) =>
      n.key.includes('|Approve|'),
    )
    expect(approvals.map((n) => n.key)).toEqual(['Container|shop|api|Approve|prod'])
    expect(review.items.map((i) => i.key)).toEqual(['Container|shop|api|Approve|prod'])
  })

  it('counts a deploy that waits with no entry naming it once, from Deployed', () => {
    const c = container(
      [],
      [],
      [{ type: 'Deployed', status: 'False', reason: 'WaitingForApproval', message: 'waits', lastTransitionTime: T }],
    )
    expect(names({ containers: [c] }, 'productReview')).toEqual(['api'])
  })

  it('puts Crews whose release run is Waiting or Running, and deploys on their way, at Ship', () => {
    const deploying = container(
      [
        { target: 'staging', phase: 'Deploying', wantedVersion: 'v2', url: 'https://staging.example' },
        { target: 'prod', phase: 'Waiting', reason: 'WaitingForEarlier', wantedVersion: 'v2' },
        { target: 'qa', phase: 'Deployed', health: 'Healthy', version: 'v2' },
        { target: 'dev', phase: 'Failed', version: 'v1' },
      ],
      [{ target: 'staging' }, { target: 'prod', after: 'staging' }, { target: 'qa' }, { target: 'dev' }],
    )
    const stages = loopStages({ ...none, containers: [deploying], crews: [crew('Running')] })
    const ship = stages.find((s) => s.key === 'ship')!
    expect(ship.items.map((i) => i.key)).toEqual([
      'Container|shop|api|Ship|staging',
      'Container|shop|api|Ship|prod',
      'Crew|shop|api',
    ])
    expect(ship.items[0].links).toEqual([{ label: 'At staging', url: 'https://staging.example' }])
    expect(ship.items[2].links).toEqual([{ label: 'The run', url: 'https://git/run/1' }])
    expect(names({ crews: [crew('Waiting')] }, 'ship')).toEqual(['api'])
    expect(names({ crews: [crew('Succeeded')] }, 'ship')).toEqual([])
    expect(names({ crews: [crew('Failed')] }, 'ship')).toEqual([])
    expect(names({ crews: [crew()] }, 'ship')).toEqual([])
  })

  it('shows a degraded or failed deploy at Monitor with its bug, and counts the healthy', () => {
    const c = container([
      {
        target: 'prod',
        phase: 'Deployed',
        health: 'Degraded',
        version: 'v2',
        issue: { number: 31, version: 'v2', state: 'Degraded', url: 'https://git/shop/api/issues/31' },
      },
      { target: 'staging', phase: 'Deployed', health: 'Healthy', version: 'v2' },
      { target: 'qa', phase: 'Deployed', health: 'Healthy', version: 'v2' },
      {
        target: 'dev',
        phase: 'Failed',
        wantedVersion: 'v3',
        issue: { number: 32, version: 'v3', state: 'Failed', url: 'https://git/shop/api/issues/32' },
      },
      { target: 'lab', phase: 'Deployed', health: 'Degraded', version: 'v2' },
      { target: 'new', phase: 'Deploying', health: 'Progressing' },
    ])
    const monitor = loopStages({ ...none, containers: [c] }).at(-1)!
    expect(monitor.healthy).toBe(2)
    expect(monitor.count).toBe(3)
    expect(monitor.items.map((i) => i.key)).toEqual([
      'Container|shop|api|Monitor|prod',
      'Container|shop|api|Monitor|dev',
      'Container|shop|api|Monitor|lab',
    ])
    expect(monitor.items.every((i) => i.problem)).toBe(true)
    expect(monitor.items[0].detail).toBe('Bug #31 filed for v2, Degraded')
    expect(monitor.items[0].links).toEqual([{ label: 'Bug #31', url: 'https://git/shop/api/issues/31' }])
    expect(monitor.items[1].title).toContain('v3 failed at dev')
    expect(monitor.items[1].links[0].url).toBe('https://git/shop/api/issues/32')
    expect(monitor.items[2].detail).toBe('No bug filed yet')
    expect(monitor.items[2].links).toEqual([])
  })

  it('counts each stage as its items', () => {
    const stages = loopStages({
      ...none,
      briefs: [brief('a', 'Pending')],
      tasks: [task('x', 'Spec', 'AwaitingApproval'), task('y', undefined, 'Reviewing')],
    })
    expect(Object.fromEntries(stages.map((s) => [s.key, s.count]))).toMatchObject({
      idea: 1,
      specReview: 1,
      codeReview: 1,
      implement: 0,
    })
  })

  it('has something at every stage in the demo project', () => {
    const data = demoStory(Date.parse('2026-06-01T12:00:00Z'))
    const stages = loopStages(data)
    expect(stages.filter((s) => s.count === 0).map((s) => s.label)).toEqual([])
    expect(stages.at(-1)?.healthy).toBeGreaterThan(0)
    expect(stages.at(-1)?.items[0].links[0].label).toBe('Bug #19')
  })
})

// The Loop opens on one stage beside the ring (docs/plans/done/ui-shell.md, step 2).
describe('defaultStage', () => {
  it('opens on the first review point of a person that has items', () => {
    const stages = loopStages({
      ...none,
      briefs: [brief('a', 'Pending')],
      tasks: [task('x', 'Spec', 'AwaitingApproval'), task('y', undefined, 'Reviewing')],
    })
    expect(defaultStage(stages)).toBe('specReview')
  })

  it('skips a review point with nothing in it for the first stage with items', () => {
    const stages = loopStages({
      ...none,
      tasks: [task('y', undefined, 'Implementing'), task('z', undefined, 'Verifying')],
    })
    expect(defaultStage(stages)).toBe('implement')
  })

  it('opens on the first stage when nothing is in flight', () => {
    expect(defaultStage(loopStages(none))).toBe('idea')
    expect(defaultStage([])).toBeUndefined()
  })

  it('opens on the stage the address names, if the loop has it', () => {
    const stages = loopStages({ ...none, tasks: [task('x', 'Spec', 'AwaitingApproval')] })
    expect(selectedStage(stages, 'monitor')).toBe('monitor')
    expect(selectedStage(stages, 'nowhere')).toBe('specReview')
    expect(selectedStage(stages, undefined)).toBe('specReview')
  })
})
