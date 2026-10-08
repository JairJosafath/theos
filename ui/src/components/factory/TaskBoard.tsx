// The factory view's Task board: a card per Task, with its checks, verdict and
// a small timeline.

import type { Agent, Task } from '../../k8s/types'
import { timeAgo } from '../../model/describe'
import { CheckIcon, CrossIcon } from '../icons'
import { agentBackAt, isFinal, stageSince, taskHeadline, taskSegments } from '../../model/factory'
import { formatClock, formatDuration } from '../../model/format'

export function ChecksPill({ state }: { state?: string }) {
  if (!state) return null
  const tone = state === 'success' ? 'ok' : state === 'pending' ? 'progress' : 'error'
  return (
    <span className={`badge badge-sm badge-${tone}`} title={`CI: ${state}`}>
      {state === 'success' ? <CheckIcon size={11} /> : state === 'pending' ? null : <CrossIcon size={11} />}
      CI
    </span>
  )
}

export function VerdictPill({ verdict }: { verdict?: string }) {
  if (!verdict) return null
  return (
    <span className={`badge badge-sm badge-${verdict === 'approve' ? 'ok' : 'waiting'}`} title={`Review: ${verdict}`}>
      {verdict === 'approve' ? 'approved' : 'changes'}
    </span>
  )
}

export function TaskCard({
  task,
  agent,
  now,
  selected,
  onSelect,
}: {
  task: Task
  agent?: Agent
  now: number
  selected: boolean
  onSelect: () => void
}) {
  const phase = task.status?.phase
  const since = stageSince(task)
  const maxRounds = agent?.spec.maxRounds
  const headline = taskHeadline(task)
  const back = agentBackAt(task)
  return (
    <button
      type="button"
      className={`task-card${selected ? ' is-selected' : ''}${phase === 'Failed' ? ' is-failed' : ''}`}
      aria-pressed={selected}
      onClick={onSelect}
    >
      <span className="task-card-name">{task.metadata.name}</span>
      {headline.issue ? <span className="task-card-issue">{headline.text}</span> : null}
      <span className="task-card-sub">
        {task.metadata.namespace} · {task.spec.containerRef?.name ?? `brief ${task.spec.briefRef?.name ?? ''}`}
      </span>
      <span className="task-card-meta">
        {phase === 'Merged' ? (
          <span className="badge badge-sm badge-ok">Merged</span>
        ) : phase === 'Failed' ? (
          <span className="badge badge-sm badge-error">Failed</span>
        ) : null}
        <span className="task-round">
          round {task.status?.round ?? 0}
          {maxRounds ? `/${maxRounds}` : ''}
        </span>
        {!isFinal(phase) ? <ChecksPill state={task.status?.checks?.state} /> : null}
        {!isFinal(phase) ? <VerdictPill verdict={task.status?.review?.verdict} /> : null}
        {back !== undefined ? (
          <span className="badge badge-sm badge-waiting" title="Waiting for the agent: its service is unavailable">
            agent back {formatClock(back, now)}
          </span>
        ) : null}
      </span>
      <span className="task-card-time">
        {isFinal(phase)
          ? `${timeAgo(task.status?.completedAt, now)}`
          : since !== undefined
            ? `for ${formatDuration(now - since)}`
            : ''}
      </span>
      <MiniTimeline task={task} now={now} />
    </button>
  )
}

/** A thin bar of the Task's stages, proportional to their time. */
function MiniTimeline({ task, now }: { task: Task; now: number }) {
  const segments = taskSegments(task, now)
  if (!segments.length) return null
  const start = segments[0].start
  const total = Math.max(1, segments[segments.length - 1].end - start)
  return (
    <span className="mini-timeline" aria-hidden="true">
      {segments.map((s, i) => (
        <span
          key={i}
          className={`mini-seg stage-${s.stage}${s.current ? ' is-current' : ''}`}
          style={{ width: `${((s.end - s.start) / total) * 100}%` }}
        />
      ))}
    </span>
  )
}
