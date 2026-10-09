// What the object pages list, worked out from status: a Brief's rounds and a
// Task's steps as a timeline, a Container's Relationships, a Crew's Tasks, a
// Task's agent runs, and a Brief's planner limit.

import { LABEL_CREW, type Agent, type Brief, type Crew, type Relationship, type Task } from '../../k8s/types'
import { describeTaskPhase } from '../../model/describe'
import { isFinal, taskHeadline } from '../../model/factory'
import type { ChildRef, TimelineEntry } from '../ObjectHeader'
import { pageHref } from './links'

/** The sessionTokenLimit of the Brief's planner Agent, when known. */
export function plannerLimit(agents: Agent[], brief: Brief): number | undefined {
  return agents.find((a) => a.metadata.name === brief.spec.agents?.planner?.name)?.spec.sessionTokenLimit
}

const OUTCOMES = {
  Applied: { title: 'applied', tone: 'ok' },
  Tended: { title: 'tended its failed issues', tone: 'ok' },
  PlannerNeedsInput: { title: 'asked you', tone: 'waiting' },
  RoundFailed: { title: 'failed', tone: 'error' },
} as const

/** The Brief's rounds, oldest first, as the header's timeline. */
export function briefTimeline(brief: Brief): TimelineEntry[] {
  return (brief.status?.history ?? []).map((h) => {
    const outcome = h.outcome ? OUTCOMES[h.outcome] : undefined
    return {
      key: String(h.round),
      title: `Round ${h.round}${outcome ? ` ${outcome.title}` : ''}`,
      detail: h.needsInput ?? h.summary,
      tone: outcome?.tone,
    }
  })
}

/** The Container's Relationships, each with why it is or isn't in the scaffold. */
export function relationshipsOf(relationships: Relationship[], container: string): ChildRef[] {
  return relationships
    .filter(
      (r) =>
        (r.spec.source.kind === 'Container' && r.spec.source.name === container) ||
        (r.spec.target.kind === 'Container' && r.spec.target.name === container),
    )
    .map((r) => {
      const applied = r.status?.conditions?.find((c) => c.type === 'Applied')
      const other =
        r.spec.source.kind === 'Container' && r.spec.source.name === container ? r.spec.target : r.spec.source
      const arrow = other === r.spec.target ? `to ${other.kind} ${other.name}` : `from ${other.kind} ${other.name}`
      return {
        kind: 'Relationship',
        name: r.metadata.name,
        note: `${arrow} · Applied: ${applied ? `${applied.status}${applied.reason ? `, ${applied.reason}` : ''}` : 'not reported yet'}`,
      }
    })
}

/** A Task's status.history, oldest first, as the header's timeline. */
export function taskTimeline(task: Task): TimelineEntry[] {
  return (task.status?.history ?? []).map((h, i) => ({
    key: String(i),
    title: `${describeTaskPhase(h.phase).label}${h.round ? `, round ${h.round}` : ''}${h.reason ? ` · ${h.reason}` : ''}`,
    detail: h.message || undefined,
    at: h.at,
    tone: h.phase === 'Failed' ? 'error' : h.phase === 'Blocked' || h.phase === 'Pending' ? 'waiting' : 'ok',
  }))
}

/** One agent run of a Task: an implement or a review run of a round. */
export interface AgentRun {
  /** 1 for the first run. */
  n: number
  round: number
  role: 'implement' | 'review'
  /** The Agent that ran it. */
  agent: string
  /** When it started. */
  at: string
  /** Whether it runs now. */
  current: boolean
}

/** History reasons on an Implementing or Reviewing step that mean no agent runs yet. */
const NOT_RUNNING = new Set(['WaitingForSlot', 'AgentUnavailable'])

/**
 * The Task's agent runs, oldest first, from status.history: each step into
 * Implementing or Reviewing starts one, unless it only waits for a slot or
 * for the agent; later steps of the same run (its round and role) don't.
 */
export function agentRuns(task: Task): AgentRun[] {
  const runs: AgentRun[] = []
  const history = task.status?.history ?? []
  let last: string | undefined
  for (const h of history) {
    const role = h.phase === 'Implementing' ? 'implement' : h.phase === 'Reviewing' ? 'review' : undefined
    if (!role || (h.reason && NOT_RUNNING.has(h.reason))) {
      last = undefined
      continue
    }
    const round = h.round ?? 0
    const key = `${role}|${round}`
    if (key === last) continue
    last = key
    runs.push({
      n: runs.length + 1,
      round,
      role,
      agent: role === 'review' ? (task.spec.reviewerRef?.name ?? task.spec.agentRef.name) : task.spec.agentRef.name,
      at: h.at,
      current: false,
    })
  }
  // The run the newest step belongs to runs now, unless the Task has ended.
  const end = runs[runs.length - 1]
  if (end && !isFinal(task.status?.phase) && last === `${end.role}|${end.round}`) end.current = true
  return runs
}

/** The Crew's Tasks, newest first, each with what it works on and its phase. */
export function crewTasks(crew: Crew, tasks: Task[]): ChildRef[] {
  const ns = crew.metadata.namespace ?? ''
  return tasks
    .filter((t) => (t.metadata.namespace ?? '') === ns && t.metadata.labels?.[LABEL_CREW] === crew.metadata.name)
    .sort(
      (a, b) =>
        (b.metadata.creationTimestamp ?? '').localeCompare(a.metadata.creationTimestamp ?? '') ||
        a.metadata.name.localeCompare(b.metadata.name),
    )
    .map((t) => ({
      kind: 'Task',
      name: t.metadata.name,
      href: pageHref(ns, 'Task', t.metadata.name),
      note: `${taskHeadline(t).text} · ${describeTaskPhase(t.status?.phase).label}`,
    }))
}
