// Test environments for display (docs/plans/done/test-environments.md, steps 6
// to 10): a TestEnvironment's provider member (local, aws or deviceFarm),
// Ready, a remote member's CredentialsValid and CI, live leases against its
// limits, today's spend against its budget, its CI, an EC2 Mac's kept hosts
// and a deviceFarm environment's test runs; and a Task's run's leases and why
// it waits for one. Pure, and read only from spec and status: the operator
// leases and returns, the page reports. Of a credential, only the Secret's
// name: the UI never reads Secrets.

import type {
  Condition,
  Task,
  TaskEnvironmentLease,
  TestEnvironment,
  TestEnvironmentLease,
  TestEnvironmentTestRun,
} from '../k8s/types'
import { findCondition } from '../k8s/types'
import { formatCost } from './brief'
import { plural, splitWords, type Tone } from './describe'

/** The CRD's defaults, which the API server fills in (api/v1beta1/testenvironment_types.go). */
export const TEST_ENV_DEFAULTS = { maxLeases: 2, maxLeaseMinutes: 180, keepHostHours: 24 } as const

/** The keys the operator reads from a remote member's Secret, by name only (model.AWSCredentialKeys); the last is optional. */
export const AWS_SECRET_KEYS = ['accessKeyId', 'secretAccessKey', 'sessionToken'] as const

/** Device Farm's one region, where a deviceFarm member's project is. */
const DEVICE_FARM_REGION = 'us-west-2'

/** Whether an aws member's instance type is an EC2 Mac's (model.TestEnvMac), which runs on a dedicated host. */
export function isMacInstance(instanceType: string | undefined): boolean {
  return (instanceType ?? '').startsWith('mac')
}

/** The environment's provider member, what it is in words and where it runs. */
export function testEnvMember(env: TestEnvironment): { member: string; label: string; where: string } {
  const { local, aws, deviceFarm } = env.spec
  if (local) return { member: 'local', label: 'A pod per lease, on this cluster', where: local.image }
  if (aws) {
    const mac = isMacInstance(aws.instanceType)
    return {
      member: 'aws',
      label: mac
        ? 'An EC2 Mac per lease, on a dedicated host the operator keeps, in your AWS account'
        : 'An EC2 machine per lease, in your AWS account',
      where: `EC2 ${aws.instanceType} in ${aws.region}${aws.account ? `, account ${aws.account}` : ''}`,
    }
  }
  if (deviceFarm)
    return {
      member: 'deviceFarm',
      label: 'AWS Device Farm’s real phones, which run test packages',
      where: `Device Farm in ${DEVICE_FARM_REGION}${deviceFarm.account ? `, account ${deviceFarm.account}` : ''}`,
    }
  // A member this UI doesn't know yet: name it from the spec, show nothing of it.
  const known = ['description', 'limits', 'ci']
  const member =
    Object.entries(env.spec).find(([k, v]) => !known.includes(k) && v !== undefined && v !== null)?.[0] ?? 'none'
  return { member, label: member, where: '' }
}

/** Whether the environment's member is remote (aws, deviceFarm): it has a credential, a budget and costs. */
export function testEnvRemote(env: TestEnvironment): boolean {
  return Boolean(env.spec.aws || env.spec.deviceFarm)
}

/** The Secret a remote member's credential is in, by name; none for local. */
export function testEnvSecret(env: TestEnvironment): string | undefined {
  return (env.spec.aws ?? env.spec.deviceFarm)?.credentialsSecretRef?.name
}

/** A condition as a badge: trueLabel when True, its reason in words when not, "Not checked yet" while it is missing. */
function conditionBadge(
  condition: Condition | undefined,
  trueLabel: string,
): { label: string; tone: Tone; condition?: Condition } {
  if (!condition) return { label: 'Not checked yet', tone: 'unknown' }
  if (condition.status === 'True') return { label: trueLabel, tone: 'ok', condition }
  return {
    label: condition.reason ? splitWords(condition.reason) : `Not ${trueLabel.toLowerCase()}`,
    tone: condition.status === 'False' ? 'error' : 'waiting',
    condition,
  }
}

/** The environment's Ready condition, and how it reads: its reason in words when it isn't. */
export function testEnvReadiness(env: TestEnvironment): { label: string; tone: Tone; condition?: Condition } {
  return conditionBadge(findCondition(env.status?.conditions, 'Ready'), 'Ready')
}

/**
 * A remote member's CredentialsValid, and how it reads: its reason in words
 * when it isn't (Secret missing, Secret incomplete, Credential refused,
 * Unreachable); undefined for a local member, which needs none.
 */
export function testEnvCredentials(
  env: TestEnvironment,
): { label: string; tone: Tone; condition?: Condition } | undefined {
  if (!testEnvRemote(env)) return undefined
  return conditionBadge(findCondition(env.status?.conditions, 'CredentialsValid'), 'Valid')
}

/**
 * An aws member's CI condition, while its ci block is set: Serving, or why
 * not in words (No git provider, No public address, Label clash,
 * Unsupported, Unreachable, Over budget); undefined otherwise, a local
 * pool's being the Project's CIRunners.
 */
