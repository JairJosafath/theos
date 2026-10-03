// Sources of story data for the React app. A live source watches the cluster
// through the /k8s proxy; the demo source (src/demo) serves a fixture. Both have
// the same shape, so the views never know the difference.

import {
  RESOURCES,
  resourcePath,
  type Agent,
  type CatalogEntry,
  type Container,
  type Crew,
  type DataStore,
  type K8sObject,
  type Person,
  type Project,
  type Relationship,
  type Session,
  type SoftwareSystem,
  type Task,
} from './types'
import type { WatchMux } from './mux'
import { ApiError, ResourceWatcher, type WatchState } from './watch'

/** Everything the views need about one project (one namespace). */
export interface StoryData {
  namespace: string
  project?: Project
  people: Person[]
  systems: SoftwareSystem[]
  containers: Container[]
  dataStores: DataStore[]
  relationships: Relationship[]
  sessions: Session[]
  /** Catalog entries, used only for display names. Optional: may stay empty. */
  frameworks: CatalogEntry[]
  connectors: CatalogEntry[]
  /** Who works on each Container's issues, with their Agents and Tasks. Optional: may stay empty. */
  crews: Crew[]
  agents: Agent[]
  tasks: Task[]
}

export interface Problem {
  /** What could not be read, in plain words, such as "containers". */
  what: string
  message: string
  code?: number
}

export interface Connection {
  /** demo: fixture data; connecting: nothing loaded yet; live: all watches open. */
  phase: 'demo' | 'connecting' | 'live' | 'reconnecting'
  /** True once every required list has loaded at least once. */
  loaded: boolean
  problems: Problem[]
}

export interface Snapshot<T> {
  data: T
  connection: Connection
}

export interface Source<T> {
  subscribe(listener: () => void): () => void
  getSnapshot(): Snapshot<T>
  start(): void
  stop(): void
}

export function emptyStory(namespace: string): StoryData {
  return {
    namespace,
    people: [],
    systems: [],
    containers: [],
    dataStores: [],
    relationships: [],
    sessions: [],
    frameworks: [],
    connectors: [],
    crews: [],
    agents: [],
    tasks: [],
  }
}

/** Combines the state of several watchers into one connection state. */
export function combineConnection(
  states: { what: string; state: WatchState<unknown>; optional?: boolean }[],
): Connection {
  const required = states.filter((s) => !s.optional)
  const problems: Problem[] = []
  for (const { what, state, optional } of states) {
    // Optional kinds (the catalog, Agents, Crews) only add details, so a
    // failure there is not worth alarming anyone about.
    if (state.error && !optional) {
      problems.push({
        what,
        message: state.error.message,
        code: state.error instanceof ApiError ? state.error.code : undefined,
      })
    }
  }
  const loaded = required.every((s) => s.state.loaded)
  let phase: Connection['phase'] = 'live'
  if (!loaded) phase = 'connecting'
  else if (required.some((s) => s.state.phase !== 'live' || s.state.error)) phase = 'reconnecting'
  return { phase, loaded, problems }
}

type Watched = { what: string; watcher: ResourceWatcher<K8sObject>; optional?: boolean }

/** Base class: owns a set of watchers and rebuilds a cached snapshot on change. */
abstract class WatchingSource<T> implements Source<T> {
  private readonly listeners = new Set<() => void>()
  private readonly unsubscribers: (() => void)[] = []
  private snapshot: Snapshot<T>
  protected readonly watched: Watched[]

  protected constructor(watched: Watched[], initial: T) {
    this.watched = watched
    this.snapshot = { data: initial, connection: { phase: 'connecting', loaded: false, problems: [] } }
  }

  protected abstract build(): T

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  getSnapshot(): Snapshot<T> {
    return this.snapshot
  }

  start(): void {
    for (const { watcher } of this.watched) {
      this.unsubscribers.push(watcher.subscribe(() => this.changed()))
      watcher.start()
    }
  }

  stop(): void {
    for (const unsubscribe of this.unsubscribers.splice(0)) unsubscribe()
    for (const { watcher } of this.watched) watcher.stop()
  }

  protected items<K extends K8sObject>(index: number): K[] {
    return this.watched[index].watcher.getState().items as K[]
  }

  private changed(): void {
    this.snapshot = {
      data: this.build(),
      connection: combineConnection(
        this.watched.map(({ what, watcher, optional }) => ({ what, state: watcher.getState(), optional })),
      ),
    }
    for (const listener of this.listeners) listener()
  }
}

