// The Work area's "How well it works" (docs/plans/measure-and-improve.md, step
// 3): over a period, how much merged, how much merged with no person's code,
// how long each stage of a merged Task took and what a merged Task cost by its
// size, for every Task and per Agent configuration. Plain functions over Tasks,
// tested without React; the numbers come from what each Task records (each
// run's Agent, harness and model; personCommits; pullRequestSize).

import type { Task, TaskAgentRun } from '../k8s/types'
import { median, time } from './factory'

/** The periods the panel offers, in days. */
export const PERIODS = [7, 30] as const
export type Period = (typeof PERIODS)[number]

/** The sizes of a change, by its changed lines, as internal/model's ChangeSize splits them. */
export type Size = 'S' | 'M' | 'L' | 'XL'
export const SIZES: { key: Size; label: string }[] = [
  { key: 'S', label: 'S (under 100 lines)' },
  { key: 'M', label: 'M (100 to 499)' },
  { key: 'L', label: 'L (500 to 999)' },
  { key: 'XL', label: 'XL (1000 or more)' },
]

/** The size of a change of additions and deletions: S under 100 changed lines, M under 500, L under 1000, XL beyond. */
export function changeSize(additions = 0, deletions = 0): Size {
  const lines = additions + deletions
  if (lines < 100) return 'S'
  if (lines < 500) return 'M'
  if (lines < 1000) return 'L'
  return 'XL'
}

/** What a merged Task's implementer ran on: its harness and model, from its last implement run that recorded them. */
export interface RunConfig {
  harness: string
  /** Empty for the harness's default model. */
  model: string
}

/** The configuration key of the Tasks whose runs recorded none. */
export const NOT_RECORDED = 'not recorded'

export function configKey(config: RunConfig | undefined): string {
  return config ? `${config.harness} · ${config.model || 'default model'}` : NOT_RECORDED
}

/** The implementer's configuration a Task ran on: its newest implement run's that recorded one. */
export function implementerConfig(task: Task): RunConfig | undefined {
  const runs: TaskAgentRun[] = task.status?.runs ?? []
  for (let i = runs.length - 1; i >= 0; i--) {
    const run = runs[i]
    if (run.role === 'implement' && run.harness) return { harness: run.harness, model: run.model ?? '' }
  }
  return undefined
}

/** One row of the panel: the merged Tasks of a configuration, or of all of them. */
export interface MetricsRow {
  /** configKey, or "all". */
  key: string
  merged: number
  /** Merged with no person's commit, of those whose commits were read; undefined when none were. */
  autonomy?: number
  /** How many merged Tasks had their commits read: autonomy's whole. */
  commitsRead: number
  /** Merged in round 1, of merged. */
  firstRound?: number
  /** Medians from the first Implementing step: to the pull request (Checking), to the first review, to the merge. */
  toPullMs?: number
  toReviewMs?: number
  toMergeMs?: number
  /** The median cost of a merged Task, in thousandths of a US dollar, over those that reported one. */
  costMilliUSD?: number
}

/** The median cost of the merged Tasks of one size, and how many there were. */
export interface SizeCost {
  size: Size
  tasks: number
  costMilliUSD?: number
}

export interface Metrics {
  period: Period
  all: MetricsRow
  /** One row per configuration, most merged first; "not recorded" last. */
  byConfig: MetricsRow[]
  bySize: SizeCost[]
  /** Merged Tasks whose size wasn't read: left out of bySize. */
  withoutSize: number
}

const DAY = 24 * 60 * 60 * 1000

/** A Task that merged a pull request inside the period ending now. */
function mergedIn(task: Task, from: number, now: number): boolean {
  const done = time(task.status?.completedAt)
  return (
    task.status?.phase === 'Merged' &&
    Boolean(task.status.pullRequest) &&
    done !== undefined &&
    done >= from &&
    done <= now
  )
}

/** When a Task's timeline first reached phase, at or after from; undefined when it never did. */
function firstAt(task: Task, phase: string, from: number): number | undefined {
  for (const step of task.status?.history ?? []) {
    const at = time(step.at)
    if (step.phase === phase && at !== undefined && at >= from) return at
  }
  return undefined
}

function row(key: string, tasks: Task[]): MetricsRow {
  const read = tasks.filter((t) => t.status?.personCommits !== undefined)
  const toPull: number[] = []
  const toReview: number[] = []
  const toMerge: number[] = []
  for (const t of tasks) {
    const start = firstAt(t, 'Implementing', -Infinity)
    if (start === undefined) continue
    const pull = firstAt(t, 'Checking', start)
    const review = firstAt(t, 'Reviewing', start)
    const merged = time(t.status?.completedAt)
    if (pull !== undefined) toPull.push(pull - start)
    if (review !== undefined) toReview.push(review - start)
    if (merged !== undefined) toMerge.push(merged - start)
  }
  const costs = tasks.map((t) => t.status?.costMilliUSD).filter((c): c is number => c !== undefined && c > 0)
  return {
    key,
    merged: tasks.length,
    commitsRead: read.length,
    autonomy: read.length ? read.filter((t) => t.status?.personCommits === 0).length / read.length : undefined,
    firstRound: tasks.length ? tasks.filter((t) => (t.status?.round ?? 1) <= 1).length / tasks.length : undefined,
    toPullMs: median(toPull),
    toReviewMs: median(toReview),
    toMergeMs: median(toMerge),
    costMilliUSD: median(costs),
  }
}

/** The metrics of the Tasks that merged in the period days long ending now. */
export function factoryMetrics(tasks: Task[], now: number, period: Period): Metrics {
  const merged = tasks.filter((t) => mergedIn(t, now - period * DAY, now))
  const groups = new Map<string, Task[]>()
  for (const t of merged) {
    const key = configKey(implementerConfig(t))
    groups.set(key, [...(groups.get(key) ?? []), t])
  }
  const byConfig = [...groups.entries()]
    .map(([key, ts]) => row(key, ts))
    .sort(
      (a, b) =>
        Number(a.key === NOT_RECORDED) - Number(b.key === NOT_RECORDED) ||
        b.merged - a.merged ||
        a.key.localeCompare(b.key),
    )
  const sized = merged.filter((t) => t.status?.pullRequestSize)
  const bySize = SIZES.map(({ key }) => {
    const ts = sized.filter(
      (t) => changeSize(t.status?.pullRequestSize?.additions, t.status?.pullRequestSize?.deletions) === key,
    )
    const costs = ts.map((t) => t.status?.costMilliUSD).filter((c): c is number => c !== undefined && c > 0)
    return { size: key, tasks: ts.length, costMilliUSD: median(costs) }
  })
  return { period, all: row('all', merged), byConfig, bySize, withoutSize: merged.length - sized.length }
}
