// What waits on a person, across projects (docs/plans/done/ui-lifecycle.md,
// decision 5): a plain function over the data the Factory source reads. Every
// item comes from status, never from timing, and says why in the condition's
// own words and where it is done: a place in the UI, a link out, a kubectl line.

import type { Brief, Condition, Container, Crew, Project, StandardFile, Task } from '../k8s/types'
import { findCondition } from '../k8s/types'
import type { Area } from './areas'
import { failureAnswered } from './attention'
import { approveCommand, deploysOf, type DeployRow } from './deploys'
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
      command: `kubectl -n ${ns} annotate container ${c.metadata.name} theos.factory.io/retry=$(date +%s) --overwrite`,
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
  items.push(...deployNeeds(c, place))
  return items
}

/** A deploy row the operator says waits for approval of `awaiting`, whose spec entry is known. */
export type AwaitingRow = DeployRow & { entry: NonNullable<DeployRow['entry']>; awaiting: string }

/**
 * The Container's deploys that wait for a person's approval: the entries the
 * operator says wait (rows, each with the version in awaiting), and whether
 * Deployed says a deploy waits while no entry names it. The Needs you item and
 * the Loop's "Reviewing the product" stage both read it from here.
 */
export function approvalWaits(c: Container): { rows: AwaitingRow[]; unnamed: boolean } {
  const deployed = findCondition(c.status?.conditions, 'Deployed')
  const all = deploysOf(c)
  const rows = all.filter((r): r is AwaitingRow => Boolean(r.awaiting && r.entry))
  const unnamed =
    deployed?.status === 'False' && deployed.reason === 'WaitingForApproval' && !all.some((r) => r.awaiting)
  return { rows, unnamed }
}

/** The Deployed reasons that wait on a person (docs/plans/done/deploy.md, decision 6). */
export const DEPLOY_HELD = ['WaitingForApproval', 'DeployFailed']

/**
 * A Container's deploys that wait on a person: an item per entry waiting for
 * approval, with the kubectl line that approves its version, per entry Failed,
 * and per deploy whose target's tool reports it Degraded. Each is read from its
 * own entry, not from Deployed, which names only the first reason in its order:
 * a target that can't be reached would otherwise hide another's approval. A
 * Deployed that waits for approval or says a deploy failed with no entry saying
 * which is one item from the condition.
 */
function deployNeeds(c: Container, place: Need['place']): Need[] {
  const deployed = findCondition(c.status?.conditions, 'Deployed')
  const held = deployed?.status === 'False' && DEPLOY_HELD.includes(deployed.reason ?? '') ? deployed.reason : undefined
  const rows = deploysOf(c)
  const approvals = approvalWaits(c)
  const items: Need[] = []
  const entry = (row: (typeof rows)[number]) => ({
    reason: row.status?.reason || deployed?.reason || 'Deployed',
    message: row.status?.message ?? '',
    since: row.status?.since ?? deployed?.lastTransitionTime,
  })
  const link = (row: (typeof rows)[number]) =>
    row.status?.url ? [{ label: `At ${row.target}`, url: row.status.url }] : []
  for (const row of rows) {
    const approval = approvals.rows.find((r) => r.target === row.target)
    if (approval) {
      items.push({
        ...base('Container', c, `Approve|${row.target}`),
        title: `waits for your approval of ${row.awaiting} at ${row.target}`,
        ...entry(row),
        place,
        links: [],
        command: approveCommand(c, row.index, approval.entry, approval.awaiting),
      })
    }
    if (row.status?.phase === 'Failed') {
      items.push({
        ...base('Container', c, `DeployFailed|${row.target}`),
        title: `failed to deploy${row.status.wantedVersion ? ` ${row.status.wantedVersion}` : ''} to ${row.target}`,
        ...entry(row),
        place,
        links: link(row),
      })
    }
  }
  // Status that names no entry in that state still says the Container waits: one item from the condition.
  const named =
    held === 'WaitingForApproval' ? rows.some((r) => r.awaiting) : rows.some((r) => r.status?.phase === 'Failed')
  if (held && !named) {
    items.push({
      ...base('Container', c, `Deployed|${held}`),
      title: held === 'WaitingForApproval' ? 'has a deploy waiting for your approval' : 'has a deploy that failed',
      ...why(deployed, held),
      place,
      links: [],
    })
  }
  for (const row of rows) {
    if (row.status?.health !== 'Degraded') continue
    items.push({
      ...base('Container', c, `Degraded|${row.target}`),
      title: `runs ${row.status.version || 'a version'} degraded at ${row.target}`,
      reason: 'Degraded',
      message: row.status.message || `The tool of DeployTarget ${row.target} reports what runs there Degraded`,
      since: row.status.since,
      place,
      links: link(row),
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
      command: `kubectl -n ${ns} annotate crew ${crew.metadata.name} theos.factory.io/rerun-release=$(date +%s) --overwrite`,
    })
  }
  return items
}
