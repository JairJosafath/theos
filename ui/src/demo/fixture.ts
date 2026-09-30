// The webshop story with every state the UI can show: containers in all four
// phases, a failed type check, agent and human Sessions (one Stale), and a
// Relationship added after scaffolding. Used by demo mode (?demo=1), so the whole
// UI can be seen and screenshotted without writing to a cluster.

import type { StoryData } from '../k8s/sources'
import type {
  CatalogEntry,
  Condition,
  Container,
  DataStore,
  Person,
  Project,
  Relationship,
  Session,
  SoftwareSystem,
} from '../k8s/types'

export const DEMO_NAMESPACE = 'webshop'

const API_VERSION = 'c4.factory.io/v1alpha1'
const GITEA = 'http://gitea.gitea.svc.cluster.local:3000'

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
    spec: { displayName: 'Webshop', description: 'An online shop where customers browse, order and reorder.' },
    status: {
      softwareSystems: 3,
      containers: 4,
      readyContainers: 1,
      activeSessions: 2,
      summary: '1 of 4 containers Ready, 2 active sessions',
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
      },
      status: {
        containers: 4,
        readyContainers: 1,
        dataStores: 3,
        activeSessions: 2,
        summary: '1 of 4 containers Ready, 2 active sessions',
      },
    },
    {
      kind: 'SoftwareSystem',
      metadata: meta('payment-provider', created),
      spec: {
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
    spec: Partial<Container['spec']> & Pick<Container['spec'], 'type' | 'frameworkRef'>,
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
        computeRef: { name: 'small' },
        deploymentRef: { name: 'k8s-manifests' },
      },
      {
        phase: 'Ready',
        repositoryURL: `${GITEA}/webshop/orders-api`,
        scaffoldedFrom: ['python-fastapi@v1.2.0', 'small@v1.0.0', 'k8s-manifests@v1.0.0', 'postgres@v1.1.0'],
        appliedRelationships: ['orders-api-to-orders-db'],
        activeSessions: 1,
        conditions: [
          cond('ReferencesResolved', 'True', 'Resolved', 'All references exist', ago(10700)),
          cond('ChoicesValid', 'True', 'Valid', 'Every catalog entry supports service', ago(10700)),
          cond('RepositoryCreated', 'True', 'Created', `Created ${GITEA}/webshop/orders-api`, ago(10690)),
          cond('Scaffolded', 'True', 'Scaffolded', 'Pushed the initial commit', ago(10680)),
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

  const frameworks = [catalog('Framework', 'python-fastapi', 'Python, FastAPI (uv)', ['service'], 'v1.2.0', created)]
  const connectors = [
    catalog('Connector', 'postgres', 'PostgreSQL', ['service', 'worker', 'job'], 'v1.1.0', created),
    catalog('Connector', 'redis', 'Redis', ['service', 'worker'], 'v1.0.0', created),
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
  }
}
