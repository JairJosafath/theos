// The factory view: is the factory healthy, what is it doing, where does the
// time go, and what needs a look. Across all projects; read-only like the rest.

import { useMemo } from 'react'
import type { FactoryData } from '../k8s/sources'
import type { ComponentHealth, FactoryHealth, NodeHealth, PodHealth } from '../k8s/health'
import { findCondition, type Agent, type Task } from '../k8s/types'
import { plural, timeAgo } from '../model/describe'
import {
  API_STALL_MS,
  attention,
  boardColumns,
  COLUMNS,
  currentStage,
  factoryStats,
  formatBytes,
  formatCpu,
  formatDuration,
  isFinal,
  percent,
  runningTasks,
  slotQueue,
  STAGES,
  stageSince,
  taskKey,
  taskSegments,
  timeByStage,
  timelineSteps,
  type Attention,
  type Stage,
  type Step,
} from '../model/factory'
import { CheckIcon, CrossIcon, ExternalLinkIcon, WarningIcon } from './icons'

export interface FactoryPageProps {
  data: FactoryData
  loaded: boolean
  health?: FactoryHealth
  healthLoaded: boolean
  now: number
  selectedTask?: string
  onSelectTask: (key: string | undefined) => void
}

export function FactoryPage({ data, loaded, health, healthLoaded, now, selectedTask, onSelectTask }: FactoryPageProps) {
  const stats = useMemo(() => factoryStats(data.tasks, now), [data.tasks, now])
  const columns = useMemo(() => boardColumns(data.tasks), [data.tasks])
  const items = useMemo(
    () => attention({ tasks: data.tasks, containers: data.containers, sessions: data.sessions, health, now }),
    [data, health, now],
  )
  const agents = useMemo(
    () => new Map(data.agents.map((a) => [`${a.metadata.namespace}/${a.metadata.name}`, a])),
    [data.agents],
  )
  const agentOf = (t: Task) => agents.get(`${t.metadata.namespace}/${t.spec.agentRef.name}`)
  const selected = data.tasks.find((t) => taskKey(t) === selectedTask)
  const running = runningTasks(data.tasks)
  const maxSlots = health?.agents.maxSandboxes

  return (
    <>
      <section className="hero">
        <div className="hero-main">
          <div className="eyebrow">All projects</div>
          <h1 className="hero-title">
            The factory <OverallBadge health={health} loaded={healthLoaded} />
          </h1>
          <p className="hero-desc">
            Its parts, what it is working on, and where the time goes. Tasks from every project, live; health every 10
            seconds.
          </p>
        </div>
        <dl className="stats">
          <Stat label="Active tasks" value={stats.active} sub={`of ${stats.total}`} />
          <Stat
            label="Agent slots"
            value={maxSlots === undefined ? '–' : `${running.length}/${maxSlots}`}
            sub={slotQueue(data.tasks).length ? `${slotQueue(data.tasks).length} waiting` : undefined}
            subTone="waiting"
          />
          <Stat
            label="Merged"
            value={stats.merged}
            sub={stats.successRate === undefined ? undefined : `${Math.round(stats.successRate * 100)}% of finished`}
          />
          <Stat
            label="Lead time"
            value={stats.medianLeadMs === undefined ? '–' : formatDuration(stats.medianLeadMs)}
            sub="median"
            subTone="muted"
          />
        </dl>
      </section>

      <main className="workspace factory">
        <section className="factory-main" aria-label="Work">
          {items.length ? <AttentionPanel items={items} onSelectTask={onSelectTask} /> : null}

          <div className="panel">
            <div className="panel-head">
              <div>
                <h2 className="panel-title">Tasks</h2>
                <p className="panel-caption">
                  Each Task moves left to right every round: an agent implements, CI checks, an agent reviews. Select
                  one for its timeline.
                </p>
              </div>
            </div>
            {!loaded && !data.tasks.length ? (
              <div className="placeholder">Loading the Tasks…</div>
            ) : !data.tasks.length ? (
              <div className="placeholder">No Tasks yet. They show up here as soon as one is created.</div>
            ) : (
              <div className="board" role="list">
                {COLUMNS.map((col) => (
                  <div key={col.key} className={`board-col stage-${col.key}`} role="listitem">
                    <h3 className="board-head">
                      <span className="stage-swatch" aria-hidden="true" />
                      {col.label}
                      <span className="board-count">{columns[col.key].length}</span>
                    </h3>
                    {columns[col.key].map((t) => (
                      <TaskCard
                        key={taskKey(t)}
                        task={t}
                        agent={agentOf(t)}
                        now={now}
                        selected={taskKey(t) === selectedTask}
                        onSelect={() => onSelectTask(taskKey(t) === selectedTask ? undefined : taskKey(t))}
                      />
                    ))}
                  </div>
                ))}
              </div>
            )}
          </div>

          <TimePanel stats={stats} />
        </section>

        <aside className="side">
          {selected ? (
            <TaskPanel task={selected} agent={agentOf(selected)} now={now} onClose={() => onSelectTask(undefined)} />
          ) : null}
          <HealthPanel health={health} loaded={healthLoaded} now={now} />
          <SlotsPanel tasks={data.tasks} health={health} now={now} onSelectTask={onSelectTask} />
        </aside>
      </main>
    </>
  )
}

