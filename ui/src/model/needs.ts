// What waits on a person, across projects (docs/plans/done/ui-lifecycle.md,
// decision 5): a plain function over the data the Factory source reads. Every
// item comes from status, never from timing, and says why in the condition's
// own words and where it is done: a place in the UI, a link out, a kubectl line.

import type { Brief, Condition, Container, Crew, Project, StandardFile, Task } from '../k8s/types'
import { findCondition } from '../k8s/types'
import type { Area } from './areas'
import { failureAnswered } from './attention'
import { time } from './factory'
import { hasPage, standardKeepCommand, standardTakeCommand } from './lifecycle'

export type NeedKind = 'Brief' | 'Container' | 'Task' | 'Crew'

/** A place in the UI, as the URL says it: an area, or an object's page. */
export type Place =
  | { area: Area; project?: string; view?: 'containers'; system?: string }
  | { page: { ns: string; kind: string; name: string } }

export interface Need {
  /** Tells items apart: kind, namespace, name and what is asked. */
  key: string
  kind: NeedKind
  namespace: string
  name: string
  /** What waits, in a few words, such as "needs your input". */
  title: string
  /** Why, verbatim from status: the condition's reason and message. */
  reason: string
  message: string
  /** Since when, from the condition; unset when status says no time. */
  since?: string
  /** Where it is seen in the UI. */
  place: { label: string; to: Place }
  /** Where it is done outside the UI: a pull request, an issue, a run. */
  links: { label: string; url: string }[]
  /** The command that does it, when no page can. */
  command?: string
  /**
   * The two answers to a Standard's files left out, at the Standard's ref the
   * Container's status names: a button each with actions on, else their commands,
   * and the files left out with what the Standard changed in each.
   */
  standardAnswer?: { ref: string; take: string; keep: string; files: StandardFile[] }
}

export interface NeedsInput {
  projects: Project[]
  briefs: Brief[]
  containers: Container[]
  crews: Crew[]
  tasks: Task[]
}

/** The Crew's Released reasons that leave a release to a person (internal/model/release.go). */
export const RELEASE_HELD_BY_PERSON = ['NoMergedIssue', 'TagClash', 'MergedAfterPrepare', 'BehindPreviousRelease']

/** The StandardUpdated reasons whose update pull request waits on a person. */
export const STANDARD_UPDATE_HELD = ['ChecksFailed', 'UpdateRefused']

/**
 * The ModulePublished reasons that say nobody can fetch the Crew's Go module
 * (docs/plans/done/module-fetchable.md, decision 8); WaitsForRelease only counts
 * while the Published condition is False, else the publish is on its way.
 */
export const MODULE_NOT_FETCHABLE = ['InvalidModule', 'NotFetchable']

/** Condition reasons that say a Container is on its way, not why it failed. */
const IN_PROGRESS = ['Scaffolding', 'WaitingForScaffold']

/** Every item, grouped by namespace in name order, oldest first within each. */
export function needs(input: NeedsInput): Need[] {
  const items: Need[] = [
    ...input.briefs.flatMap(briefNeeds),
    ...input.containers.flatMap(containerNeeds),
    ...input.tasks.flatMap((t) => taskNeeds(t, input)),
    ...input.crews.flatMap((c) => crewNeeds(c, input.containers)),
  ]
  return items.sort(
    (a, b) =>
      a.namespace.localeCompare(b.namespace) ||
      (time(a.since) ?? Infinity) - (time(b.since) ?? Infinity) ||
      a.key.localeCompare(b.key),
  )
}

/** The items by project, in the order needs() gives them, each with the Project's display name. */
export function byProject(items: Need[], projects: Project[]): { namespace: string; title: string; items: Need[] }[] {
  const groups: { namespace: string; title: string; items: Need[] }[] = []
  for (const item of items) {
    let group = groups.at(-1)
    if (group?.namespace !== item.namespace) {
      const project = projects.find((p) => p.metadata.namespace === item.namespace)
      group = { namespace: item.namespace, title: project?.spec?.displayName || item.namespace, items: [] }
      groups.push(group)
    }
    group.items.push(item)
  }
  return groups
}

function base(kind: NeedKind, obj: { metadata: { namespace?: string; name: string } }, what: string) {
  const namespace = obj.metadata.namespace ?? ''
  return { key: `${kind}|${namespace}|${obj.metadata.name}|${what}`, kind, namespace, name: obj.metadata.name }
}

/** The object's page where its kind has one, else the area that shows it. */
function placeOf(kind: NeedKind, ns: string, name: string, fallback: Need['place']): Need['place'] {
  return hasPage(kind) ? { label: `Open the ${kind}`, to: { page: { ns, kind, name } } } : fallback
}

function why(c: Condition | undefined, fallback: string) {
  return { reason: c?.reason || fallback, message: c?.message ?? '', since: c?.lastTransitionTime }
}

function briefNeeds(brief: Brief): Need[] {
  const ns = brief.metadata.namespace ?? ''
  const place = placeOf('Brief', ns, brief.metadata.name, {
    label: 'Open its Brief',
    to: { area: 'briefs', project: ns },
  })
  const items: Need[] = []
  for (const [type, title] of [
    ['NeedsInput', 'needs your input'],
    ['NeedsYou', 'has something only you can decide'],
  ] as const) {
    const c = findCondition(brief.status?.conditions, type)
    if (c?.status !== 'True') continue
    items.push({ ...base('Brief', brief, type), title, ...why(c, type), place, links: [] })
  }
  return items
}

