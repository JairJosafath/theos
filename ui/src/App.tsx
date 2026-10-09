// The page: a header with the project, its Briefs, two views of its story
// (system context and containers), and a side panel with details, sessions and
// a legend. With ?shell=next, the same parts inside the new shell's areas.

import { memo, useCallback, useEffect, useMemo, useState } from 'react'
import './app.css'
import { Account } from './components/Account'
import { Briefs } from './components/Briefs'
import { Diagram } from './components/Diagram'
import { StoryLine } from './components/StoryLine'
import { ViewCaption } from './components/ViewCaption'
import { FactoryPage } from './components/Factory'
import { LogoMark, WarningIcon } from './components/icons'
import { NeedsArea } from './components/Needs'
import { FactoryArea, Shell } from './components/Shell'
import { pageHref } from './components/objects/links'
import { ObjectPage } from './components/objects/ObjectPage'
import { ConnectionStatus, ProblemsBanner, ReadProblems } from './components/Status'
import { PanelState } from './components/PanelState'
import { DetailsPanel, Legend, SessionsPanel } from './components/Panels'
import { VersionLine } from './components/Versions'
import { LiveBriefWriter, type BriefWriter } from './k8s/briefs'
import { LiveContainerWriter } from './k8s/containers'
import { LiveCrewWriter } from './k8s/crews'
import { LiveStandardWriter } from './k8s/standards'
import { HealthSource, type FactoryHealth } from './k8s/health'
import { fetchSession, signOut, type UiSession } from './k8s/session'
import { WatchMux } from './k8s/mux'
import {
  emptyFactory,
  emptyStory,
  LiveFactorySource,
  LiveProjectsSource,
  LiveStorySource,
  mergeConnections,
  problemsOf,
  readOf,
  type Api,
  type FactoryData,
  type Snapshot,
  type Source,
  type StoryData,
} from './k8s/sources'
import { GROUP, RESOURCES, VERSION, type Project } from './k8s/types'
import { useDemo } from './lib/demo'
import { RefreshSession, SessionContext, useSource, useUrlState, type View } from './lib/hooks'
import { areaLabel } from './model/areas'
import { plural, titleOf } from './model/describe'
import { factoryVersions } from './model/versions'
import { hasPage } from './model/lifecycle'
import { activeCrewTasks } from './model/crews'
import { needs } from './model/needs'
import {
  buildContainerDiagram,
  buildContextDiagram,
  containerNodes,
  systemsForPicker,
  type DiagramNode,
  workingNow,
} from './model/story'

/**
 * Where the Kubernetes API is proxied, relative to the page (see server.mjs).
 * All watches of the page share connections through one WatchMux.
 */
const API: Api = { baseUrl: 'k8s', mux: new WatchMux({ url: 'k8s-watch' }) }
/** The Brief writes, through the same proxy: create a Brief, add a prompt to one, pause or resume one. */
const LIVE_WRITER = new LiveBriefWriter(API.baseUrl)
/** With UI actions on: a Container's scaffold retry and a Task from a goal. */
const CONTAINER_WRITER = new LiveContainerWriter(API.baseUrl)
/** A Crew page's writes, taken only with the UI server's actions on. */
const CREW_WRITER = new LiveCrewWriter(API.baseUrl)
/** With UI actions on: a Standard's ref bump. */
const STANDARD_WRITER = new LiveStandardWriter(API.baseUrl)

