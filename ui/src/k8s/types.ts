// TypeScript shapes of the c4.factory.io/v1alpha1 resources, as the Kubernetes API
// returns them. They mirror api/v1alpha1/*_types.go; every status field is optional
// because the operator may not have reported yet.

export const GROUP = 'c4.factory.io'
export const VERSION = 'v1alpha1'

export interface ObjectMeta {
  name: string
  namespace?: string
  uid?: string
  resourceVersion?: string
  generation?: number
  creationTimestamp?: string
  deletionTimestamp?: string
  labels?: Record<string, string>
}

export interface ListMeta {
  resourceVersion?: string
  continue?: string
}

export interface K8sObject {
  apiVersion?: string
  kind?: string
  metadata: ObjectMeta
}

export interface K8sList<T> {
  apiVersion?: string
  kind?: string
  metadata: ListMeta
  items: T[]
}

/** metav1.Status, returned for errors and in ERROR watch events. */
export interface K8sStatus {
  kind: 'Status'
  status?: 'Success' | 'Failure'
  message?: string
  reason?: string
  code?: number
}

export interface Condition {
  type: string
  status: 'True' | 'False' | 'Unknown'
  reason?: string
  message?: string
  lastTransitionTime?: string
  observedGeneration?: number
}

export interface LocalRef {
  name: string
}

export type ElementKind = 'Person' | 'SoftwareSystem' | 'Container' | 'DataStore'

export interface ElementRef {
  kind: ElementKind
  name: string
}

export type ContainerType = 'static-site' | 'service' | 'job' | 'worker' | 'mobile-app' | 'cli' | 'extension'
export type ContainerPhase = 'Pending' | 'Scaffolding' | 'Ready' | 'Failed'
export type SessionPhase = 'Active' | 'Stale'
export type Actor = 'agent' | 'human'

export interface Project extends K8sObject {
  spec?: { displayName?: string; description?: string }
  status?: {
    softwareSystems?: number
    containers?: number
    readyContainers?: number
    activeSessions?: number
    summary?: string
    conditions?: Condition[]
  }
}

export interface Person extends K8sObject {
  spec?: { displayName?: string; description?: string }
}

export interface SoftwareSystem extends K8sObject {
  spec?: {
    displayName?: string
    description?: string
    needs?: string[]
    kpis?: string[]
  }
  status?: {
    containers?: number
    readyContainers?: number
    dataStores?: number
    activeSessions?: number
    summary?: string
    conditions?: Condition[]
  }
}

export interface Container extends K8sObject {
  spec: {
    softwareSystemRef: LocalRef
    displayName?: string
    description?: string
    type: ContainerType
    gitProviderRef: LocalRef
    frameworkRef: LocalRef
    standardRef?: LocalRef
    computeRef?: LocalRef
    deploymentRef?: LocalRef
  }
  status?: {
    phase?: ContainerPhase
    repositoryURL?: string
    scaffoldedFrom?: string[]
    appliedRelationships?: string[]
    appliedConnectors?: { relationship: string; dataStore: string; connector: string }[]
    activeSessions?: number
    observedGeneration?: number
    conditions?: Condition[]
  }
}

export interface DataStore extends K8sObject {
  spec: {
    softwareSystemRef: LocalRef
    displayName?: string
    description?: string
    connectorRef: LocalRef
  }
  status?: { conditions?: Condition[] }
}

export interface Relationship extends K8sObject {
  spec: {
    source: ElementRef
    target: ElementRef
    description?: string
  }
  status?: { conditions?: Condition[] }
}

export interface Session extends K8sObject {
  spec: {
    containerRef: LocalRef
    actor: Actor
    summary?: string
  }
  status?: {
    phase?: SessionPhase
    lastHeartbeat?: string
    openInEditor?: string
  }
}

/** Framework, Compute, Deployment and Connector share one spec. */
export interface CatalogEntry extends K8sObject {
  spec: {
    displayName?: string
    description?: string
    supportedTypes: ContainerType[]
    template: { url: string; ref: string }
  }
}

/** Plural resource names, as used in API paths. */
export const RESOURCES = {
  projects: 'projects',
  people: 'people',
  softwareSystems: 'softwaresystems',
  containers: 'containers',
  dataStores: 'datastores',
  relationships: 'relationships',
  sessions: 'sessions',
  frameworks: 'frameworks',
  connectors: 'connectors',
  tasks: 'tasks',
  agents: 'agents',
  crews: 'crews',
} as const