function containerNeeds(c: Container): Need[] {
  const ns = c.metadata.namespace ?? ''
  const place = placeOf('Container', ns, c.metadata.name, {
    label: 'Find it in Story',
    to: { area: 'story', project: ns, view: 'containers', system: c.spec.softwareSystemRef.name },
  })
  const items: Need[] = []
  if (c.status?.phase === 'Failed') {
    const unmet = (c.status.conditions ?? []).filter((cond) => cond.status === 'False')
    const failed = unmet.find((cond) => !IN_PROGRESS.includes(cond.reason ?? '')) ?? unmet[0]
    items.push({
      ...base('Container', c, 'Failed'),
      title: 'failed to scaffold',
      ...why(failed, 'Failed'),
      place,
      links: c.status.repositoryURL ? [{ label: 'Its repository', url: c.status.repositoryURL }] : [],
      command: `kubectl -n ${ns} annotate container ${c.metadata.name} c4.factory.io/retry=$(date +%s) --overwrite`,
    })
  }
  const updated = findCondition(c.status?.conditions, 'StandardUpdated')
  if (updated?.status === 'False' && STANDARD_UPDATE_HELD.includes(updated.reason ?? '')) {
    const url = c.status?.standard?.update?.pullRequestURL
    items.push({
      ...base('Container', c, 'StandardUpdated'),
      title: 'has a Standard update waiting on you',
      ...why(updated, 'StandardUpdated'),
      place,
      links: url ? [{ label: 'The update pull request', url }] : [],
    })
  }
  if (updated?.status === 'False' && updated.reason === 'FilesBehind') {
    const urls = [...new Set((c.status?.standard?.behind ?? []).flatMap((b) => b.pullRequestURL ?? []))]
    items.push({
      ...base('Container', c, 'StandardFilesBehind'),
      title: 'has Standard files left out of an update',
      ...why(updated, 'FilesBehind'),
      place,
      links: urls.map((url) => ({ label: 'The update pull request', url })),
      standardAnswer: {
        ref: c.status?.standard?.ref ?? '',
        take: standardTakeCommand(c),
        keep: standardKeepCommand(c),
        files: c.status?.standard?.behind ?? [],
      },
    })
  }
  return items
}

function taskNeeds(task: Task, input: NeedsInput): Need[] {
  const ns = task.metadata.namespace ?? ''
  const place = placeOf('Task', ns, task.metadata.name, {
    label: 'Open it in Work',
    to: { area: 'work' },
  })
  const s = task.status
  if (s?.phase === 'Blocked') {
    const change = s.standardChange
    return [
      {
        ...base('Task', task, 'Blocked'),
        title: 'is blocked',
        ...why(findCondition(s.conditions, 'Progressing'), 'Blocked'),
        place,
        links: change?.issueURL ? [{ label: `The Standard change, issue #${change.issue}`, url: change.issueURL }] : [],
      },
    ]
  }
  if (s?.phase === 'Failed') {
    if (failureAnswered(task, time(s.completedAt) ?? 0, input.tasks, input.briefs)) return []
    const links = []
    if (s.issueURL) links.push({ label: `Its issue${task.spec.issue ? ` #${task.spec.issue}` : ''}`, url: s.issueURL })
    if (s.pullRequestURL) links.push({ label: `Its pull request #${s.pullRequest ?? ''}`, url: s.pullRequestURL })
    return [
      {
        ...base('Task', task, 'Failed'),
        title: 'failed',
        ...why(findCondition(s.conditions, 'Succeeded'), 'Failed'),
        place,
        links,
      },
    ]
  }
  return []
}

function crewNeeds(crew: Crew, containers: Container[]): Need[] {
  const ns = crew.metadata.namespace ?? ''
  const place = placeOf('Crew', ns, crew.metadata.name, { label: 'Open Work', to: { area: 'work' } })
  const items: Need[] = []
  const released = findCondition(crew.status?.conditions, 'Released')
  if (released?.status === 'False' && RELEASE_HELD_BY_PERSON.includes(released.reason ?? '')) {
    const repo = containers.find(
      (c) => c.metadata.namespace === crew.metadata.namespace && c.metadata.name === crew.spec.containerRef.name,
    )?.status?.repositoryURL
    items.push({
      ...base('Crew', crew, 'Released'),
      title: 'left a release to you',
      ...why(released, 'Released'),
      place,
      links: repo ? [{ label: 'Its milestones', url: `${repo.replace(/\/$/, '')}/milestones` }] : [],
    })
  }
  const module = findCondition(crew.status?.conditions, 'ModulePublished')
  const published = findCondition(crew.status?.conditions, 'Published')
  if (
    module?.status === 'False' &&
    (MODULE_NOT_FETCHABLE.includes(module.reason ?? '') ||
      (module.reason === 'WaitsForRelease' && published?.status === 'False'))
  ) {
    const source = crew.status?.lastRelease?.publish?.source?.url
    items.push({
      ...base('Crew', crew, 'ModulePublished'),
      title: 'has a Go module nobody can fetch',
      ...why(module, 'ModulePublished'),
      place,
      links: source ? [{ label: 'Its source on GitHub', url: source }] : [],
    })
  }
  const last = crew.status?.lastRelease
  if (last?.run === 'Failed') {
    items.push({
      ...base('Crew', crew, 'ReleaseRunFailed'),
      title: `has a failed release run, ${last.tag}`,
      // No condition says it: the reason is the operator's Event's, the message status.lastRelease's.
      reason: 'ReleaseRunFailed',
      message: `status.lastRelease.run is Failed for ${last.tag} at ${last.commit.slice(0, 12)}`,
      since: last.time,
      place,
      links: last.runURL ? [{ label: 'The failed run', url: last.runURL }] : [],
      command: `kubectl -n ${ns} annotate crew ${crew.metadata.name} c4.factory.io/rerun-release=$(date +%s) --overwrite`,
    })
  }
  return items
}
