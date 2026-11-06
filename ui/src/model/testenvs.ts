// Test environments for display (docs/plans/test-environments.md, step 6): a
// TestEnvironment's provider member, Ready, live leases against its limits,
// today's spend and its CI pool; and a Task's run's leases and why it waits
// for one. Pure, and read only from spec and status: the operator leases and
// returns, the page reports.

import type { Condition, Task, TaskEnvironmentLease, TestEnvironment, TestEnvironmentLease } from '../k8s/types'
import { findCondition } from '../k8s/types'
import { formatCost } from './brief'
import { plural, splitWords, type Tone } from './describe'

/** The CRD's defaults, which the API server fills in (api/v1beta1/testenvironment_types.go). */
export const TEST_ENV_DEFAULTS = { maxLeases: 2, maxLeaseMinutes: 180 } as const

/** The environment's provider member, and what it runs. */
export function testEnvMember(env: TestEnvironment): { member: string; label: string; where: string } {
  if (env.spec.local) return { member: 'local', label: 'A pod per lease, on this cluster', where: env.spec.local.image }
  // A member this UI doesn't know yet: name it from the spec, show nothing of it.
  const known = ['description', 'limits', 'ci']
  const member =
    Object.entries(env.spec).find(([k, v]) => !known.includes(k) && v !== undefined && v !== null)?.[0] ?? 'none'
  return { member, label: member, where: '' }
}

/** The environment's Ready condition, and how it reads: its reason in words when it isn't. */
export function testEnvReadiness(env: TestEnvironment): { label: string; tone: Tone; condition?: Condition } {
  const ready = findCondition(env.status?.conditions, 'Ready')
  if (!ready) return { label: 'Not checked yet', tone: 'unknown' }
  if (ready.status === 'True') return { label: 'Ready', tone: 'ok', condition: ready }
  return {
    label: ready.reason ? splitWords(ready.reason) : 'Not ready',
    tone: ready.status === 'False' ? 'error' : 'waiting',
    condition: ready,
  }
}

/** How many leases may be live at once, and how long each lasts at most, the CRD's defaults where unset. */
export function testEnvLimits(env: TestEnvironment): { maxLeases: number; maxLeaseMinutes: number } {
  return {
    maxLeases: env.spec.limits?.maxLeases ?? TEST_ENV_DEFAULTS.maxLeases,
    maxLeaseMinutes: env.spec.limits?.maxLeaseMinutes ?? TEST_ENV_DEFAULTS.maxLeaseMinutes,
  }
}

/** The live leases, oldest first. */
export function testEnvLeases(env: TestEnvironment): TestEnvironmentLease[] {
  return [...(env.status?.leases ?? [])].sort(
    (a, b) => (a.since ?? '').localeCompare(b.since ?? '') || a.id.localeCompare(b.id),
  )
}

/** "1 of 2 leased", and whether a run that wants one more now waits (AllLeased). */
export function describeLeaseCount(env: TestEnvironment): { text: string; full: boolean } {
  const live = env.status?.leases?.length ?? 0
  const { maxLeases } = testEnvLimits(env)
  return { text: `${live} of ${maxLeases} leased`, full: live >= maxLeases }
}

/** The limits in words: how many leases at once, and how long one lasts. */
export function describeLimits(env: TestEnvironment): string {
  const { maxLeases, maxLeaseMinutes } = testEnvLimits(env)
  return `${plural(maxLeases, 'lease')} at once, each ${maxLeaseMinutes} min at most`
}

/** The UTC date of a time, as status.spentToday names its day. */
function utcDay(now: number): string {
  return new Date(now).toISOString().slice(0, 10)
}

/**
 * What the leases cost today (UTC): status.spentToday when it is today's, else
 * nothing yet; a local member's leases cost nothing.
 */
export function describeSpend(env: TestEnvironment, now: number): string {
  const spent = env.status?.spentToday
  const milliUSD = spent && spent.day === utcDay(now) ? (spent.milliUSD ?? 0) : 0
  if (env.spec.local && milliUSD === 0) return 'Nothing today: a local lease costs nothing'
  return `${formatCost(milliUSD)} today`
}

/** How a lease's state reads. */
export function leaseTone(state: TestEnvironmentLease['state']): Tone {
  switch (state) {
    case 'Ready':
      return 'ok'
    case 'Pending':
      return 'progress'
    case 'Failed':
      return 'error'
    default:
      return 'unknown'
  }
}

/** The CI label a workflow's runs-on names to run on the environment (model.EnvRunnerLabel). */
export function envRunnerLabel(name: string): string {
  return `env-${name}`
}

/** The environment's CI pool in words: how many runner pods wait for runs-on: env-<name>, or none. */
export function describeCIPool(env: TestEnvironment): string {
  const label = envRunnerLabel(env.metadata.name)
  const size = env.spec.ci?.size ?? 0
  if (size === 0) return `None: no runner takes runs-on: ${label} until ci.size is set`
  return `${plural(size, 'runner pod')} for runs-on: ${label}`
}

/** The extended resources the pod requests, as "devices.kubevirt.io/kvm: 1", sorted. */
export function describeDevices(env: TestEnvironment): string[] {
  return Object.entries(env.spec.local?.devices ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, q]) => `${name}: ${q}`)
}

/** The Tasks whose runs hold a lease of the environment, by its status, each once. */
export function leaseHolders(env: TestEnvironment): string[] {
  return [...new Set((env.status?.leases ?? []).map((l) => l.holder).filter((h): h is string => Boolean(h)))]
}

/** The leases the Task's current run holds. */
export function taskLeases(task: Task): TaskEnvironmentLease[] {
  return task.status?.environments ?? []
}

/** Why a Task's run waits for a test environment: the reason its message starts with, and the message. */
export function environmentWait(task: Task): { why: string; message: string } | undefined {
  const progressing = findCondition(task.status?.conditions, 'Progressing')
  if (progressing?.reason !== 'WaitingForEnvironment') return undefined
  const message = progressing.message ?? ''
  const colon = message.indexOf(':')
  return { why: colon > 0 ? message.slice(0, colon) : '', message }
}
