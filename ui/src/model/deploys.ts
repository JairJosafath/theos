// Where a Container ships and what runs there (docs/plans/done/deploy.md, decision
// 6): a DeployTarget's member, tier, approval and Ready, and a Container's
// deploys with the approval a person gives. Pure, and read only from spec and
// status: the operator decides what ships, and the CRD and RBAC who may approve.

import type { Condition, Container, ContainerDeploy, ContainerDeployStatus, DeployTarget } from '../k8s/types'
import { findCondition } from '../k8s/types'
import type { Tone } from './describe'

/** The target's member, the tool that owns the place, and where in it. */
export function memberOf(target: DeployTarget): { member: string; label: string; where: string } {
  if (target.spec.github) return { member: 'github', label: 'GitHub releases', where: target.spec.github.repository }
  if (target.spec.argoCD) {
    const { namespace, server } = target.spec.argoCD.destination
    const where = server && server !== 'https://kubernetes.default.svc' ? `${namespace} on ${server}` : namespace
    return { member: 'argoCD', label: 'Argo CD', where }
  }
  // A member this UI doesn't know yet: name it from the spec, show nothing of it.
  const known = ['credentialsSecretRef', 'tier', 'approval', 'parameters']
  const member =
    Object.entries(target.spec).find(([k, v]) => !known.includes(k) && v !== undefined && v !== null)?.[0] ?? 'none'
  return { member, label: member, where: '' }
}

/** Where a github target puts each release's source, unset parts named as the operator fills them; undefined when it publishes none. */
export function githubSource(target: DeployTarget): { path: string; tagPrefix: string } | undefined {
  const source = target.spec.github?.source
  if (!source) return undefined
  const path = source.path ?? '<container>'
  const tagPrefix = source.tagPrefix ?? (path === '.' ? '' : `${path}/`)
  return { path, tagPrefix }
}

/**
 * The keys a github target's Secret holds, by name: the operator reads them
 * (internal/model, GitHubSecret*), the UI never does.
 */
export const GITHUB_SECRET_KEYS = {
  token: 'token',
  app: ['appId', 'installationId', 'privateKey'],
  packages: 'packagesToken',
} as const

/**
 * The image a Container's release goes to on GHCR at a github target:
 * ghcr.io/owner/name/<container>, lower-cased, or ghcr.io/owner/name when
 * the target's tagPrefix is "" (model.GitHubImage). Undefined for another member.
 */
export function githubImage(target: DeployTarget, container = '<container>'): string | undefined {
  const github = target.spec.github
  if (!github) return undefined
  const repository = github.tagPrefix === '' ? github.repository : `${github.repository}/${container}`
  return `ghcr.io/${repository.toLowerCase()}`
}

/** The target's tier as asked; unset is development, the CRD's documented default. */
export function tierOf(target: DeployTarget): 'development' | 'production' {
  return target.spec.tier ?? 'development'
}

/** What the target's approval says, in words; unset is its tier's default, which the operator applies. */
export function describeApproval(target: DeployTarget): string {
  if (target.spec.approval === 'Required') return 'A person approves each version'
  if (target.spec.approval === 'None') return 'Every release ships by itself'
  return tierOf(target) === 'production'
    ? 'Not set: production’s default, a person approves each version'
    : 'Not set: development’s default, every release ships by itself'
}

/** The target's Ready condition, and how it reads. */
export function readinessOf(target: DeployTarget): { label: string; tone: Tone; condition?: Condition } {
  const ready = findCondition(target.status?.conditions, 'Ready')
  if (!ready) return { label: 'Not checked yet', tone: 'unknown' }
  if (ready.status === 'True') return { label: 'Ready', tone: 'ok', condition: ready }
  return { label: ready.reason || 'Not ready', tone: ready.status === 'False' ? 'error' : 'waiting', condition: ready }
}

/** How a deploy's phase reads. */
export function phaseTone(phase: ContainerDeployStatus['phase'] | undefined): Tone {
  switch (phase) {
    case 'Deployed':
      return 'ok'
    case 'Deploying':
      return 'progress'
    case 'Failed':
      return 'error'
    case 'Waiting':
      return 'waiting'
    default:
      return 'unknown'
  }
}

/** How a deploy's health reads. */
export function healthTone(health: ContainerDeployStatus['health'] | undefined): Tone {
  switch (health) {
    case 'Healthy':
      return 'ok'
    case 'Progressing':
      return 'progress'
    case 'Degraded':
      return 'error'
    default:
      return 'unknown'
  }
}

/** One of a Container's deploys: its spec entry, where it is in the list, and its status. */
export interface DeployRow {
  target: string
  /** The entry's index in spec.deploy; -1 for a status left over from a removed entry. */
  index: number
  after?: string
  pinned?: string
  approved?: string
  /** The spec.deploy entry as read; none for a status left over from a removed entry. */
  entry?: ContainerDeploy
  status?: ContainerDeployStatus
  /** The version waiting for a person's approval, when the operator says so. */
  awaiting?: string
}

/** The Container's deploys, in spec.deploy's order, then any status of an entry since removed. */
export function deploysOf(container: Container): DeployRow[] {
  const statuses = container.status?.deploys ?? []
  const rows: DeployRow[] = (container.spec.deploy ?? []).map((entry, index) => {
    const status = statuses.find((s) => s.target === entry.target)
    return {
      target: entry.target,
      index,
      after: entry.after,
      pinned: entry.version,
      approved: entry.approved,
      entry,
      status,
      awaiting: status?.reason === 'WaitingForApproval' ? status.wantedVersion || undefined : undefined,
    }
  })
  for (const status of statuses)
    if (!rows.some((r) => r.target === status.target)) rows.push({ target: status.target, index: -1, status })
  return rows
}

/** The Containers that name the target in spec.deploy. */
export function shippingTo(target: string, containers: Container[]): Container[] {
  return containers.filter((c) => c.spec.deploy?.some((d) => d.target === target))
}

/** A JSON patch operation of the approval. */
export type ApproveOp =
  { op: 'test' | 'add'; path: string; value: string } | { op: 'test'; path: string; value: ContainerDeploy }

/**
 * The patch that approves `version` at the entry `index` of spec.deploy, read
 * as `entry`: its only write sets that entry's approved, after a test that the
 * entry still names its target, so a list reordered since can't approve it at
 * another target; then a test that the entry still has the approval the page
 * showed, so a stale page or a copied line can't take back a newer approval
 * someone made since. JSON Patch can't test that a member is missing, so where
 * the entry had no approval the test is of the whole entry as read (#1461): a
 * first approval someone made since, or any other change to the entry, fails
 * it.
 */
export function approvePatch(index: number, entry: ContainerDeploy, version: string): ApproveOp[] {
  const at = `/spec/deploy/${index}`
  return [
    { op: 'test', path: `${at}/target`, value: entry.target },
    entry.approved
      ? { op: 'test', path: `${at}/approved`, value: entry.approved }
      : { op: 'test', path: at, value: entry },
    { op: 'add', path: `${at}/approved`, value: version },
  ]
}

/** The kubectl line that approves the version waiting at a Container's entry `index`, read as `entry`. */
export function approveCommand(container: Container, index: number, entry: ContainerDeploy, version: string): string {
  const ns = container.metadata.namespace ?? ''
  const patch = JSON.stringify(approvePatch(index, entry, version))
  return `kubectl -n ${ns} patch container ${container.metadata.name} --type=json -p '${patch}'`
}
