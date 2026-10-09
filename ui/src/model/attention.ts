// What needs attention in the factory: stuck and failed Tasks, Briefs waiting on
// the person, unhealthy components and API stalls. Plain functions over
// resources and the /factory/health summary, so they are tested without React.

import type { Brief, BriefItemKind, Container, Crew, Session, Task } from '../k8s/types'
import { findCondition } from '../k8s/types'
import type { FactoryHealth } from '../k8s/health'
import { agentBackAt, currentStage, isFinal, STAGES, stageSince, taskKey, time, type Stage } from './factory'
import { formatClock, formatDuration } from './format'

/** How long a Task may stay in a stage before it needs a look. */
export const STUCK_AFTER_MS: Record<Stage, number> = {
  queued: 60 * 60_000,
  slot: 60 * 60_000,
  implement: 2 * 60 * 60_000,
  // The operator fails a Task after 30 minutes without a CI result.
  checks: 30 * 60_000,
  review: 60 * 60_000,
  // It waits for a person or agent to merge the Standard change.
  blocked: 24 * 60 * 60_000,
}

/** Failed Tasks stay on the attention list this long. */
const RECENT_FAILURE_MS = 24 * 60 * 60_000

/** Above this share of the node's memory, the laptop is in trouble. */
const MEMORY_WARN = 0.85

export interface Attention {
  level: 'error' | 'warning'
  title: string
  detail: string
  /** The Task it is about, if any (namespace/name). */
  task?: string
  /** The Brief that watches the item, and what it will do about it. */
  brief?: { name: string; note: string }
}

/**
 * The Brief that watches a Task, Crew or Container, from its status.attention
 * (healing: when it acts) or status.needsYou (on its list); undefined when no
 * Brief in the namespace lists it.
 */
export function briefOn(
  briefs: Brief[],
  kind: Exclude<BriefItemKind, 'Issue'>,
  namespace: string | undefined,
  name: string,
  now: number,
): { name: string; note: string } | undefined {
  for (const brief of briefs) {
    if (brief.metadata.namespace !== namespace) continue
    const briefName = brief.metadata.name
    if ((brief.status?.needsYou ?? []).some((n) => n.kind === kind && n.name === name)) {
      return { name: briefName, note: `On Brief ${briefName}’s Needs you list.` }
    }
    const healing = (brief.status?.attention ?? []).find((h) => h.kind === kind && h.name === name)
    if (healing) {
      const at = time(healing.actsAt)
      return {
        name: briefName,
        note:
          at === undefined
            ? `Brief ${briefName} leaves it alone until it changes.`
            : `Brief ${briefName} acts after ${formatClock(at, now)} if this hasn’t healed.`,
      }
    }
  }
  return undefined
}

/** The Container and issue a Task works, or undefined for a Task with a goal. */
function issueOf(task: Task): { namespace?: string; container: string; number: number } | undefined {
  const container = task.spec.containerRef?.name
  const number = task.spec.issue
  if (!container || number === undefined) return undefined
  return { namespace: task.metadata.namespace, container, number }
}

/** Whether a is newer than b: created later, or at the same second with the later name. */
function newerTask(a: Task, b: Task): boolean {
  const ta = time(a.metadata.creationTimestamp) ?? 0
  const tb = time(b.metadata.creationTimestamp) ?? 0
  return ta !== tb ? ta > tb : a.metadata.name > b.metadata.name
}

/**
 * Whether a failed Task's issue has been answered since it failed, so it no
 * longer needs a look: a newer Task works the same issue of the same Container
 * (a retry or a new attempt, whatever its phase), or a Brief's round answered
 * the issue with a retry or a close after the Task failed. A Task with a goal
 * has no issue to answer, and an answer of wait or ask still needs the person.
 */
function failureAnswered(task: Task, done: number, tasks: Task[], briefs: Brief[]): boolean {
  const issue = issueOf(task)
  if (!issue) return false
  const sameIssue = (t: Task) => {
    const other = issueOf(t)
    return (
      other !== undefined &&
      other.namespace === issue.namespace &&
      other.container === issue.container &&
      other.number === issue.number
    )
  }
  if (tasks.some((t) => t !== task && sameIssue(t) && newerTask(t, task))) return true
  return briefs.some(
    (b) =>
      b.metadata.namespace === issue.namespace &&
      (b.status?.tended ?? []).some(
        (t) =>
          t.container === issue.container &&
          t.number === issue.number &&
          (t.answer === 'retry' || t.answer === 'close') &&
          (time(t.at) ?? 0) >= done,
      ),
  )
}

