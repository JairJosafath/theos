// The factory view: is the factory healthy, what is it doing, where does the
// time go, and what needs a look. Across all projects; read-only.

import { useMemo } from 'react'
import type { FactoryData } from '../k8s/sources'
import type { FactoryHealth } from '../k8s/health'
import type { Task } from '../k8s/types'
import { useNow } from '../lib/hooks'
import { boardColumns, COLUMNS, factoryStats, runningTasks, slotQueue, taskKey, taskRoles } from '../model/factory'
import { attention } from '../model/attention'
import { formatDuration } from '../model/format'
import { crewSummaries } from '../model/crews'
import { AttentionPanel } from './factory/Attention'
import { TaskCard } from './factory/TaskBoard'
import { CrewsPanel } from './factory/Crews'
import { TimePanel } from './factory/TimePanel'
import { TaskPanel } from './factory/TaskPanel'
import { HealthPanel } from './factory/Health'
import { SlotsPanel } from './factory/Slots'

export interface FactoryPageProps {
  data: FactoryData
  loaded: boolean
  health?: FactoryHealth
  healthLoaded: boolean
  selectedTask?: string
  onSelectTask: (key: string | undefined) => void
}

export function FactoryPage({ data, loaded, health, healthLoaded, selectedTask, onSelectTask }: FactoryPageProps) {
  const now = useNow()
  const stats = useMemo(() => factoryStats(data.tasks, now), [data.tasks, now])
  const columns = useMemo(() => boardColumns(data.tasks), [data.tasks])
  const items = useMemo(
    () =>
      attention({
        tasks: data.tasks,
        containers: data.containers,
        sessions: data.sessions,
        crews: data.crews,
        briefs: data.briefs,
        health,
        now,
      }),
    [data, health, now],
  )
  const agents = useMemo(
    () => new Map(data.agents.map((a) => [`${a.metadata.namespace}/${a.metadata.name}`, a])),
    [data.agents],
  )
  const agentOf = (t: Task) => agents.get(`${t.metadata.namespace}/${t.spec.agentRef.name}`)
  const crews = useMemo(() => crewSummaries(data.crews, data.agents, data.tasks), [data])
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

          <CrewsPanel crews={crews} loaded={loaded} now={now} onSelectTask={onSelectTask} />

          <TimePanel stats={stats} />
        </section>

        <aside className="side">
          {selected ? (
            <TaskPanel
              task={selected}
              agent={agentOf(selected)}
              roles={taskRoles(selected, data.agents)}
              now={now}
              onClose={() => onSelectTask(undefined)}
            />
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