/** A source with nothing in it: for a view that is not shown, or no project yet. */
class Idle<T> implements Source<T> {
  private readonly snapshot: Snapshot<T>
  constructor(data: T) {
    this.snapshot = { data, connection: { phase: 'connecting', loaded: false, problems: [] } }
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
const NO_PROJECTS = new Idle<Project[]>([])
const NO_STORY = new Idle<StoryData>(emptyStory(''))
const NO_FACTORY = new Idle<FactoryData>(emptyFactory())
const NO_HEALTH = new Idle<FactoryHealth | undefined>(undefined)

const MemoDiagram = memo(Diagram)

/** The kinds the Story diagram draws, and those of who is working, by their plurals. */
const DIAGRAM_KINDS = [
  RESOURCES.projects,
  RESOURCES.people,
  RESOURCES.softwareSystems,
  RESOURCES.containers,
  RESOURCES.dataStores,
  RESOURCES.relationships,
]
const WORKING_KINDS = [
  RESOURCES.softwareSystems,
  RESOURCES.containers,
  RESOURCES.sessions,
  RESOURCES.crews,
  RESOURCES.tasks,
  RESOURCES.agents,
]

export default function App() {
  const [url, setUrl] = useUrlState()
  const demo = url.demo
  const { module: demoModule, problem: demoProblem } = useDemo(demo)

  const projectsSource = useMemo<Source<Project[]>>(() => {
    if (!demo) return new LiveProjectsSource(API)
    return demoModule ? new demoModule.DemoProjectsSource() : NO_PROJECTS
  }, [demo, demoModule])
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
  const shell = url.shell
  // An object page of a kind that has one watches the object's namespace.
  const objectKind = shell && url.kind && url.name && hasPage(url.kind) ? url.kind : undefined
  const namespace = (objectKind ? url.ns : undefined) ?? url.project ?? sortedProjects[0]?.metadata.namespace
  const versions = useMemo(
    () => factoryVersions(sortedProjects, __C4_UI_VERSION__, `${GROUP}/${VERSION}`),
    [sortedProjects],
  )
  // Without an area, the shell opens on Needs you while it loads, then stays
  // there if anything waits on a person, else goes to Story (the effect below).
  const area = url.area ?? 'needs'
  const objectPage = shell && Boolean(url.kind && url.name)
  // In the shell, Story and Briefs show one project; Needs you, Work and Factory every one.
  const factoryView = shell ? !objectPage && (area === 'work' || area === 'factory') : url.view === 'factory'
  const needsView = shell && !objectPage && area === 'needs'
  const storyView = shell ? (objectPage ? Boolean(objectKind) : area === 'story' || area === 'briefs') : !factoryView

  // Only the view on screen watches the cluster.
  const storySource = useMemo<Source<StoryData>>(() => {
    if (!namespace || !storyView) return NO_STORY
    if (!demo) return new LiveStorySource(namespace, API)
    return demoModule ? new demoModule.DemoStorySource() : NO_STORY
  }, [namespace, demo, demoModule, storyView])
  const factorySource = useMemo<Source<FactoryData>>(() => {
    if (!factoryView && !needsView) return NO_FACTORY
    if (!demo) return new LiveFactorySource(API)
    return demoModule ? new demoModule.DemoFactorySource() : NO_FACTORY
  }, [demo, demoModule, factoryView, needsView])
  const healthSource = useMemo<Source<FactoryHealth | undefined>>(() => {
    if (!factoryView) return NO_HEALTH
    if (!demo) return new HealthSource()
    return demoModule ? new demoModule.DemoHealthSource() : NO_HEALTH
  }, [demo, demoModule, factoryView])
  const { session, onSignOut, refresh: refreshSession } = useSession(demo)
  const factory = useSource(factorySource)
  const health = useSource(healthSource)
  const story = useSource(storySource)
  const data = story.data
  const needItems = useMemo(() => needs(factory.data), [factory.data])
  // Every list, Briefs and Crews included, so a late one can't hide its items;
  // a factory that can't be read goes to Story rather than waiting on it.
  const factoryRead = factory.connection.settled ?? factory.connection.loaded
  const factoryBroken = factory.connection.problems.length > 0 || Boolean(demoProblem)
  const pickArea = shell && !url.area && !objectPage && ((factory.connection.loaded && factoryRead) || factoryBroken)
  const firstArea = needItems.length > 0 ? 'needs' : 'story'
  useEffect(() => {
    // Once, so the area doesn't change under the person as items come and go.
    if (pickArea) setUrl({ area: firstArea }, true)
  }, [pickArea, firstArea, setUrl])
  // Demo mode writes into its own fixture; live mode through the proxy.
  const writer: BriefWriter =
    demoModule && storySource instanceof demoModule.DemoStorySource ? storySource : LIVE_WRITER

  const systems = useMemo(() => systemsForPicker(data), [data])
  const systemName = url.system && systems.some((s) => s.name === url.system) ? url.system : systems[0]?.name
  const view: Exclude<View, 'factory'> = url.view === 'containers' ? 'containers' : 'context'
  const selectTask = useCallback(
    // Selecting a Task replaces the history entry; in the shell, a Task opens its page.
    (task: string | undefined) => {
      if (!shell) return setUrl({ view: 'factory', task }, true)
      const [ns, name] = task?.split('/') ?? []
      if (name) setUrl({ ns, kind: 'Task', name, task: undefined })
      else setUrl({ task: undefined }, true)
    },
    [setUrl, shell],
  )

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
    (id: string) => {
      // In the shell, a box of a kind with a page opens it.
      const node = shell ? diagram?.nodes.find((n) => n.id === id) : undefined
      if (node && namespace && hasPage(node.kind)) return setUrl({ ns: namespace, kind: node.kind, name: node.name })
      setSelection((cur) => (cur?.scope === scope && cur.id === id ? undefined : { scope, id }))
    },
    [scope, shell, diagram, namespace, setUrl],
  )
  const nodeHref = useMemo(
    () => (shell && namespace ? (node: DiagramNode) => pageHref(namespace, node.kind, node.name) : undefined),
    [shell, namespace],
  )
  const openSystem = useCallback((name: string) => setUrl({ view: 'containers', system: name }), [setUrl])

  const sessionContainers = useMemo(
    () => containerNodes(data, view === 'containers' ? systemName : undefined),
    [data, view, systemName],
  )

  const crewTasks = useMemo(
    () =>
      activeCrewTasks(
        data.crews,
        data.tasks,
        data.agents,
        sessionContainers.map((c) => c.name),
      ),
    [data, sessionContainers],
  )

  const project = data.project
  const pageTitle = shell
    ? `${objectPage ? `${url.kind} ${url.name}` : areaLabel(area)}${demo ? ' (demo)' : ''} · C4 Factory`
    : factoryView
      ? `The factory${demo ? ' (demo)' : ''} · C4 Factory`
      : project
        ? `${titleOf(project)}${demo ? ' (demo)' : ''} · C4 Factory`
        : 'C4 Factory'
  useEffect(() => {
    document.title = pageTitle
  }, [pageTitle])
  const shownConnection = needsView
    ? factory.connection
    : factoryView
      ? demo
        ? factory.connection
        : mergeConnections(factory.connection, health.connection)
      : demo || !storyView
        ? projects.connection
        : mergeConnections(projects.connection, story.connection)
  const connection = demoProblem
    ? { ...shownConnection, problems: [...shownConnection.problems, demoProblem] }
    : shownConnection
  const noProjects = !demo && projects.connection.loaded && sortedProjects.length === 0
  const systemTitle = systems.find((s) => s.name === systemName)?.title

  const picker =
    sortedProjects.length > 0 ? (
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
    ) : null
  const banners = (
    <>
      {demo ? (
        <div className="banner banner-demo" role="status">
          <strong>Demo mode.</strong> You are looking at built-in example data, not a cluster. Nothing here is live.
          <a href={shell ? '?shell=next' : '?'} className="banner-link">
            Leave demo
          </a>
        </div>
      ) : null}
      {/* In the shell, each panel says the reads behind it that failed; only demo mode's own failure is the page's. */}
      <ProblemsBanner problems={shell ? (demoProblem ? [demoProblem] : []) : connection.problems} />
    </>
  )
  // In the shell, the failed reads behind a Story panel; the old layout has the banner.
  // Every list read or failed: a failed one doesn't hold up the rest of the story.
  const storyRead = story.connection.settled ?? story.connection.loaded
  const storyProblems = (...resources: string[]) => (shell ? problemsOf(story.connection, ...resources) : undefined)
  // Without the list of projects there is no project to show.
  const projectsFailed =
    shell && !namespace && projects.connection.problems.length ? (
      <main className="shell-page">
        <section className="panel">
          <h1 className="panel-title">Projects</h1>
          <ReadProblems problems={projects.connection.problems} />
        </section>
      </main>
    ) : null
  const briefs =
    namespace && story.connection.loaded ? (
      <Briefs
        briefs={data.briefs}
        namespace={namespace}
        canCreate={Boolean(data.project)}
        writer={writer}
        agents={data.agents}
        pageHref={shell ? (name) => pageHref(namespace, 'Brief', name)! : undefined}
      />
    ) : null
  const storyBody = noProjects ? (
    <EmptyProjects />
  ) : (
    <>
      <ProjectHeader data={data} namespace={namespace} loaded={storyRead} />
      {shell ? storyRead ? <StoryLine data={data} /> : null : briefs}
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
          <ViewCaption view={view} systemTitle={systemTitle} opensPages={shell} />
          <PanelState
            loaded={readOf(story.connection, ...DIAGRAM_KINDS)}
            problems={storyProblems(...DIAGRAM_KINDS)}
            empty={!diagram || diagram.nodes.length + (diagram.boundary ? 1 : 0) === 0}
            loading="Loading the story…"
            emptyText={
              view === 'context'
                ? 'This project has no people or software systems yet. They appear here as soon as they are added.'
                : 'Pick a software system to see its containers.'
            }
          >
            {diagram ? (
              <MemoDiagram
                diagram={diagram}
                selectedId={selectedId}
                onSelect={onSelect}
                onOpenSystem={view === 'context' ? openSystem : undefined}
                nodeHref={nodeHref}
                emptyBoundaryText="No containers or data stores in this system yet."
              />
            ) : null}
          </PanelState>
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
              onClose={() => setSelection(undefined)}
              onOpenSystem={openSystem}
            />
          ) : null}
          <SessionsPanel
            containers={sessionContainers}
            tasks={crewTasks}
            scope={view === 'containers' && systemTitle ? systemTitle : project ? titleOf(project) : 'this project'}
            loaded={readOf(story.connection, ...WORKING_KINDS)}
            problems={storyProblems(...WORKING_KINDS)}
          />
          <Legend view={view} />
        </aside>
      </main>
    </>
  )
  const factoryPage = (
    <FactoryPage
      data={factory.data}
      loaded={factory.connection.loaded}
      connection={shell ? factory.connection : undefined}
      health={health.data}
      healthLoaded={health.connection.loaded}
      healthProblems={shell ? health.connection.problems : undefined}
      selectedTask={url.task}
      onSelectTask={selectTask}
      pageHref={shell ? pageHref : undefined}
    />
  )

  const page = shell ? (
    <Shell
      url={url}
      area={area}
      setUrl={setUrl}
      connection={connection}
      namespace={namespace}
      versions={versions}
      onSignOut={onSignOut}
      picker={picker}
      banners={banners}
      areas={{
        needs: (
          <NeedsArea
            items={needItems}
            projects={factory.data.projects}
            // An optional list read late may still add items.
            loaded={factory.connection.loaded && factoryRead}
            problems={[...factory.connection.problems, ...(factory.connection.unread ?? [])]}
            setUrl={setUrl}
          />
        ),
        briefs: noProjects ? (
          <EmptyProjects />
        ) : (
          (projectsFailed ??
          (namespace ? (
            <Briefs
              briefs={data.briefs}
              namespace={namespace}
              canCreate={Boolean(data.project)}
              writer={writer}
              agents={data.agents}
              pageHref={(name) => pageHref(namespace, 'Brief', name)!}
              loaded={readOf(story.connection, RESOURCES.briefs, RESOURCES.projects)}
              problems={problemsOf(story.connection, RESOURCES.briefs, RESOURCES.projects)}
            />
          ) : (
            <div className="placeholder" role="status">
              Loading the projects…
            </div>
          )))
        ),
        story: projectsFailed ?? storyBody,
        work: factoryPage,
        factory: (
          <FactoryArea
            data={factory.data}
            connection={factory.connection}
            health={health.data}
            healthLoaded={health.connection.loaded}
            healthProblems={health.connection.problems}
            onSelectTask={selectTask}
          />
        ),
      }}
      objectPage={
        objectKind && url.name ? (
          <ObjectPage
            kind={objectKind}
            name={url.name}
            data={data}
            connection={story.connection}
            writer={writer}
            containerWriter={CONTAINER_WRITER}
            crewWriter={CREW_WRITER}
            standardWriter={STANDARD_WRITER}
          />
        ) : undefined
      }
    />
  ) : (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <LogoMark size={22} />
          <span className="brand-name">C4 Factory</span>
          <span
            className="brand-sub"
            title="Reads everything; its only writes create a Brief, add a prompt to one, and pause or resume one"
          >
            read-only but for creating, prompting and pausing Briefs
          </span>
        </div>
        <nav className="modes" aria-label="Mode">
          <button
            type="button"
            className="mode"
            aria-current={factoryView ? undefined : 'page'}
            onClick={() => setUrl({ view: 'context', task: undefined })}
          >
            Story
          </button>
          <button
            type="button"
            className="mode"
            aria-current={factoryView ? 'page' : undefined}
            onClick={() => setUrl({ view: 'factory' })}
          >
            Factory
          </button>
        </nav>
        <div className="topbar-right">
          <ConnectionStatus connection={connection} />
          <Account onSignOut={onSignOut} />
          {factoryView ? null : picker}
        </div>
      </header>

      {banners}

      {factoryView ? factoryPage : storyBody}

      <footer className="footer">
        This page reads the C4 Factory resources in the cluster and updates as they change. It can change only three
        things: create a Brief from an app idea, add a prompt to a Brief, and pause or resume a Brief. The cluster
        decides what it accepts, and a refusal shows its message.
        <VersionLine versions={versions} />
      </footer>
    </div>
  )
  return (
    <SessionContext.Provider value={session}>
      <RefreshSession.Provider value={refreshSession}>{page}</RefreshSession.Provider>
    </SessionContext.Provider>
  )
}

/**
 * The UI server's session answer (none in demo mode), asked when the page
 * opens and again on refresh, such as after a write refused with 401; and a
 * sign-out. Without an answer the page shows no account.
 */
function useSession(demo: boolean): {
  session: UiSession | undefined
  onSignOut: () => Promise<void>
  refresh: () => void
} {
  const [session, setSession] = useState<UiSession>()
  const [asked, setAsked] = useState(0)
  useEffect(() => {
    if (demo) return
    let live = true
    fetchSession().then(
      (answer) => live && setSession(answer),
      () => live && setSession(undefined),
    )
    return () => {
      live = false
    }
  }, [demo, asked])
  const refresh = useCallback(() => setAsked((n) => n + 1), [])
  const onSignOut = useCallback(async () => {
    if (!session?.logout) return
    await signOut(session.logout)
    setSession({ ...session, user: null })
  }, [session])
  return { session: demo ? undefined : session, onSignOut, refresh }
}

function ProjectHeader({ data, namespace, loaded }: { data: StoryData; namespace?: string; loaded: boolean }) {
  const project = data.project
  const ready = data.containers.filter((c) => c.status?.phase === 'Ready').length
  const active = workingNow(data)
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
