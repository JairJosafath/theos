// The page: a header with the project, two views of its story (system context
// and containers), and a side panel with details, sessions and a legend.

import { memo, useCallback, useEffect, useMemo, useState } from 'react'
import { Diagram } from './components/Diagram'
import { LogoMark, WarningIcon } from './components/icons'
import { DetailsPanel, Legend, SessionsPanel } from './components/Panels'
import { DemoProjectsSource, DemoStorySource } from './demo/demoSource'
import { WatchMux } from './k8s/mux'
import {
  emptyStory,
  LiveProjectsSource,
  LiveStorySource,
  type Api,
  type Connection,
  type Problem,
  type Snapshot,
  type Source,
  type StoryData,
} from './k8s/sources'
import type { Project } from './k8s/types'
import { useNow, useSource, useUrlState, type View } from './lib/hooks'
import { plural, titleOf } from './model/describe'
import { buildContainerDiagram, buildContextDiagram, containerNodes, systemsForPicker } from './model/story'

/**
 * Where the Kubernetes API is proxied, relative to the page (see server.mjs).
 * All watches of the page share connections through one WatchMux.
 */
const API: Api = { baseUrl: 'k8s', mux: new WatchMux({ url: 'k8s-watch' }) }

/** A source with nothing in it, for when no project is selected yet. */
class NoStory implements Source<StoryData> {
  private readonly snapshot: Snapshot<StoryData> = {
    data: emptyStory(''),
    connection: { phase: 'connecting', loaded: false, problems: [] },
  }
  subscribe() {
    return () => undefined
  }
  getSnapshot() {
    return this.snapshot
  }
  start() {}
  stop() {}
}
const NO_STORY = new NoStory()

const MemoDiagram = memo(Diagram)

