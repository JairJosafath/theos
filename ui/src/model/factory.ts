// The factory view's model: what the Tasks across all projects are doing, where
// their time goes, and what needs attention. Plain functions over resources and
// the /factory/health summary, so they are tested without React.

import type { Container, Session, Task, TaskPhase, TaskTransition } from '../k8s/types'
import { findCondition } from '../k8s/types'
import type { FactoryHealth } from '../k8s/health'

/** Where a Task's time goes. Each timeline step falls in one stage. */
export type Stage = 'queued' | 'slot' | 'implement' | 'checks' | 'review'

export const STAGES: { key: Stage; label: string; doing: string; explanation: string }[] = [
  {
    key: 'queued',
    label: 'Queued',
    doing: 'queued',
    explanation: 'Waiting for its Container, or for an earlier Task on it',
  },
  {
    key: 'slot',
    label: 'Waiting for a slot',
    doing: 'waiting for a slot',
    explanation: 'Waiting for a free agent slot',
  },
  {
    key: 'implement',
    label: 'Implementing',
    doing: 'implementing',
    explanation: 'The agent writes the change in a sandbox',
  },
  { key: 'checks', label: 'CI checks', doing: 'waiting for CI', explanation: 'CI runs make check on the pull request' },
  { key: 'review', label: 'Reviewing', doing: 'in review', explanation: 'An agent reviews the pull request' },
]

export function isFinal(phase: TaskPhase | undefined): boolean {
  return phase === 'Merged' || phase === 'Failed'
}

/** The stage of one timeline step; undefined for Merged and Failed, which end the timeline. */
export function stageOf(step: Pick<TaskTransition, 'phase' | 'reason'>): Stage | undefined {
  if (step.reason === 'WaitingForSlot') return 'slot'
  switch (step.phase) {
    case 'Pending':
      return 'queued'
    case 'Implementing':
      return 'implement'
    case 'Checking':
      return 'checks'
    case 'Reviewing':
      return 'review'
    default:
      return undefined
  }
}

export interface Segment {
  stage: Stage
  phase: TaskPhase
  round: number
  reason?: string
  message?: string
  start: number
  end: number
  /** True for the step the Task is in now. */
  current: boolean
}

function time(iso: string | undefined): number | undefined {
  if (!iso) return undefined
  const t = Date.parse(iso)
  return Number.isNaN(t) ? undefined : t
}

/**
 * A Task's timeline as segments, oldest first: each step lasts until the next
 * one, the last until the Task completed or until now.
 */
export function taskSegments(task: Task, now: number): Segment[] {
  const history = task.status?.history ?? []
  const segments: Segment[] = []
  const completed = time(task.status?.completedAt)
  history.forEach((step, i) => {
    const stage = stageOf(step)
    const start = time(step.at)
    if (!stage || start === undefined) return
    const next = history[i + 1]
    const end = next ? (time(next.at) ?? start) : isFinal(task.status?.phase) ? (completed ?? start) : now
    segments.push({
      stage,
      phase: step.phase,
      round: step.round ?? 0,
      reason: step.reason,
      message: step.message,
      start,
      end: Math.max(start, end),
      current: !next && !isFinal(task.status?.phase),
    })
  })
  return segments
}

/** Time per stage, summed over segments. */
export function timeByStage(segments: Segment[]): Record<Stage, number> {
  const totals: Record<Stage, number> = { queued: 0, slot: 0, implement: 0, checks: 0, review: 0 }
  for (const s of segments) totals[s.stage] += s.end - s.start
  return totals
}

/** The Task's stage now: for the board. Tasks without a timeline go by phase and slot wait. */
export function currentStage(task: Task): Stage | 'done' {
  const phase = task.status?.phase ?? 'Pending'
  if (isFinal(phase)) return 'done'
  if (task.status?.slotWaitSince) return 'slot'
  return stageOf({ phase, reason: findCondition(task.status?.conditions, 'Progressing')?.reason }) ?? 'queued'
}

/** Since when the Task is in its current stage. */
export function stageSince(task: Task): number | undefined {
  if (task.status?.slotWaitSince) return time(task.status.slotWaitSince)
  const history = task.status?.history
  if (history?.length) return time(history[history.length - 1].at)
  return time(task.status?.phaseChangedAt) ?? time(task.metadata.creationTimestamp)
}

export function taskKey(task: Task): string {
  return `${task.metadata.namespace ?? ''}/${task.metadata.name}`
}

export type Column = Stage | 'done'

export const COLUMNS: { key: Column; label: string }[] = [
  { key: 'queued', label: 'Queued' },
  { key: 'slot', label: 'Waiting for a slot' },
  { key: 'implement', label: 'Implementing' },
  { key: 'checks', label: 'CI checks' },
  { key: 'review', label: 'Reviewing' },
  { key: 'done', label: 'Done' },
]

/**
 * Tasks on a board by stage. Active Tasks are oldest in their stage first; done
 * Tasks newest first, at most `doneLimit` of them.
 */
export function boardColumns(tasks: Task[], doneLimit = 8): Record<Column, Task[]> {
  const columns: Record<Column, Task[]> = { queued: [], slot: [], implement: [], checks: [], review: [], done: [] }
  for (const task of tasks) columns[currentStage(task)].push(task)
  for (const key of Object.keys(columns) as Column[]) {
    if (key === 'done') continue
    columns[key].sort((a, b) => (stageSince(a) ?? 0) - (stageSince(b) ?? 0))
  }
  columns.done.sort(
    (a, b) =>
      (time(b.status?.completedAt) ?? 0) - (time(a.status?.completedAt) ?? 0) || taskKey(a).localeCompare(taskKey(b)),
  )
  columns.done = columns.done.slice(0, doneLimit)
  return columns
}

