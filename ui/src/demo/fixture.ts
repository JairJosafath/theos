// The webshop story with every state the UI can show: containers in all four
// phases, a failed type check, agent and human Sessions (one Stale), a
// Relationship added after scaffolding, and Crews working, paused and waiting
// for their Container: the working one with a Sonnet implementer, an Opus
// reviewer and Tasks made from issues; Briefs planned, needing input,
// paused and planning; and a Container shipped to staging, waiting for a
// person's approval of the same release for prod. Used by demo mode (?demo=1), so the whole
// UI can be seen and screenshotted without writing to a cluster.

import type { StoryData } from '../k8s/sources'
import type {
  Agent,
  Brief,
  Bundle,
  CatalogEntry,
  Condition,
  Container,
  Crew,
  DataStore,
  DeployTarget,
  Domain,
  GitProvider,
  Person,
  Project,
  Relationship,
  Session,
  SoftwareSystem,
  Standard,
  Task,
  TaskPhase,
  TaskTransition,
} from '../k8s/types'

export const DEMO_NAMESPACE = 'webshop'

const API_VERSION = 'theos.factory.io/v1beta1'
const GITEA = 'http://gitea.gitea.svc.cluster.local:3000'
/** Where the operator's issue and pull request links point: Gitea's own address, port-forwarded. */
const GITEA_WEB = 'http://localhost:3000'
const MIN = 60_000

function meta(name: string, created: string) {
  return { name, namespace: DEMO_NAMESPACE, uid: `demo-${name}`, resourceVersion: '1', creationTimestamp: created }
}

function cond(type: string, status: Condition['status'], reason: string, message: string, at: string): Condition {
  return { type, status, reason, message, lastTransitionTime: at }
}

function catalog(
  kind: string,
  name: string,
  displayName: string,
  types: string[],
  ref: string,
  at: string,
): CatalogEntry {
  return {
    apiVersion: API_VERSION,
    kind,
    metadata: meta(name, at),
    spec: {
      displayName,
      supportedTypes: types as CatalogEntry['spec']['supportedTypes'],
      template: { url: `${GITEA}/templates/${name}`, ref },
    },
  }
}

