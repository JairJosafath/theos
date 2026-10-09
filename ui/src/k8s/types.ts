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

export type ContainerType =
  'static-site' | 'service' | 'job' | 'worker' | 'mobile-app' | 'cli' | 'extension' | 'operator'
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
    /** The operator that last reconciled the Project, and the chart it was installed with. */
    factory?: { operatorVersion?: string; chartVersion?: string }
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
    /** The latest CI result on the default branch; only Containers with a Standard have it. */
    checks?: CheckStatus
    /** Which ref of its Standard the repository's Standard files are at, and the update in flight. */
    standard?: {
      name: string
      ref: string
      /** The files the last update left out, because the repository had changed them. */
      leftOut?: string[]
      /** The Standard update in flight, or the last one, stopped. */
      update?: {
        ref: string
        branch?: string
        commit?: string
        pullRequest?: number
        pullRequestURL?: string
        paths?: string[]
        leftOut?: string[]
        /** Why the update stopped without merging, such as UpdateClosed. */
        stopped?: string
      }
    }
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
  briefs: 'briefs',
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
    /** Implement (the default), or Plan: a planning round of a Brief. */
    kind?: 'Implement' | 'Plan'
    /** Every Task but a Plan Task names its Container. */
    containerRef?: LocalRef
    /** A Plan Task names its Brief in place of a Container. */
    briefRef?: LocalRef
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
    /** What the Task's agent runs cost over all rounds, in thousandths of a US dollar. */
    costMilliUSD?: number
    /** The Task's agent runs whose agent didn't report their cost. */
    runsWithoutCost?: number
    /** What the Task's agent runs used, in tokens, over all rounds. */
    tokens?: TokenUsage
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
    /** How large, in tokens, one agent run may grow before it hands off; the API server fills in its default. */
    sessionTokenLimit?: number
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
    /** How many Tasks run at once; the API server fills in its default. */
    parallel?: number
    /** Lets the Crew tag a release once its milestone is done; off, a person tags it. */
    tagReleases?: boolean
    /** Lets the Crew file each release's prepare issue. */
    prepareReleases?: boolean
    /** Copies each release the Crew tags to another repository once its runs succeed. */
    publishReleases?: { github: { repository: string; tokenSecretRef: LocalRef }; tagPrefix?: string }
  }
  status?: {
    phase?: CrewPhase
    /** Names of the Crew's running Tasks, in its namespace. */
    activeTasks?: string[]
    /** Ready issues the Crew can start, in pick-up order. */
    ready?: number[]
    /** Ready issues blocked by open dependencies. */
    waiting?: number[]
    /** The release the Crew tagged last, and how its runs and publishing went. */
    lastRelease?: CrewRelease
    /** The c4.factory.io/rerun-release value the Crew answered last. */
    rerunRelease?: string
    lastPolled?: string
    observedGeneration?: number
    /** Ready: whether the Crew can pick up issues, and why not; Released: whether its next release went out, and why not. */
    conditions?: Condition[]
  }
}

/** A release a Crew tagged. */
export interface CrewRelease {
  tag: string
  commit: string
  time: string
  /** How the CI runs the tag started went. */
  run?: 'Waiting' | 'Running' | 'Succeeded' | 'Failed'
  /** The failed run, else the newest one. */
  runURL?: string
  /** When the operator last re-ran the release's failed runs. */
  rerunTime?: string
  /** The bump issues filed in the Containers that bundle this one. */
  bumps?: { container: string; issueURL?: string }[]
  publish?: { state: 'Pending' | 'Publishing' | 'Published' | 'Failed'; url?: string; message?: string }
  goModule?: { state: 'Published' | 'NotGo' | 'Invalid' | 'Failed'; path?: string; version?: string; message?: string }
}

export type BriefPhase = 'Pending' | 'Planning' | 'Applying' | 'Planned' | 'NeedsInput' | 'Failed'

/** A Container the Brief made, or one spec.releases names, and how its releases go: copied from its Crew. */
export interface BriefApp {
  container: string
  /** The Container's Crew; empty while it has none. */
  crew?: string
  /** On when the Crew tags releases. */
  releases: 'On' | 'Off'
  /** How many issues the Crew works on at once; 0 or unset while it has no Crew. */
  parallel?: number
  /** The GitHub repository, owner/name, the Crew publishes its releases to; empty when it publishes none. */
  publishTo?: string
  /** The Crew's latest release. */
  lastRelease?: {
    tag: string
    /** The release's page on the Git host. */
    url?: string
    time?: string
    /** How the tag's CI runs went. */
    run?: 'Waiting' | 'Running' | 'Succeeded' | 'Failed'
    /** How publishing it went; empty when the Crew publishes none. */
    publishState?: 'Pending' | 'Publishing' | 'Published' | 'Failed'
    /** The published release. */
    publishedURL?: string
  }
  /** One plain sentence on how the app's releases go. */
  message?: string
}

/** Why an issue was handed back: no acceptance criteria, merged already, tried as often as a Crew does, its last Task failed, or a person did. */
export type BriefFailedKind = 'unclear' | 'merged' | 'exhausted' | 'failed' | 'person' | 'byHand'

/** An issue handed back to a person in the repository of a Container the Brief made, and why. */
export interface BriefFailedIssue {
  container: string
  number: number
  title?: string
  url?: string
  kind: BriefFailedKind
  /** Why, in a sentence, such as the failed Task's message. */
  reason?: string
  /** How many Tasks the issue has had. */
  tries?: number
}

/** Whether a Brief tends its handed-back work by itself: Off, Ask (decides and asks you) or On (applies). */
export type BriefAutopilot = 'Off' | 'Ask' | 'On'

