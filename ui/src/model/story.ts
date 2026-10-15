// Turns the watched resources into diagram models: what boxes and arrows the
// system context view and the container view draw. Pure functions, no React.
//
// Relationships may connect elements at any level (a Person to a Container, a
// Container to another system's DataStore). Each view "rolls up" an element it
// doesn't draw to the nearest one it does: in the context view a Container stands
// in for its SoftwareSystem; in a container view another system's Container
// stands in for that system.

import type { StoryData } from '../k8s/sources'
import {
  findCondition,
  type Condition,
  type ContainerPhase,
  type Crew,
  type ElementKind,
  type ElementRef,
  type Relationship,
  type Session,
} from '../k8s/types'
import { describeContainerType, plural, titleOf } from './describe'
import { activeCrewTasks, crewSummary, type CrewSummary } from './crews'

export type NodeRole = 'person' | 'system' | 'external-system' | 'container' | 'datastore'

interface NodeBase {
  /** "<Kind>/<name>", unique within a project. */
  id: string
  kind: ElementKind
  name: string
  title: string
  description?: string
}

export interface PersonNode extends NodeBase {
  role: 'person'
}

export interface SystemNode extends NodeBase {
  role: 'system' | 'external-system'
  /** spec.location is External: a system the factory doesn't build. */
  external: boolean
  needs: string[]
  kpis: string[]
  /** status.summary, or counts of what the system holds when that isn't there yet. */
  summary: string
  /** True when summary comes from the operator's roll-up. */
  summaryReported: boolean
  containers: number
  dataStores: number
}

export interface ContainerNode extends NodeBase {
  role: 'container'
  type: string
  typeLabel: string
  /** Display name of the Framework catalog entry, when it could be read. */
  framework?: string
  phase?: ContainerPhase
  repositoryURL?: string
  scaffoldedFrom: string[]
  conditions: Condition[]
  /** Sessions on this Container: Active first, then most recent heartbeat. */
  sessions: Session[]
  activeSessions: number
  /** The Crew that picks up its issues, if it has one. */
  crew?: CrewSummary
}

export interface DataStoreNode extends NodeBase {
  role: 'datastore'
  connectorName: string
  /** Display name of the Connector catalog entry, falling back to its name. */
  connector: string
  conditions: Condition[]
}

export type DiagramNode = PersonNode | SystemNode | ContainerNode | DataStoreNode

export interface DiagramEdge {
  id: string
  source: string
  target: string
  label: string
  /** Names of the Relationships drawn as this one arrow. */
  relationships: string[]
  /** True when the arrow stands for links between elements inside a box. */
  implied: boolean
  /** Set for a Container to DataStore link that was added after scaffolding. */
  notApplied?: { message?: string }
}

/** The selected system in a container view, drawn as a dashed box around its parts. */
export interface Boundary {
  id: string
  name: string
  title: string
  nodeIds: string[]
}

export interface Diagram {
  nodes: DiagramNode[]
  edges: DiagramEdge[]
  boundary?: Boundary
  /** Things that could not be drawn, in plain words, such as a link to a missing element. */
  looseEnds: string[]
}

export function elementId(kind: ElementKind, name: string): string {
  return `${kind}/${name}`
}

const KIND_WORDS: Record<ElementKind, string> = {
  Person: 'person',
  SoftwareSystem: 'system',
  Container: 'container',
  DataStore: 'data store',
}

const ROLE_ORDER: Record<NodeRole, number> = {
  person: 0,
  system: 1,
  container: 2,
  datastore: 3,
  'external-system': 4,
}

function byRoleThenTitle(a: DiagramNode, b: DiagramNode): number {
  return ROLE_ORDER[a.role] - ROLE_ORDER[b.role] || a.title.localeCompare(b.title) || a.id.localeCompare(b.id)
}

/** Lookups shared by both views. */
class StoryIndex {
  readonly data: StoryData
  /** Which system each Container and DataStore belongs to, by element id. */
  readonly systemOf = new Map<string, string>()
  readonly exists = new Set<string>()

