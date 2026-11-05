// The Work area's model of Tasks: their stages and timelines, the board, the
// agent slots and the stats. Plain functions over resources, so they are tested
// without React; attention.ts, crews.ts and format.ts hold the rest of the area's model.

import type { Agent, Task, TaskPhase, TaskTransition } from '../k8s/types'
import { findCondition } from '../k8s/types'

/** Where a Task's time goes. Each timeline step falls in one stage. */
export type Stage = 'queued' | 'slot' | 'implement' | 'checks' | 'review' | 'approval' | 'blocked'

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
    key: 'approval',
    label: 'Awaiting approval',
    doing: 'waiting for a person to approve its spec',
    explanation: 'A spec waits for a person to approve its pull request',
  },
  {
    key: 'blocked',
    label: 'Blocked',
    doing: 'waiting on a Standard change',
    explanation: 'Waits for the Standard change it filed to merge',
  },
]

export function stageLabel(stage: Stage): string {
  return STAGES.find((s) => s.key === stage)?.label ?? stage
}

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
    case 'AwaitingApproval':
      return 'approval'
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

export function time(iso: string | undefined): number | undefined {
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
  const totals: Record<Stage, number> = {
    queued: 0,
    slot: 0,
    implement: 0,
    checks: 0,
    review: 0,
    approval: 0,
    blocked: 0,
  }
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
export function findAgent(agents: Agent[], namespace: string | undefined, name: string): Agent | undefined {
  return agents.find(
    (a) => a.metadata.name === name && (!namespace || !a.metadata.namespace || a.metadata.namespace === namespace),
  )
}

/** The Agent a reference names, as the views show it. */
export function agentRole(agents: Agent[], namespace: string | undefined, name: string): AgentRole {
  const agent = findAgent(agents, namespace, name)
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

export type Column = Stage | 'done'

export const COLUMNS: { key: Column; label: string }[] = [
  { key: 'queued', label: 'Queued' },
  { key: 'slot', label: 'Waiting for a slot' },
  { key: 'implement', label: 'Implementing' },
  { key: 'checks', label: 'CI checks' },
  { key: 'review', label: 'Reviewing' },
  { key: 'approval', label: 'Awaiting approval' },
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
    approval: [],
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

export function median(values: number[]): number | undefined {
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
  const totals: Record<Stage, number> = {
    queued: 0,
    slot: 0,
    implement: 0,
    checks: 0,
    review: 0,
    approval: 0,
    blocked: 0,
  }
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
