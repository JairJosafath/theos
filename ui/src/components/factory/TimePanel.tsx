// The Work area's "where the time goes": time per stage over all Tasks.

import { factoryStats, STAGES } from '../../model/factory'
import { formatDuration, percent } from '../../model/format'

export function TimePanel({ stats }: { stats: ReturnType<typeof factoryStats> }) {
  const total = STAGES.reduce((n, s) => n + stats.timeByStage[s.key], 0)
  return (
    <div className="panel">
      <h2 className="panel-title">Where the time goes</h2>
      <p className="panel-caption">
        Time spent in each stage, over {stats.withTimeline} of {stats.total} Tasks.
        {stats.withTimeline < stats.total ? ' Tasks that ran before the factory recorded timelines are left out.' : ''}
      </p>
      {total > 0 ? (
        <>
          <div className="stage-bar" role="img" aria-label="Share of time per stage">
            {STAGES.map((s) =>
              stats.timeByStage[s.key] > 0 ? (
                <span
                  key={s.key}
                  className={`stage-bar-seg stage-${s.key}`}
                  style={{ width: `${(stats.timeByStage[s.key] / total) * 100}%` }}
                  title={`${s.label}: ${formatDuration(stats.timeByStage[s.key])}`}
                />
              ) : null,
            )}
          </div>
          <dl className="stage-legend">
            {STAGES.map((s) => (
              <div key={s.key} className={`stage-${s.key}`} title={s.explanation}>
                <dt>
                  <span className="stage-swatch" aria-hidden="true" />
                  {s.label}
                </dt>
                <dd>
                  {formatDuration(stats.timeByStage[s.key])}
                  <span className="muted"> · {percent(stats.timeByStage[s.key], total) ?? 0}%</span>
                </dd>
              </div>
            ))}
          </dl>
        </>
      ) : (
        <div className="placeholder small">No timelines yet: they start with the next Task.</div>
      )}
      <dl className="facts">
        <dt>Rounds</dt>
        <dd>
          {stats.meanRounds === undefined
            ? '–'
            : `${stats.meanRounds.toFixed(1)} on average to merge; ${Math.round((stats.firstRoundRate ?? 0) * 100)}% merged in round 1`}
        </dd>
        <dt>Outcomes</dt>
        <dd>
          {stats.merged} merged, {stats.failed} failed, {stats.active} active
        </dd>
      </dl>
    </div>
  )
}
