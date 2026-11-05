// The Loop area's stages (docs/plans/sdlc-loop.md, step 6): one project's
// software-development cycle, drawn from the resources alone. A pure function
// over the lists the UI already watches; it reads kind, phase and status and
// decides nothing, so the cycle is what the operator says it is.
//
//   Idea or bug -> Triage -> Spec -> Reviewing the spec (person) -> Implement
//   -> Reviewing the code -> Verify -> Reviewing the product (person) -> Ship
//   -> Monitor, and a bug found there is an idea again.

import type { Brief, Container, Crew, Task } from '../k8s/types'
import { deploysOf, type DeployRow } from './deploys'
import { isFinal } from './factory'
import { approvalWaits } from './needs'

export type StageKey =
  | 'idea'
  | 'triage'
  | 'spec'
  | 'specReview'
  | 'implement'
  | 'codeReview'
  | 'verify'
  | 'productReview'
  | 'ship'
  | 'monitor'

/** One thing at a stage, with the object page it opens and the links out to the Git host. */
export interface LoopItem {
  /** Tells items apart within the project. */
  key: string
  kind: 'Brief' | 'Task' | 'Container' | 'Crew'
  name: string
  namespace: string
  /** What it is, in a few words: the issue's title, the goal, the target and version. */
  title: string
  /** Where it is, in the words of status: its phase, or the reason a deploy waits. */
  detail: string
  /** The object's page (the UI decides whether its kind has one). */
  page: { ns: string; kind: string; name: string }
  links: { label: string; url: string }[]
  /** Monitor only: a degraded or failed deploy is a problem. */
  problem?: boolean
}

export interface LoopStage {
  key: StageKey
  /** From 1, in the order of the cycle. */
  number: number
  /** Short, for the ring. */
  short: string
  label: string
  /** One sentence on what the stage is. */
  about: string
  /** The two review points wait on a person. */
  person: boolean
  /** How many are at the stage. At Monitor, the problems; the healthy are counted apart. */
  count: number
  items: LoopItem[]
  /** Monitor only: the deploys that report Healthy. */
  healthy?: number
}

export interface LoopInput {
  briefs: Brief[]
  tasks: Task[]
  containers: Container[]
  crews: Crew[]
}

type Static = Pick<LoopStage, 'key' | 'number' | 'short' | 'label' | 'about' | 'person'>

function stage(number: number, key: StageKey, short: string, label: string, about: string, person = false): Static {
  return { key, number, short, label, about, person }
}

const STAGES: Static[] = [
  stage(1, 'idea', 'Idea', 'Idea or bug', 'Briefs that are queued or being planned.'),
  stage(2, 'triage', 'Triage', 'Triage', 'Triage Tasks that have not finished.'),
  stage(3, 'spec', 'Spec', 'Spec', 'Spec Tasks writing a specification.'),
  stage(
    4,
    'specReview',
    'Spec review',
    'Reviewing the spec',
    'Spec Tasks whose pull request waits for a person.',
    true,
  ),
  stage(5, 'implement', 'Implement', 'Implement', 'Implement Tasks queued, being written, checked or blocked.'),
  stage(6, 'codeReview', 'Code review', 'Reviewing the code', 'Implement Tasks an agent is reviewing.'),
  stage(7, 'verify', 'Verify', 'Verify', 'Implement Tasks being verified before they ship.'),
  stage(
    8,
    'productReview',
    'Product review',
    'Reviewing the product',
    'Deploys that wait for a person to approve a version.',
    true,
  ),
  stage(9, 'ship', 'Ship', 'Ship', 'Releases whose CI is running, and deploys on their way.'),
  stage(
    10,
    'monitor',
    'Monitor',
    'Monitor',
    'What runs: problems with the bug filed for each, and how many are healthy.',
  ),
]