export default function App() {
  const [url, setUrl] = useUrlState()
  const demo = url.demo

  const projectsSource = useMemo<Source<Project[]>>(
    () => (demo ? new DemoProjectsSource() : new LiveProjectsSource(API)),
    [demo],
  )
  const projects = useSource(projectsSource)
  // Oldest first, so the default is the project the cluster started with.
  const sortedProjects = useMemo(
    () =>
      [...projects.data].sort(
        (a, b) =>
          (a.metadata.creationTimestamp ?? '').localeCompare(b.metadata.creationTimestamp ?? '') ||
          (a.metadata.namespace ?? '').localeCompare(b.metadata.namespace ?? ''),
      ),
    [projects.data],
  )
  const namespace = url.project ?? sortedProjects[0]?.metadata.namespace

  const storySource = useMemo<Source<StoryData>>(() => {
    if (!namespace) return NO_STORY
    return demo ? new DemoStorySource() : new LiveStorySource(namespace, API)
  }, [namespace, demo])
  const story = useSource(storySource)
  const data = story.data

  const systems = useMemo(() => systemsForPicker(data), [data])
  const systemName = url.system && systems.some((s) => s.name === url.system) ? url.system : systems[0]?.name
  const view: View = url.view

  const contextDiagram = useMemo(() => buildContextDiagram(data), [data])
  const containerDiagram = useMemo(
    () => (systemName ? buildContainerDiagram(data, systemName) : undefined),
    [data, systemName],
  )
  const diagram = view === 'containers' ? containerDiagram : contextDiagram

  // A selection belongs to one view of one project; switching views drops it.
  const scope = `${namespace}|${view}|${systemName}`
  const [selection, setSelection] = useState<{ scope: string; id: string }>()
  const selectedId = selection?.scope === scope ? selection.id : undefined
  const selectedNode = diagram?.nodes.find((n) => n.id === selectedId)
  const onSelect = useCallback(
    (id: string) => setSelection((cur) => (cur?.scope === scope && cur.id === id ? undefined : { scope, id })),
    [scope],
  )
  const openSystem = useCallback((name: string) => setUrl({ view: 'containers', system: name }), [setUrl])

  const now = useNow(1000)
  const sessionContainers = useMemo(
    () => containerNodes(data, view === 'containers' ? systemName : undefined),
    [data, view, systemName],
  )

  const project = data.project
  const pageTitle = project ? `${titleOf(project)}${demo ? ' (demo)' : ''} · C4 Factory` : 'C4 Factory'
  useEffect(() => {
    document.title = pageTitle
  }, [pageTitle])
  const connection = demo ? projects.connection : mergeConnections(projects.connection, story.connection)
  const noProjects = !demo && projects.connection.loaded && sortedProjects.length === 0
  const systemTitle = systems.find((s) => s.name === systemName)?.title

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <LogoMark size={22} />
          <span className="brand-name">C4 Factory</span>
          <span className="brand-sub">Story viewer · read-only</span>
        </div>
        <div className="topbar-right">
          <ConnectionStatus connection={connection} />
          {sortedProjects.length > 0 ? (
            <label className="picker">
              <span className="picker-label">Project</span>
              <select
                value={namespace}
                onChange={(e) => setUrl({ project: e.target.value, view: 'context', system: undefined })}
              >
                {sortedProjects.map((p) => (
                  <option key={`${p.metadata.namespace}/${p.metadata.name}`} value={p.metadata.namespace}>
                    {titleOf(p)}
                  </option>
                ))}
              </select>
            </label>
          ) : null}
        </div>
      </header>

      {demo ? (
        <div className="banner banner-demo" role="status">
          <strong>Demo mode.</strong> You are looking at built-in example data, not a cluster. Nothing here is live.
          <a href="?" className="banner-link">
            Leave demo
          </a>
        </div>
      ) : null}
      <ProblemsBanner problems={connection.problems} />

      {noProjects ? (
        <EmptyProjects />
      ) : (
        <>
          <ProjectHeader data={data} namespace={namespace} loaded={story.connection.loaded} />

          <nav className="tabs" aria-label="Views">
            <div className="tablist" role="tablist">
              <button
                type="button"
                role="tab"
                aria-selected={view === 'context'}
                className="tab"
                onClick={() => setUrl({ view: 'context' })}
              >
                <span className="tab-step">1</span> System context
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={view === 'containers'}
                className="tab"
                onClick={() => setUrl({ view: 'containers', system: systemName })}
                disabled={!systems.length}
              >
                <span className="tab-step">2</span> Containers
              </button>
            </div>
            {view === 'containers' && systems.length > 1 ? (
              <label className="picker">
                <span className="picker-label">System</span>
                <select value={systemName} onChange={(e) => setUrl({ view: 'containers', system: e.target.value })}>
                  {systems.map((s) => (
                    <option key={s.name} value={s.name}>
                      {s.title} ({plural(s.containers, 'container')})
                    </option>
                  ))}
                </select>
              </label>
            ) : null}
          </nav>

          <main className="workspace">
            <section className="stage" aria-label="Diagram">
              <ViewCaption view={view} systemTitle={systemTitle} />
              {!story.connection.loaded ? (
                <div className="placeholder">Loading the story…</div>
              ) : !diagram || diagram.nodes.length + (diagram.boundary ? 1 : 0) === 0 ? (
                <div className="placeholder">
                  {view === 'context'
                    ? 'This project has no people or software systems yet. They appear here as soon as they are added.'
                    : 'Pick a software system to see its containers.'}
                </div>
              ) : (
                <MemoDiagram
                  diagram={diagram}
                  selectedId={selectedId}
                  onSelect={onSelect}
                  onOpenSystem={view === 'context' ? openSystem : undefined}
                  emptyBoundaryText="No containers or data stores in this system yet."
                />
              )}
              {diagram?.looseEnds.length ? (
                <div className="loose-ends">
                  <h3 className="section-title">
                    <WarningIcon size={14} /> Not drawn
                  </h3>
                  <ul>
                    {diagram.looseEnds.map((text) => (
                      <li key={text}>{text}</li>
                    ))}
                  </ul>
                </div>
              ) : null}
            </section>

            <aside className="side">
              {selectedNode ? (
                <DetailsPanel
                  node={selectedNode}
                  data={data}
                  now={now}
                  onClose={() => setSelection(undefined)}
                  onOpenSystem={openSystem}
                />
              ) : null}
              <SessionsPanel
                containers={sessionContainers}
                now={now}
                scope={view === 'containers' && systemTitle ? systemTitle : project ? titleOf(project) : 'this project'}
              />
              <Legend view={view} />
            </aside>
          </main>
        </>
      )}

      <footer className="footer">
        This page only reads the C4 Factory resources in the cluster and updates as they change. Nothing can be changed
        from here.
      </footer>
    </div>
  )
}

function mergeConnections(a: Connection, b: Connection): Connection {
  const order: Connection['phase'][] = ['demo', 'live', 'reconnecting', 'connecting']
  return {
    phase: order[Math.max(order.indexOf(a.phase), order.indexOf(b.phase))],
    loaded: a.loaded && b.loaded,
    problems: [...a.problems, ...b.problems],
  }
}

