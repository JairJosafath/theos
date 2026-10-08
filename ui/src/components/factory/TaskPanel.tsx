// The selected Task in the factory view: what it works on, who works it, and
// its timeline step by step.

import { findCondition, LABEL_CREW, type Agent, type Task } from '../../k8s/types'
import { describeUsage } from '../../model/brief'
import { timeAgo } from '../../model/describe'
import { AgentName, UsageLine } from '../bits'
import { CrossIcon, ExternalLinkIcon } from '../icons'
import {
  agentBackAt,
  currentStage,
  followUpLinks,
  isFinal,
  stageLabel,
  STAGES,
  standardChangeLink,
  taskHeadline,
  taskSegments,
  timeByStage,
  timelineSteps,
  type AgentRole,
  type Step,
} from '../../model/factory'
import { formatClock, formatDuration } from '../../model/format'
import { ChecksPill, VerdictPill } from './TaskBoard'

export function TaskPanel({
  task,
  agent,
  roles,
  now,
  onClose,
}: {
  task: Task
  agent?: Agent
  roles: { implementer: AgentRole; reviewer: AgentRole }
  now: number
  onClose: () => void
}) {
  const segments = taskSegments(task, now)
  const progressing = findCondition(task.status?.conditions, 'Progressing')
  const stage = currentStage(task)
  const headline = taskHeadline(task)
  const goalRest = headline.issue ? '' : (task.spec.goal ?? '').split('\n').slice(1).join('\n').trim()
  const byStage = timeByStage(segments)
  const back = agentBackAt(task)
  const followUps = followUpLinks(task)
  const usage = describeUsage(task.status?.costMilliUSD, task.status?.runsWithoutCost, task.status?.tokens)
  const standardChange = standardChangeLink(task)
  const crew = task.metadata.labels?.[LABEL_CREW]
  return (
    <div className="panel task-panel">
      <div className="panel-head">
        <div>
          <div className="eyebrow">Task · {task.metadata.namespace}</div>
          <h2 className="panel-title">{task.metadata.name}</h2>
        </div>
        <button type="button" className="icon-button" onClick={onClose} aria-label="Close">
          <CrossIcon size={16} />
        </button>
      </div>
      <p className="panel-text">
        {headline.url ? (
          <a href={headline.url} target="_blank" rel="noreferrer" className="headline-link">
            {headline.text} <ExternalLinkIcon size={12} />
          </a>
        ) : (
          <strong>{headline.text}</strong>
        )}
      </p>
      {goalRest ? (
        <details className="goal-more">
          <summary>The whole goal</summary>
          <pre className="pre">{goalRest}</pre>
        </details>
      ) : null}
      {progressing?.message ? (
        <p className="summary-line">
          {stage === 'done' ? '' : 'Now: '}
          {progressing.message}
        </p>
      ) : null}
      <dl className="facts">
        {task.spec.briefRef ? (
          <>
            <dt>Brief</dt>
            <dd className="mono">{task.spec.briefRef.name}</dd>
          </>
        ) : (
          <>
            <dt>Container</dt>
            <dd className="mono">{task.spec.containerRef?.name}</dd>
          </>
        )}
        {crew ? (
          <>
            <dt>Crew</dt>
            <dd className="mono">{crew}</dd>
          </>
        ) : null}
        <dt>Implements</dt>
        <dd>
          <AgentName role={roles.implementer} />
        </dd>
        <dt>Reviews</dt>
        <dd>
          <AgentName role={roles.reviewer} />
        </dd>
        {back !== undefined ? (
          <>
            <dt>Agent back</dt>
            <dd>
              at {formatClock(back, now)}
              <span className="muted">
                {back > now ? ` · in ${formatDuration(back - now)}` : ' · any moment'}; waiting costs no round
              </span>
            </dd>
          </>
        ) : null}
        <dt>Round</dt>
        <dd>
          {task.status?.round ?? 0}
          {agent?.spec.maxRounds ? ` of ${agent.spec.maxRounds}` : ''}
        </dd>
        <dt>Started</dt>
        <dd>{timeAgo(task.metadata.creationTimestamp, now)}</dd>
        {usage ? (
          <>
            <dt>Used</dt>
            <dd>
              <UsageLine usage={usage} />
            </dd>
          </>
        ) : null}
        {task.status?.completedAt ? (
          <>
            <dt>Took</dt>
            <dd>
              {formatDuration(Date.parse(task.status.completedAt) - Date.parse(task.metadata.creationTimestamp ?? ''))}
            </dd>
          </>
        ) : null}
        {task.status?.pullRequestURL ? (
          <>
            <dt>Pull request</dt>
            <dd>
              <a href={task.status.pullRequestURL} target="_blank" rel="noreferrer">
                #{task.status.pullRequest} <ExternalLinkIcon size={12} />
              </a>
            </dd>
          </>
        ) : null}
        {task.status?.checks?.state ? (
          <>
            <dt>CI</dt>
            <dd>
              <ChecksPill state={task.status.checks.state} /> <span className="muted">{task.status.checks.state}</span>
            </dd>
          </>
        ) : null}
        {task.status?.review?.verdict ? (
          <>
            <dt>Review</dt>
            <dd>
              <VerdictPill verdict={task.status.review.verdict} />
            </dd>
          </>
        ) : null}
        {standardChange ? (
          <>
            <dt>Blocked on</dt>
            <dd>
              <span className="muted">Waits on </span>
              {standardChange.url ? (
                <a href={standardChange.url} target="_blank" rel="noreferrer" title={standardChange.url}>
                  {standardChange.label} <ExternalLinkIcon size={11} />
                </a>
              ) : (
                standardChange.label
              )}
              <span className="muted"> ({standardChange.paths.join(', ')})</span>
            </dd>
          </>
        ) : null}
        {followUps.length ? (
          <>
            <dt>Follow-ups</dt>
            <dd>
              <span className="follow-ups">
                {followUps.map((f) => (
                  <a key={f.url} href={f.url} target="_blank" rel="noreferrer" title={f.url}>
                    {f.label} <ExternalLinkIcon size={11} />
                  </a>
                ))}
              </span>
              <span className="muted"> proposed from the review</span>
            </dd>
          </>
        ) : null}
      </dl>

      <h3 className="section-title">Timeline</h3>
      {segments.length ? (
        <>
          <div className="stage-bar">
            {segments.map((s, i) => (
              <span
                key={i}
                className={`stage-bar-seg stage-${s.stage}${s.current ? ' is-current' : ''}`}
                style={{ flexGrow: Math.max(1, s.end - s.start) }}
                title={`Round ${s.round}: ${stageLabel(s.stage)}, ${formatDuration(s.end - s.start)}`}
              />
            ))}
          </div>
          <p className="panel-note">
            {STAGES.filter((s) => byStage[s.key] > 0)
              .map((s) => `${s.label} ${formatDuration(byStage[s.key])}`)
              .join(' · ')}
          </p>
          <ol className="timeline">
            {timelineSteps(segments).map((s, i) => (
              <TimelineStep key={i} step={s} now={now} />
            ))}
            {isFinal(task.status?.phase) ? (
              <li className={`timeline-step stage-done ${task.status?.phase === 'Merged' ? 'tone-ok' : 'tone-error'}`}>
                <span className="timeline-dot" aria-hidden="true" />
                <div>
                  <strong>{task.status?.phase}</strong>
                  <span className="muted"> · {timeAgo(task.status?.completedAt, now)}</span>
                </div>
              </li>
            ) : null}
          </ol>
        </>
      ) : (
        <p className="panel-note">No timeline: this Task ran before the factory recorded timelines.</p>
      )}

      {task.status?.summary ? (
        <>
          <h3 className="section-title">The agent’s account</h3>
          <details className="goal-more">
            <summary>{firstLine(task.status.summary)}</summary>
            <pre className="pre">{task.status.summary}</pre>
          </details>
        </>
      ) : null}
      {task.status?.feedback ? (
        <>
          <h3 className="section-title">Feedback for the next round</h3>
          <details className="goal-more">
            <summary>{firstLine(task.status.feedback)}</summary>
            <pre className="pre">{task.status.feedback}</pre>
          </details>
        </>
      ) : null}
    </div>
  )
}

function firstLine(text: string): string {
  const line = text.split('\n').find((l) => l.trim()) ?? ''
  return line.length > 120 ? `${line.slice(0, 119)}…` : line
}

function TimelineStep({ step: segment, now }: { step: Step; now: number }) {
  return (
    <li className={`timeline-step stage-${segment.stage}${segment.current ? ' is-current' : ''}`}>
      <span className="timeline-dot" aria-hidden="true" />
      <div>
        <strong>{stageLabel(segment.stage)}</strong>
        <span className="muted">
          {' '}
          · round {segment.round} · {formatDuration(segment.end - segment.start)}
          {segment.current ? ' so far' : ''}
        </span>
        {segment.messages.map((m) => (
          <p key={m} className="timeline-msg">
            {m}
          </p>
        ))}
        <time className="timeline-at" dateTime={new Date(segment.start).toISOString()}>
          {timeAgo(new Date(segment.start).toISOString(), now)}
        </time>
      </div>
    </li>
  )
}
