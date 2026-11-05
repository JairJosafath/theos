// What the object pages list, worked out from status: a Brief's rounds and a
// Task's steps as a timeline, a Container's Relationships, a Crew's Tasks, a
// Task's agent runs, and a Brief's planner limit.

import { LABEL_CREW, type Agent, type Brief, type Crew, type Relationship, type Task } from '../../k8s/types'
import { describeTaskPhase } from '../../model/describe'
import { isFinal, taskHeadline } from '../../model/factory'
import { runAgentName, runRole, type RunRole } from '../../model/tokens'
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

/** One agent run of a Task: an implement, review or plan run of a round. */
export interface AgentRun {
  /** 1 for the first run. */
  n: number
  round: number
  role: RunRole
  /** The Agent that ran it. */
  agent: string
  /** When it started, or, for a run the Task recorded, when it ended. */
  at: string
  /** Whether it runs now. */
  current: boolean
  /** Whether the Task recorded it (status.runs): it ended, at `at`. */
  recorded?: boolean
  /** The sizes, in tokens, at which its sessions handed off; only a recorded run knows them. */
  handoffs?: number[]
}

/** History reasons on an Implementing or Reviewing step that mean no agent runs yet. */
const NOT_RUNNING = new Set(['WaitingForSlot', 'AgentUnavailable', 'OverBudget'])

/**
 * The Task's agent runs, oldest first. A Task records each run as it ends
 * (status.runs); the one in its sandbox now follows them. A Task that recorded
 * none derives them from status.history: each step into Implementing or
 * Reviewing starts one, unless it only waits for a slot, for the agent or at
 * its Crew's cost cap; later steps of the same run (its round and role) don't.
 */
export function agentRuns(task: Task): AgentRun[] {
  const recorded = task.status?.runs ?? []
  if (!recorded.length) return historyRuns(task)
  const runs: AgentRun[] = recorded.map((r, i) => {
    const role = runRole(task, r)
    return {
      n: i + 1,
      round: r.round,
      role,
      agent: runAgentName(task, role),
      at: r.endedAt,
      current: false,
      recorded: true,
      handoffs: r.sessionHandoffs ?? [],
    }
  })
  const status = task.status
  if (status?.sandbox && !isFinal(status.phase)) {
    const role = sandboxRole(status.sandbox) ?? phaseRole(status.phase) ?? runRole(task, {})
    const history = status.history ?? []
    const at = history[history.length - 1]?.at ?? status.phaseChangedAt ?? ''
    runs.push({
      n: runs.length + 1,
      round: status.round ?? 0,
      role,
      agent: runAgentName(task, role),
      at,
      current: true,
    })
  }
  return runs
}

/**
 * The role a sandbox name carries (<task>-r<round>-<role>[-a<n>|-x<n>]), as
 * model.SandboxRole reads it: a review sandbox also runs while Checking.
 */
function sandboxRole(sandbox: string): AgentRun['role'] | undefined {
  const m = /-r\d+-(implement|review|verify|plan)(?:-[ax]\d+)?$/.exec(sandbox)
  return m ? (m[1] as AgentRun['role']) : undefined
}

/** The role of the run a reviewing or verifying phase waits on. */
function phaseRole(phase: string | undefined): RunRole | undefined {
  if (phase === 'Reviewing') return 'review'
  if (phase === 'Verifying') return 'verify'
  return undefined
}

function historyRuns(task: Task): AgentRun[] {
  const runs: AgentRun[] = []
  const history = task.status?.history ?? []
  let last: string | undefined
  for (const h of history) {
    const role = h.phase === 'Implementing' ? 'implement' : phaseRole(h.phase)
    if (!role || (h.reason && NOT_RUNNING.has(h.reason))) {
      last = undefined
      continue
    }
    const round = h.round ?? 0
    const key = `${role}|${round}`
    if (key === last) continue
    last = key
    runs.push({ n: runs.length + 1, round, role, agent: runAgentName(task, role), at: h.at, current: false })
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
