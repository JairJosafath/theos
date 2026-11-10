// Scores in the UI (docs/plans/done/measure-and-improve.md, step 5): the verdicts
// of the Score Tasks a Scorer made, on the page of the Task they judged, and
// each Scorer's pass rate per criterion and implementer configuration in the
// Work area. Read from the Score Tasks alone, which copy their Scorer's
// criteria and the judged Task's configuration.

import type { ScoreCriterion, Task } from '../k8s/types'
import { compareText, configKey, NOT_RECORDED } from './metrics'
import { isFinal, time } from './factory'

export function isScoreTask(task: Task): boolean {
  return task.spec.kind === 'Score'
}

/** A Score of one Task: which Scorer judged it, its Score Task, and the verdicts once there are any. */
export interface TaskScore {
  scorer: string
  task: string
  phase?: string
  verdicts: ScoreCriterion[]
}

/** The Scores of task: the Score Tasks of its namespace that judge it, oldest first. */
export function scoresOf(task: Task, tasks: Task[]): TaskScore[] {
  const ns = task.metadata.namespace ?? ''
  return tasks
    .filter(
      (t) => isScoreTask(t) && (t.metadata.namespace ?? '') === ns && t.spec.score?.taskRef.name === task.metadata.name,
    )
    .sort(
      (a, b) =>
        (time(a.metadata.creationTimestamp) ?? 0) - (time(b.metadata.creationTimestamp) ?? 0) ||
        a.metadata.name.localeCompare(b.metadata.name),
    )
    .map((t) => ({
      scorer: t.spec.score?.scorerRef.name ?? '',
      task: t.metadata.name,
      phase: t.status?.phase,
      verdicts: t.status?.score?.criteria ?? [],
    }))
}

/** How one criterion fared under one configuration. */
export interface CriterionRate {
  pass: number
  fail: number
  unclear: number
  /** Pass out of pass and fail; undefined when neither. Unclear is said apart, not counted against. */
  rate?: number
}

/** A Scorer's verdicts: per criterion of its newest generation, per configuration. */
export interface ScorerRates {
  /** namespace/name */
  key: string
  scorer: string
  namespace: string
  /** The finished Score Tasks of its newest generation. */
  scored: number
  criteria: string[]
  /** The configurations, the most judged first, then by name (compareText), and "not recorded" last. */
  configs: string[]
  /** rates[i][config] is criterion i's under config. */
  rates: Record<string, CriterionRate>[]
}

function rate(r: Omit<CriterionRate, 'rate'>): CriterionRate {
  const judged = r.pass + r.fail
  return { ...r, rate: judged ? r.pass / judged : undefined }
}

/**
 * Each Scorer's pass rates from its finished Score Tasks: only its newest
 * generation's, whose criteria the Score Tasks copied; one with other
 * criteria would mix questions.
 */
export function scorerRates(tasks: Task[]): ScorerRates[] {
  const byScorer = new Map<string, Task[]>()
  for (const t of tasks) {
    const score = t.spec.score
    if (!isScoreTask(t) || !score || !isFinal(t.status?.phase) || !t.status?.score?.criteria) continue
    const key = `${t.metadata.namespace ?? ''}/${score.scorerRef.name}`
    byScorer.set(key, [...(byScorer.get(key) ?? []), t])
  }
  const out: ScorerRates[] = []
  for (const [key, all] of byScorer) {
    const newest = Math.max(...all.map((t) => t.spec.score?.scorerGeneration ?? 0))
    const scored = all.filter((t) => t.spec.score?.scorerGeneration === newest)
    const criteria = scored[0].spec.score?.criteria ?? []
    const counts = new Map<string, number>()
    const raw: Record<string, Omit<CriterionRate, 'rate'>>[] = criteria.map(() => ({}))
    for (const t of scored) {
      const config = configKey(
        t.spec.score?.config?.harness
          ? { harness: t.spec.score.config.harness, model: t.spec.score.config.model ?? '' }
          : undefined,
      )
      counts.set(config, (counts.get(config) ?? 0) + 1)
      ;(t.status?.score?.criteria ?? []).forEach((v, i) => {
        if (i >= raw.length) return
        const r = (raw[i][config] ??= { pass: 0, fail: 0, unclear: 0 })
        if (v.verdict === 'Pass') r.pass++
        else if (v.verdict === 'Fail') r.fail++
        else r.unclear++
      })
    }
    const configs = [...counts.keys()].sort(
      (a, b) =>
        Number(a === NOT_RECORDED) - Number(b === NOT_RECORDED) ||
        (counts.get(b) ?? 0) - (counts.get(a) ?? 0) ||
        compareText(a, b),
    )
    const [namespace, scorer] = [key.slice(0, key.indexOf('/')), key.slice(key.indexOf('/') + 1)]
    out.push({
      key,
      scorer,
      namespace,
      scored: scored.length,
      criteria,
      configs,
      rates: raw.map((byConfig) => Object.fromEntries(Object.entries(byConfig).map(([c, r]) => [c, rate(r)]))),
    })
  }
  return out.sort((a, b) => compareText(a.key, b.key))
}

/** "75% (3 of 4)", and how many were unclear; "–" when none was judged either way. */
export function describeRate(r: CriterionRate | undefined): string {
  if (!r) return '–'
  const judged = r.pass + r.fail
  const shown = judged ? `${Math.round((r.rate ?? 0) * 100)}% (${r.pass} of ${judged})` : '–'
  return r.unclear ? `${shown}, ${r.unclear} unclear` : shown
}
