import { describe, expect, it } from 'vitest'
import type { StoryData } from '../k8s/sources'
import type { Brief, Container, Crew, CrewRelease, Task } from '../k8s/types'
import { crewReleases } from './releases'
import { storyline, type StoryStep } from './storyline'

const empty = (): StoryData => ({
  namespace: 'shop',
  people: [],
  systems: [],
  containers: [],
  dataStores: [],
  relationships: [],
  sessions: [],
  frameworks: [],
  connectors: [],
  standards: [],
  computes: [],
  deployments: [],
  gitProviders: [],
  deployTargets: [],
  bundles: [],
  domains: [],
  crews: [],
  agents: [],
  tasks: [],
  briefs: [],
})

const brief = (name: string, status: Brief['status']): Brief => ({
  metadata: { name, namespace: 'shop' },
  spec: { idea: 'A shop' },
  status,
})

const container = (name: string, status: Container['status']): Container => ({
  metadata: { name, namespace: 'shop' },
  spec: {
    softwareSystemRef: { name: 'store' },
    type: 'service',
    gitProviderRef: { name: 'gitea' },
    frameworkRef: { name: 'go' },
  },
  status,
})

const task = (name: string, phase: NonNullable<Task['status']>['phase']): Task => ({
  metadata: { name, namespace: 'shop' },
  spec: { containerRef: { name: 'api' }, agentRef: { name: 'claude' }, issue: 1 },
  status: { phase },
})

const release = (tag: string, time: string, run?: CrewRelease['run']): CrewRelease => ({
  tag,
  commit: 'abc',
  time,
  run,
})

const crew = (name: string, lastRelease?: CrewRelease, ns = 'shop'): Crew => ({
  metadata: { name, namespace: ns },
  spec: { containerRef: { name }, implementer: { name: 'claude' }, reviewer: { name: 'claude' } },
  status: { lastRelease },
})

const step = (data: StoryData, key: StoryStep['key']) => storyline(data).find((s) => s.key === key)!

describe('storyline', () => {
  it('names every step in order, each with nothing there yet', () => {
    const steps = storyline(empty())
    expect(steps.map((s) => s.key)).toEqual(['briefs', 'plan', 'model', 'repos', 'tasks', 'releases'])
    expect(steps.map((s) => s.value)).toEqual([
      'No Brief yet',
      'Not planned yet',
      'No system yet',
      'No container yet',
      'No Task yet',
      'No release yet',
    ])
    expect(steps.every((s) => s.tone === 'unknown')).toBe(true)
  })

  it('counts the Briefs by phase, and opens the one Brief', () => {
    const data = { ...empty(), briefs: [brief('idea', { phase: 'Planned' })] }
    expect(step(data, 'briefs')).toMatchObject({
      value: '1 Brief',
      detail: '1 Planned',
      tone: 'ok',
      to: { kind: 'Brief', name: 'idea' },
    })
    const two = { ...data, briefs: [...data.briefs, brief('other', { phase: 'NeedsInput' })] }
    expect(step(two, 'briefs')).toMatchObject({
      detail: '1 Planned, 1 NeedsInput',
      tone: 'waiting',
      to: { area: 'briefs' },
    })
  })

  it('counts applied rounds and filed issues, and says when a Brief waits for an answer', () => {
    const planned = brief('idea', {
      phase: 'Planned',
      history: [
        { round: 1, outcome: 'Applied' },
        { round: 2, outcome: 'RoundFailed' },
        { round: 3, outcome: 'Tended' },
      ],
      issues: [
        { repository: 'api', number: 1 },
        { repository: 'api', number: 2 },
      ],
    })
    expect(step({ ...empty(), briefs: [planned] }, 'plan')).toMatchObject({
      value: '2 rounds applied',
      detail: '2 issues filed',
      tone: 'ok',
    })
    const asking = brief('other', { phase: 'NeedsInput' })
    expect(step({ ...empty(), briefs: [planned, asking] }, 'plan')).toMatchObject({
      detail: '1 Brief waits for your answer',
      tone: 'waiting',
    })
  })

  it('counts the model and points at the Project', () => {
    const data: StoryData = {
      ...empty(),
      systems: [
        { metadata: { name: 'store' }, spec: { projectRef: { name: 'shop' } } } as StoryData['systems'][number],
      ],
      containers: [container('api', {})],
    }
    expect(step(data, 'model')).toMatchObject({
      value: '1 system',
      detail: '1 container, 0 data stores, 0 people',
      to: { kind: 'Project', name: 'shop' },
    })
  })

  it('counts repositories from status, failed ones first', () => {
    const data = {
      ...empty(),
      containers: [
        container('api', { phase: 'Ready', repositoryURL: 'http://git/api' }),
        container('web', { phase: 'Failed' }),
      ],
    }
    expect(step(data, 'repos')).toMatchObject({
      value: '1 of 2 containers',
      detail: '1 Failed',
      tone: 'error',
      to: { area: 'story', view: 'containers' },
    })
    const one = { ...empty(), containers: [container('api', { phase: 'Ready', repositoryURL: 'http://git/api' })] }
    expect(step(one, 'repos')).toMatchObject({ detail: '0 without one yet', to: { kind: 'Container', name: 'api' } })
  })

  it('counts Tasks by phase', () => {
    const data = {
      ...empty(),
      tasks: [task('a', 'Implementing'), task('b', 'Merged'), task('c', 'Blocked'), task('d', 'Failed')],
    }
    expect(step(data, 'tasks')).toMatchObject({
      value: '1 active',
      detail: '1 merged, 1 blocked, 1 failed',
      tone: 'waiting',
      to: { area: 'work' },
    })
  })

  it('names the newest release, and a failed run', () => {
    const data = {
      ...empty(),
      crews: [
        crew('api', release('v0.1.0', '2026-06-01T10:00:00Z', 'Succeeded')),
        crew('web', release('v0.3.0', '2026-06-02T10:00:00Z', 'Failed')),
        crew('worker'),
      ],
    }
    expect(step(data, 'releases')).toMatchObject({
      value: 'v0.3.0',
      detail: '2 of 3 Crews released',
      tone: 'error',
      to: { area: 'work' },
    })
    const one = { ...empty(), crews: [crew('api', release('v0.1.0', '2026-06-01T10:00:00Z'))] }
    expect(step(one, 'releases').to).toEqual({ kind: 'Crew', name: 'api' })
    const unpublished = {
      ...release('v0.1.0', '2026-06-01T10:00:00Z', 'Succeeded'),
      publish: { state: 'Failed' as const },
    }
    expect(step({ ...empty(), crews: [crew('api', unpublished)] }, 'releases').tone).toBe('error')
  })
})

describe('crewReleases', () => {
  it('lists each Crew’s last release, newest first, and skips Crews without one', () => {
    const rows = crewReleases([
      crew('api', release('v0.1.0', '2026-06-01T10:00:00Z'), 'a'),
      crew('none', undefined, 'a'),
      crew('web', release('v0.2.0', '2026-06-03T10:00:00Z'), 'b'),
    ])
    expect(rows.map((r) => [r.key, r.release.tag, r.container])).toEqual([
      ['b/web', 'v0.2.0', 'web'],
      ['a/api', 'v0.1.0', 'api'],
    ])
  })
})