function ConnectionStatus({ connection }: { connection: Connection }) {
  const labels: Record<Connection['phase'], [string, string]> = {
    demo: ['Demo data', 'Built-in example data, not a cluster'],
    live: ['Live', 'Watching the cluster: changes show up as they happen'],
    reconnecting: ['Reconnecting…', 'Lost the connection to the cluster; retrying'],
    connecting: ['Connecting…', 'Loading the story from the cluster'],
  }
  const [label, title] = labels[connection.phase]
  return (
    <span className={`status status-${connection.phase}`} title={title} role="status">
      <span className="status-dot" aria-hidden="true" />
      {label}
    </span>
  )
}

/** Explains why something couldn't be read, with a hint at the usual fix. */
function ProblemsBanner({ problems }: { problems: Problem[] }) {
  if (!problems.length) return null
  const hint = (p: Problem) => {
    if (p.code === 403)
      return 'The UI is not allowed to read these. Its ClusterRole (config/ui) needs get, list and watch.'
    if (p.code === 404) return 'This kind is not installed in the cluster. Are the C4 Factory CRDs installed?'
    if (p.code === 502 || p.code === 503 || p.code === 504) return 'The UI server cannot reach the Kubernetes API.'
    return 'Retrying automatically.'
  }
  // One line per distinct message, so nine failing watches read as one problem.
  const distinct = [...new Map(problems.map((p) => [`${p.code}|${p.message}`, p])).values()]
  return (
    <div className="banner banner-problem" role="alert">
      <WarningIcon size={16} />
      <div>
        {distinct.map((p) => (
          <p key={`${p.code}|${p.message}`}>
            <strong>
              Could not read{' '}
              {problems
                .filter((q) => q.message === p.message)
                .map((q) => q.what)
                .join(', ')}
              .
            </strong>{' '}
            {hint(p)}{' '}
            <span className="muted">
              ({p.code ? `${p.code}: ` : ''}
              {p.message})
            </span>
          </p>
        ))}
      </div>
    </div>
  )
}

function ProjectHeader({ data, namespace, loaded }: { data: StoryData; namespace?: string; loaded: boolean }) {
  const project = data.project
  const ready = data.containers.filter((c) => c.status?.phase === 'Ready').length
  const active = data.sessions.filter((s) => s.status?.phase === 'Active').length
  const summary = project?.status?.summary?.trim()
  return (
    <section className="hero">
      <div className="hero-main">
        <div className="eyebrow">Project{namespace ? ` · ${namespace}` : ''}</div>
        <h1 className="hero-title">{project ? titleOf(project) : (namespace ?? 'Loading…')}</h1>
        {project?.spec?.description ? <p className="hero-desc">{project.spec.description}</p> : null}
        {loaded ? (
          <p className="hero-summary">
            <span className="hero-summary-label">Factory report</span>
            {summary || 'The factory has not summarised this project yet.'}
          </p>
        ) : null}
      </div>
      <dl className="stats">
        <div className="stat">
          <dt>Systems</dt>
          <dd>{data.systems.length}</dd>
        </div>
        <div className="stat">
          <dt>Containers</dt>
          <dd>
            {data.containers.length}
            <span className="stat-sub">{ready} ready</span>
          </dd>
        </div>
        <div className="stat">
          <dt>Data stores</dt>
          <dd>{data.dataStores.length}</dd>
        </div>
        <div className="stat">
          <dt>Working now</dt>
          <dd>{active}</dd>
        </div>
      </dl>
    </section>
  )
}

function ViewCaption({ view, systemTitle }: { view: View; systemTitle?: string }) {
  if (view === 'context') {
    return (
      <div className="view-head">
        <h2 className="view-title">System context: the big picture</h2>
        <p className="view-caption">
          Who uses the software, and which software systems are involved. Each arrow says who uses what, and what for.
          Select a box for details, or <em>Look inside</em> a system to see what it is made of.
        </p>
      </div>
    )
  }
  return (
    <div className="view-head">
      <h2 className="view-title">Containers of {systemTitle ?? 'a system'}</h2>
      <p className="view-caption">
        The applications (containers) and data stores inside the dashed box make up this system. The factory gives each
        container its own Git repository, started from templates. People and grey systems around it are outside. Hover
        over a container to see how the factory is getting on.
      </p>
    </div>
  )
}

function EmptyProjects() {
  return (
    <div className="empty">
      <h1>No projects yet</h1>
      <p>
        A project is the whole story of a piece of software: who uses it, what it is made of, and who is working on it.
        When one is added to the cluster, it shows up here by itself.
      </p>
    </div>
  )
}