  constructor(data: StoryData) {
    this.data = data
    for (const p of data.people) this.exists.add(elementId('Person', p.metadata.name))
    for (const s of data.systems) this.exists.add(elementId('SoftwareSystem', s.metadata.name))
    for (const c of data.containers) {
      const id = elementId('Container', c.metadata.name)
      this.exists.add(id)
      this.systemOf.set(id, c.spec.softwareSystemRef?.name)
    }
    for (const d of data.dataStores) {
      const id = elementId('DataStore', d.metadata.name)
      this.exists.add(id)
      this.systemOf.set(id, d.spec.softwareSystemRef?.name)
    }
  }

  has(ref: ElementRef): boolean {
    return this.exists.has(elementId(ref.kind, ref.name))
  }

  /** The system a Container or DataStore belongs to, if that system exists. */
  owningSystem(ref: ElementRef): string | undefined {
    const system = this.systemOf.get(elementId(ref.kind, ref.name))
    return system && this.exists.has(elementId('SoftwareSystem', system)) ? system : undefined
  }

  systemNode(name: string, role: SystemNode['role']): SystemNode | undefined {
    const system = this.data.systems.find((s) => s.metadata.name === name)
    if (!system) return undefined
    const containers = this.data.containers.filter((c) => c.spec.softwareSystemRef?.name === name).length
    const dataStores = this.data.dataStores.filter((d) => d.spec.softwareSystemRef?.name === name).length
    const reported = system.status?.summary?.trim()
    return {
      id: elementId('SoftwareSystem', name),
      kind: 'SoftwareSystem',
      name,
      title: titleOf(system),
      description: system.spec?.description,
      role,
      external: system.spec?.location === 'External',
      needs: system.spec?.needs ?? [],
      kpis: system.spec?.kpis ?? [],
      summary:
        reported ||
        (containers + dataStores === 0
          ? 'No containers yet'
          : `${plural(containers, 'container')}, ${plural(dataStores, 'data store')}`),
      summaryReported: Boolean(reported),
      containers,
      dataStores,
    }
  }

  personNode(name: string): PersonNode | undefined {
    const person = this.data.people.find((p) => p.metadata.name === name)
    if (!person) return undefined
    return {
      id: elementId('Person', name),
      kind: 'Person',
      name,
      title: titleOf(person),
      description: person.spec?.description,
      role: 'person',
    }
  }

  containerNodes(system: string): ContainerNode[] {
    return this.data.containers
      .filter((c) => c.spec.softwareSystemRef?.name === system)
      .map((c) => {
        const framework = this.data.frameworks.find((f) => f.metadata.name === c.spec.frameworkRef?.name)
        const sessions = sessionsOf(this.data.sessions, c.metadata.name)
        const crew = crewOf(this.data.crews, c.metadata.name)
        return {
          id: elementId('Container', c.metadata.name),
          kind: 'Container',
          name: c.metadata.name,
          title: titleOf(c),
          description: c.spec.description,
          role: 'container',
          type: c.spec.type,
          typeLabel: describeContainerType(c.spec.type),
          framework: framework ? titleOf(framework) : c.spec.frameworkRef?.name,
          phase: c.status?.phase,
          repositoryURL: c.status?.repositoryURL,
          scaffoldedFrom: c.status?.scaffoldedFrom ?? [],
          conditions: c.status?.conditions ?? [],
          sessions,
          // Counted from the live Sessions rather than status.activeSessions, so
          // the badge and the session list never disagree while the operator
          // catches up.
          activeSessions: sessions.filter((s) => s.status?.phase === 'Active').length,
          crew: crew ? crewSummary(crew, this.data.agents, this.data.tasks) : undefined,
        }
      })
  }

  dataStoreNodes(system: string): DataStoreNode[] {
    return this.data.dataStores
      .filter((d) => d.spec.softwareSystemRef?.name === system)
      .map((d) => {
        const connectorName = d.spec.connectorRef?.name ?? ''
        const connector = this.data.connectors.find((c) => c.metadata.name === connectorName)
        return {
          id: elementId('DataStore', d.metadata.name),
          kind: 'DataStore',
          name: d.metadata.name,
          title: titleOf(d),
          description: d.spec.description,
          role: 'datastore',
          connectorName,
          connector: connector ? titleOf(connector) : connectorName,
          conditions: d.status?.conditions ?? [],
        }
      })
  }
}

