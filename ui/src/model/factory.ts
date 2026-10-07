// The factory view's model: what the Tasks across all projects are doing, where
// their time goes, and what needs attention. Plain functions over resources and
// the /factory/health summary, so they are tested without React.

import type {
  Agent,
  Brief,
  BriefItemKind,
  Container,
  Crew,
  CrewPhase,
  Session,
  Task,
  TaskPhase,
  TaskTransition,
} from '../k8s/types'
import { findCondition } from '../k8s/types'
import { plural, titleOf } from './describe'
import type { FactoryHealth } from '../k8s/health'

/** Where a Task's time goes. Each timeline step falls in one stage. */
export type Stage = 'queued' | 'slot' | 'implement' | 'checks' | 'review' | 'blocked'

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
  {
    key: 'blocked',
    label: 'Blocked',
    doing: 'waiting on a Standard change',
    explanation: 'Waits for the Standard change it filed to merge',
  },
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
    case 'Blocked':
      return 'blocked'
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
  const totals: Record<Stage, number> = { queued: 0, slot: 0, implement: 0, checks: 0, review: 0, blocked: 0 }
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

/** What a Task works on, in one line. */
export interface Headline {
  /** "#3 Map positions inside YAML scalars" for an issue, else the goal's first line. */
  text: string
  /** The issue's number and address, for a Task made from an issue. */
  issue?: number
  url?: string
}

export function taskHeadline(task: Task): Headline {
  const issue = task.spec.issue
  if (issue) {
    // The title arrives once the operator has read the issue.
    const title = task.status?.issueTitle?.trim()
    return { text: title ? `#${issue} ${title}` : `Issue #${issue}`, issue, url: task.status?.issueURL || undefined }
  }
  return { text: (task.spec.goal ?? '').split('\n')[0].trim() }
}

/** An Agent in a role, as the views show it: its name and the model it runs. */
export interface AgentRole {
  /** metadata.name, from the reference. */
  name: string
  /** displayName, or the name. */
  title: string
  /** spec.model; "stand-in" for the scripted agent; undefined for the harness's default model. */
  model?: string
  /** False when the Agent could not be found: then only its name is known. */
  found: boolean
}

/** The Agent a reference names, in the namespace of the object that refers to it. */
export function agentRole(agents: Agent[], namespace: string | undefined, name: string): AgentRole {
  const agent = agents.find(
    (a) => a.metadata.name === name && (!namespace || !a.metadata.namespace || a.metadata.namespace === namespace),
  )
  if (!agent) return { name, title: name, found: false }
  const model = agent.spec.model?.trim() || (agent.spec.harness === 'stand-in' ? 'stand-in' : undefined)
  return { name, title: agent.spec.displayName?.trim() || name, model, found: true }
}

/** Who implements a Task, and who reviews it: its reviewerRef, or else the implementer. */
export function taskRoles(task: Task, agents: Agent[]): { implementer: AgentRole; reviewer: AgentRole } {
  const namespace = task.metadata.namespace
  return {
    implementer: agentRole(agents, namespace, task.spec.agentRef.name),
    reviewer: agentRole(agents, namespace, (task.spec.reviewerRef ?? task.spec.agentRef).name),
  }
}

/**
 * When a Task tries its agent again, while it waits for an unavailable one (a
 * usage limit, an overloaded service): undefined when it isn't waiting for that.
 */
export function agentBackAt(task: Task): number | undefined {
  if (isFinal(task.status?.phase)) return undefined
  if (findCondition(task.status?.conditions, 'Progressing')?.reason !== 'AgentUnavailable') return undefined
  return time(task.status?.agentBackAt)
}

/** The issues filed from a Task's review on merge, as links: "#15" when the address ends in an issue number. */
export function followUpLinks(task: Task): { url: string; label: string }[] {
  return (task.status?.followUps ?? []).map((url) => {
    const number = /\/issues\/(\d+)\/?$/.exec(url)?.[1]
    return { url, label: number ? `#${number}` : url }
  })
}

