// TypeScript shapes of theos.factory.io/v1beta1 resources, as the Kubernetes API
// returns them. They mirror api/v1beta1/*_types.go; every status field is optional
// because the operator may not have reported yet.

export const GROUP = 'theos.factory.io'
export const VERSION = 'v1beta1'

export interface ObjectMeta {
  name: string
  namespace?: string
  uid?: string
  resourceVersion?: string
  generation?: number
  creationTimestamp?: string
  deletionTimestamp?: string
  labels?: Record<string, string>
  annotations?: Record<string, string>
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
  'static-site' | 'service' | 'job' | 'worker' | 'mobile-app' | 'cli' | 'extension' | 'operator' | 'function'
export type ContainerPhase = 'Pending' | 'Scaffolding' | 'Ready' | 'Failed'
export type SessionPhase = 'Active' | 'Stale'
export type Actor = 'agent' | 'human'

/** The SPDX id of a license the scaffold can write; the schema's enum (api/v1beta1/shared_types.go). */
export type License =
  | 'MIT'
  | 'Apache-2.0'
  | 'BSD-2-Clause'
  | 'BSD-3-Clause'
  | 'ISC'
  | '0BSD'
  | 'MPL-2.0'
  | 'GPL-2.0-only'
  | 'GPL-3.0-only'
  | 'LGPL-3.0-only'
  | 'AGPL-3.0-only'
  | 'Unlicense'

export interface Project extends K8sObject {
  spec?: {
    displayName?: string
    description?: string
    /** The license its Containers are scaffolded with, unless their own spec.license says otherwise. */
    license?: License
  }
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
    /** Internal (the default): the factory builds its Containers. External: the story only names it. */
    location?: 'Internal' | 'External'
    displayName?: string
    description?: string
    needs?: string[]
    kpis?: string[]
    /** The Domains it is about, rendered into each of its Containers at their scaffold. */
    domainRefs?: string[]
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

/** When a Container's CI runs its full check (design.md, Crews and releases, "The full check"). */
export type ChecksMode = 'EveryChange' | 'BeforeRelease'

/** One of a Standard's files in a Container's repository, at a ref of the Standard. */
export interface StandardFile {
  path: string
  ref: string
  pullRequestURL?: string
  /**
   * For a file behind, what the Standard changed in it from ref to the
   * Standard's ref, as a unified diff, and the repository's lines a take would
   * drop; cut at 4 KiB, and only the first 16 files behind carry it.
   */
  change?: string
}

/** A Container's operations: what its deploy target's backups, recovery and alarms are rendered with. */
export interface Operations {
  backups?: {
    /** Days of backups kept; 0 is point-in-time recovery only. */
    retentionDays?: number
    /** An AWS region a copy goes to; empty is none. */
    copyToRegion?: string
  }
  recovery?: {
    /** How much data may be lost, such as 1h. */
    rpo?: string
    /** How long a recovery may take, such as 4h. */
    rto?: string
  }
  alerts?: {
    /** Who the alarms tell; empty is nobody. */
    email?: string
  }
}

export interface Container extends K8sObject {
  spec: {
    softwareSystemRef: LocalRef
    displayName?: string
    description?: string
    type: ContainerType
    gitProviderRef: LocalRef
    /** Picks the four sides below instead; the API server takes one of bundleRef and frameworkRef. */
    bundleRef?: LocalRef
    frameworkRef?: LocalRef
    standardRef?: LocalRef
    computeRef?: LocalRef
    deploymentRef?: LocalRef
    /** When CI runs the full check; the API server defaults it to EveryChange. */
    checks?: ChecksMode
    /** It takes over a repository that already has commits; nothing is scaffolded, and it needs nothing from the catalog. */
    adopt?: boolean
    /** The check of a repository adopted with no Standard: what the agent's workspace runs; make check when unset. */
    checkCommand?: string
    /** The status checks main requires on a repository adopted with no Standard, as patterns; check* when unset. */
    checkContexts?: string[]
    /** Backups, recovery and alerts; any field left out takes its default. */
    operations?: Operations
    /** The DeployTargets each release ships to, in order (docs/plans/done/deploy.md). */
    deploy?: ContainerDeploy[]
    /** The license it is scaffolded with, over the Project's; read at the scaffold only (docs/plans/license.md). */
    license?: License
  }
  status?: {
    phase?: ContainerPhase
    /** The operations infra/operations.ts was last rendered with, every field filled in. */
    operations?: Operations
    /**
     * The pull request that renders infra/operations.ts again from a changed spec.operations: in
     * flight, stopped, or merged (it stays, naming its commit, until the next change).
     */
    operationsUpdate?: {
      /** The block it renders, every field filled in. */
      operations: Operations
      branch?: string
      commit?: string
      pullRequest?: number
      pullRequestURL?: string
      /** Why it stopped without merging, such as UpdateClosed. */
      stopped?: string
      /** The commit on main that holds the block, once merged. */
      mergeCommit?: string
    }
    repositoryURL?: string
    scaffoldedFrom?: string[]
    /** The Domains its system named when it was scaffolded, by name; their refs are in scaffoldedFrom. */
    scaffoldedDomains?: string[]
    /** What spec.bundleRef expanded to: the entries' names. */
    bundle?: { name: string; framework: string; standard?: string; compute?: string; deployment?: string }
    appliedRelationships?: string[]
    appliedConnectors?: { relationship: string; dataStore: string; connector: string }[]
    /** The latest CI result on the default branch; only Containers with a Standard have it. */
    checks?: CheckStatus
    /** Which ref of its Standard the repository's Standard files are at, and the update in flight. */
    standard?: {
      name: string
      ref: string
      /** When the repository's CI runs the full check, as its Standard files were last rendered; empty is EveryChange. */
      checks?: ChecksMode
      /** The paths of behind. */
      leftOut?: string[]
      /**
       * The Standard's files its updates left out, because the repository changed them in lines the
       * Standard changed too, each with the ref its copy was last brought to and the pull request that
       * last left it out. They wait on a person (StandardUpdated False, FilesBehind).
       */
      behind?: StandardFile[]
      /** The Standard's files a person kept as the repository has them, with the ref kept at. */
      kept?: StandardFile[]
      /** The Standard update in flight, or the last one, stopped. */
      update?: {
        ref: string
        /** The pull request takes the Standard's version at ref of the files behind, as a person answered. */
        take?: boolean
        branch?: string
        commit?: string
        pullRequest?: number
        pullRequestURL?: string
        paths?: string[]
        merged?: string[]
        leftOut?: string[]
        /** Why the update stopped without merging, such as UpdateClosed. */
        stopped?: string
      }
    }
    /** The newest release whose runs succeeded: the version an unpinned spec.deploy entry follows. */
    newestRelease?: string
    /** What runs at each of spec.deploy's targets, in its order. */
    deploys?: ContainerDeployStatus[]
    activeSessions?: number
    observedGeneration?: number
    conditions?: Condition[]
  }
}

/** One place a Container's releases ship to. */
export interface ContainerDeploy {
  /** The DeployTarget, in the Container's namespace. */
  target: string
  /** An earlier entry's target: a version ships here only once that entry runs it. */
  after?: string
  /** A pinned release tag; empty follows the newest release. */
  version?: string
  /** The version a person let in, where the target needs approval. */
  approved?: string
}

export type DeployPhase = 'Waiting' | 'Deploying' | 'Deployed' | 'Failed'
export type DeployHealth = 'Healthy' | 'Progressing' | 'Degraded' | 'Unknown'

/** What runs at one of a Container's targets. */
export interface ContainerDeployStatus {
  target: string
  /** The version running there, as its tool last reported. */
  version?: string
  /** The version the entry should run: its pin, else the newest release. */
  wantedVersion?: string
  phase: DeployPhase
  /** What the target's tool reports of what runs there. */
  health?: DeployHealth
  /** Where the app, or its release, answers, when the tool knows it. */
  url?: string
  reason?: string
  message?: string
  /** When the phase, reason or wanted version last changed. */
  since?: string
  /** The release's image at the target, host/repository@digest, once copied there (a github target's GHCR, a gitlab target's project registry). */
  image?: string
  /** The bug the operator filed for a degraded or failed deploy of a version, in the Container's repository. */
  issue?: ContainerDeployIssue
}

/** A bug the monitor filed for a deploy entry (api's ContainerDeployIssue). */
export interface ContainerDeployIssue {
  number: number
  version: string
  state: 'Degraded' | 'Failed'
  url?: string
}

/**
 * A place Containers are shipped to: exactly one member says which tool owns
 * it. Its Secret is named, never read: the UI's proxy serves only
 * theos.factory.io, so no Secret data reaches the browser.
 */
export interface DeployTarget extends K8sObject {
  spec: {
    /**
     * Ships each release as a GitHub release of a repository: owner/name, on
     * github.com or, when url is set (https://host, a GitHub Enterprise
     * Server), on that server, whose registry is containers.<host>;
     * tagPrefix before each tag (unset: the Container's name and "-");
     * latest (default true) whether each release becomes Latest; source, set,
     * commits the app's files at the tag into path (unset: the Container's
     * name) under its own tagPrefix (unset: the path and "/").
     */
    github?: {
      url?: string
      repository: string
      tagPrefix?: string
      latest?: boolean
      source?: { path?: string; tagPrefix?: string }
    }
    /**
     * Ships each release as a GitLab release of a project: its full path
     * (group/sub/project), on gitlab.com or, when url is set
     * (https://host[:port], a path allowed for an instance under a relative
     * URL root), on that instance; tagPrefix and source as github's, the
     * source committed through the Commits API and the release made of that
     * commit; the files go to the project's generic package registry and the
     * image to its container registry. No latest: GitLab's latest release is
     * the newest (docs/plans/gitlab-deploy.md, decision 1).
     */
    gitlab?: {
      url?: string
      project: string
      tagPrefix?: string
      source?: { path?: string; tagPrefix?: string }
    }
    /**
     * Ships each release as one Argo CD Application, in the operator's
     * cluster: namespace (default argocd) and project (default default) are
     * Argo CD's; destination is where the apps run (server defaults to the
     * cluster Argo CD runs in).
     */
    argoCD?: { namespace?: string; project?: string; destination: { namespace: string; server?: string } }
    /**
     * Ships each release as CloudFormation stacks, through the CDK bootstrap's
     * roles, in an AWS region; account, when set, is the twelve-digit account
     * the credential must belong to.
     */
    cloudFormation?: { region: string; account?: string }
    /** Optional only for argoCD, which uses the operator's own ServiceAccount. */
    credentialsSecretRef?: LocalRef
    /** development (the default) or production. */
    tier?: 'development' | 'production'
    /** Required or None; empty is the tier's default. */
    approval?: 'Required' | 'None'
    /** Deploy-time values that differ per place and are not secret. */
    parameters?: Record<string, string>
  }
  status?: {
    /** Ready: whether the target accepts the credential (for argoCD, whether Argo CD and its project are there). */
    conditions?: Condition[]
    /** When the operator last asked the target whether it accepts the credential. */
    checkedTime?: string
  }
}

/** A Kubernetes toleration, as a test environment's local member lists it. */
export interface Toleration {
  key?: string
  operator?: 'Exists' | 'Equal'
  value?: string
  effect?: string
  tolerationSeconds?: number
}

/** One live lease of a test environment, as its provider reports it. */
export interface TestEnvironmentLease {
  /** The provider's name for it: for local, the pod's; for aws, the machine's lease tag. */
  id: string
  /** The Task whose run holds it, or, for a remote machine that runs a CI job, ci/<GitProvider>/<runner id>. */
  holder?: string
  state?: 'Pending' | 'Ready' | 'Failed' | 'Ended'
  since?: string
  /** Why a lease is Pending or Failed. */
  message?: string
  /** What it cost so far, in thousandths of a US dollar; 0 for local. */
  costMilliUSD?: number
}

/**
 * A test package run a deviceFarm environment's lease asked for, over its Git
 * host, with theos-env test (docs/plans/done/test-environments.md, step 10).
 */
export interface TestEnvironmentTestRun {
  /** The run's ID on the provider: its lease and its request. */
  id: string
  /** The lease whose branch the request is on. */
  lease: string
  /** The Task whose run holds the lease. */
  holder?: string
  /** The Container on whose repository the lease's branch is. */
  container?: string
  /** The request commit on the lease's branch, and the run's commit the app and tests were built from. */
  request: string
  commit?: string
  /** The test package's type, such as instrumentation or xctest. */
  type?: string
  /** Pending or Running on the provider, Reported once its result is on the branch, Rejected when nothing ran. */
  state?: 'Pending' | 'Running' | 'Reported' | 'Rejected'
  /** Once there is one: Passed, Warned, Failed, Skipped, Errored, Stopped or Rejected. */
  result?: 'Passed' | 'Warned' | 'Failed' | 'Skipped' | 'Errored' | 'Stopped' | 'Rejected'
  /** The result's one line, such as "passed: 12 passed of 12 tests on 2 devices". */
  outcome?: string
  /** The run on the provider's console. */
  url?: string
  /** What it cost, in thousandths of a US dollar. */
  costMilliUSD?: number
  since?: string
  /** The operator's result commit on the lease's branch. */
  answer?: string
  /** The pull request comment still to post once the holder has a pull request; empty once posted. */
  comment?: string
}

/**
 * A named set of tools the agents' runs and CI lease, such as a browser, an
 * Android emulator or a game engine on a GPU (docs/plans/done/test-environments.md):
 * exactly one provider member, its limits and its CI.
 */
export interface TestEnvironment extends K8sObject {
  spec: {
    /** One line for people and the agents that choose it. */
    description?: string
    /** A pod per lease on the cluster, from image, fenced like an agent sandbox. */
    local?: {
      image: string
      resources?: { requests?: Record<string, string>; limits?: Record<string, string> }
      /** Extended resources the cluster's device plugins name, such as devices.kubevirt.io/kvm. */
      devices?: Record<string, string>
      runtimeClassName?: string
      nodeSelector?: Record<string, string>
      tolerations?: Toleration[]
    }
    /**
     * An EC2 machine per lease in the user's AWS account, from imageId in
     * subnetId; an EC2 Mac (a mac* instanceType) on a dedicated host the
     * operator keeps for mac.keepHostHours (default 24). Only the operator
     * reads the Secret credentialsSecretRef names.
     */
    aws?: {
      region: string
      /** The twelve-digit account the credential must be in. */
      account?: string
      instanceType: string
      mac?: { keepHostHours?: number }
      imageId: string
      subnetId: string
      credentialsSecretRef: LocalRef
    }
    /** AWS Device Farm's real phones, which run the test packages a lease's run asks for. */
    deviceFarm?: {
      projectArn: string
      devicePoolArn: string
      account?: string
      credentialsSecretRef: LocalRef
    }
    /**
     * maxLeases (default 2) at once, each lasting maxLeaseMinutes (default
     * 180) at most, costing budgetUSDPerDay a UTC day at most (required for a
     * remote member).
     */
    limits?: { maxLeases?: number; maxLeaseMinutes?: number; budgetUSDPerDay?: number }
    /**
     * A local member's pool of size (default 0) CI runner pods for jobs that
     * run on env-<name>; an aws member's {} leases a machine for each such job.
     */
    ci?: { size?: number; runnerImage?: string }
  }
  status?: {
    leases?: TestEnvironmentLease[]
    /** What the leases cost on day (UTC, such as 2026-11-06), the ended ones' included. */
    spentToday?: { day: string; milliUSD?: number }
    /** A deviceFarm environment's test runs, the last ones of each lease. */
    testRuns?: TestEnvironmentTestRun[]
    /**
     * Ready: a local member's image is pinned and the project's fence for its
     * pods exists, a remote member's credential is valid; CredentialsValid,
     * for a remote member; CI, for an aws member with a ci block.
     */
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

/** Framework, Compute, Deployment and Connector share one spec; a Framework adds deploysItself. */
export interface CatalogEntry extends K8sObject {
  spec: {
    displayName?: string
    description?: string
    supportedTypes: ContainerType[]
    template: { url: string; ref: string; path?: string }
    /** A Framework only: its template is its Container's deployment, so it takes no Compute or Deployment. */
    deploysItself?: boolean
    /** A Framework only: its template renders infra/operations.ts from a Container's spec.operations. */
    readsOperations?: boolean
  }
}

/** A Connector: a catalog entry that may name the Frameworks whose templates wire its files. */
export interface Connector extends K8sObject {
  spec: CatalogEntry['spec'] & {
    /** The Frameworks, by name, that wire it; a Container on any other is refused. Unset means no restriction. */
    wiredBy?: string[]
  }
}

/** A Standard: a catalog entry with the coding rules and checks of a Container. */
export interface Standard extends K8sObject {
  spec: CatalogEntry['spec'] & {
    /** The Frameworks, by name, whose code it can check. */
    supportedFrameworks: string[]
    /** Files agents must not change: a path, or a directory ending in "/". */
    protectedPaths?: string[]
  }
}

/**
 * A Bundle: the four catalog entries a team picked together, which a Container
 * names instead of the four (docs/plans/done/catalog-bundles.md). It has no status:
 * what it supports is computed from its entries (model/choices.ts).
 */
export interface Bundle extends K8sObject {
  spec: {
    displayName?: string
    description?: string
    frameworkRef: LocalRef
    standardRef?: LocalRef
    computeRef?: LocalRef
    deploymentRef?: LocalRef
  }
}

/**
 * A Domain: what a system is about, a template of Markdown rendered into each
 * of the system's Containers once, at their scaffold, under
 * docs/agents/domain/<name>/ (docs/plans/domains.md). It supports every
 * Container type, so it has no supportedTypes, and it has no status.
 */
export interface Domain extends K8sObject {
  spec: {
    displayName?: string
    description?: string
    template: { url: string; ref: string; path?: string }
  }
}

/**
 * Where repositories are created. Its Secret is named, never read: the UI's
 * proxy serves only theos.factory.io, so no Secret data reaches the browser.
 */
export interface GitProvider extends K8sObject {
  spec: {
    provider?: string
    url: string
    owner: string
    credentialsSecretRef: LocalRef
  }
}

/** What a Chat's room holds (docs/design.md, Chat), in the order the operator makes them. */
export const CHAT_PURPOSES = ['updates', 'general', 'triage', 'specs', 'reviews', 'alerts'] as const
export type ChatPurpose = (typeof CHAT_PURPOSES)[number]

/** A project's Space on a Matrix homeserver, where people follow and steer the factory: one per namespace. */
export interface Chat extends K8sObject {
  spec: {
    homeserver?: string
    credentialsSecretRef: LocalRef
    space?: string
    aliasPrefix?: string
    rooms?: Partial<Record<ChatPurpose, string>>
    digestMinutes?: number
    people?: { matrixID: string; user: string; groups?: string[] }[]
  }
  status?: {
    homeserver?: string
    bot?: string
    space?: { id: string; alias?: string }
    rooms?: { purpose: ChatPurpose; id: string; alias?: string }[]
    invited?: string[]
    conditions?: Condition[]
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
  standards: 'standards',
  computes: 'computes',
  deployments: 'deployments',
  bundles: 'bundles',
  domains: 'domains',
  gitProviders: 'gitproviders',
  deployTargets: 'deploytargets',
  tasks: 'tasks',
  agents: 'agents',
  crews: 'crews',
  briefs: 'briefs',
  chats: 'chats',
  benchmarks: 'benchmarks',
  testEnvironments: 'testenvironments',
} as const

/** API path of a theos.factory.io resource, in one namespace or across all of them. */
export function resourcePath(plural: string, namespace?: string): string {
  const ns = namespace ? `/namespaces/${encodeURIComponent(namespace)}` : ''
  return `/apis/${GROUP}/${VERSION}${ns}/${plural}`
}

/** The condition of the given type, if the object has reported it. */
export function findCondition(conditions: Condition[] | undefined, type: string): Condition | undefined {
  return conditions?.find((c) => c.type === type)
}

export type TaskPhase =
  | 'Pending'
  | 'Implementing'
  | 'Checking'
  | 'Reviewing'
  | 'Verifying'
  | 'AwaitingApproval'
  | 'Blocked'
  | 'Merged'
  | 'Failed'
  /** A Benchmark's run whose change passed its checks and review: it would have merged, and never does. */
  | 'Approved'

/** One step in a Task's timeline: from `at` on, the Task was in phase and round, for reason. */
export interface TaskTransition {
  at: string
  phase: TaskPhase
  round?: number
  reason?: string
  message?: string
}

/** What a Task keeps of one agent run once it ended: its round, role, cost, tokens and handoff sizes. */
export interface TaskAgentRun {
  round: number
  /** implement, review, verify or plan; omitted when neither theos-agent nor the sandbox's name said. */
  role?: string
  sandbox?: string
  /** The Agent, harness and model the run ran on, as they were then; omitted for runs from before they were recorded. */
  agent?: string
  harness?: string
  model?: string
  /** When the Task saw the run end. */
  endedAt: string
  /** What the run cost, in thousandths of a US dollar; omitted when the agent didn't say. */
  costMilliUSD?: number
  tokens?: TokenUsage
  /** The sizes, in tokens, at which the run's sessions handed off to fresh ones. */
  sessionHandoffs?: number[]
}

export interface CheckStatus {
  commit?: string
  /** pending, success, failure or error. */
  state?: string
  url?: string
}

/** Labels on the Tasks a Crew starts: the Crew's name, and the issue number. */
export const LABEL_CREW = 'theos.factory.io/crew'
export const LABEL_ISSUE = 'theos.factory.io/issue'

/** Who approves a spec's pull request: a person on the Git host, or the reviewer Agent alone. */
export type SpecApproval = 'Person' | 'Reviewer'

/** A share with its 95% Wilson interval, in whole percent. */
export interface BenchmarkRate {
  of?: number
  percent?: number
  low?: number
  high?: number
}

/** The same goals from one pinned commit under two or more Agent variants (docs/plans/done/measure-and-improve.md). */
export interface Benchmark extends K8sObject {
  spec: {
    containerRef: LocalRef
    base: string
    goals: { goal?: string; issue?: number }[]
    variants: { name: string; implementerRef: LocalRef; reviewerRef?: LocalRef; verifierRef?: LocalRef }[]
    repetitions?: number
    scorerRefs?: LocalRef[]
    parallel?: number
    budgetUSD: number
  }
  status?: {
    goals?: string[]
    costMilliUSD?: number
    variants?: {
      name: string
      tasks?: number
      finished?: number
      approval?: BenchmarkRate
      meanRounds?: string
      medianMinutes?: number
      medianCostMilliUSD?: number
      criteria?: { scorer: string; criterion: string; rate?: BenchmarkRate; unclear?: number }[]
    }[]
    comparison?: string
    cleaned?: string[]
    conditions?: Condition[]
  }
}

/** A lease of a test environment a Task's run holds: the TestEnvironment, the lease's ID, the run and since when. */
export interface TaskEnvironmentLease {
  name: string
  lease: string
  sandbox: string
  since?: string
}

/** The Agent configuration an agent run ran on. */
export interface RunConfig {
  agent?: string
  harness?: string
  model?: string
}

/** What a Score Task judges (docs/plans/done/measure-and-improve.md). */
export interface TaskScoreSpec {
  scorerRef: LocalRef
  scorerGeneration: number
  taskRef: LocalRef
  criteria: string[]
  /** The judged Task's implementer's configuration. */
  config?: RunConfig
}

export type ScoreVerdict = 'Pass' | 'Fail' | 'Unclear'

/** A judge's verdict on one criterion, and why. */
export interface ScoreCriterion {
  criterion: string
  verdict: ScoreVerdict
  reason?: string
}

export interface Task extends K8sObject {
  spec: {
    /**
     * Implement (the default), Plan: a planning round of a Brief, Triage: a
     * triager's decision on a proposed issue, Spec: an issue's spec, written
     * in a pull request a person approves, or Score: a Scorer's judge run on
     * another Task's finished change.
     */
    kind?: 'Implement' | 'Plan' | 'Triage' | 'Spec' | 'Score'
    /** Every Task but a Plan Task names its Container. */
    containerRef?: LocalRef
    /** A Plan Task names its Brief in place of a Container. */
    briefRef?: LocalRef
    /** Implements the goal, and reviews it when there is no reviewerRef. */
    agentRef: LocalRef
    reviewerRef?: LocalRef
    /** Builds, starts and checks the change once the reviewer approved it, before it merges (phase Verifying). */
    verifierRef?: LocalRef
    /** A Task has either a goal or an issue of its Container's repository. */
    goal?: string
    issue?: number
    /** The protected paths a gated Task may change. */
    allowedProtectedPaths?: string[]
    /** A Score Task's Scorer, the Task it judges and the criteria, as they were. */
    score?: TaskScoreSpec
    /** A Benchmark's run: which Benchmark, variant, goal (from 0) and repetition, and the base it branches from. */
    benchmark?: { benchmarkRef: LocalRef; variant: string; goal?: number; repetition: number; base: string }
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
    /** A Triage Task's decision, its reason, and what the operator did with it. */
    /** A Score Task's verdicts, one per criterion; a failed run leaves each Unclear. */
    score?: { criteria?: ScoreCriterion[] }
    triage?: {
      decision: 'ready' | 'spec' | 'park' | 'duplicate'
      reason?: string
      criteria?: string[]
      question?: string
      duplicate?: number
      applied?: string
    }
    /** A Spec Task's spec: its file, who approves it, and what the operator did to the issue once it merged. */
    spec?: {
      path: string
      approval?: SpecApproval
      approvedBy?: string
      criteria?: string[]
      applied?: string
    }
    /** What the Crew's approval rules ask of the change, which rule and why, and who approved it. */
    approval?: {
      require: ApprovalRequirement
      rule?: number
      reason?: string
      commit?: string
      approvedBy?: string
      approvedInChat?: boolean
    }
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
    /** The merged pull request's size, read once it merged; omitted before, or when it couldn't be read. */
    pullRequestSize?: { additions?: number; deletions?: number; files?: number }
    /** How many of the merged pull request's commits a person wrote; 0 means the factory alone. Omitted when not read. */
    personCommits?: number
    checks?: CheckStatus
    review?: {
      verdict?: string
      summary?: string
      /** Kept while a verifier runs the approved change, for the merge to file. */
      followUps?: { title: string; body?: string; criteria?: string[] }[]
      open?: OpenCriterion[]
    }
    /** The verifier's latest verdict: what it ran and found, and the criteria it left open for a person. */
    verification?: { verdict?: string; summary?: string; commit?: string; open?: OpenCriterion[] }
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
    /**
     * The test environments the current run holds, a lease each, taken before
     * its sandbox and returned when it ends or is suspended.
     */
    environments?: TaskEnvironmentLease[]
    /** The test environments any run of the Task leased, by name. */
    leasedEnvironments?: string[]
    phaseChangedAt?: string
    completedAt?: string
    /** The Task's agent runs as each ended, oldest first; the newest 64. */
    runs?: TaskAgentRun[]
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
    /** With paused, also pauses its running Tasks at their next tool boundary; unset, each resumes where it stopped. */
    pauseRuns?: boolean
    /** How many Tasks run at once; the API server fills in its default. */
    parallel?: number
    /** Lets the Crew tag a release once its milestone is done; off, a person tags it. */
    tagReleases?: boolean
    /** Lets the Crew file each release's prepare issue. */
    prepareReleases?: boolean
    /** Copies each release the Crew tags to another repository once its runs succeed. */
    publishReleases?: {
      github: { repository: string; tokenSecretRef: LocalRef }
      tagPrefix?: string
      latest?: boolean
      source?: PublishSource
    }
    /**
     * Sends the issues with a label to other Agents: per role, the first route
     * whose label the issue has and that names an Agent for it decides; with
     * none, the Crew's own Agent takes it.
     */
    routes?: CrewRoute[]
    /** The Agent that triages the proposed issues, one at a time; unset, people triage. */
    triager?: LocalRef
    /** The Agent that writes the specs of the issues labelled status/needs-spec; unset, people write them. */
    specWriter?: LocalRef
    /** Who approves a spec's pull request before it merges; unset means Person. */
    specApproval?: SpecApproval
    /** The Agent that builds, starts and checks each approved change before it merges; unset, the review merges it. */
    verifier?: LocalRef
    /** Recurring goals: when one comes due, the Crew files an issue with its goal and works it. */
    schedules?: CrewSchedule[]
    /**
     * Which changes wait for a person and which merge on CI alone: each changed
     * file takes the first rule that matches it, and the change needs the
     * strictest of what its files need. Unset, the reviewer's approval merges.
     */
    approval?: ApprovalRule[]
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
    /** The GitHub repository the Crew published to, by GitHub's id: a renamed or replaced one isn't published to. */
    publishTarget?: { repository: string; id: number }
    /** theos.factory.io/rerun-release value the Crew answered last. */
    rerunRelease?: string
    lastPolled?: string
    observedGeneration?: number
    /** Ready: whether the Crew can pick up issues, and why not; Released: whether its next release went out, and why not. */
    conditions?: Condition[]
    /** Where each of spec.schedules stands. */
    schedules?: CrewScheduleStatus[]
  }
}

/** What a change needs before it merges: a person's approval, the reviewer's, or CI alone. */
export type ApprovalRequirement = 'Person' | 'Reviewer' | 'None'

/**
 * One rule of a Crew's approval: for the changes whose issue has one of labels
 * and that change more than changedLinesOver lines (either unset is any), it
 * matches the files under paths (unset is every file) and asks require of them.
 */
export interface ApprovalRule {
  paths?: string[]
  labels?: string[]
  changedLinesOver?: number
  require: ApprovalRequirement
}

/** One recurring goal of a Crew: a five-field cron expression in timeZone (UTC unset), and what its issue asks for. */
export interface CrewSchedule {
  name: string
  cron: string
  timeZone?: string
  goal: string
}

/** A schedule's next time, and the issue it filed last. */
export interface CrewScheduleStatus {
  name: string
  since: string
  lastFiled?: string
  lastIssue?: number
  next?: string
}

/**
 * The folder of the GitHub repository a release's source goes to; "." is the
 * whole repository. tagPrefix goes before the release's tag in the source's
 * tag; unset, it is path + "/", and nothing for ".".
 */
export interface PublishSource {
  path: string
  tagPrefix?: string
}

/** A Crew route: the issues with the label go to these Agents; a role it leaves out stays with the Crew's. */
export interface CrewRoute {
  /** An issue label, such as kind/docs, matched exactly. */
  label: string
  implementer?: LocalRef
  reviewer?: LocalRef
  /** Verifies the label's issues, even in a Crew without a verifier. */
  verifier?: LocalRef
}

/** An acceptance criterion an approval left open for a person, and why. */
export interface OpenCriterion {
  criterion: string
  why?: string
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
  publish?: {
    state: 'Pending' | 'Publishing' | 'Published' | 'Failed'
    url?: string
    message?: string
    /** The release's source on GitHub, once it is out. */
    source?: { commit: string; tag: string; url?: string; private?: boolean }
  }
  goModule?: {
    state: 'Published' | 'NotGo' | 'Invalid' | 'Failed' | 'Waiting' | 'NotFetchable'
    path?: string
    version?: string
    message?: string
    /** When the Go proxy was last asked about the version. */
    checkedTime?: string
  }
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
    /** How publishing its Go module went, the Crew's goModule.state; empty when the Crew reports none. */
    moduleState?: 'Published' | 'NotGo' | 'Invalid' | 'Failed' | 'Waiting' | 'NotFetchable'
    /** The module path go.mod declares. */
    modulePath?: string
    /** Why the module isn't published, from the Crew. */
    moduleMessage?: string
  }
  /** The release the Crew's next cut ships, under a cadence of Day or Week; unset while none holds work. */
  nextRelease?: {
    tag: string
    /** When the cut is. */
    time: string
    /** The cadence's time zone, an IANA name, to say the time as the person set it. */
    timeZone?: string
    /** How many of its issues a Task merged so far. */
    merged?: number
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

/** A Container whose releases the person asks for, and the GitHub repository they are published to. */
export interface BriefRelease {
  /** The Container, a DNS label. */
  container: string
  github: {
    /** owner/name */
    repository: string
    /** The Secret in the Brief's namespace with the GitHub token. */
    tokenSecretRef: LocalRef
  }
  tagPrefix?: string
  /** False keeps its releases from taking Latest on a repository other apps publish to. */
  latest?: boolean
  /** The folder its source goes to with each release; unset publishes none. */
  source?: PublishSource
  /** Whether the Brief takes requests from the repository's GitHub issues, and whose; unset takes none. */
  requests?: BriefRequests
}

/** How a Brief takes requests: Off reads no issue, Ask waits for a person's yes, On lets each through. */
export type BriefRequestsMode = 'Off' | 'Ask' | 'On'

/** Requests from a releases repository's GitHub issues: an open issue of one of authors, or of a member of teams. */
export interface BriefRequests {
  /** Unset is Off. */
  mode?: BriefRequestsMode
  /** GitHub logins. */
  authors?: string[]
  /** GitHub teams, org/slug. */
  teams?: string[]
  /** Ask: the issue numbers a person let through. */
  accepted?: number[]
  /** The issue numbers a person turned down; the Brief says so on each and closes it. */
  declined?: number[]
}

/** How far a request got: waiting for a person (Ask), for a round, in one, and after. */
export type BriefRequestStage = 'Waiting' | 'Accepted' | 'Planning' | 'Planned' | 'Skipped' | 'Declined' | 'Released'

/** A GitHub issue taken as a request, its text as it was when let through, and how far it got. */
export interface BriefRequest {
  /** owner/name on github.com */
  repository: string
  number: number
  author: string
  title: string
  /** Empty while it waits in Ask. */
  body?: string
  url?: string
  stage: BriefRequestStage
  /** The round that took it; 0 or unset before one did. */
  round?: number
  /** The issues it became, container#number. */
  issues?: string[]
  /** The release its issues are filed in, such as v0.3.0. */
  release?: string
  /** The round's reason for skipping it. */
  why?: string
  since?: string
}

/** Whether a Brief tends its handed-back work by itself: Off, Ask (decides and asks you) or On (applies). */
export type BriefAutopilot = 'Off' | 'Ask' | 'On'

/** What a Brief's item is: a failed issue, a Task, Crew or Container it made, or a request from GitHub. */
export type BriefItemKind = 'Issue' | 'Task' | 'Crew' | 'Container' | 'Request'

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
  /** The item's name; container#number for an issue, owner/name#number for a request. */
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
    /** Follow-ups, oldest first; only ever appended, the oldest planned ones retired by the write that appends to a full list. */
    prompts?: string[]
    /** How many of the oldest prompts were taken out of prompts once planned; only grows. */
    promptsRetired?: number
    agents?: { planner?: LocalRef; implementer?: LocalRef; reviewer?: LocalRef; designer?: LocalRef }
    gitProviderRef?: LocalRef
    /** Whether it tends its handed-back work by itself; unset is the operator's default. */
    autopilot?: BriefAutopilot
    /** How many issues each Crew the Brief made works on at once; unset is the operator's default. */
    parallel?: number
    /** The Containers whose releases are published to GitHub; each entry turns releases on for its Container's Crew. */
    releases?: BriefRelease[]
    /** Pauses the Brief and every Crew it made; a Task already started runs to its end. The Paused condition says what still runs. */
    paused?: boolean
    /** With paused, also pauses running Tasks, its Crews' and its planner's, at their next tool boundary; unset, each resumes where it stopped. */
    pauseRuns?: boolean
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
    /** The GitHub issues taken as requests and still open, oldest first, at most 50. */
    requests?: BriefRequest[]
    /** How many requests are open in all; can exceed the listed ones. */
    requestsWaiting?: number
    /**
     * Per repository, the numbers in its entry's accepted and declined whose
     * issues are closed: they do nothing more, and an answer drops them.
     */
    requestsSettled?: { repository: string; numbers?: number[] }[]
    /** The requests the running or last round took, owner/name#number. */
    roundRequests?: string[]
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
      /** The reviewers' suggestions a prompt named that the round couldn't answer, in the operator's words. */
      unanswered?: string[]
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
    /** Planned, and NeedsInput: while True, its message says what the Brief waits for; Requests: whether the issues of the repositories that take requests can be read. */
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