/** Where the live sources read from: the /k8s proxy, and optionally a WatchMux for watches. */
export interface Api {
  baseUrl: string
  mux?: WatchMux
}

function watcher(api: Api, plural: string, namespace?: string): ResourceWatcher<K8sObject> {
  const mux = api.mux
  return new ResourceWatcher<K8sObject>({
    baseUrl: api.baseUrl,
    path: resourcePath(plural, namespace),
    openWatch: mux ? (path, rv, timeout, signal) => mux.watch(path, rv, timeout, signal) : undefined,
  })
}

/** Watches every story kind in one project namespace. */
export class LiveStorySource extends WatchingSource<StoryData> {
  private readonly namespace: string

  constructor(namespace: string, api: Api) {
    super(
      [
        { what: 'the project', watcher: watcher(api, RESOURCES.projects, namespace) },
        { what: 'people', watcher: watcher(api, RESOURCES.people, namespace) },
        { what: 'software systems', watcher: watcher(api, RESOURCES.softwareSystems, namespace) },
        { what: 'containers', watcher: watcher(api, RESOURCES.containers, namespace) },
        { what: 'data stores', watcher: watcher(api, RESOURCES.dataStores, namespace) },
        { what: 'relationships', watcher: watcher(api, RESOURCES.relationships, namespace) },
        { what: 'sessions', watcher: watcher(api, RESOURCES.sessions, namespace) },
        { what: 'frameworks', watcher: watcher(api, RESOURCES.frameworks, namespace), optional: true },
        { what: 'connectors', watcher: watcher(api, RESOURCES.connectors, namespace), optional: true },
        { what: 'crews', watcher: watcher(api, RESOURCES.crews, namespace), optional: true },
        { what: 'agents', watcher: watcher(api, RESOURCES.agents, namespace), optional: true },
        { what: 'tasks', watcher: watcher(api, RESOURCES.tasks, namespace), optional: true },
      ],
      emptyStory(namespace),
    )
    this.namespace = namespace
  }

  protected build(): StoryData {
    const projects = this.items<Project>(0)
    return {
      namespace: this.namespace,
      // One Project per namespace, named after it.
      project: projects.find((p) => p.metadata.name === this.namespace) ?? projects[0],
      people: this.items<Person>(1),
      systems: this.items<SoftwareSystem>(2),
      containers: this.items<Container>(3),
      dataStores: this.items<DataStore>(4),
      relationships: this.items<Relationship>(5),
      sessions: this.items<Session>(6),
      frameworks: this.items<CatalogEntry>(7),
      connectors: this.items<CatalogEntry>(8),
      crews: this.items<Crew>(9),
      agents: this.items<Agent>(10),
      tasks: this.items<Task>(11),
    }
  }
}

/** Watches Projects across all namespaces, for the project picker. */
export class LiveProjectsSource extends WatchingSource<Project[]> {
  constructor(api: Api) {
    super([{ what: 'projects', watcher: watcher(api, RESOURCES.projects) }], [])
  }

  protected build(): Project[] {
    return this.items<Project>(0)
  }
}

/** Everything the factory view needs, across all projects. */
export interface FactoryData {
  projects: Project[]
  tasks: Task[]
  agents: Agent[]
  containers: Container[]
  sessions: Session[]
  crews: Crew[]
}

export function emptyFactory(): FactoryData {
  return { projects: [], tasks: [], agents: [], containers: [], sessions: [], crews: [] }
}

/** Watches the work of the factory in every namespace: Tasks, Agents, Containers, Sessions and Crews. */
export class LiveFactorySource extends WatchingSource<FactoryData> {
  constructor(api: Api) {
    super(
      [
        { what: 'projects', watcher: watcher(api, RESOURCES.projects) },
        { what: 'tasks', watcher: watcher(api, RESOURCES.tasks) },
        { what: 'agents', watcher: watcher(api, RESOURCES.agents), optional: true },
        { what: 'containers', watcher: watcher(api, RESOURCES.containers) },
        { what: 'sessions', watcher: watcher(api, RESOURCES.sessions) },
        // Optional, so a cluster without the Crew kind still shows its Tasks.
        { what: 'crews', watcher: watcher(api, RESOURCES.crews), optional: true },
      ],
      emptyFactory(),
    )
  }

  protected build(): FactoryData {
    return {
      projects: this.items<Project>(0),
      tasks: this.items<Task>(1),
      agents: this.items<Agent>(2),
      containers: this.items<Container>(3),
      sessions: this.items<Session>(4),
      crews: this.items<Crew>(5),
    }
  }
}
