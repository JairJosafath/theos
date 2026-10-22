// The Work area's chart of what agent runs use: each run's largest session, by
// its Task's round and outcome, against its Agent's sessionTokenLimit, so a
// reporter can judge the limit (docs/plans/done/token-chart.md). The limit is
// the Agent's current spec.sessionTokenLimit and the Agent the Task's current
// agentRef/reviewerRef: a run records neither, so past runs are rejudged when
// either changes (#1268). Plain functions over Tasks and Agents, tested without React.

import type { Agent, Task, TaskAgentRun, TokenUsage } from '../k8s/types'
import { formatTokens } from './brief'
import { plural } from './describe'
import { findAgent, median, taskHeadline, taskKey } from './factory'

/** How a run's Task ended: merged, failed, or not yet. */
export type Outcome = 'merged' | 'failed' | 'active'

export const OUTCOMES: { key: Outcome; label: string }[] = [
  { key: 'merged', label: 'Merged' },
  { key: 'failed', label: 'Failed' },
  { key: 'active', label: 'Still going' },
]

export type RunRole = 'implement' | 'review' | 'plan'

/** One recorded agent run that reported its tokens. */
export interface RunPoint {
  /** Its Task, by namespace/name. */
  task: string
  /** The Task's name and namespace, for its page. */
  name: string
  namespace: string
  /** What the Task works on, in one line. */
  headline: string
  round: number
  role: RunRole
  /** How large the run got: the prompt of its biggest turn, in tokens. */
  largest: number
  /** All the tokens it used; undefined when it reported only its largest session. */
  total?: number
  /** The sizes at which its sessions handed off to fresh ones. */
  handoffs: number[]
  outcome: Outcome
  /** The rounds its Task used, so far for one still going. */
  rounds: number
}

/** What one Agent's runs used, by its Tasks' outcome: a row of the chart's table. */
export interface OutcomeRow {
  outcome: Outcome
  /** Its Tasks with a run on the chart. */
  tasks: number
  /** The rounds those Tasks used, on average. */
  meanRounds: number
  runs: number
  medianLargest: number
  /** All tokens per run, on average, over the runs that reported them. */
  meanTotal?: number
  /** Runs whose largest session passed the Agent's limit. */
  pastLimit: number
  /** Runs whose sessions handed off. */
  handedOff: number
}

/** One Agent's recorded runs: the points of its chart. */
export interface AgentTokens {
  /** namespace/name. */
  key: string
  name: string
  namespace: string
  /** displayName, or the name. */
  title: string
  /** spec.sessionTokenLimit; undefined for an Agent that has none, or wasn't found. */
  limit?: number
  /** False when no Agent of that name exists any more: only its name is known. */
  found: boolean
  /** Its runs that reported their tokens, oldest Task first. */
  points: RunPoint[]
  /** Its recorded runs that didn't report their tokens, such as the stand-in's. */
  unreported: number
  /** The highest round any of its points is in. */
  maxRound: number
  /** One row per outcome that has points, in OUTCOMES' order. */
  byOutcome: OutcomeRow[]
}

export function outcomeOf(task: Task): Outcome {
  const phase = task.status?.phase
  return phase === 'Merged' ? 'merged' : phase === 'Failed' ? 'failed' : 'active'
}

/** A recorded run's role; one that didn't say is its Task's own: implement, or plan for a Plan Task. */
export function runRole(task: Task, run: Pick<TaskAgentRun, 'role'>): RunRole {
  if (run.role === 'implement' || run.role === 'review' || run.role === 'plan') return run.role
  return task.spec.kind === 'Plan' ? 'plan' : 'implement'
}

/** The Agent that ran a role of a Task: the reviewer reviews, when the Task has one; the agentRef does the rest. */
export function runAgentName(task: Task, role: RunRole): string {
  return role === 'review' ? (task.spec.reviewerRef?.name ?? task.spec.agentRef.name) : task.spec.agentRef.name
}

/** Every token a run reported; undefined when it reported none of the four kinds. */
export function totalTokens(tokens: TokenUsage | undefined): number | undefined {
  if (!tokens) return undefined
  const parts = [tokens.input, tokens.output, tokens.cacheRead, tokens.cacheWrite].filter(
    (v): v is number => v !== undefined,
  )
  return parts.length ? parts.reduce((a, b) => a + b, 0) : undefined
}

/**
 * Each Agent's recorded runs (status.runs), with what they used against its
 * sessionTokenLimit: the Agents with the most runs on the chart first. A run
 * that reported no largest session counts as unreported; a Task that recorded
 * no runs (one from before runs were recorded) adds nothing.
 */