/** The Crew of one Container; should there be several, the first by name. */
export function crewOf(crews: Crew[], container: string): Crew | undefined {
  return crews
    .filter((c) => c.spec.containerRef?.name === container)
    .sort((a, b) => a.metadata.name.localeCompare(b.metadata.name))[0]
}

/** How many work on the project now: active Sessions, and the Tasks its Crews are on. */
export function workingNow(data: StoryData): number {
  const sessions = data.sessions.filter((s) => s.status?.phase === 'Active').length
  const containers = data.containers.map((c) => c.metadata.name)
  return sessions + activeCrewTasks(data.crews, data.tasks, data.agents, containers).length
}

/** Sessions on one Container: Active first, then the most recent heartbeat. */
export function sessionsOf(sessions: Session[], container: string): Session[] {
  const rank = (s: Session) => (s.status?.phase === 'Active' ? 0 : s.status?.phase ? 2 : 1)
  const beat = (s: Session) => Date.parse(s.status?.lastHeartbeat ?? s.metadata.creationTimestamp ?? '') || 0
  return sessions
    .filter((s) => s.spec.containerRef?.name === container)
    .sort((a, b) => rank(a) - rank(b) || beat(b) - beat(a) || a.metadata.name.localeCompare(b.metadata.name))
}

function describeRef(ref: ElementRef): string {
  return `${KIND_WORDS[ref.kind] ?? ref.kind} "${ref.name}"`
}

/** Collects Relationships into one arrow per (source, target) pair. */
class EdgeCollector {
  private readonly edges = new Map<string, DiagramEdge & { explicitLabel?: string }>()

  add(rel: Relationship, source: string, target: string, implied: boolean): void {
    const key = `${source}->${target}`
    const description = rel.spec.description?.trim() ?? ''
    const applied = findCondition(rel.status?.conditions, 'Applied')
    const notApplied =
      applied?.status === 'False' && applied.reason === 'AddedAfterScaffold' ? { message: applied.message } : undefined

    let edge = this.edges.get(key)
    if (!edge) {
      edge = { id: key, source, target, label: '', relationships: [], implied: true }
      this.edges.set(key, edge)
    }
    edge.relationships.push(rel.metadata.name)
    // A Relationship drawn at its own level names the arrow; implied ones only
    // fill in when there is none.
    if (!implied) {
      edge.implied = false
      if (edge.explicitLabel === undefined && description) edge.explicitLabel = description
    }
    if (!edge.label && description) edge.label = description
    if (notApplied) edge.notApplied = notApplied
  }

  result(): DiagramEdge[] {
    return [...this.edges.values()]
      .map(({ explicitLabel, ...edge }) => {
        let label = explicitLabel ?? edge.label
        const others = edge.relationships.length - 1
        if (others > 0) label = label ? `${label} (+${others} more)` : plural(edge.relationships.length, 'link')
        return { ...edge, label }
      })
      .sort((a, b) => a.id.localeCompare(b.id))
  }
}

function sortedRelationships(data: StoryData): Relationship[] {
  return [...data.relationships].sort((a, b) => a.metadata.name.localeCompare(b.metadata.name))
}

/**
 * C4 level 1: People and SoftwareSystems, and the Relationships between them.
 * Links between Containers or DataStores of different systems show as an arrow
 * between those systems.
 */
