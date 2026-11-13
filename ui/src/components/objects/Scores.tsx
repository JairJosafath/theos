// A Task's Scores (docs/plans/done/measure-and-improve.md, step 5): each Scorer's
// verdicts on its change, criterion by criterion with the judge's why, and on
// a Score Task's own page its verdicts. Verdicts advise: they changed nothing
// about the Task.

import type { ScoreCriterion, ScoreVerdict, Task } from '../../k8s/types'
import type { Tone } from '../../model/describe'
import { scoresOf } from '../../model/scores'
import { pageHref } from './links'

const TONES: Record<ScoreVerdict, Tone> = { Pass: 'ok', Fail: 'error', Unclear: 'unknown' }

/** One judge's verdicts, one line each. */
export function Verdicts({ verdicts }: { verdicts: ScoreCriterion[] }) {
  return (
    <ul className="score-verdicts">
      {verdicts.map((v, i) => (
        <li key={i}>
          <span className={`badge badge-${TONES[v.verdict] ?? 'unknown'} badge-sm`}>
            <span className="badge-dot" aria-hidden="true" />
            {v.verdict}
          </span>{' '}
          <strong>{v.criterion}</strong>
          {v.reason ? <span className="muted"> · {v.reason}</span> : null}
        </li>
      ))}
    </ul>
  )
}

/** The Scores of task, from the Score Tasks of its namespace; nothing when none judged it. */
export function TaskScores({ task, tasks }: { task: Task; tasks: Task[] }) {
  const ns = task.metadata.namespace ?? ''
  const scores = scoresOf(task, tasks)
  if (!scores.length) return null
  return (
    <section className="panel" aria-labelledby="task-scores">
      <h2 className="panel-title small-title" id="task-scores">
        Scores
      </h2>
      <p className="panel-text small muted">
        What each Scorer’s judge found of this change. Scores advise: they changed nothing about the Task.
      </p>
      {scores.map((s) => (
        <div key={s.task}>
          <h3 className="object-section">
            Scorer {s.scorer} · <a href={pageHref(ns, 'Task', s.task)}>its judge run</a>
          </h3>
          {s.verdicts.length ? (
            <Verdicts verdicts={s.verdicts} />
          ) : (
            <p className="panel-text small muted">The judge hasn’t given its verdicts yet.</p>
          )}
        </div>
      ))}
    </section>
  )
}

/** A Score Task's own verdicts, and what it judged. */
export function ScoreTaskVerdicts({ task }: { task: Task }) {
  const ns = task.metadata.namespace ?? ''
  const score = task.spec.score
  if (!score) return null
  const verdicts = task.status?.score?.criteria
  return (
    <section className="panel" aria-labelledby="score-verdicts">
      <h2 className="panel-title small-title" id="score-verdicts">
        Verdicts
      </h2>
      <p className="panel-text small muted">
        Scorer {score.scorerRef.name}’s judge on{' '}
        <a href={pageHref(ns, 'Task', score.taskRef.name)}>Task {score.taskRef.name}</a>
        {score.config?.harness
          ? `, whose implementer ran on ${score.config.harness} · ${score.config.model || 'default model'}`
          : ''}
        .
      </p>
      {verdicts?.length ? (
        <Verdicts verdicts={verdicts} />
      ) : (
        <ul className="score-verdicts">
          {score.criteria.map((c, i) => (
            <li key={i}>
              <strong>{c}</strong> <span className="muted">· not judged yet</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