/** The stages, in the order of the cycle, for the resources of one project. */
export function loopStages(input: LoopInput): LoopStage[] {
  const stages: LoopStage[] = STAGES.map((s) => ({ ...s, count: 0, items: [] }))
  const at = (key: StageKey) => stages.find((s) => s.key === key)!.items

  for (const brief of sorted(input.briefs)) {
    const phase = brief.status?.phase ?? 'Pending'
    if (phase !== 'Pending' && phase !== 'Planning') continue
    const round = brief.status?.round
    at('idea').push({
      ...ref('Brief', brief),
      title: brief.spec.idea.length > 90 ? `${brief.spec.idea.slice(0, 89)}…` : brief.spec.idea,
      detail: phase === 'Planning' ? `Planning${round ? `, round ${round}` : ''}` : 'Waiting to be planned',
      links: [],
    })
  }

  for (const task of sorted(input.tasks)) {
    const key = taskStage(task)
    if (key) at(key).push(taskItem(task))
  }

  for (const container of sorted(input.containers)) {
    const approvals = approvalWaits(container)
    for (const row of approvals.rows) {
      at('productReview').push(
        deployItem(container, row, `${row.awaiting} waits for approval at ${row.target}`, `Approve|${row.target}`),
      )
    }
    if (approvals.unnamed) {
      at('productReview').push({
        ...ref('Container', container, 'Approve'),
        title: `${shown(container)}: a deploy waits for approval`,
        detail: 'WaitingForApproval',
        links: [],
      })
    }
    for (const row of deploysOf(container)) {
      const phase = row.status?.phase
      // An entry that waits for a person is at Reviewing the product, not shipping.
      if ((phase === 'Deploying' || phase === 'Waiting') && !row.awaiting) {
        at('ship').push(deployItem(container, row, shipTitle(row), `Ship|${row.target}`))
      }
    }
  }

  for (const crew of sorted(input.crews)) {
    const last = crew.status?.lastRelease
    if (last?.run !== 'Waiting' && last?.run !== 'Running') continue
    at('ship').push({
      ...ref('Crew', crew),
      title: `Release ${last.tag}`,
      detail: last.run === 'Running' ? 'Its CI run is running' : 'Its CI run is waiting',
      links: last.runURL ? [{ label: 'The run', url: last.runURL }] : [],
    })
  }

  let healthy = 0
  for (const container of sorted(input.containers)) {
    for (const row of deploysOf(container)) {
      const s = row.status
      if (!s) continue
      if (s.phase === 'Failed' || s.health === 'Degraded') at('monitor').push(monitorItem(container, row))
      else if (s.health === 'Healthy') healthy++
    }
  }

  for (const s of stages) s.count = s.items.length
  stages[stages.length - 1].healthy = healthy
  return stages
}

/** The stage of a Task, from its kind and phase; none for a Plan Task or one that is finished. */
export function taskStage(task: Task): StageKey | undefined {
  const phase = task.status?.phase ?? 'Pending'
  switch (task.spec.kind) {
    case 'Triage':
      return isFinal(phase) ? undefined : 'triage'
    case 'Spec':
      if (isFinal(phase)) return undefined
      return phase === 'AwaitingApproval' ? 'specReview' : 'spec'
    case undefined:
    case 'Implement':
      switch (phase) {
        case 'Pending':
        case 'Implementing':
        case 'Checking':
        case 'Blocked':
          return 'implement'
        case 'Reviewing':
          return 'codeReview'
        case 'Verifying':
          return 'verify'
        default:
          return undefined
      }
    default:
      return undefined
  }
}

function ref(
  kind: LoopItem['kind'],
  obj: { metadata: { name: string; namespace?: string } },
  what = '',
): Pick<LoopItem, 'key' | 'kind' | 'name' | 'namespace' | 'page'> {
  const namespace = obj.metadata.namespace ?? ''
  const name = obj.metadata.name
  return {
    key: `${kind}|${namespace}|${name}${what ? `|${what}` : ''}`,
    kind,
    name,
    namespace,
    page: { ns: namespace, kind, name },
  }
}

function shown(container: Container): string {
  return container.spec.displayName || container.metadata.name
}

function taskItem(task: Task): LoopItem {
  const s = task.status
  const issue = task.spec.issue ? `Issue #${task.spec.issue}` : ''
  return {
    ...ref('Task', task),
    title: s?.issueTitle || task.spec.goal || issue || task.metadata.name,
    detail: `${s?.phase ?? 'Pending'}${s?.round && s.round > 1 ? `, round ${s.round}` : ''}`,
    links: [],
  }
}

function shipTitle(row: DeployRow): string {
  const version = row.status?.wantedVersion || row.status?.version
  return `${version ? `${version} ` : ''}to ${row.target}`
}

function deployItem(container: Container, row: DeployRow, title: string, what: string): LoopItem {
  const s = row.status
  return {
    ...ref('Container', container, what),
    title: `${shown(container)}: ${title}`,
    detail: s?.reason || s?.phase || '',
    links: s?.url ? [{ label: `At ${row.target}`, url: s.url }] : [],
  }
}

function monitorItem(container: Container, row: DeployRow): LoopItem {
  const s = row.status!
  const issue = s.issue
  const links = []
  if (issue?.url) links.push({ label: `Bug #${issue.number}`, url: issue.url })
  if (s.url) links.push({ label: `At ${row.target}`, url: s.url })
  return {
    ...ref('Container', container, `Monitor|${row.target}`),
    title: `${shown(container)}: ${s.version || s.wantedVersion || 'a version'} ${s.phase === 'Failed' ? 'failed' : 'degraded'} at ${row.target}`,
    detail: issue ? `Bug #${issue.number} filed for ${issue.version}, ${issue.state}` : 'No bug filed yet',
    links,
    problem: true,
  }
}

/** Name order, so the lists keep still as the resources change. */
function sorted<T extends { metadata: { name: string } }>(list: T[]): T[] {
  return [...list].sort((a, b) => a.metadata.name.localeCompare(b.metadata.name))
}