/** API path of a c4.factory.io resource, in one namespace or across all of them. */
export function resourcePath(plural: string, namespace?: string): string {
  const ns = namespace ? `/namespaces/${encodeURIComponent(namespace)}` : ''
  return `/apis/${GROUP}/${VERSION}${ns}/${plural}`
}

/** The condition of the given type, if the object has reported it. */
export function findCondition(conditions: Condition[] | undefined, type: string): Condition | undefined {
  return conditions?.find((c) => c.type === type)
}

export type TaskPhase = 'Pending' | 'Implementing' | 'Checking' | 'Reviewing' | 'Blocked' | 'Merged' | 'Failed'

/** One step in a Task's timeline: from `at` on, the Task was in phase and round, for reason. */
export interface TaskTransition {
  at: string
  phase: TaskPhase
  round?: number
  reason?: string
  message?: string
}

export interface CheckStatus {
  commit?: string
  /** pending, success, failure or error. */
  state?: string
  url?: string
}

/** Labels on the Tasks a Crew starts: the Crew's name, and the issue number. */
export const LABEL_CREW = 'c4.factory.io/crew'
export const LABEL_ISSUE = 'c4.factory.io/issue'

export interface Task extends K8sObject {
  spec: {
    containerRef: LocalRef
    /** Implements the goal, and reviews it when there is no reviewerRef. */
    agentRef: LocalRef
    reviewerRef?: LocalRef
    /** A Task has either a goal or an issue of its Container's repository. */
    goal?: string
    issue?: number
    /** The protected paths a gated Task may change. */
    allowedProtectedPaths?: string[]
  }
  status?: {
    phase?: TaskPhase
    round?: number
    reviewAttempts?: number
    /** spec.issue's title when the Task last read it, and where the issue is. */
    issueTitle?: string
    issueURL?: string
    /** The issues filed from the reviewer's suggestions on merge, as URLs. */
    followUps?: string[]
    /** The Standard change the Task waits for while Blocked. */
    standardChange?: {
      issue: number
      issueURL?: string
      paths: string[]
      branch?: string
      count: number
      resumedRound?: number
    }
    branch?: string
    commit?: string
    pullRequest?: number
    pullRequestURL?: string
    checks?: CheckStatus
    review?: { verdict?: string; summary?: string }
    summary?: string
    feedback?: string
    sandbox?: string
    sandboxPod?: string
    /** When an unavailable agent is tried again; Progressing's reason is then AgentUnavailable. */
    agentBackAt?: string
    slotWaitSince?: string
    phaseChangedAt?: string
    completedAt?: string
    history?: TaskTransition[]
    conditions?: Condition[]
  }
}

export type AgentHarness = 'claude-code' | 'stand-in'

export interface Agent extends K8sObject {
  spec: {
    displayName?: string
    description?: string
    image: string
    /** The agent program; the API server defaults it to claude-code. */
    harness?: AgentHarness
    /** Such as claude-sonnet-5-5; empty means the harness's default. */
    model?: string
    maxRounds?: number
    timeoutMinutes?: number
  }
}

export type CrewPhase = 'Waiting' | 'Idle' | 'Working' | 'Paused'

/** Who works on a Container's issues: up to spec.parallel Tasks at a time, lowest ready issue first. */
export interface Crew extends K8sObject {
  spec: {
    containerRef: LocalRef
    displayName?: string
    description?: string
    implementer: LocalRef
    reviewer: LocalRef
    /** Stops picking up new issues; a Task already started runs to its end. */
    paused?: boolean
    /** How many Tasks run at once; 1 when unset. */
    parallel?: number
  }
  status?: {
    phase?: CrewPhase
    /** Names of the Crew's running Tasks, in its namespace. */
    activeTasks?: string[]
    /** Ready issues the Crew can start, in pick-up order. */
    ready?: number[]
    /** Ready issues blocked by open dependencies. */
    waiting?: number[]
    lastPolled?: string
    observedGeneration?: number
    /** Ready: whether the Crew can pick up issues, and why not. */
    conditions?: Condition[]
  }
}