/** The Standard change a Blocked Task waits on: its issue, and the protected paths it asks for. */
export function standardChangeLink(task: Task): { label: string; url?: string; paths: string[] } | undefined {
  const change = task.status?.standardChange
  if (task.status?.phase !== 'Blocked' || !change) return undefined
  return { label: `#${change.issue}`, url: change.issueURL, paths: change.paths }
}

export type Column = Stage | 'done'

export const COLUMNS: { key: Column; label: string }[] = [
  { key: 'queued', label: 'Queued' },
  { key: 'slot', label: 'Waiting for a slot' },
  { key: 'implement', label: 'Implementing' },
  { key: 'checks', label: 'CI checks' },
  { key: 'review', label: 'Reviewing' },
  { key: 'blocked', label: 'Blocked' },
  { key: 'done', label: 'Done' },
]

/**
 * Tasks on a board by stage. Active Tasks are oldest in their stage first; done
 * Tasks newest first, at most `doneLimit` of them.
 */
export function boardColumns(tasks: Task[], doneLimit = 8): Record<Column, Task[]> {
  const columns: Record<Column, Task[]> = {
    queued: [],
    slot: [],
    implement: [],
    checks: [],
    review: [],
    blocked: [],
    done: [],
  }
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
  const totals: Record<Stage, number> = { queued: 0, slot: 0, implement: 0, checks: 0, review: 0, blocked: 0 }
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
      if (done !== undefined && now - done < RECENT_FAILURE_MS) {
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

/** A Crew as the views show it: who implements and reviews, and what it is doing. */
export interface CrewSummary {
  /** namespace/name */
  key: string
  namespace: string
  name: string
  title: string
  description?: string
  container: string
  phase?: CrewPhase
  /** spec.paused: it picks up no new issues, though a Task already started runs to its end. */
  paused: boolean
  /** Set when the Ready condition is False: the Crew can't pick up issues, and why. */
  notReady?: { reason?: string; message?: string }
  implementer: AgentRole
  reviewer: AgentRole
  /** spec.parallel: how many Tasks it runs at once (1 when unset). */
  parallel: number
  /** The Tasks working on the Container now, one per place in use. */
  active: { name: string; key: string; issue?: number; headline?: string; found: boolean }[]
  /** Ready issues in pick-up order, and ready ones blocked by open dependencies. */
  ready: number[]
  waiting: number[]
  lastPolled?: string
}

export function crewSummary(crew: Crew, agents: Agent[], tasks: Task[]): CrewSummary {
  const namespace = crew.metadata.namespace ?? ''
  const readyCondition = findCondition(crew.status?.conditions, 'Ready')
  return {
    key: `${namespace}/${crew.metadata.name}`,
    namespace,
    name: crew.metadata.name,
    title: titleOf(crew),
    description: crew.spec.description?.trim() || undefined,
    container: crew.spec.containerRef.name,
    phase: crew.status?.phase,
    paused: Boolean(crew.spec.paused),
    notReady:
      readyCondition?.status === 'False'
        ? { reason: readyCondition.reason, message: readyCondition.message?.trim() || undefined }
        : undefined,
    implementer: agentRole(agents, namespace, crew.spec.implementer.name),
    reviewer: agentRole(agents, namespace, crew.spec.reviewer.name),
    parallel: crew.spec.parallel || 1,
    active: (crew.status?.activeTasks ?? []).map((name) => {
      const task = tasks.find((t) => t.metadata.name === name && (t.metadata.namespace ?? '') === namespace)
      return {
        name,
        key: `${namespace}/${name}`,
        issue: task?.spec.issue || undefined,
        headline: task ? taskHeadline(task).text : undefined,
        found: Boolean(task),
      }
    }),
    ready: crew.status?.ready ?? [],
    waiting: crew.status?.waiting ?? [],
    lastPolled: crew.status?.lastPolled,
  }
}

/** Every Crew, by project and then by name. */
export function crewSummaries(crews: Crew[], agents: Agent[], tasks: Task[]): CrewSummary[] {
  return crews
    .map((c) => crewSummary(c, agents, tasks))
    .sort((a, b) => a.namespace.localeCompare(b.namespace) || a.title.localeCompare(b.title))
}

/** What a Crew is doing, in a few words: "working on #4", "paused", "no ready issues". */
export function crewDoing(crew: CrewSummary): string {
  if (crew.active.length) return `working on ${crew.active.map((a) => (a.issue ? `#${a.issue}` : a.name)).join(', ')}`
  if (crew.notReady) return 'can’t pick up issues'
  if (crew.paused || crew.phase === 'Paused') return 'paused'
  if (crew.phase === 'Idle') {
    return crew.waiting.length
      ? `no ready issues; ${plural(crew.waiting.length, 'waits', 'wait')} on open dependencies`
      : 'no ready issues'
  }
  if (crew.phase === 'Waiting') return 'waiting'
  return 'not started yet'
}

/** "#5, #6": issue numbers as a list. */
export function issueList(numbers: number[]): string {
  return numbers.map((n) => `#${n}`).join(', ')
}

/** A time of day, "15:04"; with the weekday when it isn't today, "Fri 03:00". */
export function formatClock(t: number, now: number): string {
  const d = new Date(t)
  const clock = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  if (d.toDateString() === new Date(now).toDateString()) return clock
  return `${d.toLocaleDateString('en-US', { weekday: 'short' })} ${clock}`
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

/** A timeline row: consecutive segments of one stage and round, with their messages. */
export interface Step {
  stage: Stage
  round: number
  start: number
  end: number
  current: boolean
  messages: string[]
}

/** Joins consecutive segments of the same stage and round, such as "Running" and then "Waiting". */
export function timelineSteps(segments: Segment[]): Step[] {
  const steps: Step[] = []
  for (const s of segments) {
    const last = steps[steps.length - 1]
    if (last && last.stage === s.stage && last.round === s.round) {
      last.end = s.end
      last.current = s.current
      if (s.message && !last.messages.includes(s.message)) last.messages.push(s.message)
      continue
    }
    steps.push({
      stage: s.stage,
      round: s.round,
      start: s.start,
      end: s.end,
      current: s.current,
      messages: s.message ? [s.message] : [],
    })
  }
  return steps
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

/** A Crew's running Task as the "who's working" panel shows it: who works on it now, with their model. */
export interface ActiveCrewTask {
  /** namespace/name */
  key: string
  name: string
  crew: string
  container: string
  issue?: number
  issueTitle?: string
  phase?: TaskPhase
  /** The implementer while the Task implements, the reviewer while it reviews. */
  agent: AgentRole
}

/** The running Tasks of the Crews of the given Containers, by Container and then issue. */
export function activeCrewTasks(crews: Crew[], tasks: Task[], agents: Agent[], containers: string[]): ActiveCrewTask[] {
  const inScope = new Set(containers)
  const found: ActiveCrewTask[] = []
  for (const crew of crews) {
    const namespace = crew.metadata.namespace ?? ''
    if (!inScope.has(crew.spec.containerRef.name)) continue
    for (const name of crew.status?.activeTasks ?? []) {
      const task = tasks.find((t) => t.metadata.name === name && (t.metadata.namespace ?? '') === namespace)
      if (!task) continue
      const roles = taskRoles(task, agents)
      found.push({
        key: `${namespace}/${name}`,
        name,
        crew: crew.metadata.name,
        container: crew.spec.containerRef.name,
        issue: task.spec.issue || undefined,
        issueTitle: task.status?.issueTitle?.trim() || undefined,
        phase: task.status?.phase,
        agent: task.status?.phase === 'Reviewing' ? roles.reviewer : roles.implementer,
      })
    }
  }
  return found.sort((a, b) => a.container.localeCompare(b.container) || (a.issue ?? 0) - (b.issue ?? 0))
}