export function agentTokens(tasks: Task[], agents: Agent[]): AgentTokens[] {
  const byKey = new Map<string, AgentTokens>()
  const ordered = [...tasks].sort(
    (a, b) =>
      (a.metadata.creationTimestamp ?? '').localeCompare(b.metadata.creationTimestamp ?? '') ||
      taskKey(a).localeCompare(taskKey(b)),
  )
  for (const task of ordered) {
    const namespace = task.metadata.namespace ?? ''
    const outcome = outcomeOf(task)
    const runs = task.status?.runs ?? []
    const rounds = Math.max(task.status?.round ?? 0, ...runs.map((r) => r.round))
    for (const run of runs) {
      const role = runRole(task, run)
      const name = runAgentName(task, role)
      const key = `${namespace}/${name}`
      let entry = byKey.get(key)
      if (!entry) {
        entry = newEntry(agents, namespace, name)
        byKey.set(key, entry)
      }
      const largest = run.tokens?.largestSession ?? 0
      if (largest <= 0) {
        entry.unreported++
        continue
      }
      entry.points.push({
        task: taskKey(task),
        name: task.metadata.name,
        namespace,
        headline: taskHeadline(task).text,
        round: run.round,
        role,
        largest,
        total: totalTokens(run.tokens),
        handoffs: run.sessionHandoffs ?? [],
        outcome,
        rounds,
      })
    }
  }
  const result = [...byKey.values()].map((entry) => ({
    ...entry,
    maxRound: Math.max(1, ...entry.points.map((p) => p.round)),
    byOutcome: outcomeRows(entry.points, entry.limit),
  }))
  return result.sort((a, b) => b.points.length - a.points.length || a.key.localeCompare(b.key))
}

function newEntry(agents: Agent[], namespace: string, name: string): AgentTokens {
  const agent = findAgent(agents, namespace, name)
  return {
    key: `${namespace}/${name}`,
    name,
    namespace,
    title: agent?.spec.displayName?.trim() || name,
    limit: agent?.spec.sessionTokenLimit,
    found: !!agent,
    points: [],
    unreported: 0,
    maxRound: 1,
    byOutcome: [],
  }
}

/** The table under the chart: per outcome, its Tasks' rounds and what their runs used. */
export function outcomeRows(points: RunPoint[], limit: number | undefined): OutcomeRow[] {
  const rows: OutcomeRow[] = []
  for (const { key } of OUTCOMES) {
    const runs = points.filter((p) => p.outcome === key)
    if (!runs.length) continue
    const rounds = new Map(runs.map((p) => [p.task, p.rounds]))
    const totals = runs.map((p) => p.total).filter((v): v is number => v !== undefined)
    rows.push({
      outcome: key,
      tasks: rounds.size,
      meanRounds: [...rounds.values()].reduce((a, b) => a + b, 0) / rounds.size,
      runs: runs.length,
      medianLargest: median(runs.map((p) => p.largest)) ?? 0,
      meanTotal: totals.length ? totals.reduce((a, b) => a + b, 0) / totals.length : undefined,
      pastLimit: limit === undefined ? 0 : runs.filter((p) => p.largest > limit).length,
      handedOff: runs.filter((p) => p.handoffs.length > 0).length,
    })
  }
  return rows
}

/**
 * The chart's token axis: its top, a round step above both the largest point
 * and the limit with room for the limit's label, and the gridlines below it.
 */
export function tokenAxis(largest: number, limit: number | undefined): { max: number; ticks: number[] } {
  const top = Math.max(largest, (limit ?? 0) * 1.15, 1)
  const step = niceStep(top / 4)
  const max = Math.ceil(top / step) * step
  return { max, ticks: Array.from({ length: Math.round(max / step) + 1 }, (_, i) => i * step) }
}

/** 1, 2, 2.5 or 5 times a power of ten, the smallest at least `raw`. */
function niceStep(raw: number): number {
  const power = 10 ** Math.floor(Math.log10(raw))
  for (const m of [1, 2, 2.5, 5, 10]) if (m * power >= raw) return m * power
  return 10 * power
}

/**
 * Where each point sits across its round's band, 0 to 1: a round's points
 * spread evenly over the band in their order, so none hides another of the
 * same size.
 */
export function spreadInRounds(points: Pick<RunPoint, 'round'>[]): number[] {
  const counts = new Map<number, number>()
  for (const p of points) counts.set(p.round, (counts.get(p.round) ?? 0) + 1)
  const seen = new Map<number, number>()
  return points.map((p) => {
    const i = seen.get(p.round) ?? 0
    seen.set(p.round, i + 1)
    return (i + 1) / ((counts.get(p.round) ?? 1) + 1)
  })
}

/** When a run's sessions handed off: "handed off 2 times, at 134k and 141k tokens". */
export function describeHandoffs(sizes: number[]): string {
  const at = sizes.map(formatTokens)
  const list = at.length > 1 ? `${at.slice(0, -1).join(', ')} and ${at[at.length - 1]}` : at[0]
  return `handed off${sizes.length > 1 ? ` ${sizes.length} times,` : ''} at ${list} tokens`
}

const OUTCOME_TEXT: Record<Outcome, (rounds: number) => string> = {
  merged: (n) => `the Task merged after ${plural(n, 'round')}`,
  failed: (n) => `the Task failed after ${plural(n, 'round')}`,
  active: (n) => `the Task is still going, in round ${n}`,
}

/** A run's tooltip: its Task, round and role, what it used, and how its Task ended. */
export function describeRun(p: RunPoint, limit: number | undefined): string {
  const past = limit !== undefined && p.largest > limit ? `, past the ${formatTokens(limit)} limit` : ''
  return [
    p.headline ? `${p.headline} (${p.name})` : p.name,
    `round ${p.round}, ${p.role}`,
    `largest ${formatTokens(p.largest)}${past}`,
    p.total !== undefined ? `${formatTokens(p.total)} tokens in all` : undefined,
    p.handoffs.length ? describeHandoffs(p.handoffs) : undefined,
    OUTCOME_TEXT[p.outcome](p.rounds),
  ]
    .filter(Boolean)
    .join(' · ')
}