/** What a Brief's item is: a failed issue, or a Task, Crew or Container it made. */
export type BriefItemKind = 'Issue' | 'Task' | 'Crew' | 'Container'

/** An item the Brief leaves alone for now, as it may heal by itself, and when it acts if it hasn't. */
export interface BriefHealing {
  kind: BriefItemKind
  /** The item's name; container#number for an issue. */
  name: string
  reason?: string
  /** When the Brief acts if it hasn't healed; unset when it acts only once the item changes. */
  actsAt?: string
}

/** An item only the person can decide: the question, what the Brief would do and why. */
export interface BriefNeedsYou {
  kind: BriefItemKind
  /** The item's name; container#number for an issue. */
  name: string
  question: string
  recommend?: string
  why?: string
  since?: string
}

/** A failed issue a round answered, as the Brief remembers it. */
export interface BriefTendedIssue {
  container: string
  number: number
  answer: 'retry' | 'close' | 'wait' | 'ask'
  why?: string
  recommend?: { answer: 'retry' | 'close' | 'wait'; why?: string; clarify?: string }
  tries?: number
  at: string
  round: number
}

/** An app idea in plain words, steered by follow-up prompts; a planner Agent turns it into the model, round by round. */
export interface Brief extends K8sObject {
  spec: {
    /** What the app is for and who uses it; it can't change. */
    idea: string
    /** Follow-ups, oldest first; only ever appended. */
    prompts?: string[]
    agents?: { planner?: LocalRef; implementer?: LocalRef; reviewer?: LocalRef; designer?: LocalRef }
    gitProviderRef?: LocalRef
    /** Whether it tends its handed-back work by itself; unset is the operator's default. */
    autopilot?: BriefAutopilot
    /** How many issues each Crew the Brief made works on at once; unset is the operator's default. */
    parallel?: number
    /** Pauses the Brief and every Crew it made; a Task already started runs to its end. The Paused condition says what still runs. */
    paused?: boolean
  }
  status?: {
    phase?: BriefPhase
    /** The mode the Brief runs in, set every reconcile: spec.autopilot, or the operator's default when that is unset. */
    autopilot?: BriefAutopilot
    /** The round running now or last run; 0 before the first. */
    round?: number
    /** How many of spec.prompts the running or last round took. */
    roundPrompts?: number
    /** How many of spec.prompts the last applied round took; the rest wait for the next round. */
    promptsPlanned?: number
    /** What the planner chose in the last round, in plain words. */
    choices?: string[]
    /** The objects the Brief made, over all rounds. */
    created?: { kind: string; name: string }[]
    /** The issues the Brief filed, over all rounds. */
    issues?: { repository: string; number: number; url?: string; title?: string; round?: number; n?: number }[]
    /** Reviewers' suggestions waiting in the repositories of the Containers the Brief made: at most 50, by container then number. */
    suggestions?: { container: string; number: number; title?: string; url?: string; criteria?: boolean }[]
    /** How many reviewer suggestions wait in all; can exceed the listed ones. */
    suggestionsWaiting?: number
    /** Issues handed back to a person (status/needs-human) in the repositories of the Containers the Brief made: at most 50, by container then number. */
    failed?: BriefFailedIssue[]
    /** How many failed issues wait for an answer in all; can exceed the listed ones. */
    failedWaiting?: number
    /** What the Brief made that it leaves alone for now, and when it acts; empty while autopilot is Off. */
    attention?: BriefHealing[]
    /** What only the person can decide, oldest first, at most 50; empty while autopilot is Off. */
    needsYou?: BriefNeedsYou[]
    /** How many items need the person in all; can exceed the listed ones. */
    needsYouWaiting?: number
    /** The failed issues rounds answered, at most 50. */
    tended?: BriefTendedIssue[]
    /** The last tending round: a round with no new prompt that decides the failed issues. */
    tendRound?: number
    lastTendTime?: string
    /** A line per Container the Brief made, then one per spec.releases entry naming none it made. */
    apps?: BriefApp[]
    /** One entry per round, oldest first. */
    history?: {
      round: number
      summary?: string
      choices?: string[]
      costMilliUSD?: number
      /** The round's planner runs whose agent didn't report their cost. */
      runsWithoutCost?: number
      /** What the round's planner runs used, in tokens. */
      tokens?: TokenUsage
      /** Applied, PlannerNeedsInput or RoundFailed; Tended for a tending round that applied. */
      outcome?: 'Applied' | 'PlannerNeedsInput' | 'RoundFailed' | 'Tended'
      /** The planner's question, when the round asked for input. */
      needsInput?: string
    }[]
    /** The Brief's own repository, where every round's plan is kept. */
    repositoryURL?: string
    /** What planning cost over all rounds, in thousandths of a US dollar. */
    costMilliUSD?: number
    /** The planner runs, over all rounds, whose agent didn't report their cost. */
    runsWithoutCost?: number
    /** What the planner runs used, in tokens, over all rounds. */
    tokens?: TokenUsage
    observedGeneration?: number
    /** Planned, and NeedsInput: while True, its message says what the Brief waits for. */
    conditions?: Condition[]
  }
}

/** What agent runs used, in tokens, as the agents reported it; every field is omitted when zero. */
export interface TokenUsage {
  /** Prompt tokens neither read from nor written to the cache. */
  input?: number
  /** Tokens the agents wrote. */
  output?: number
  /** Prompt tokens read from the cache. */
  cacheRead?: number
  /** Prompt tokens written to the cache. */
  cacheWrite?: number
  /** How large the largest agent run got: the prompt of its biggest turn. */
  largestSession?: number
  /** The agent runs whose agent didn't report their tokens; the other fields leave them out. */
  runsWithout?: number
  /** The agent runs that handed their work to a fresh one. */
  sessionHandoffs?: number
}