export function buildContextDiagram(data: StoryData): Diagram {
  const index = new StoryIndex(data)
  const looseEnds: string[] = []
  const nodes: DiagramNode[] = [
    ...data.people.map((p) => index.personNode(p.metadata.name)!),
    ...data.systems.map((s) =>
      index.systemNode(s.metadata.name, s.spec?.location === 'External' ? 'external-system' : 'system')!,
    ),
  ]

  // Where each end of a Relationship shows up at this level.
  const place = (ref: ElementRef): string | undefined => {
    if (ref.kind === 'Person' || ref.kind === 'SoftwareSystem') {
      return index.has(ref) ? elementId(ref.kind, ref.name) : undefined
    }
    const system = index.owningSystem(ref)
    return system ? elementId('SoftwareSystem', system) : undefined
  }

  const edges = new EdgeCollector()
  for (const rel of sortedRelationships(data)) {
    const { source, target } = rel.spec
    const s = place(source)
    const t = place(target)
    if (!s || !t) {
      const missing = !s ? source : target
      looseEnds.push(
        `The link "${rel.spec.description || rel.metadata.name}" points to ${describeRef(missing)}, which does not exist.`,
      )
      continue
    }
    if (s === t) continue // Inside one system: the container view shows it.
    const implied = !(elementId(source.kind, source.name) === s && elementId(target.kind, target.name) === t)
    edges.add(rel, s, t, implied)
  }

  for (const c of data.containers) {
    const system = c.spec.softwareSystemRef?.name
    if (!index.exists.has(elementId('SoftwareSystem', system))) {
      looseEnds.push(`Container "${titleOf(c)}" belongs to system "${system}", which does not exist.`)
    }
  }
  for (const d of data.dataStores) {
    const system = d.spec.softwareSystemRef?.name
    if (!index.exists.has(elementId('SoftwareSystem', system))) {
      looseEnds.push(`Data store "${titleOf(d)}" belongs to system "${system}", which does not exist.`)
    }
  }

  return { nodes: nodes.sort(byRoleThenTitle), edges: edges.result(), looseEnds }
}

/**
 * C4 level 2 for one SoftwareSystem: its Containers and DataStores inside a
 * boundary, and the People and other systems they are linked with around it.
 * Links to the system as a whole attach to the boundary.
 */
export function buildContainerDiagram(data: StoryData, systemName: string): Diagram | undefined {
  const index = new StoryIndex(data)
  const system = index.systemNode(systemName, 'system')
  if (!system) return undefined

  const inside: DiagramNode[] = [...index.containerNodes(systemName), ...index.dataStoreNodes(systemName)]
  const insideIds = new Set(inside.map((n) => n.id))
  const boundary: Boundary = {
    id: system.id,
    name: system.name,
    title: system.title,
    nodeIds: inside.map((n) => n.id),
  }

  const outside = new Map<string, DiagramNode>()
  const looseEnds: string[] = []

  // Where each end of a Relationship shows up in this view.
  const place = (ref: ElementRef): string | undefined => {
    const id = elementId(ref.kind, ref.name)
    if (insideIds.has(id) || id === boundary.id) return id
    if (!index.has(ref)) return undefined
    if (ref.kind === 'Person') return id
    const other = ref.kind === 'SoftwareSystem' ? ref.name : index.owningSystem(ref)
    return other ? elementId('SoftwareSystem', other) : undefined
  }
  const touchesThisSystem = (id: string | undefined) => id !== undefined && (insideIds.has(id) || id === boundary.id)
  const belongsHere = (ref: ElementRef) =>
    (ref.kind === 'SoftwareSystem' && ref.name === systemName) ||
    index.systemOf.get(elementId(ref.kind, ref.name)) === systemName

  const edges = new EdgeCollector()
  for (const rel of sortedRelationships(data)) {
    const { source, target } = rel.spec
    const s = place(source)
    const t = place(target)
    if (!touchesThisSystem(s) && !touchesThisSystem(t)) {
      // Not about this system, unless one end is ours and the other is missing.
      if ((s === undefined && belongsHere(target)) || (t === undefined && belongsHere(source))) {
        const missing = s === undefined ? source : target
        looseEnds.push(
          `The link "${rel.spec.description || rel.metadata.name}" points to ${describeRef(missing)}, which does not exist.`,
        )
      }
      continue
    }
    if (!s || !t) {
      const missing = !s ? source : target
      looseEnds.push(
        `The link "${rel.spec.description || rel.metadata.name}" points to ${describeRef(missing)}, which does not exist.`,
      )
      continue
    }
    // A part linked to its own system, or the system to itself, says nothing here.
    if (s === t || (s === boundary.id && insideIds.has(t)) || (t === boundary.id && insideIds.has(s))) continue

    for (const id of [s, t]) {
      if (touchesThisSystem(id) || outside.has(id)) continue
      const [kind, name] = [id.slice(0, id.indexOf('/')), id.slice(id.indexOf('/') + 1)]
      const node = kind === 'Person' ? index.personNode(name) : index.systemNode(name, 'external-system')
      if (node) outside.set(id, node)
    }
    const implied = !(elementId(source.kind, source.name) === s && elementId(target.kind, target.name) === t)
    edges.add(rel, s, t, implied)
  }

  return {
    nodes: [...inside, ...outside.values()].sort(byRoleThenTitle),
    edges: edges.result(),
    boundary,
    looseEnds,
  }
}