/** The demo story, with times relative to `now` so ages read naturally. */
export function demoStory(now: number): StoryData {
  const ago = (seconds: number) => new Date(now - seconds * 1000).toISOString()
  const created = ago(3 * 3600)

  const project: Project = {
    apiVersion: API_VERSION,
    kind: 'Project',
    metadata: meta('webshop', created),
    spec: {
      displayName: 'Webshop',
      description: 'An online shop where customers browse, order and reorder.',
      license: 'MIT',
    },
    status: {
      softwareSystems: 3,
      containers: 5,
      readyContainers: 2,
      activeSessions: 2,
      summary: '2 of 5 containers Ready, 2 active sessions',
    },
  }

  const people: Person[] = [
    {
      kind: 'Person',
      metadata: meta('customer', created),
      spec: { displayName: 'Customer', description: 'Someone who buys from the shop.' },
    },
    {
      kind: 'Person',
      metadata: meta('shop-manager', created),
      spec: { displayName: 'Shop manager', description: 'Keeps the catalog and prices up to date.' },
    },
  ]

  const systems: SoftwareSystem[] = [
    {
      kind: 'SoftwareSystem',
      metadata: meta('webshop-core', created),
      spec: {
        displayName: 'Webshop Core',
        description: 'Catalog, cart and ordering',
        needs: ['Customers can reorder in two clicks', 'Managers change prices without a release'],
        kpis: ['Checkout conversion', 'Time to first order'],
        domainRefs: ['webshop'],
      },
      status: {
        containers: 5,
        readyContainers: 2,
        dataStores: 3,
        activeSessions: 2,
        summary: '2 of 5 containers Ready, 2 active sessions',
      },
    },
    {
      kind: 'SoftwareSystem',
      metadata: meta('payment-provider', created),
      spec: {
        location: 'External',
        displayName: 'Payment Provider',
        description: 'Takes card and wallet payments.',
        needs: ['Payments never charge twice'],
      },
      status: { summary: 'No containers' },
    },
    {
      kind: 'SoftwareSystem',
      metadata: meta('warehouse', created),
      spec: { displayName: 'Warehouse', description: 'Picks, packs and ships orders.' },
      status: { summary: 'No containers' },
    },
  ]

  const container = (
    name: string,
    spec: Partial<Container['spec']> & Pick<Container['spec'], 'type'>,
    status: Container['status'],
  ): Container => ({
    kind: 'Container',
    metadata: meta(name, created),
    spec: { softwareSystemRef: { name: 'webshop-core' }, gitProviderRef: { name: 'gitea' }, ...spec },
    status,
  })

  const containers: Container[] = [
    container(
      'orders-api',
      {
        displayName: 'Orders API',
        description: 'Takes and tracks orders.',
        type: 'service',
        frameworkRef: { name: 'python-fastapi' },
        standardRef: { name: 'python-standard' },
        computeRef: { name: 'small' },
        deploymentRef: { name: 'k8s-manifests' },
        operations: {
          backups: { retentionDays: 90, copyToRegion: 'eu-west-1' },
          recovery: { rpo: '15m' },
          alerts: { email: 'orders-oncall@example.com' },
        },
        deploy: [{ target: 'staging' }, { target: 'prod', after: 'staging', approved: 'v0.1.0' }],
      },
      {
        phase: 'Ready',
        operations: {
          backups: { retentionDays: 90, copyToRegion: 'eu-west-1' },
          recovery: { rpo: '15m', rto: '4h' },
          alerts: { email: 'orders-oncall@example.com' },
        },
        repositoryURL: `${GITEA}/webshop/orders-api`,
        scaffoldedFrom: [
          'python-fastapi@v1.3.0',
          'python-standard@v1.2.0',
          'small@v1.0.0',
          'k8s-manifests@v1.1.0',
          'postgres@v1.1.0',
          'webshop@v1.0.0',
        ],
        scaffoldedDomains: ['webshop'],
        standard: { name: 'python-standard', ref: 'v1.2.0' },
        appliedRelationships: ['orders-api-to-orders-db'],
        appliedConnectors: [{ relationship: 'orders-api-to-orders-db', dataStore: 'orders-db', connector: 'postgres' }],
        activeSessions: 1,
        // Staging runs the newest release; prod still runs the one a person let in before, and waits for the next.
        newestRelease: 'v0.2.0',
        deploys: [
          {
            target: 'staging',
            version: 'v0.2.0',
            wantedVersion: 'v0.2.0',
            phase: 'Deployed',
            health: 'Degraded',
            url: 'https://github.com/webshop-co/orders-staging/releases/tag/v0.2.0',
            reason: 'Deployed',
            message: 'staging runs v0.2.0, and its tool reports it Degraded',
            since: ago(40 * 60),
            // The monitor filed a bug in the Container's repository for this version.
            issue: {
              number: 19,
              version: 'v0.2.0',
              state: 'Degraded',
              url: `${GITEA_WEB}/webshop/orders-api/issues/19`,
            },
          },
          {
            target: 'prod',
            version: 'v0.1.0',
            wantedVersion: 'v0.2.0',
            phase: 'Waiting',
            health: 'Healthy',
            url: 'https://github.com/webshop-co/orders-api/releases/tag/v0.1.0',
            reason: 'WaitingForApproval',
            message:
              "v0.2.0 waits for a person's approval to ship to prod: set approved to v0.2.0 in its spec.deploy entry",
            since: ago(2 * 3600),
          },
        ],
        conditions: [
          cond('ReferencesResolved', 'True', 'Resolved', 'All references exist', ago(10700)),
          cond('ChoicesValid', 'True', 'Valid', 'Every catalog entry supports service', ago(10700)),
          cond('RepositoryCreated', 'True', 'Created', `Created ${GITEA}/webshop/orders-api`, ago(10690)),
          cond('Scaffolded', 'True', 'Scaffolded', 'Pushed the initial commit', ago(10680)),
          cond('DomainsApplied', 'True', 'AppliedAtScaffold', '', ago(10680)),
          cond(
            'Deployed',
            'False',
            'WaitingForApproval',
            "v0.2.0 waits for a person's approval to ship to prod: set approved to v0.2.0 in its spec.deploy entry",
            ago(2 * 3600),
          ),
        ],
      },
    ),
    // Named its Bundle instead of the four sides; the operator expanded it into status.bundle.
    container(
      'inventory-api',
      {
        displayName: 'Inventory API',
        description: 'Counts stock and reserves it for orders.',
        type: 'service',
        bundleRef: { name: 'python-service' },
      },
      {
        phase: 'Ready',
        repositoryURL: `${GITEA}/webshop/inventory-api`,
        scaffoldedFrom: ['python-fastapi@v1.3.0', 'python-standard@v1.2.0', 'small@v1.0.0', 'k8s-manifests@v1.1.0'],
        bundle: {
          name: 'python-service',
          framework: 'python-fastapi',
          standard: 'python-standard',
          compute: 'small',
          deployment: 'k8s-manifests',
        },
        standard: { name: 'python-standard', ref: 'v1.2.0' },
        conditions: [
          cond('ReferencesResolved', 'True', 'Resolved', 'All references exist', ago(8000)),
          cond('ChoicesValid', 'True', 'Valid', 'Every catalog entry supports service', ago(8000)),
          cond('RepositoryCreated', 'True', 'Created', `Created ${GITEA}/webshop/inventory-api`, ago(7990)),
          cond('Scaffolded', 'True', 'Scaffolded', 'Pushed the initial commit', ago(7980)),
          cond('BundleCurrent', 'True', 'Current', 'Bundle python-service names what it was scaffolded from', ago(600)),
          // Scaffolded before webshop-core named the webshop Domain: reported, never applied.
          cond(
            'DomainsApplied',
            'False',
            'AddedAfterScaffold',
            'Domains added after the scaffold: webshop; scaffolding happens once',
            ago(600),
          ),
        ],
      },
    ),
    container(
      'storefront',
      {
        displayName: 'Storefront',
        description: 'The pages customers browse and buy from.',
        type: 'static-site',
        frameworkRef: { name: 'python-fastapi' },
      },
      {
        phase: 'Failed',
        activeSessions: 1,
        conditions: [
          cond('ReferencesResolved', 'True', 'Resolved', 'All references exist', ago(9000)),
          cond('ChoicesValid', 'False', 'UnsupportedType', 'python-fastapi does not support static-site', ago(9000)),
        ],
      },
    ),
    container(
      'order-worker',
      {
        displayName: 'Order Worker',
        description: 'Sends paid orders to the warehouse.',
        type: 'worker',
        frameworkRef: { name: 'python-fastapi' },
        computeRef: { name: 'small' },
        deploymentRef: { name: 'k8s-manifests' },
      },
      {
        phase: 'Scaffolding',
        repositoryURL: `${GITEA}/webshop/order-worker`,
        conditions: [
          cond('ReferencesResolved', 'True', 'Resolved', 'All references exist', ago(40)),
          cond('ChoicesValid', 'True', 'Valid', 'Every catalog entry supports worker', ago(40)),
          cond('RepositoryCreated', 'True', 'Created', `Created ${GITEA}/webshop/order-worker`, ago(20)),
          cond('Scaffolded', 'False', 'Scaffolding', 'Rendering the template layers', ago(20)),
        ],
      },
    ),
    container(
      'catalog-api',
      {
        displayName: 'Catalog API',
        description: 'Products, prices and stock.',
        type: 'service',
        frameworkRef: { name: 'go-chi' },
        computeRef: { name: 'small' },
      },
      {
        phase: 'Pending',
        conditions: [cond('ReferencesResolved', 'False', 'NotFound', 'Framework "go-chi" not found', ago(600))],
      },
    ),
  ]

  const dataStores: DataStore[] = [
    {
      kind: 'DataStore',
      metadata: meta('orders-db', created),
      spec: {
        softwareSystemRef: { name: 'webshop-core' },
        displayName: 'Orders DB',
        description: 'Orders and their lines.',
        connectorRef: { name: 'postgres' },
      },
      status: { conditions: [cond('ReferencesResolved', 'True', 'Resolved', 'Connector postgres exists', created)] },
    },
    {
      kind: 'DataStore',
      metadata: meta('catalog-db', created),
      spec: {
        softwareSystemRef: { name: 'webshop-core' },
        displayName: 'Catalog DB',
        description: 'Products and prices.',
        connectorRef: { name: 'postgres' },
      },
      status: { conditions: [cond('ReferencesResolved', 'True', 'Resolved', 'Connector postgres exists', created)] },
    },
    {
      kind: 'DataStore',
      metadata: meta('cart-cache', created),
      spec: {
        softwareSystemRef: { name: 'webshop-core' },
        displayName: 'Cart Cache',
        description: 'Open carts, kept for a day.',
        connectorRef: { name: 'redis' },
      },
      status: { conditions: [cond('ReferencesResolved', 'True', 'Resolved', 'Connector redis exists', created)] },
    },
  ]

  const rel = (
    name: string,
    source: Relationship['spec']['source'],
    target: Relationship['spec']['target'],
    description: string,
    conditions?: Condition[],
  ): Relationship => ({
    kind: 'Relationship',
    metadata: meta(name, created),
    spec: { source, target, description },
    status: conditions ? { conditions } : undefined,
  })

  const relationships: Relationship[] = [
    rel(
      'customer-uses-webshop-core',
      { kind: 'Person', name: 'customer' },
      { kind: 'SoftwareSystem', name: 'webshop-core' },
      'Browses, orders and reorders',
    ),
    rel(
      'shop-manager-uses-webshop-core',
      { kind: 'Person', name: 'shop-manager' },
      { kind: 'SoftwareSystem', name: 'webshop-core' },
      'Manages products and prices',
    ),
    rel(
      'storefront-to-orders-api',
      { kind: 'Container', name: 'storefront' },
      { kind: 'Container', name: 'orders-api' },
      'Places orders (JSON over HTTPS)',
    ),
    rel(
      'storefront-to-catalog-api',
      { kind: 'Container', name: 'storefront' },
      { kind: 'Container', name: 'catalog-api' },
      'Reads products',
    ),
    rel(
      'orders-api-to-orders-db',
      { kind: 'Container', name: 'orders-api' },
      { kind: 'DataStore', name: 'orders-db' },
      'Reads and writes orders',
      [
        cond('ReferencesResolved', 'True', 'Resolved', 'Both ends exist', created),
        cond('Applied', 'True', 'AppliedAtScaffold', 'postgres was part of the scaffold', ago(10680)),
      ],
    ),
    rel(
      'orders-api-to-cart-cache',
      { kind: 'Container', name: 'orders-api' },
      { kind: 'DataStore', name: 'cart-cache' },
      'Caches open carts',
      [
        cond('ReferencesResolved', 'True', 'Resolved', 'Both ends exist', ago(1800)),
        cond(
          'Applied',
          'False',
          'AddedAfterScaffold',
          'orders-api was scaffolded before this relationship existed; add the redis connector by hand',
          ago(1800),
        ),
      ],
    ),
    rel(
      'catalog-api-to-catalog-db',
      { kind: 'Container', name: 'catalog-api' },
      { kind: 'DataStore', name: 'catalog-db' },
      'Reads and writes products',
      [
        cond('ReferencesResolved', 'True', 'Resolved', 'Both ends exist', created),
        cond('Applied', 'False', 'WaitingForScaffold', 'catalog-api is not scaffolded yet', ago(600)),
      ],
    ),
    rel(
      'order-worker-to-orders-db',
      { kind: 'Container', name: 'order-worker' },
      { kind: 'DataStore', name: 'orders-db' },
      'Picks up paid orders',
      [
        cond('ReferencesResolved', 'True', 'Resolved', 'Both ends exist', created),
        cond('Applied', 'False', 'WaitingForScaffold', 'order-worker is being scaffolded', ago(20)),
      ],
    ),
    rel(
      'orders-api-to-payment-provider',
      { kind: 'Container', name: 'orders-api' },
      { kind: 'SoftwareSystem', name: 'payment-provider' },
      'Charges the customer',
    ),
    rel(
      'order-worker-to-warehouse',
      { kind: 'Container', name: 'order-worker' },
      { kind: 'SoftwareSystem', name: 'warehouse' },
      'Sends shipping requests',
    ),
  ]

  const session = (
    name: string,
    containerName: string,
    actor: Session['spec']['actor'],
    summary: string,
    phase: 'Active' | 'Stale',
    heartbeat: number,
  ): Session => ({
    kind: 'Session',
    metadata: meta(name, ago(heartbeat + 1200)),
    spec: { containerRef: { name: containerName }, actor, summary },
    status: { phase, lastHeartbeat: ago(heartbeat) },
  })

  const sessions: Session[] = [
    session('orders-api-k7f3q', 'orders-api', 'agent', 'Adding order validation', 'Active', 12),
    session(
      'storefront-m2x8d',
      'storefront',
      'human',
      'Switching the storefront to a static-site framework',
      'Active',
      21,
    ),
    session('orders-api-p9w4t', 'orders-api', 'agent', 'Writing the API reference', 'Stale', 6 * 60 + 5),
  ]

  const agents: Agent[] = [
    {
      apiVersion: API_VERSION,
      kind: 'Agent',
      metadata: meta('claude', created),
      // No model: Claude Code's default. sessionTokenLimit as the API server fills it in.
      spec: {
        displayName: 'Claude Code (Python)',
        image: 'theos/agent-python:dev',
        maxRounds: 3,
        sessionTokenLimit: 130_000,
      },
    },
    {
      apiVersion: API_VERSION,
      kind: 'Agent',
      metadata: meta('claude-implementer', created),
      spec: {
        displayName: 'Claude Code, Sonnet (implementer)',
        description: 'Implements one issue at a time, test first, until make check passes.',
        image: 'theos/agent-python:dev',
        harness: 'claude-code',
        model: 'claude-sonnet-5-5',
        maxRounds: 3,
        sessionTokenLimit: 130_000,
        timeoutMinutes: 90,
      },
    },
    {
      apiVersion: API_VERSION,
      kind: 'Agent',
      metadata: meta('claude-reviewer', created),
      spec: {
        displayName: 'Claude Code, Opus (reviewer)',
        description: 'Reviews each round against the issue’s acceptance criteria and the Standard’s checklist.',
        image: 'theos/agent-python:dev',
        harness: 'claude-code',
        model: 'claude-opus-5-5',
        maxRounds: 3,
        sessionTokenLimit: 130_000,
        timeoutMinutes: 45,
      },
    },
  ]

  const at = (minutesAgo: number) => ago(minutesAgo * 60)
  const step = (minutesAgo: number, phase: TaskPhase, round: number, reason: string, message = ''): TaskTransition => ({
    at: at(minutesAgo),
    phase,
    round,
    reason,
    message,
  })
  const issueURL = (repo: string, n: number) => `${GITEA_WEB}/webshop/${repo}/issues/${n}`
  const pr = (repo: string, n: number) => ({
    pullRequest: n,
    pullRequestURL: `${GITEA_WEB}/webshop/${repo}/pulls/${n}`,
  })
  const merged = (minutesAgo: number) => ({
    phase: 'Merged' as const,
    completedAt: at(minutesAgo),
    checks: { state: 'success' },
    review: { verdict: 'approve', summary: 'Every acceptance criterion is met.' },
    conditions: [
      cond('Progressing', 'False', 'Merged', 'Approved; merging', at(minutesAgo)),
      cond('Succeeded', 'True', 'Merged', 'Approved; merging', at(minutesAgo)),
    ],
  })
  /** A Task a Crew started from an issue: named <container>-<issue>, with the attempt from the second one on. */
  const issueTask = (
    repo: string,
    issue: number,
    title: string,
    createdMinutesAgo: number,
    status: Task['status'],
    attempt = 1,
  ): Task => {
    const name = `${repo}-${issue}${attempt > 1 ? `-${attempt}` : ''}`
    return {
      apiVersion: API_VERSION,
      kind: 'Task',
      metadata: {
        ...meta(name, at(createdMinutesAgo)),
        labels: { 'theos.factory.io/crew': repo, 'theos.factory.io/issue': `${issue}` },
      },
      spec: {
        containerRef: { name: repo },
        agentRef: { name: 'claude-implementer' },
        reviewerRef: { name: 'claude-reviewer' },
        issue,
      },
      status: { branch: `task/${name}`, issueTitle: title, issueURL: issueURL(repo, issue), ...status },
    }
  }

  // A Task of the loop's kinds: the same shape as an issue Task, with its kind.
  const loopTask = (
    repo: string,
    issue: number,
    title: string,
    createdMinutesAgo: number,
    kind: NonNullable<Task['spec']['kind']> | undefined,
    status: Task['status'],
  ): Task => {
    const t = issueTask(repo, issue, title, createdMinutesAgo, status)
    return { ...t, spec: { ...t.spec, ...(kind ? { kind } : {}) } }
  }

  const back = now + 38 * MIN
  const unavailable = `The agent is unavailable (usage limit reached); round 2 waits for it; trying again at ${new Date(back).toISOString().replace(/\.\d+Z$/, 'Z')}`

  // The orders-api Crew's issues: merged ones with follow-ups, a failed one
  // tried again, one waiting for its agent after a usage limit, and one in review.
  const tasks: Task[] = [
    issueTask('orders-api', 10, 'List a customer’s past orders', 140, {
      ...merged(118),
      round: 1,
      summary: 'GET /customers/{id}/orders, newest first and paged, with tests for an empty history.',
      costMilliUSD: 1_380,
      tokens: { input: 8_400, output: 41_000, cacheRead: 1_910_000, cacheWrite: 176_000, largestSession: 88_000 },
      ...pr('orders-api', 3),
      pullRequestSize: { additions: 182, deletions: 21, files: 6 },
      personCommits: 0,
      runs: [
        {
          round: 1,
          role: 'implement',
          sandbox: 'orders-api-10-r1-implement',
          agent: 'claude-implementer',
          harness: 'claude-code',
          model: 'claude-sonnet-5-5',
          endedAt: at(126),
          costMilliUSD: 1_090,
          tokens: { input: 6_100, output: 33_000, cacheRead: 1_520_000, cacheWrite: 141_000, largestSession: 88_000 },
        },
        {
          round: 1,
          role: 'review',
          sandbox: 'orders-api-10-r1-review-a1',
          endedAt: at(118),
          costMilliUSD: 290,
          tokens: { input: 2_300, output: 8_000, cacheRead: 390_000, cacheWrite: 35_000, largestSession: 41_000 },
        },
      ],
      history: [
        step(140, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(139, 'Implementing', 1, 'Waiting', 'The agent is working on round 1'),
        step(126, 'Checking', 1, 'Checking', 'The agent pushed 3e1f0a2; waiting for checks'),
        step(123, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(118, 'Merged', 1, 'Merged', 'Approved; merging'),
      ],
    }),
    issueTask('orders-api', 11, 'Reorder a past order in one call', 117, {
      ...merged(85),
      round: 2,
      summary: 'POST /orders/{id}/reorder copies the lines at today’s prices; sold-out lines are reported.',
      costMilliUSD: 2_940,
      tokens: { input: 15_200, output: 83_000, cacheRead: 4_120_000, cacheWrite: 352_000, largestSession: 117_000 },
      followUps: [issueURL('orders-api', 16), issueURL('orders-api', 17)],
      ...pr('orders-api', 4),
      pullRequestSize: { additions: 64, deletions: 12, files: 3 },
      // A person pushed a fix of the prices to its branch between the rounds.
      personCommits: 1,
      runs: [
        {
          round: 1,
          role: 'implement',
          sandbox: 'orders-api-11-r1-implement',
          agent: 'claude-implementer',
          harness: 'claude-code',
          model: 'claude-sonnet-5-5',
          endedAt: at(104),
          costMilliUSD: 1_020,
          tokens: { input: 5_000, output: 31_000, cacheRead: 1_500_000, cacheWrite: 130_000, largestSession: 96_000 },
        },
        {
          round: 1,
          role: 'review',
          sandbox: 'orders-api-11-r1-review-a1',
          endedAt: at(97),
          costMilliUSD: 360,
          tokens: { input: 2_200, output: 9_000, cacheRead: 480_000, cacheWrite: 40_000, largestSession: 52_000 },
        },
        {
          round: 2,
          role: 'implement',
          sandbox: 'orders-api-11-r2-implement',
          agent: 'claude-implementer',
          harness: 'claude-code',
          model: 'claude-sonnet-5-5',
          endedAt: at(91),
          costMilliUSD: 1_240,
          tokens: { input: 6_000, output: 34_000, cacheRead: 1_750_000, cacheWrite: 150_000, largestSession: 117_000 },
        },
        {
          round: 2,
          role: 'review',
          sandbox: 'orders-api-11-r2-review-a1',
          endedAt: at(85),
          costMilliUSD: 320,
          tokens: { input: 2_000, output: 9_000, cacheRead: 390_000, cacheWrite: 32_000, largestSession: 47_000 },
        },
      ],
      history: [
        step(117, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(104, 'Checking', 1, 'Checking', 'The agent pushed 7b2c9d4; waiting for checks'),
        step(101, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(97, 'Implementing', 2, 'NextRound', 'Round 1 was not accepted (old prices); starting round 2'),
        step(91, 'Checking', 2, 'Checking', 'The agent pushed a04e6f1; waiting for checks'),
        step(89, 'Reviewing', 2, 'Running', 'Checks passed; starting the review'),
        step(85, 'Merged', 2, 'Merged', 'Approved; merging'),
      ],
    }),
    issueTask('orders-api', 13, 'Refund an order through the payment provider', 84, {
      phase: 'Failed',
      round: 3,
      completedAt: at(52),
      checks: { state: 'failure' },
      review: { verdict: 'changes', summary: 'A retried refund pays out twice; send an idempotency key.' },
      feedback: 'CI failed: test_refund_retry pays out twice.',
      // A long agent run, past the Agent's limit, that handed off twice, on a Task that failed.
      costMilliUSD: 6_120,
      tokens: {
        input: 31_000,
        output: 162_000,
        cacheRead: 8_870_000,
        cacheWrite: 694_000,
        largestSession: 142_000,
        sessionHandoffs: 2,
      },
      ...pr('orders-api', 5),
      conditions: [
        cond('Progressing', 'False', 'RoundsExhausted', 'Failed after 3 rounds', at(52)),
        cond('Succeeded', 'False', 'RoundsExhausted', 'Failed after 3 rounds: a retried refund pays out twice', at(52)),
      ],
      history: [
        step(84, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(74, 'Checking', 1, 'Checking', 'Waiting for checks'),
        step(71, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(68, 'Implementing', 2, 'NextRound', 'Round 1 was not accepted; starting round 2'),
        step(61, 'Checking', 2, 'Checking', 'Waiting for checks'),
        step(58, 'Implementing', 3, 'NextRound', 'Round 2 was not accepted (CI failed); starting round 3'),
        step(54, 'Checking', 3, 'Checking', 'Waiting for checks'),
        step(52, 'Failed', 3, 'RoundsExhausted', 'Failed after 3 rounds'),
      ],
      // Round 2's implement run is the one that handed off.
      runs: [
        {
          round: 1,
          role: 'implement',
          sandbox: 'orders-api-13-r1-implement',
          endedAt: at(74),
          costMilliUSD: 1_420,
          tokens: { input: 7_000, output: 38_000, cacheRead: 2_050_000, cacheWrite: 160_000, largestSession: 112_000 },
        },
        {
          round: 1,
          role: 'review',
          sandbox: 'orders-api-13-r1-review-a1',
          endedAt: at(68),
          costMilliUSD: 380,
          tokens: { input: 2_000, output: 8_000, cacheRead: 420_000, cacheWrite: 34_000, largestSession: 58_000 },
        },
        {
          round: 2,
          role: 'implement',
          sandbox: 'orders-api-13-r2-implement',
          endedAt: at(61),
          costMilliUSD: 3_010,
          tokens: {
            input: 13_000,
            output: 72_000,
            cacheRead: 4_600_000,
            cacheWrite: 350_000,
            largestSession: 142_000,
            sessionHandoffs: 2,
          },
          sessionHandoffs: [131_000, 138_400],
        },
        {
          round: 3,
          role: 'implement',
          sandbox: 'orders-api-13-r3-implement',
          endedAt: at(54),
          costMilliUSD: 1_310,
          tokens: { input: 9_000, output: 44_000, cacheRead: 1_800_000, cacheWrite: 150_000, largestSession: 121_000 },
        },
      ],
    }),
    issueTask(
      'orders-api',
      13,
      'Refund an order through the payment provider',
      45,
      {
        ...merged(30),
        round: 1,
        summary: 'Refunds send an idempotency key, so a retry never pays out twice.',
        followUps: [issueURL('orders-api', 18)],
        ...pr('orders-api', 6),
        costMilliUSD: 2_210,
        pullRequestSize: { additions: 431, deletions: 88, files: 9 },
        personCommits: 0,
        history: [
          step(45, 'Implementing', 1, 'Running', 'Starting round 1'),
          step(36, 'Checking', 1, 'Checking', 'The agent pushed 0c9d3be; waiting for checks'),
          step(33, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
          step(30, 'Merged', 1, 'Merged', 'Approved; merging'),
        ],
      },
      2,
    ),
    issueTask('orders-api', 14, 'Reject orders for products out of stock', 29, {
      phase: 'Implementing',
      round: 2,
      checks: { state: 'success' },
      review: { verdict: 'changes', summary: 'Stock is read before the lock; criterion 2 is not met.' },
      feedback: 'The reviewer asked for changes:\nStock is read before the lock; criterion 2 is not met.',
      agentBackAt: new Date(back).toISOString(),
      ...pr('orders-api', 7),
      costMilliUSD: 1_160,
      tokens: { input: 5_900, output: 30_000, cacheRead: 1_430_000, cacheWrite: 128_000, largestSession: 79_000 },
      runs: [
        {
          round: 1,
          role: 'implement',
          sandbox: 'orders-api-14-r1-implement',
          endedAt: at(17),
          costMilliUSD: 860,
          tokens: { input: 4_000, output: 23_000, cacheRead: 1_080_000, cacheWrite: 98_000, largestSession: 79_000 },
        },
        {
          round: 1,
          role: 'review',
          sandbox: 'orders-api-14-r1-review-a1',
          endedAt: at(10),
          costMilliUSD: 300,
          tokens: { input: 1_900, output: 7_000, cacheRead: 350_000, cacheWrite: 30_000, largestSession: 44_000 },
        },
      ],
      conditions: [cond('Progressing', 'True', 'AgentUnavailable', unavailable, at(6))],
      history: [
        step(29, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(17, 'Checking', 1, 'Checking', 'The agent pushed 5d8e2a7; waiting for checks'),
        step(14, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(10, 'Implementing', 2, 'NextRound', 'Round 1 was not accepted (stock lock); starting round 2'),
        step(6, 'Implementing', 2, 'AgentUnavailable', unavailable),
      ],
    }),
    // The loop's other stages (docs/plans/done/sdlc-loop.md): the bug the monitor filed is triaged, a spec is
    // written and waits for a person, and two Implement Tasks are in review and in verification.
    loopTask('orders-api', 19, 'Orders API runs v0.2.0 degraded at staging', 30, 'Triage', {
      phase: 'Implementing',
      round: 1,
      conditions: [cond('Progressing', 'True', 'Triaging', 'The triager is reading the bug', at(5))],
    }),
    loopTask('orders-api', 20, 'Paginate the order list', 90, 'Spec', {
      phase: 'Implementing',
      round: 1,
      conditions: [cond('Progressing', 'True', 'Writing', 'The agent is writing the spec', at(9))],
    }),
    loopTask('orders-api', 21, 'Let a customer cancel an unshipped order', 70, 'Spec', {
      phase: 'AwaitingApproval',
      round: 1,
      summary: 'Cancel only while the order has not left the warehouse; refund through the payment provider.',
      ...pr('orders-api', 9),
      conditions: [
        cond('Progressing', 'True', 'AwaitingApproval', 'The spec waits for a person to approve it', at(12)),
      ],
    }),
    loopTask('orders-api', 22, 'Add a health endpoint', 50, undefined, {
      phase: 'Reviewing',
      round: 1,
      checks: { state: 'success' },
      ...pr('orders-api', 10),
      conditions: [cond('Progressing', 'True', 'Reviewing', 'Checks passed; the review is running', at(4))],
    }),
    loopTask('orders-api', 23, 'Log a request id on every line', 45, 'Implement', {
      phase: 'Verifying',
      round: 1,
      checks: { state: 'success' },
      ...pr('orders-api', 11),
      conditions: [cond('Progressing', 'True', 'Verifying', 'Merged; verifying the change before it ships', at(3))],
    }),
    issueTask('orders-api', 15, 'Email the customer when an order ships', 25, {
      phase: 'Checking',
      round: 1,
      checks: { state: 'pending' },
      commit: '9f41c2e',
      summary: 'Sends a shipping email from the warehouse callback, with a test for a missing address.',
      ...pr('orders-api', 8),
      conditions: [cond('Progressing', 'True', 'Checking', 'The agent pushed 9f41c2e; waiting for checks', at(8))],
      history: [
        step(25, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(24, 'Implementing', 1, 'Waiting', 'The agent is working on round 1'),
        step(8, 'Checking', 1, 'Checking', 'The agent pushed 9f41c2e; waiting for checks'),
      ],
    }),
  ]

  const crew = (
    name: string,
    displayName: string,
    paused: boolean,
    status: Crew['status'],
    parallel = 1,
    releases: Partial<Crew['spec']> = {},
  ): Crew => ({
    apiVersion: API_VERSION,
    kind: 'Crew',
    metadata: meta(name, created),
    spec: {
      containerRef: { name },
      displayName,
      implementer: { name: 'claude-implementer' },
      reviewer: { name: 'claude-reviewer' },
      ...(paused ? { paused } : {}),
      ...(parallel > 1 ? { parallel } : {}),
      ...releases,
    },
    status,
  })
  // Where orders-api publishes: the Brief's spec.releases entry, which the Brief set on its Crew.
  const ordersPublish = {
    github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'orders-api-github' } },
  }
  const ordersCrew = (c: Crew): Crew => ({
    ...c,
    metadata: {
      ...c.metadata,
      labels: { 'theos.factory.io/brief': 'webshop' },
      annotations: { 'theos.factory.io/brief-publish-releases': JSON.stringify(ordersPublish) },
    },
  })
  const crews: Crew[] = [
    ordersCrew(
      crew(
        'orders-api',
        'Orders API crew',
        false,
        {
          phase: 'Working',
          activeTasks: ['orders-api-14', 'orders-api-15'],
          ready: [],
          waiting: [16, 17, 18],
          lastPolled: ago(30),
          conditions: [
            cond(
              'Ready',
              'True',
              'Working',
              'Tasks orders-api-14 and orders-api-15 are working on issues #14 and #15',
              at(25),
            ),
            cond('Released', 'True', 'Released', 'Released v0.2.0', ago(3 * 3600)),
            // The issue's case (#1001): GitHub refused the token, so the source tag Go
            // fetches github.com/webshop-co/orders-api@v0.2.0 from was never pushed.
            cond(
              'Published',
              'False',
              'Unauthorized',
              'GitHub refused the token in Secret orders-api-github: 401 Bad credentials',
              ago(3 * 3600),
            ),
            cond(
              'ModulePublished',
              'False',
              'WaitsForRelease',
              'Uploaded github.com/webshop-co/orders-api v0.2.0 to the registry, which go get reads only for theos.local/ paths; go get reads it from GitHub, where webshop-co/orders-api has no tag v0.2.0 yet: Published is Unauthorized, GitHub refused the token in Secret orders-api-github: 401 Bad credentials',
              ago(3 * 3600),
            ),
            cond(
              'ReleaseTarget',
              'True',
              'SetByBrief',
              'Brief webshop sets this in spec.releases; change it there',
              ago(2 * 86400),
            ),
          ],
          // The release its Brief's app shows.
          lastRelease: {
            tag: 'v0.2.0',
            commit: 'a04e6f1c2d3b4e5f60718293a4b5c6d7e8f90a1b',
            time: ago(3 * 3600),
            run: 'Succeeded',
            runURL: `${GITEA_WEB}/webshop/orders-api/actions/runs/12`,
            publish: {
              state: 'Failed',
              message: 'GitHub refused the token in Secret orders-api-github: 401 Bad credentials',
            },
            goModule: {
              state: 'Waiting',
              path: 'github.com/webshop-co/orders-api',
              version: 'v0.2.0',
              message:
                'Uploaded github.com/webshop-co/orders-api v0.2.0 to the registry, which go get reads only for theos.local/ paths; go get reads it from GitHub, where webshop-co/orders-api has no tag v0.2.0 yet: Published is Unauthorized, GitHub refused the token in Secret orders-api-github: 401 Bad credentials',
            },
          },
        },
        2,
        {
          tagReleases: true,
          publishReleases: ordersPublish,
        },
      ),
    ),
    // Paused by a person until the storefront has a framework that fits it.
    crew('storefront', 'Storefront crew', true, {
      phase: 'Paused',
      ready: [2, 3],
      // A tag pushed before the pause: its CI run is still going.
      lastRelease: {
        tag: 'v0.1.0',
        commit: '5d8e2a71c3b4e5f60718293a4b5c6d7e8f90a1b2',
        time: ago(8 * 60),
        run: 'Running',
        runURL: `${GITEA_WEB}/webshop/storefront/actions/runs/3`,
      },
      lastPolled: ago(40),
      conditions: [cond('Ready', 'True', 'Paused', 'Paused; not picking up issues', ago(3600))],
    }),
    crew('catalog-api', 'Catalog API crew', false, {
      phase: 'Waiting',
      conditions: [
        cond('Ready', 'False', 'ContainerNotReady', 'Waiting for Container catalog-api to be Ready', ago(600)),
      ],
    }),
  ]

  const frameworks = [catalog('Framework', 'python-fastapi', 'Python, FastAPI (uv)', ['service'], 'v1.3.0', created)]
  const connectors = [
    catalog('Connector', 'postgres', 'PostgreSQL', ['service', 'worker', 'job'], 'v1.1.0', created),
    catalog('Connector', 'redis', 'Redis', ['service', 'worker'], 'v1.0.0', created),
  ]

  const standards: Standard[] = [
    {
      apiVersion: API_VERSION,
      kind: 'Standard',
      metadata: meta('python-standard', created),
      spec: {
        displayName: 'Python standard (ruff, pytest)',
        description: 'Lint, format and test rules, and the CI that checks them.',
        supportedTypes: ['service', 'worker', 'job'],
        template: { url: `${GITEA}/templates/python-standard`, ref: 'v1.2.0' },
        supportedFrameworks: ['python-fastapi'],
        protectedPaths: ['ruff.toml', '.c4/checks/'],
      },
    },
  ]
  const computes = [catalog('Compute', 'small', 'Small: 1 replica, 256 MiB', ['service', 'worker'], 'v1.0.0', created)]
  const deployments = [
    catalog('Deployment', 'k8s-manifests', 'Kubernetes manifests', ['service', 'worker', 'job'], 'v1.1.0', created),
  ]
  // The entries a team picked together, as config/samples/webshop/bundle.yaml.
  const bundles: Bundle[] = [
    {
      apiVersion: API_VERSION,
      kind: 'Bundle',
      metadata: meta('python-service', created),
      spec: {
        displayName: 'Python service',
        description: 'A FastAPI service with the Python Standard, small sizing and plain Kubernetes manifests.',
        frameworkRef: { name: 'python-fastapi' },
        standardRef: { name: 'python-standard' },
        computeRef: { name: 'small' },
        deploymentRef: { name: 'k8s-manifests' },
      },
    },
  ]
  const domains: Domain[] = [
    {
      kind: 'Domain',
      metadata: meta('webshop', created),
      spec: {
        displayName: 'Webshop',
        description: 'What a webshop is: catalog, cart, checkout and orders, and the words the team uses for them.',
        template: { url: `${GITEA}/templates/webshop-domain`, ref: 'v1.0.0' },
      },
    },
  ]

  const gitProviders: GitProvider[] = [
    {
      apiVersion: API_VERSION,
      kind: 'GitProvider',
      metadata: meta('gitea', created),
      spec: { provider: 'gitea', url: GITEA, owner: 'webshop', credentialsSecretRef: { name: 'gitea-credentials' } },
    },
  ]
  const deployTargets: DeployTarget[] = [
    {
      apiVersion: API_VERSION,
      kind: 'DeployTarget',
      metadata: meta('staging', created),
      spec: {
        github: { repository: 'webshop-co/orders-staging' },
        credentialsSecretRef: { name: 'github-staging' },
        tier: 'development',
      },
      status: {
        checkedTime: ago(4 * 60),
        conditions: [
          cond('Ready', 'True', 'Ready', 'GitHub accepts the token for webshop-co/orders-staging', ago(3 * 3600)),
        ],
      },
    },
    {
      apiVersion: API_VERSION,
      kind: 'DeployTarget',
      metadata: meta('prod', created),
      spec: {
        github: { repository: 'webshop-co/orders-api' },
        credentialsSecretRef: { name: 'github-prod' },
        tier: 'production',
        parameters: { domain: 'orders.webshop.example' },
      },
      status: {
        checkedTime: ago(4 * 60),
        conditions: [
          cond('Ready', 'True', 'Ready', 'GitHub accepts the token for webshop-co/orders-api', ago(3 * 3600)),
        ],
      },
    },
  ]

  const briefs: Brief[] = [
    // Two rounds applied; a third prompt came in since and waits for the next.
    {
      apiVersion: API_VERSION,
      kind: 'Brief',
      metadata: meta('webshop', created),
      spec: {
        idea: 'An online shop where customers browse the catalog, order, and reorder what they bought before. Shop managers keep the catalog up to date.',
        prompts: [
          'Customers can reorder a past order with one click.',
          'Add gift cards that customers can buy and redeem.',
        ],
        agents: {
          planner: { name: 'claude' },
          implementer: { name: 'claude-implementer' },
          reviewer: { name: 'claude-reviewer' },
        },
        autopilot: 'Ask',
        releases: [{ container: 'orders-api', ...ordersPublish }],
      },
      status: {
        phase: 'Planned',
        round: 3,
        promptsPlanned: 1,
        choices: [
          'Reorder is a button on each past order in the storefront, served by orders-api.',
          'Past orders are read from orders-db; no new data store.',
          'The catalog API gets its own Crew once its container is Ready.',
        ],
        created: [
          { kind: 'SoftwareSystem', name: 'webshop-core' },
          { kind: 'Container', name: 'storefront' },
          { kind: 'Container', name: 'orders-api' },
          { kind: 'Container', name: 'catalog-api' },
          { kind: 'DataStore', name: 'orders-db' },
          { kind: 'DataStore', name: 'catalog-db' },
          { kind: 'Crew', name: 'orders-api' },
          { kind: 'Crew', name: 'storefront' },
        ],
        issues: [
          {
            repository: 'webshop/orders-api',
            number: 14,
            url: issueURL('orders-api', 14),
            title: 'Create an order from the cart',
          },
          {
            repository: 'webshop/orders-api',
            number: 15,
            url: issueURL('orders-api', 15),
            title: "List a customer's past orders",
          },
          {
            repository: 'webshop/orders-api',
            number: 16,
            url: issueURL('orders-api', 16),
            title: 'Reorder a past order',
          },
          { repository: 'webshop/storefront', number: 2, url: issueURL('storefront', 2), title: 'Product list page' },
          {
            repository: 'webshop/storefront',
            number: 3,
            url: issueURL('storefront', 3),
            title: 'Reorder button on past orders',
          },
        ],
        suggestionsWaiting: 2,
        suggestions: [
          {
            container: 'orders-api',
            number: 17,
            url: issueURL('orders-api', 17),
            title: 'Reject orders with a negative quantity',
            criteria: true,
          },
          {
            container: 'storefront',
            number: 4,
            url: issueURL('storefront', 4),
            title: 'Escape the product names on the list page',
          },
        ],
        // orders-api releases and publishes them to GitHub, where its token was refused; storefront's are off.
        apps: [
          {
            container: 'orders-api',
            crew: 'orders-api',
            releases: 'On',
            parallel: 3,
            publishTo: 'webshop-co/orders-api',
            lastRelease: {
              tag: 'v0.2.0',
              url: `${GITEA_WEB}/webshop/orders-api/releases/tag/v0.2.0`,
              time: ago(3 * 3600),
              run: 'Succeeded',
              publishState: 'Failed',
              moduleState: 'Waiting',
              modulePath: 'github.com/webshop-co/orders-api',
              moduleMessage:
                'Uploaded github.com/webshop-co/orders-api v0.2.0 to the registry, which go get reads only for theos.local/ paths; go get reads it from GitHub, where webshop-co/orders-api has no tag v0.2.0 yet: Published is Unauthorized, GitHub refused the token in Secret orders-api-github: 401 Bad credentials',
            },
            message:
              'Release v0.2.0 could not be published to webshop-co/orders-api; the Crew says why and tries again.',
          },
          {
            container: 'storefront',
            crew: 'storefront',
            releases: 'Off',
            parallel: 1,
            message: 'Releases are off: ask for them in a prompt, or name this app in spec.releases.',
          },
          {
            container: 'catalog-api',
            releases: 'Off',
            message: 'The app has no Crew yet, so nothing releases it.',
          },
        ],
        history: [
          {
            round: 1,
            summary: 'Planned the shop: storefront, orders and catalog',
            costMilliUSD: 312,
            outcome: 'Applied',
            tokens: {
              input: 2_100,
              output: 14_800,
              cacheRead: 486_000,
              cacheWrite: 61_000,
              largestSession: 74_000,
              sessionHandoffs: 1,
            },
          },
          {
            round: 2,
            summary: 'Added one-click reorder',
            costMilliUSD: 148,
            outcome: 'Applied',
            tokens: { input: 900, output: 6_200, cacheRead: 231_000, cacheWrite: 28_000, largestSession: 52_000 },
          },
          {
            round: 3,
            summary: 'Decided the failed reorder button',
            costMilliUSD: 41,
            outcome: 'Tended',
            tokens: { input: 300, output: 1_900, cacheRead: 64_000, cacheWrite: 9_000, largestSession: 31_000 },
          },
        ],
        // In Ask, the tending round's answer waits under Needs you.
        tended: [
          {
            container: 'storefront',
            number: 3,
            answer: 'ask',
            why: 'Its Task failed on the cart API, which orders-api#14 adds.',
            recommend: { answer: 'wait', why: 'orders-api#14 is in review.' },
            tries: 1,
            at: ago(20 * 60),
            round: 3,
          },
        ],
        needsYou: [
          {
            kind: 'Issue',
            name: 'storefront#3',
            question: 'Its Task failed on the cart API; try it again, or wait for orders-api#14?',
            recommend: 'Wait until orders-api#14 merges, then try it again.',
            why: 'The button calls the cart API that orders-api#14 adds.',
            since: ago(20 * 60),
          },
        ],
        needsYouWaiting: 1,
        // The agent's usage limit resets on its own: the Brief waits an hour past it.
        attention: [
          {
            kind: 'Task',
            name: 'orders-api-14',
            reason: 'Its agent is back after a usage limit.',
            actsAt: new Date(back + 60 * MIN).toISOString(),
          },
        ],
        lastTendTime: ago(25 * 60),
        tendRound: 3,
        repositoryURL: `${GITEA_WEB}/webshop/brief-webshop`,
        costMilliUSD: 501,
        tokens: {
          input: 3_300,
          output: 22_900,
          cacheRead: 781_000,
          cacheWrite: 98_000,
          largestSession: 74_000,
          sessionHandoffs: 1,
        },
        conditions: [
          cond('Planned', 'True', 'Applied', 'Round 3 is applied', ago(1800)),
          cond('NeedsInput', 'False', 'NothingAsked', 'Nothing is asked', ago(1800)),
        ],
      },
    },
    // The planner asked a question it can't answer itself.
    {
      apiVersion: API_VERSION,
      kind: 'Brief',
      metadata: meta('loyalty-points', ago(50 * 60)),
      spec: {
        idea: 'Customers collect loyalty points with every order and spend them at checkout.',
        agents: { planner: { name: 'claude' } },
      },
      status: {
        phase: 'NeedsInput',
        round: 1,
        choices: ['Points live in orders-db, next to the orders that earned them.'],
        history: [
          {
            round: 1,
            summary: 'Asked how points are paid out',
            costMilliUSD: 96,
            outcome: 'PlannerNeedsInput',
            tokens: { input: 600, output: 4_100, cacheRead: 152_000, cacheWrite: 19_000, largestSession: 38_000 },
          },
        ],
        repositoryURL: `${GITEA_WEB}/webshop/brief-loyalty-points`,
        costMilliUSD: 96,
        tokens: { input: 600, output: 4_100, cacheRead: 152_000, cacheWrite: 19_000, largestSession: 38_000 },
        conditions: [
          cond('Planned', 'False', 'NeedsInput', 'Round 1 waits for an answer', ago(40 * 60)),
          cond(
            'NeedsInput',
            'True',
            'Question',
            'Which payment provider pays out redeemed points, the existing one or a new one? Add a prompt that says which.',
            ago(40 * 60),
          ),
        ],
      },
    },
    // Paused before an upgrade: its Crews start nothing new, and one Task still runs.
    {
      apiVersion: API_VERSION,
      kind: 'Brief',
      metadata: meta('gift-cards', ago(3 * 3600)),
      spec: { idea: 'Customers buy gift cards and pay with them at checkout.', paused: true },
      status: {
        phase: 'Planned',
        round: 1,
        promptsPlanned: 0,
        choices: ['Gift cards get their own gift-cards-api, with balances in orders-db.'],
        created: [{ kind: 'Container', name: 'gift-cards-api' }],
        repositoryURL: `${GITEA_WEB}/webshop/brief-gift-cards`,
        conditions: [
          cond('Planned', 'True', 'Applied', 'Round 1 applied', ago(2 * 3600)),
          cond('Paused', 'True', 'Paused', 'Paused; 1 Task still running: gift-cards-api-3', ago(4 * 60)),
        ],
      },
    },
    {
      apiVersion: API_VERSION,
      kind: 'Brief',
      metadata: meta('returns', ago(5 * 60)),
      spec: { idea: 'Customers can return items within 30 days and get their money back.' },
      status: {
        phase: 'Planning',
        round: 1,
        repositoryURL: `${GITEA_WEB}/webshop/brief-returns`,
        conditions: [cond('Planned', 'False', 'Planning', 'Round 1 is being planned', ago(4 * 60))],
      },
    },
  ]

  return {
    namespace: DEMO_NAMESPACE,
    project,
    people,
    systems,
    containers,
    dataStores,
    relationships,
    sessions,
    frameworks,
    connectors,
    standards,
    computes,
    deployments,
    bundles,
    domains,
    gitProviders,
    deployTargets,
    crews,
    agents,
    tasks,
    briefs,
  }
}
