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

export type ContainerType = 'static-site' | 'service' | 'job' | 'worker'
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