export function testEnvCI(env: TestEnvironment): { label: string; tone: Tone; condition?: Condition } | undefined {
  if (!env.spec.aws || !env.spec.ci) return undefined
  return conditionBadge(findCondition(env.status?.conditions, 'CI'), 'Serving')
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
 * What the leases cost today (UTC), from status.spentToday when it is
 * today's, against limits.budgetUSDPerDay when set; over when the day's
 * budget is spent, so no lease starts (OverBudget), as model.LeaseMayStart.
 */
export function testEnvSpend(env: TestEnvironment, now: number): { milliUSD: number; budget?: number; over: boolean } {
  const spent = env.status?.spentToday
  const milliUSD = spent && spent.day === utcDay(now) ? (spent.milliUSD ?? 0) : 0
  const budget = env.spec.limits?.budgetUSDPerDay
  if (!budget) return { milliUSD, over: false }
  return { milliUSD, budget, over: milliUSD >= budget * 1000 }
}

/**
 * What the leases cost today (UTC), against the day's budget where there is
 * one: "$3.42 of $10.00 today", "…, the budget spent" past it; a local
 * member's leases cost nothing.
 */
export function describeSpend(env: TestEnvironment, now: number): string {
  const { milliUSD, budget, over } = testEnvSpend(env, now)
  if (env.spec.local && milliUSD === 0) return 'Nothing today: a local lease costs nothing'
  if (budget === undefined) return `${formatCost(milliUSD)} today`
  return `${formatCost(milliUSD)} of ${formatCost(budget * 1000)} today${over ? ', the budget spent' : ''}`
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

/**
 * Who holds a lease: a Task's run, or, for a remote machine that runs a CI
 * job, the runner registered for it (ci/<GitProvider>/<runner id>,
 * model.CIHolder).
 */
export function leaseHolder(
  holder: string | undefined,
): { kind: 'Task'; name: string } | { kind: 'CI'; gitProvider: string; runner: string } | undefined {
  if (!holder) return undefined
  const ci = /^ci\/([^/]+)\/(.+)$/.exec(holder)
  if (ci) return { kind: 'CI', gitProvider: ci[1], runner: ci[2] }
  return { kind: 'Task', name: holder }
}

/** The CI label a workflow's runs-on names to run on the environment (model.EnvRunnerLabel). */
export function envRunnerLabel(name: string): string {
  return `env-${name}`
}

/**
 * The environment's CI in words: a local member's runner pods for runs-on:
 * env-<name>, an aws member's machine per job, or none, and why.
 */
export function describeCIPool(env: TestEnvironment): string {
  const label = envRunnerLabel(env.metadata.name)
  if (env.spec.deviceFarm) return 'None: a Device Farm environment runs test packages, not CI jobs'
  if (env.spec.aws) {
    if (isMacInstance(env.spec.aws.instanceType)) return 'None: an EC2 Mac runs no CI jobs'
    if (!env.spec.ci) return `None: no job takes runs-on: ${label} until ci: {} is set`
    return `A machine per waiting job for runs-on: ${label}, within its limits and the day’s budget`
  }
  const size = env.spec.ci?.size ?? 0
  if (size === 0) return `None: no runner takes runs-on: ${label} until ci.size is set`
  return `${plural(size, 'runner pod')} for runs-on: ${label}`
}

/**
 * How an EC2 Mac's dedicated hosts are kept (aws.mac.keepHostHours, 24 by
 * default, EC2's minimum); undefined for another member or instance type.
 */
export function describeMacHosts(env: TestEnvironment): string | undefined {
  const aws = env.spec.aws
  if (!aws || !isMacInstance(aws.instanceType)) return undefined
  const hours = aws.mac?.keepHostHours ?? TEST_ENV_DEFAULTS.keepHostHours
  return `Each host kept ${hours} h from its allocation for the leases that follow; the lease that allocates one counts its price for those hours at once`
}

/** The extended resources the pod requests, as "devices.kubevirt.io/kvm: 1", sorted. */
export function describeDevices(env: TestEnvironment): string[] {
  return Object.entries(env.spec.local?.devices ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, q]) => `${name}: ${q}`)
}

/** The Tasks whose runs hold a lease of the environment, by its status, each once; a CI job's runner is no Task. */
export function leaseHolders(env: TestEnvironment): string[] {
  const tasks = (env.status?.leases ?? []).flatMap((l) => {
    const holder = leaseHolder(l.holder)
    return holder?.kind === 'Task' ? [holder.name] : []
  })
  return [...new Set(tasks)]
}

/** A deviceFarm environment's test runs, newest first. */
export function testEnvTestRuns(env: TestEnvironment): TestEnvironmentTestRun[] {
  return [...(env.status?.testRuns ?? [])].sort(
    (a, b) => (b.since ?? '').localeCompare(a.since ?? '') || a.id.localeCompare(b.id),
  )
}

/** A test run's badge: its result once there is one, else its state on the provider. */
export function testRunBadge(run: TestEnvironmentTestRun): { label: string; tone: Tone } {
  switch (run.result) {
    case 'Passed':
      return { label: 'Passed', tone: 'ok' }
    case 'Warned':
      return { label: 'Passed with warnings', tone: 'ok' }
    case 'Failed':
    case 'Errored':
    case 'Rejected':
      return { label: run.result, tone: 'error' }
    case 'Skipped':
    case 'Stopped':
      return { label: run.result, tone: 'waiting' }
  }
  switch (run.state) {
    case 'Pending':
    case 'Running':
      return { label: run.state, tone: 'progress' }
    case 'Rejected':
      return { label: 'Rejected', tone: 'error' }
    default:
      return { label: run.state ?? 'Unknown', tone: 'unknown' }
  }
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