function Stat({
  label,
  value,
  sub,
  subTone = 'ok',
}: {
  label: string
  value: string | number
  sub?: string
  subTone?: 'ok' | 'waiting' | 'muted'
}) {
  return (
    <div className="stat">
      <dt>{label}</dt>
      <dd>
        {value}
        {sub ? <span className={`stat-sub stat-sub-${subTone}`}>{sub}</span> : null}
      </dd>
    </div>
  )
}

const OVERALL: Record<FactoryHealth['state'], [string, string]> = {
  ok: ['Healthy', 'ok'],
  degraded: ['Unsteady', 'waiting'],
  down: ['Down', 'error'],
  unknown: ['Unknown', 'unknown'],
}

function OverallBadge({ health, loaded }: { health?: FactoryHealth; loaded: boolean }) {
  if (!loaded || !health) return <span className="badge badge-unknown">Checking…</span>
  const [label, tone] = OVERALL[health.state]
  return (
    <span className={`badge badge-${tone}`} role="status">
      <span className="badge-dot" aria-hidden="true" />
      {label}
    </span>
  )
}

function AttentionPanel({ items, onSelectTask }: { items: Attention[]; onSelectTask: (key: string) => void }) {
  return (
    <div className="panel attention">
      <h2 className="panel-title small-title">
        <WarningIcon size={16} /> Needs a look
      </h2>
      <ul className="attention-list">
        {items.map((item) => (
          <li
            key={`${item.level}|${item.title}`}
            className={`attention-item tone-${item.level === 'error' ? 'error' : 'waiting'}`}
          >
            <span className="badge-dot" aria-hidden="true" />
            <div>
              {item.task ? (
                <button type="button" className="link-button" onClick={() => onSelectTask(item.task!)}>
                  {item.title}
                </button>
              ) : (
                <strong>{item.title}</strong>
              )}
              {item.detail ? <p className="attention-detail">{item.detail}</p> : null}
            </div>
          </li>
        ))}
      </ul>
    </div>
  )
}

function ChecksPill({ state }: { state?: string }) {
  if (!state) return null
  const tone = state === 'success' ? 'ok' : state === 'pending' ? 'progress' : 'error'
  return (
    <span className={`badge badge-sm badge-${tone}`} title={`CI: ${state}`}>
      {state === 'success' ? <CheckIcon size={11} /> : state === 'pending' ? null : <CrossIcon size={11} />}
      CI
    </span>
  )
}

function VerdictPill({ verdict }: { verdict?: string }) {
  if (!verdict) return null
  return (
    <span className={`badge badge-sm badge-${verdict === 'approve' ? 'ok' : 'waiting'}`} title={`Review: ${verdict}`}>
      {verdict === 'approve' ? 'approved' : 'changes'}
    </span>
  )
}