/** Everything that needs a look, errors first. */
export function attention(input: {
  tasks: Task[]
  containers: Container[]
  sessions: Session[]
  crews?: Crew[]
  /** The Briefs, to name the one that watches an item. */
  briefs?: Brief[]
  health?: FactoryHealth
  now: number
}): Attention[] {
  const { tasks, containers, sessions, crews = [], briefs = [], health, now } = input
  const items: Attention[] = []

  if (health) {
    for (const node of health.nodes) {
      if (!node.ready)
        items.push({ level: 'error', title: `Node ${node.name} is not ready`, detail: 'Nothing new can start.' })
      for (const pressure of node.pressure) {
        items.push({ level: 'error', title: `Node ${node.name} reports ${pressure}`, detail: 'Pods may be evicted.' })
      }
      if (node.memoryBytes !== undefined && node.memoryAllocatableBytes > 0) {
        const share = node.memoryBytes / node.memoryAllocatableBytes
        if (share > MEMORY_WARN) {
          items.push({
            level: 'warning',
            title: `Node memory at ${Math.round(share * 100)}%`,
            detail: 'Agents and CI are heavy; the next sandbox may not fit.',
          })
        }
      }
    }
    for (const c of health.components) {
      const restarting = c.pods.find((p) => p.restartedRecently)
      const waiting = c.pods.find((p) => p.waiting)?.waiting
      if (c.state === 'down') {
        items.push({
          level: 'error',
          title: `${c.name} is down`,
          detail: c.pods.length
            ? `${c.role}. ${waiting ?? 'No pod is ready.'}`
            : `${c.role}. No pod found in ${c.namespace}.`,
        })
      } else if (c.state === 'degraded') {
        const why = restarting
          ? `Restarted ${timeAgoShort(restarting.lastRestart?.at, now)}${restarting.lastRestart?.reason ? ` (${restarting.lastRestart.reason})` : ''}.`
          : 'Not every pod is ready.'
        items.push({ level: 'warning', title: `${c.name} is unsteady`, detail: `${c.role}. ${why}` })
      }
    }
    const stall = recentStall(health.history, now)
    if (stall) {
      items.push({
        level: 'warning',
        title: `The API server ${stall.failed ? 'did not answer' : 'stalled'} for ${formatDuration(stall.apiMs)} ${timeAgoShort(stall.at, now)}`,
        detail:
          'Controllers that miss their lease renewal restart, and pushes and CI can fail. Usually CI and an agent sandbox were busy at once; see CPU below.',
      })
    }
    for (const error of health.errors) {
      items.push({ level: 'warning', title: 'Could not read part of the factory', detail: error })
    }
  }

  for (const c of containers) {
    if (c.status?.phase !== 'Failed') continue
    const failedCondition = (c.status.conditions ?? []).find((cond) => cond.status === 'False')
    items.push({
      level: 'error',
      title: `Container ${c.metadata.namespace}/${c.metadata.name} failed`,
      detail: failedCondition?.message ?? 'Scaffolding failed.',
      brief: briefOn(briefs, 'Container', c.metadata.namespace, c.metadata.name, now),
    })
  }

  for (const crew of crews) {
    const ready = findCondition(crew.status?.conditions, 'Ready')
    if (ready?.status !== 'False') continue
    items.push({
      level: 'warning',
      title: `Crew ${crew.metadata.namespace}/${crew.metadata.name} can’t pick up issues`,
      detail: ready.message?.trim() || 'Its Ready condition is False.',
      brief: briefOn(briefs, 'Crew', crew.metadata.namespace, crew.metadata.name, now),
    })
  }

  for (const task of tasks) {
    const key = taskKey(task)
    const brief = briefOn(briefs, 'Task', task.metadata.namespace, task.metadata.name, now)
    if (task.status?.phase === 'Failed') {
      const done = time(task.status.completedAt)
      if (done !== undefined && now - done < RECENT_FAILURE_MS && !failureAnswered(task, done, tasks, briefs)) {
        items.push({
          level: 'warning',
          title: `Task ${key} failed`,
          detail: findCondition(task.status.conditions, 'Succeeded')?.message ?? 'See the Task.',
          task: key,
          brief,
        })
      }
      continue
    }
    if (isFinal(task.status?.phase)) continue
    // Waiting for an unavailable agent costs no round, and says when it ends.
    const back = agentBackAt(task)
    if (back !== undefined) {
      items.push({
        level: 'warning',
        title: `Task ${key} is waiting for the agent until ${formatClock(back, now)}`,
        detail:
          findCondition(task.status?.conditions, 'Progressing')?.message ??
          'Its agent’s service is unavailable: a usage limit, an overload or refused credentials.',
        task: key,
        brief,
      })
      continue
    }
    const stage = currentStage(task) as Stage
    const since = stageSince(task)
    if (since !== undefined && now - since > STUCK_AFTER_MS[stage]) {
      const label = STAGES.find((s) => s.key === stage)?.doing
      items.push({
        level: 'warning',
        title: `Task ${key} has been ${label} for ${formatDuration(now - since)}`,
        detail: findCondition(task.status?.conditions, 'Progressing')?.message ?? '',
        task: key,
        brief,
      })
    }
  }

  for (const s of sessions) {
    if (s.status?.phase === 'Stale') {
      items.push({
        level: 'warning',
        title: `Session ${s.metadata.namespace}/${s.metadata.name} is stale`,
        detail: `No heartbeat ${timeAgoShort(s.status.lastHeartbeat, now)}; its devcontainer may be gone.`,
      })
    }
  }

  return items.sort((a, b) => (a.level === b.level ? 0 : a.level === 'error' ? -1 : 1))
}

function timeAgoShort(iso: string | undefined, now: number): string {
  const t = time(iso)
  return t === undefined ? 'a while ago' : `${formatDuration(now - t)} ago`
}

/** An API server slower than this stalls controllers' lease renewals. */
export const API_STALL_MS = 5_000

/** The worst API stall in the last `withinMs`, if any. */
export function recentStall(
  history: { at: string; apiMs: number; apiFailed?: boolean }[] | undefined,
  now: number,
  withinMs = 15 * 60_000,
): { at: string; apiMs: number; failed: boolean } | undefined {
  let worst: { at: string; apiMs: number; failed: boolean } | undefined
  for (const s of history ?? []) {
    const t = Date.parse(s.at)
    if (now - t > withinMs || (s.apiMs < API_STALL_MS && !s.apiFailed)) continue
    if (!worst || s.apiMs > worst.apiMs) worst = { at: s.at, apiMs: s.apiMs, failed: Boolean(s.apiFailed) }
  }
  return worst
}