/** The Tasks waiting for an agent slot, in the order they get one (longest waiter first). */
export function slotQueue(tasks: Task[]): Task[] {
  return tasks
    .filter((t) => t.status?.slotWaitSince && !isFinal(t.status?.phase))
    .sort((a, b) => (time(a.status?.slotWaitSince) ?? 0) - (time(b.status?.slotWaitSince) ?? 0))
}

/** Tasks that hold an agent slot now: their sandbox runs. */
export function runningTasks(tasks: Task[]): Task[] {
  return tasks.filter((t) => t.status?.sandbox && !isFinal(t.status?.phase))
}

export interface FactoryStats {
  total: number
  active: number
  merged: number
  failed: number
  /** Merged out of finished, 0 to 1; undefined before any finished. */
  successRate?: number
  /** Mean rounds of merged Tasks. */
  meanRounds?: number
  /** Share of merged Tasks merged in round 1. */
  firstRoundRate?: number
  /** Median time from creation to merge or failure. */
  medianLeadMs?: number
  /** Time per stage over every Task with a timeline. */
  timeByStage: Record<Stage, number>
  /** How many Tasks have a timeline; older Tasks don't. */
  withTimeline: number
}

function median(values: number[]): number | undefined {
  if (!values.length) return undefined
  const sorted = [...values].sort((a, b) => a - b)
  const mid = Math.floor(sorted.length / 2)
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2
}

export function factoryStats(tasks: Task[], now: number): FactoryStats {
  const merged = tasks.filter((t) => t.status?.phase === 'Merged')
  const failed = tasks.filter((t) => t.status?.phase === 'Failed')
  const finished = [...merged, ...failed]
  const leads = finished
    .map((t) => {
      const start = time(t.metadata.creationTimestamp)
      const end = time(t.status?.completedAt)
      return start !== undefined && end !== undefined ? end - start : undefined
    })
    .filter((v): v is number => v !== undefined)
  const totals: Record<Stage, number> = { queued: 0, slot: 0, implement: 0, checks: 0, review: 0 }
  let withTimeline = 0
  for (const task of tasks) {
    const segments = taskSegments(task, now)
    if (!segments.length) continue
    withTimeline++
    const byStage = timeByStage(segments)
    for (const s of STAGES) totals[s.key] += byStage[s.key]
  }
  return {
    total: tasks.length,
    active: tasks.length - finished.length,
    merged: merged.length,
    failed: failed.length,
    successRate: finished.length ? merged.length / finished.length : undefined,
    meanRounds: merged.length ? merged.reduce((n, t) => n + (t.status?.round ?? 1), 0) / merged.length : undefined,
    firstRoundRate: merged.length
      ? merged.filter((t) => (t.status?.round ?? 1) <= 1).length / merged.length
      : undefined,
    medianLeadMs: median(leads),
    timeByStage: totals,
    withTimeline,
  }
}

/** How long a Task may stay in a stage before it needs a look. */
export const STUCK_AFTER_MS: Record<Stage, number> = {
  queued: 60 * 60_000,
  slot: 60 * 60_000,
  implement: 2 * 60 * 60_000,
  // The operator fails a Task after 30 minutes without a CI result.
  checks: 30 * 60_000,
  review: 60 * 60_000,
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
}

/** Everything that needs a look, errors first. */
export function attention(input: {
  tasks: Task[]
  containers: Container[]
  sessions: Session[]
  health?: FactoryHealth
  now: number
}): Attention[] {
  const { tasks, containers, sessions, health, now } = input
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
    })
  }

  for (const task of tasks) {
    const key = taskKey(task)
    if (task.status?.phase === 'Failed') {
      const done = time(task.status.completedAt)
      if (done !== undefined && now - done < RECENT_FAILURE_MS) {
        items.push({
          level: 'warning',
          title: `Task ${key} failed`,
          detail: findCondition(task.status.conditions, 'Succeeded')?.message ?? 'See the Task.',
          task: key,
        })
      }
      continue
    }
    if (isFinal(task.status?.phase)) continue
    const stage = currentStage(task) as Stage
    const since = stageSince(task)
    if (since !== undefined && now - since > STUCK_AFTER_MS[stage]) {
      const label = STAGES.find((s) => s.key === stage)?.doing
      items.push({
        level: 'warning',
        title: `Task ${key} has been ${label} for ${formatDuration(now - since)}`,
        detail: findCondition(task.status?.conditions, 'Progressing')?.message ?? '',
        task: key,
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

/** "45 s", "3 min", "2 h 5 min", "3 d 4 h". */
export function formatDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s} s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} min`
  const h = Math.floor(m / 60)
  if (h < 24) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`
  const d = Math.floor(h / 24)
  return h % 24 ? `${d} d ${h % 24} h` : `${d} d`
}

function timeAgoShort(iso: string | undefined, now: number): string {
  const t = time(iso)
  return t === undefined ? 'a while ago' : `${formatDuration(now - t)} ago`
}

/** "1.2 GiB", "93 MiB". */
export function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined) return '–'
  if (bytes >= 2 ** 30) return `${(bytes / 2 ** 30).toFixed(1)} GiB`
  return `${Math.round(bytes / 2 ** 20)} MiB`
}

/** "163m" as "0.16 cores", small numbers as millicores. */
export function formatCpu(milli: number | undefined): string {
  if (milli === undefined) return '–'
  if (milli >= 1000) return `${(milli / 1000).toFixed(1)} cores`
  return `${milli}m`
}

export function percent(part: number | undefined, whole: number | undefined): number | undefined {
  if (part === undefined || !whole) return undefined
  return Math.min(100, Math.round((part / whole) * 100))
}