/** Systems in the order the container view offers them: most containers first. */
export function systemsForPicker(data: StoryData): { name: string; title: string; containers: number }[] {
  return data.systems
    .map((s) => ({
      name: s.metadata.name,
      title: titleOf(s),
      containers: data.containers.filter((c) => c.spec.softwareSystemRef?.name === s.metadata.name).length,
    }))
    .sort((a, b) => b.containers - a.containers || a.title.localeCompare(b.title))
}

export interface Link {
  name: string
  direction: 'out' | 'in'
  other: { kind: ElementKind; name: string; title: string; exists: boolean }
  description: string
  /** The Applied condition, for a Container to DataStore link. */
  applied?: Condition
  /** For a system: the part of it the link really starts or ends at. */
  via?: string
}

/**
 * The Relationships from and to one element, for its details panel. For a
 * system, that includes the links of its Containers and DataStores with the
 * world outside it, which the context view draws as the system's own arrows.
 */
export function linksOf(data: StoryData, kind: ElementKind, name: string): Link[] {
  const titles = new Map<string, string>()
  for (const p of data.people) titles.set(elementId('Person', p.metadata.name), titleOf(p))
  for (const s of data.systems) titles.set(elementId('SoftwareSystem', s.metadata.name), titleOf(s))
  for (const c of data.containers) titles.set(elementId('Container', c.metadata.name), titleOf(c))
  for (const d of data.dataStores) titles.set(elementId('DataStore', d.metadata.name), titleOf(d))
  const other = (ref: ElementRef) => {
    const id = elementId(ref.kind, ref.name)
    return { kind: ref.kind, name: ref.name, title: titles.get(id) ?? ref.name, exists: titles.has(id) }
  }
  // A system's parts, by element id.
  const parts = new Set<string>()
  if (kind === 'SoftwareSystem') {
    for (const c of data.containers) {
      if (c.spec.softwareSystemRef?.name === name) parts.add(elementId('Container', c.metadata.name))
    }
    for (const d of data.dataStores) {
      if (d.spec.softwareSystemRef?.name === name) parts.add(elementId('DataStore', d.metadata.name))
    }
  }
  const isSelf = (ref: ElementRef) => ref.kind === kind && ref.name === name
  const isPart = (ref: ElementRef) => parts.has(elementId(ref.kind, ref.name))

  const links: Link[] = []
  for (const rel of sortedRelationships(data)) {
    const { source, target } = rel.spec
    const base = {
      name: rel.metadata.name,
      description: rel.spec.description ?? '',
      applied: findCondition(rel.status?.conditions, 'Applied'),
    }
    // Links within the system (between its parts, or with itself) are not
    // links with anything else.
    const inside = (ref: ElementRef) => isSelf(ref) || isPart(ref)
    if (inside(source) && !inside(target)) {
      links.push({
        ...base,
        direction: 'out',
        other: other(target),
        via: isPart(source) ? other(source).title : undefined,
      })
    } else if (inside(target) && !inside(source)) {
      links.push({
        ...base,
        direction: 'in',
        other: other(source),
        via: isPart(target) ? other(target).title : undefined,
      })
    }
  }
  return links
}

/** Container cards for one system, or for the whole project, for the sessions panel. */
export function containerNodes(data: StoryData, system?: string): ContainerNode[] {
  const index = new StoryIndex(data)
  const systems = system ? [system] : [...new Set(data.containers.map((c) => c.spec.softwareSystemRef?.name))]
  return systems.flatMap((s) => index.containerNodes(s)).sort((a, b) => a.title.localeCompare(b.title))
}