function TaskCard({
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
  return (
    <button
      type="button"
      className={`task-card${selected ? ' is-selected' : ''}${phase === 'Failed' ? ' is-failed' : ''}`}
      aria-pressed={selected}
      onClick={onSelect}
    >
      <span className="task-card-name">{task.metadata.name}</span>
      <span className="task-card-sub">
        {task.metadata.namespace} · {task.spec.containerRef.name}
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

function TimePanel({ stats }: { stats: ReturnType<typeof factoryStats> }) {
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

function TaskPanel({ task, agent, now, onClose }: { task: Task; agent?: Agent; now: number; onClose: () => void }) {
  const segments = taskSegments(task, now)
  const progressing = findCondition(task.status?.conditions, 'Progressing')
  const stage = currentStage(task)
  const [goalTitle, ...goalRest] = task.spec.goal.split('\n')
  const byStage = timeByStage(segments)
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
        <strong>{goalTitle}</strong>
      </p>
      {goalRest.join('\n').trim() ? (
        <details className="goal-more">
          <summary>The whole goal</summary>
          <pre className="pre">{goalRest.join('\n').trim()}</pre>
        </details>
      ) : null}
      {progressing?.message ? (
        <p className="summary-line">
          {stage === 'done' ? '' : 'Now: '}
          {progressing.message}
        </p>
      ) : null}
      <dl className="facts">
        <dt>Container</dt>
        <dd className="mono">{task.spec.containerRef.name}</dd>
        <dt>Agent</dt>
        <dd>{agent?.spec.displayName ?? task.spec.agentRef.name}</dd>
        <dt>Round</dt>
        <dd>
          {task.status?.round ?? 0}
          {agent?.spec.maxRounds ? ` of ${agent.spec.maxRounds}` : ''}
        </dd>
        <dt>Started</dt>
        <dd>{timeAgo(task.metadata.creationTimestamp, now)}</dd>
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

function stageLabel(stage: Stage): string {
  return STAGES.find((s) => s.key === stage)?.label ?? stage
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

const COMPONENT_TONE: Record<ComponentHealth['state'], string> = {
  ok: 'ok',
  degraded: 'waiting',
  down: 'error',
  absent: 'unknown',
  unknown: 'unknown',
}

function HealthPanel({ health, loaded, now }: { health?: FactoryHealth; loaded: boolean; now: number }) {
  return (
    <div className="panel">
      <h2 className="panel-title small-title">Health</h2>
      {!loaded || !health ? (
        <p className="panel-note">Reading the factory’s health…</p>
      ) : (
        <>
          {health.nodes.map((node) => (
            <NodeUsage key={node.name} node={node} metrics={health.metrics} />
          ))}
          <HistoryCharts health={health} now={now} />
          <ul className="components">
            {health.components.map((c) => (
              <ComponentRow key={c.key} component={c} now={now} />
            ))}
          </ul>
          {!health.metrics ? (
            <p className="panel-note">
              No CPU or memory figures: metrics-server is not installed (
              <span className="mono">make metrics-server</span>
              ).
            </p>
          ) : null}
          <p className="panel-note">Checked {timeAgo(health.generatedAt, now)}.</p>
        </>
      )}
    </div>
  )
}

function Meter({ label, used, total, text }: { label: string; used?: number; total: number; text: string }) {
  const pct = percent(used, total)
  const tone = pct === undefined ? 'unknown' : pct > 85 ? 'error' : pct > 70 ? 'waiting' : 'ok'
  return (
    <div className="meter">
      <div className="meter-head">
        <span>{label}</span>
        <span className="muted">{text}</span>
      </div>
      <div
        className={`meter-track tone-${tone}`}
        role="meter"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct ?? 0}
      >
        <span className="meter-fill" style={{ width: `${pct ?? 0}%` }} />
      </div>
    </div>
  )
}

function NodeUsage({ node, metrics }: { node: NodeHealth; metrics: boolean }) {
  return (
    <div className="node-usage">
      <div className="node-usage-name">
        <span className="mono">{node.name}</span>
        {node.ready ? null : <span className="badge badge-sm badge-error">Not ready</span>}
        {node.pressure.map((p) => (
          <span key={p} className="badge badge-sm badge-error">
            {p}
          </span>
        ))}
      </div>
      {metrics ? (
        <>
          <Meter
            label="CPU"
            used={node.cpuMilli}
            total={node.cpuAllocatableMilli}
            text={`${formatCpu(node.cpuMilli)} of ${formatCpu(node.cpuAllocatableMilli)}`}
          />
          <Meter
            label="Memory"
            used={node.memoryBytes}
            total={node.memoryAllocatableBytes}
            text={`${formatBytes(node.memoryBytes)} of ${formatBytes(node.memoryAllocatableBytes)}`}
          />
        </>
      ) : null}
    </div>
  )
}

function ComponentRow({ component, now }: { component: ComponentHealth; now: number }) {
  const cpu = sumOf(component.pods, 'cpuMilli')
  const memory = sumOf(component.pods, 'memoryBytes')
  const restarts = component.pods.reduce((n, p) => n + p.restarts, 0)
  const recent = component.pods.find((p) => p.restartedRecently)?.lastRestart
  const waiting = component.pods.find((p) => p.waiting)?.waiting
  return (
    <li className={`component tone-${COMPONENT_TONE[component.state]}`} title={component.role}>
      <span className="badge-dot" aria-hidden="true" />
      <div className="component-main">
        <div className="component-name">
          {component.name}
          {component.state === 'absent' ? <span className="muted"> · not installed</span> : null}
          {waiting ? <span className="badge badge-sm badge-error">{waiting}</span> : null}
        </div>
        {component.pods.length ? (
          <div className="component-sub muted">
            {component.pods.filter((p) => p.ready).length}/{component.pods.length} ready
            {restarts ? ` · ${plural(restarts, 'restart')}` : ''}
            {recent ? ` · last ${timeAgo(recent.at, now)}` : ''}
          </div>
        ) : null}
      </div>
      {cpu !== undefined || memory !== undefined ? (
        <div className="component-usage">
          <span>{formatCpu(cpu)}</span>
          <span>{formatBytes(memory)}</span>
        </div>
      ) : null}
    </li>
  )
}

function sumOf(pods: PodHealth[], field: 'cpuMilli' | 'memoryBytes'): number | undefined {
  const values = pods.map((p) => p[field]).filter((v): v is number => v !== undefined)
  return values.length ? values.reduce((a, b) => a + b, 0) : undefined
}

function SlotsPanel({
  tasks,
  health,
  now,
  onSelectTask,
}: {
  tasks: Task[]
  health?: FactoryHealth
  now: number
  onSelectTask: (key: string) => void
}) {
  const running = runningTasks(tasks)
  const queue = slotQueue(tasks)
  const sandboxes = new Map(
    (health?.agents.sandboxes ?? []).map(
      (s) => [`${s.namespace}/${s.task}`, s] as [string, PodHealth & { task: string }],
    ),
  )
  return (
    <div className="panel">
      <h2 className="panel-title small-title">Agent slots</h2>
      <p className="panel-caption">
        The operator runs at most {health?.agents.maxSandboxes ?? '…'} agent sandbox
        {health?.agents.maxSandboxes === 1 ? '' : 'es'} at a time; others wait their turn, longest waiter first.
      </p>
      <h3 className="section-title">Running</h3>
      {running.length ? (
        <ul className="slot-list">
          {running.map((t) => {
            const pod = sandboxes.get(taskKey(t))
            return (
              <li key={taskKey(t)}>
                <button type="button" className="link-button" onClick={() => onSelectTask(taskKey(t))}>
                  {t.metadata.name}
                </button>
                <span className="muted">
                  {' '}
                  · {stageLabel(currentStage(t) as Stage).toLowerCase()}
                  {pod
                    ? ` · ${pod.phase}${pod.memoryBytes !== undefined ? `, ${formatBytes(pod.memoryBytes)}` : ''}`
                    : ''}
                </span>
              </li>
            )
          })}
        </ul>
      ) : (
        <p className="panel-note">No sandbox is running.</p>
      )}
      <h3 className="section-title">Waiting</h3>
      {queue.length ? (
        <ol className="slot-list">
          {queue.map((t) => (
            <li key={taskKey(t)}>
              <button type="button" className="link-button" onClick={() => onSelectTask(taskKey(t))}>
                {t.metadata.name}
              </button>
              <span className="muted"> · {formatDuration(now - Date.parse(t.status?.slotWaitSince ?? ''))}</span>
            </li>
          ))}
        </ol>
      ) : (
        <p className="panel-note">Nothing is waiting for a slot.</p>
      )}
    </div>
  )
}

const HOUR_MS = 60 * 60_000

interface Series {
  label: string
  className: string
  values: { t: number; v: number | undefined }[]
}

/** A small line chart of the last hour. Points without a value break the line. */
function Sparkline({
  title,
  series,
  max,
  now,
  summary,
  marks = [],
}: {
  title: string
  series: Series[]
  max: number
  now: number
  summary: string
  marks?: { t: number; className: string }[]
}) {
  const width = 300
  const height = 44
  const x = (t: number) => ((t - (now - HOUR_MS)) / HOUR_MS) * width
  const y = (v: number) => height - 2 - (Math.min(v, max) / max) * (height - 4)
  const paths = series.map((s) => {
    let d = ''
    let open = false
    for (const { t, v } of s.values) {
      if (v === undefined) {
        open = false
        continue
      }
      d += `${open ? 'L' : 'M'}${x(t).toFixed(1)},${y(v).toFixed(1)}`
      open = true
    }
    return { ...s, d }
  })
  return (
    <figure className="spark">
      <figcaption className="meter-head">
        <span>{title}</span>
        <span className="muted">{summary}</span>
      </figcaption>
      <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label={`${title}: ${summary}`}>
        <line className="spark-base" x1="0" x2={width} y1={height - 1} y2={height - 1} />
        {paths.map((p) => (p.d ? <path key={p.label} className={`spark-line ${p.className}`} d={p.d} /> : null))}
        {marks.map((m, i) => (
          <line key={i} className={`spark-mark ${m.className}`} x1={x(m.t)} x2={x(m.t)} y1="0" y2={height} />
        ))}
      </svg>
      {series.length > 1 ? (
        <div className="spark-legend">
          {series.map((s) => (
            <span key={s.label} className={s.className}>
              <span className="spark-key" aria-hidden="true" />
              {s.label}
            </span>
          ))}
        </div>
      ) : null}
    </figure>
  )
}

/** The last hour of the API server's response time, CPU and memory. */
function HistoryCharts({ health, now }: { health: FactoryHealth; now: number }) {
  const history = health.history ?? []
  if (history.length < 2) {
    return <p className="panel-note">The last hour fills in here: the UI samples the factory every 15 seconds.</p>
  }
  const points = history.map((s) => ({ ...s, t: Date.parse(s.at) }))
  const node = health.nodes[0]
  const latest = points[points.length - 1]
  const worstApi = Math.max(...points.map((p) => p.apiMs))
  const stalls = points.filter((p) => p.apiFailed || p.apiMs >= API_STALL_MS)
  return (
    <div className="history">
      <h3 className="section-title">Last hour</h3>
      <Sparkline
        title="API server response"
        summary={`now ${formatMs(latest.apiMs)} · worst ${formatMs(worstApi)}`}
        max={Math.max(1000, worstApi)}
        now={now}
        series={[{ label: 'API', className: 'spark-api', values: points.map((p) => ({ t: p.t, v: p.apiMs })) }]}
        marks={stalls.map((p) => ({ t: p.t, className: 'spark-stall' }))}
      />
      {health.metrics && node ? (
        <>
          <Sparkline
            title="CPU"
            summary={`of ${formatCpu(node.cpuAllocatableMilli)}`}
            max={node.cpuAllocatableMilli}
            now={now}
            series={[
              { label: 'node', className: 'spark-node', values: points.map((p) => ({ t: p.t, v: p.cpuMilli })) },
              { label: 'CI', className: 'spark-ci', values: points.map((p) => ({ t: p.t, v: p.ciCpuMilli })) },
              {
                label: 'agents',
                className: 'spark-agents',
                values: points.map((p) => ({ t: p.t, v: p.agentCpuMilli ?? 0 })),
              },
            ]}
          />
          <Sparkline
            title="Memory"
            summary={`now ${formatBytes(latest.memoryBytes)} of ${formatBytes(node.memoryAllocatableBytes)}`}
            max={node.memoryAllocatableBytes}
            now={now}
            series={[
              { label: 'node', className: 'spark-node', values: points.map((p) => ({ t: p.t, v: p.memoryBytes })) },
            ]}
          />
        </>
      ) : null}
    </div>
  )
}

function formatMs(ms: number): string {
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`
}
