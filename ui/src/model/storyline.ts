// A project's story in one line (docs/plans/ui-lifecycle.md, step 9): from its
// Briefs to the plan, the model, the repositories, the Tasks and the releases,
// each step counted from `status` and pointing at where its objects are.

import type { StoryData } from '../k8s/sources'
import { plural, type Tone } from './describe'
import { crewReleases } from './releases'

/** Where a step's objects are: an area of the shell, or one object's page. */
export type StoryTarget =
  { area: 'briefs' | 'work' } | { area: 'story'; view: 'containers' } | { kind: string; name: string }

export interface StoryStep {
  key: 'briefs' | 'plan' | 'model' | 'repos' | 'tasks' | 'releases'
  label: string
  /** The step's headline, such as "3 containers". */
  value: string
  /** One line under it, or undefined when there is nothing more to say. */
  detail?: string
  /** unknown: nothing there yet. */
  tone: Tone
  to: StoryTarget
}

/** The steps of a project's story, in order. */
export function storyline(data: StoryData): StoryStep[] {
  return [briefsStep(data), planStep(data), modelStep(data), reposStep(data), tasksStep(data), releasesStep(data)]
}

/** One Brief's page, or the Briefs area when there are none or several. */
function briefsTarget(data: StoryData): StoryTarget {
  return data.briefs.length === 1 ? { kind: 'Brief', name: data.briefs[0].metadata.name } : { area: 'briefs' }
}

function briefsStep(data: StoryData): StoryStep {
  const { briefs } = data
  const counts = new Map<string, number>()
  for (const b of briefs) {
    const phase = b.status?.phase ?? 'Pending'
    counts.set(phase, (counts.get(phase) ?? 0) + 1)
  }
  return {
    key: 'briefs',
    label: 'Briefs',
    value: briefs.length ? plural(briefs.length, 'Brief') : 'No Brief yet',
    detail: briefs.length
      ? [...counts.entries()].map(([phase, n]) => `${n} ${phase}`).join(', ')
      : 'An app idea starts a Brief',
    tone: briefs.some((b) => b.status?.phase === 'Failed')
      ? 'error'
      : briefs.some((b) => b.status?.phase === 'NeedsInput')
        ? 'waiting'
        : briefs.length
          ? 'ok'
          : 'unknown',
    to: briefsTarget(data),
  }
}

function planStep(data: StoryData): StoryStep {
  const rounds = data.briefs.reduce(
    (n, b) => n + (b.status?.history ?? []).filter((h) => h.outcome === 'Applied' || h.outcome === 'Tended').length,
    0,
  )
  const issues = data.briefs.reduce((n, b) => n + (b.status?.issues?.length ?? 0), 0)
  const asking = data.briefs.filter((b) => b.status?.phase === 'NeedsInput').length
  return {
    key: 'plan',
    label: 'Plan',
    value: rounds ? `${plural(rounds, 'round')} applied` : 'Not planned yet',
    detail: asking
      ? `${plural(asking, 'Brief waits', 'Briefs wait')} for your answer`
      : issues
        ? `${plural(issues, 'issue')} filed`
        : undefined,
    tone: asking ? 'waiting' : rounds ? 'ok' : 'unknown',
    to: briefsTarget(data),
  }
}

function modelStep(data: StoryData): StoryStep {
  const parts = data.systems.length + data.containers.length + data.dataStores.length
  return {
    key: 'model',
    label: 'Model',
    value: data.systems.length ? plural(data.systems.length, 'system') : 'No system yet',
    detail: parts
      ? `${plural(data.containers.length, 'container')}, ${plural(data.dataStores.length, 'data store')}, ${plural(data.people.length, 'person', 'people')}`
      : undefined,
    tone: parts ? 'ok' : 'unknown',
    to: { kind: 'Project', name: data.namespace },
  }
}

function reposStep(data: StoryData): StoryStep {
  const { containers } = data
  const withRepo = containers.filter((c) => c.status?.repositoryURL).length
  const failed = containers.filter((c) => c.status?.phase === 'Failed').length
  const scaffolding = containers.filter((c) => c.status?.phase === 'Scaffolding').length
  const detail = [failed ? `${failed} Failed` : '', scaffolding ? `${scaffolding} Scaffolding` : '']
    .filter(Boolean)
    .join(', ')
  return {
    key: 'repos',
    label: 'Repositories',
    value: containers.length ? `${withRepo} of ${plural(containers.length, 'container')}` : 'No container yet',
    detail: detail || (containers.length ? `${containers.length - withRepo} without one yet` : undefined),
    tone: failed ? 'error' : scaffolding ? 'progress' : withRepo ? 'ok' : 'unknown',
    to:
      containers.length === 1
        ? { kind: 'Container', name: containers[0].metadata.name }
        : { area: 'story', view: 'containers' },
  }
}

function tasksStep(data: StoryData): StoryStep {
  const by = (phases: string[]) => data.tasks.filter((t) => phases.includes(t.status?.phase ?? 'Pending')).length
  const active = by(['Pending', 'Implementing', 'Checking', 'Reviewing'])
  const blocked = by(['Blocked'])
  const merged = by(['Merged'])
  const failed = by(['Failed'])
  const detail = [
    merged ? `${merged} merged` : '',
    blocked ? `${blocked} blocked` : '',
    failed ? `${failed} failed` : '',
  ]
    .filter(Boolean)
    .join(', ')
  return {
    key: 'tasks',
    label: 'Tasks',
    value: data.tasks.length ? `${active} active` : 'No Task yet',
    detail: detail || undefined,
    tone: failed || blocked ? 'waiting' : active ? 'progress' : merged ? 'ok' : 'unknown',
    to: { area: 'work' },
  }
}

function releasesStep(data: StoryData): StoryStep {
  const released = crewReleases(data.crews)
  const newest = released[0]
  const failed = released.some((r) => r.release.run === 'Failed')
  return {
    key: 'releases',
    label: 'Releases',
    value: newest ? newest.release.tag : 'No release yet',
    detail: data.crews.length ? `${released.length} of ${plural(data.crews.length, 'Crew')} released` : undefined,
    tone: failed ? 'error' : newest ? 'ok' : 'unknown',
    to: newest && released.length === 1 ? { kind: 'Crew', name: newest.crew } : { area: 'work' },
  }
}
