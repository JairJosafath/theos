// The Work area: is the factory healthy, what is it doing, where does the time
// go, and what needs a look. Across all projects; read-only. The headline
// numbers, then a one-line Needs a look strip, then tabs (docs/plans/done/ui-shell.md,
// step 2): Board, Crews, Releases, Time & tokens (with How well it works,
// docs/plans/measure-and-improve.md); the health and the agent
// slots are the Factory area's.

import { useMemo } from 'react'
import { problemsOf, readOf, type Connection, type FactoryData } from '../k8s/sources'
import type { FactoryHealth } from '../k8s/health'
import { RESOURCES, type Task } from '../k8s/types'
import { hrefFor, useNow } from '../lib/hooks'
import { boardColumns, COLUMNS, factoryStats, runningTasks, slotQueue, taskKey } from '../model/factory'
import { attention } from '../model/attention'
import { formatDuration } from '../model/format'
import { crewSummaries } from '../model/crews'
import { AttentionPanel } from './factory/Attention'
import { TaskCard } from './factory/TaskBoard'
import { CrewsPanel } from './factory/Crews'
import { ReleasesPanel } from './factory/Releases'
import { MetricsPanel } from './factory/MetricsPanel'
import { ScoresPanel } from './factory/ScoresPanel'
import { TimePanel } from './factory/TimePanel'
import { TokensPanel } from './factory/TokensPanel'
import { PanelState } from './PanelState'
import { Tabs } from './ui/Tabs'
import './factory.css'

/** The page of an object, by namespace, kind and name; undefined for a kind without one. */
export type ObjectHref = (ns: string, kind: string, name: string) => string | undefined

export interface FactoryPageProps {
  data: FactoryData
  /** The reads behind the data: each panel says those of its kinds that failed. */
  connection: Connection
  health?: FactoryHealth
  healthLoaded: boolean
  /** Opens a Task, by namespace/name. */
  onSelectTask: (key: string) => void
  /** An object's page: Crews and Tasks link to theirs. */
  pageHref: ObjectHref
}

export function FactoryPage({ data, connection, health, healthLoaded, onSelectTask, pageHref }: FactoryPageProps) {
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
  const running = runningTasks(data.tasks)
  const maxSlots = health?.agents.maxSandboxes
  const problems = (...resources: string[]) => problemsOf(connection, ...resources)
  const read = (...resources: string[]) => readOf(connection, ...resources)
  // The kinds only Needs a look reads: a failure there would hide its items.
  const attentionProblems = problems(RESOURCES.projects, RESOURCES.containers, RESOURCES.sessions, RESOURCES.briefs)

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

      <div className="work">
        {items.length || attentionProblems.length ? (
          <AttentionPanel items={items} problems={attentionProblems} onSelectTask={onSelectTask} />
        ) : null}

        <Tabs
          label="Work"
          syncUrl
          className="work-tabs"
          tabs={[
            {
              id: 'board',
              label: <TabLabel label="Board" count={COLUMNS.reduce((n, c) => n + columns[c.key].length, 0)} />,
              panel: (
                <section className="panel" aria-labelledby="work-board">
                  <div className="panel-head">
                    <div>
                      <h2 className="panel-title" id="work-board">
                        Tasks
                      </h2>
                      <p className="panel-caption">
                        Each Task moves left to right every round: an agent implements, CI checks, an agent reviews.
                        Open one for its page.
                      </p>
                    </div>
                  </div>
                  <PanelState
                    loaded={read(RESOURCES.tasks) || data.tasks.length > 0}
                    problems={problems(RESOURCES.tasks, RESOURCES.agents)}
                    empty={!data.tasks.length}
                    loading="Loading the Tasks…"
                    emptyText="No Tasks yet. They show up here as soon as one is created."
                  >
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
                              href={pageHref(t.metadata.namespace ?? '', 'Task', t.metadata.name)}
                            />
                          ))}
                        </div>
                      ))}
                    </div>
                  </PanelState>
                </section>
              ),
            },
            {
              id: 'crews',
              label: <TabLabel label="Crews" count={data.crews.length} />,
              panel: (
                <CrewsPanel
                  crews={crews}
                  loaded={read(RESOURCES.crews)}
                  problems={problems(RESOURCES.crews, RESOURCES.agents)}
                  onSelectTask={onSelectTask}
                  pageHref={pageHref}
                />
              ),
            },
            {
              id: 'releases',
              label: 'Releases',
              panel: (
                <ReleasesPanel
                  crews={data.crews}
                  loaded={read(RESOURCES.crews)}
                  problems={problems(RESOURCES.crews)}
                  pageHref={pageHref}
                />
              ),
            },
            {
              id: 'time',
              label: 'Time & tokens',
              panel: (
                <div className="work-time">
                  <MetricsPanel
                    tasks={data.tasks}
                    now={now}
                    loaded={read(RESOURCES.tasks)}
                    problems={problems(RESOURCES.tasks)}
                  />
                  <ScoresPanel tasks={data.tasks} loaded={read(RESOURCES.tasks)} problems={problems(RESOURCES.tasks)} />
                  <TimePanel stats={stats} />
                  <TokensPanel
                    tasks={data.tasks}
                    agents={data.agents}
                    loaded={read(RESOURCES.tasks, RESOURCES.agents)}
                    problems={problems(RESOURCES.tasks, RESOURCES.agents)}
                    pageHref={pageHref}
                  />
                </div>
              ),
            },
          ]}
        />
      </div>
    </>
  )
}

/** A tab's name with how many it holds. */
function TabLabel({ label, count }: { label: string; count: number }) {
  return (
    <>
      {label}
      {count ? <span className="chip-count tab-count">{count}</span> : null}
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

/** The factory's health in a word, linking to the Factory area that shows it. */
function OverallBadge({ health, loaded }: { health?: FactoryHealth; loaded: boolean }) {
  if (!loaded || !health) return <span className="badge badge-unknown">Checking…</span>
  const [label, tone] = OVERALL[health.state]
  return (
    <a
      className={`badge badge-${tone} badge-link`}
      role="status"
      href={hrefFor({ area: 'factory', tab: undefined, ns: undefined, kind: undefined, name: undefined })}
      title="The factory’s health, in the Factory area"
    >
      <span className="badge-dot" aria-hidden="true" />
      {label}
    </a>
  )
}
