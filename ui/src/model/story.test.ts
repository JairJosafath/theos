import { describe, expect, it } from 'vitest'
import { demoStory } from '../demo/fixture'
import { emptyStory, type StoryData } from '../k8s/sources'
import type { Relationship } from '../k8s/types'
import {
  buildContainerDiagram,
  buildContextDiagram,
  containerNodes,
  linksOf,
  sessionsOf,
  systemsForPicker,
  workingNow,
  type ContainerNode,
  type SystemNode,
} from './story'

const NOW = Date.parse('2026-09-30T12:00:00Z')

/** A small webshop story, as the API returns it before the operator has run. */
function sampleStory(): StoryData {
  const meta = (name: string) => ({ name, namespace: 'webshop' })
  return {
    ...emptyStory('webshop'),
    project: { metadata: meta('webshop'), spec: { displayName: 'Webshop' } },
    people: [
      { metadata: meta('customer'), spec: { displayName: 'Customer', description: 'Someone who buys from the shop.' } },
    ],
    systems: [
      {
        metadata: meta('webshop-core'),
        spec: {
          displayName: 'Webshop Core',
          description: 'Catalog, cart and ordering',
          needs: ['Customers can reorder in two clicks'],
          kpis: ['Checkout conversion'],
        },
      },
    ],
    containers: [
      {
        metadata: meta('orders-api'),
        spec: {
          softwareSystemRef: { name: 'webshop-core' },
          displayName: 'Orders API',
          type: 'service',
          gitProviderRef: { name: 'gitea' },
          frameworkRef: { name: 'python-fastapi' },
        },
      },
    ],
    dataStores: [
      {
        metadata: meta('orders-db'),
        spec: {
          softwareSystemRef: { name: 'webshop-core' },
          displayName: 'Orders DB',
          connectorRef: { name: 'postgres' },
        },
      },
    ],
    relationships: [
      {
        metadata: meta('customer-uses-webshop-core'),
        spec: {
          source: { kind: 'Person', name: 'customer' },
          target: { kind: 'SoftwareSystem', name: 'webshop-core' },
          description: 'Browses, orders and reorders',
        },
      },
      {
        metadata: meta('orders-api-to-orders-db'),
        spec: {
          source: { kind: 'Container', name: 'orders-api' },
          target: { kind: 'DataStore', name: 'orders-db' },
          description: 'Reads and writes orders',
        },
      },
    ],
  }
}

function rel(
  name: string,
  source: Relationship['spec']['source'],
  target: Relationship['spec']['target'],
  description = '',
): Relationship {
  return { metadata: { name, namespace: 'webshop' }, spec: { source, target, description } }
}

describe('buildContextDiagram', () => {
  it('draws the sample: a customer using one system, even with no status at all', () => {
    const d = buildContextDiagram(sampleStory())
    expect(d.nodes.map((n) => n.id)).toEqual(['Person/customer', 'SoftwareSystem/webshop-core'])
    expect(d.edges).toEqual([
      expect.objectContaining({
        source: 'Person/customer',
        target: 'SoftwareSystem/webshop-core',
        label: 'Browses, orders and reorders',
        implied: false,
      }),
    ])
    // The Container to DataStore link is inside one system: not at this level.
    expect(d.looseEnds).toEqual([])
    const system = d.nodes[1] as SystemNode
    expect(system.needs).toEqual(['Customers can reorder in two clicks'])
    expect(system.kpis).toEqual(['Checkout conversion'])
    // No roll-up from the operator yet: counts from the model instead.
    expect(system.summary).toBe('1 container, 1 data store')
    expect(system.summaryReported).toBe(false)
  })

  it("uses the operator's summary once it is there", () => {
    const data = sampleStory()
    data.systems[0].status = { summary: '1 of 1 container Ready, 1 active session' }
    const system = buildContextDiagram(data).nodes[1] as SystemNode
    expect(system.summary).toBe('1 of 1 container Ready, 1 active session')
    expect(system.summaryReported).toBe(true)
  })

  it('draws an External system as another system, and the rest as systems', () => {
    const d = buildContextDiagram(demoStory(NOW))
    const systems = d.nodes.filter((n): n is SystemNode => n.kind === 'SoftwareSystem')
    expect(systems.map((s) => `${s.role}:${s.name}`).sort()).toEqual([
      'external-system:payment-provider',
      'system:warehouse',
      'system:webshop-core',
    ])
    expect(systems.find((s) => s.name === 'payment-provider')!.external).toBe(true)
  })

  it('rolls links between parts of different systems up to those systems, one arrow per pair', () => {
    const d = buildContextDiagram(demoStory(NOW))
    const byPair = Object.fromEntries(d.edges.map((e) => [`${e.source} -> ${e.target}`, e]))
    expect(byPair['SoftwareSystem/webshop-core -> SoftwareSystem/payment-provider']).toMatchObject({
      label: 'Charges the customer',
      implied: true,
      relationships: ['orders-api-to-payment-provider'],
    })
    expect(Object.keys(byPair)).toHaveLength(4)
  })

  it('prefers an explicit description and counts the rest', () => {
    const data = sampleStory()
    data.relationships.push(
      rel(
        'customer-uses-orders-api',
        { kind: 'Person', name: 'customer' },
        { kind: 'Container', name: 'orders-api' },
        'Checks an order',
      ),
    )
    const [edge] = buildContextDiagram(data).edges
    expect(edge.label).toBe('Browses, orders and reorders (+1 more)')
    expect(edge.implied).toBe(false)
    expect(edge.relationships).toHaveLength(2)
  })

  it('reports links and parts it cannot draw instead of failing', () => {
    const data = sampleStory()
    data.relationships.push(
      rel('to-nowhere', { kind: 'Person', name: 'customer' }, { kind: 'SoftwareSystem', name: 'ghost' }, 'Haunts'),
    )
    data.containers.push({
      ...data.containers[0],
      metadata: { name: 'orphan' },
      spec: { ...data.containers[0].spec, softwareSystemRef: { name: 'gone' } },
    })
    const d = buildContextDiagram(data)
    expect(d.looseEnds).toEqual([
      'The link "Haunts" points to system "ghost", which does not exist.',
      'Container "Orders API" belongs to system "gone", which does not exist.',
    ])
  })

  it('draws an empty project as nothing, without errors', () => {
    expect(buildContextDiagram(emptyStory('x'))).toEqual({ nodes: [], edges: [], looseEnds: [] })
  })
})

describe('buildContainerDiagram', () => {
  it('draws the sample: parts inside the boundary, the customer outside linked to the boundary', () => {
    const d = buildContainerDiagram(sampleStory(), 'webshop-core')!
    expect(d.boundary).toMatchObject({ id: 'SoftwareSystem/webshop-core', title: 'Webshop Core' })
    expect(d.boundary!.nodeIds.sort()).toEqual(['Container/orders-api', 'DataStore/orders-db'])
    expect(d.nodes.map((n) => n.id)).toEqual(['Person/customer', 'Container/orders-api', 'DataStore/orders-db'])
    expect(d.edges.map((e) => `${e.source} -> ${e.target}`)).toEqual([
      'Container/orders-api -> DataStore/orders-db',
      'Person/customer -> SoftwareSystem/webshop-core',
    ])
    const container = d.nodes[1] as ContainerNode
    expect(container).toMatchObject({ phase: undefined, typeLabel: 'service', scaffoldedFrom: [], activeSessions: 0 })
  })

  it('shows other systems as outside boxes, and every phase of the demo', () => {
    const d = buildContainerDiagram(demoStory(NOW), 'webshop-core')!
    const outside = d.nodes.filter((n) => !d.boundary!.nodeIds.includes(n.id)).map((n) => `${n.role}:${n.name}`)
    expect(outside.sort()).toEqual([
      'external-system:payment-provider',
      'external-system:warehouse',
      'person:customer',
      'person:shop-manager',
    ])
    const phases = d.nodes.filter((n): n is ContainerNode => n.role === 'container').map((c) => c.phase)
    expect(phases.sort()).toEqual(['Failed', 'Pending', 'Ready', 'Ready', 'Scaffolding'])
    // Both sit outside; only the one the factory doesn't build says so.
    const external = Object.fromEntries(
      d.nodes.filter((n): n is SystemNode => n.kind === 'SoftwareSystem').map((n) => [n.name, n.external]),
    )
    expect(external).toEqual({ 'payment-provider': true, warehouse: false })
  })

  it('marks a link added after scaffolding, and only that one', () => {
    const d = buildContainerDiagram(demoStory(NOW), 'webshop-core')!
    const marked = d.edges.filter((e) => e.notApplied)
    expect(marked.map((e) => e.id)).toEqual(['Container/orders-api->DataStore/cart-cache'])
    expect(marked[0].notApplied!.message).toMatch(/scaffolded before/)
  })

  it("rolls another system's parts up to that system", () => {
    const data = demoStory(NOW)
    data.containers.push({
      metadata: { name: 'psp-gateway' },
      spec: { ...data.containers[0].spec, softwareSystemRef: { name: 'payment-provider' } },
    })
    data.relationships.push(
      rel(
        'orders-api-to-psp-gateway',
        { kind: 'Container', name: 'orders-api' },
        { kind: 'Container', name: 'psp-gateway' },
        'Calls the gateway',
      ),
    )
    const d = buildContainerDiagram(data, 'webshop-core')!
    const edge = d.edges.find((e) => e.id === 'Container/orders-api->SoftwareSystem/payment-provider')!
    expect(edge.relationships).toEqual(['orders-api-to-payment-provider', 'orders-api-to-psp-gateway'])
    expect(d.nodes.some((n) => n.id === 'Container/psp-gateway')).toBe(false)
  })

  it('draws a system with no parts as an empty boundary with its users', () => {
    const d = buildContainerDiagram(demoStory(NOW), 'payment-provider')!
    expect(d.boundary!.nodeIds).toEqual([])
    expect(d.edges.map((e) => `${e.source} -> ${e.target}`)).toEqual([
      'SoftwareSystem/webshop-core -> SoftwareSystem/payment-provider',
    ])
  })

  it('returns undefined for a system that does not exist', () => {
    expect(buildContainerDiagram(sampleStory(), 'nope')).toBeUndefined()
  })

  it('reports a link from one of its parts to something missing', () => {
    const data = sampleStory()
    data.relationships.push(
      rel('api-to-cache', { kind: 'Container', name: 'orders-api' }, { kind: 'DataStore', name: 'cache' }, 'Caches'),
    )
    expect(buildContainerDiagram(data, 'webshop-core')!.looseEnds).toEqual([
      'The link "Caches" points to data store "cache", which does not exist.',
    ])
  })
})

describe('sessions', () => {
  it('lists Active sessions first, then by latest heartbeat, and counts only Active ones', () => {
    const data = demoStory(NOW)
    const sessions = sessionsOf(data.sessions, 'orders-api')
    expect(sessions.map((s) => s.status?.phase)).toEqual(['Active', 'Stale'])
    const api = containerNodes(data, 'webshop-core').find((c) => c.name === 'orders-api')!
    expect(api.activeSessions).toBe(1)
    expect(containerNodes(data).map((c) => c.name)).toEqual([
      'catalog-api',
      'inventory-api',
      'order-worker',
      'orders-api',
      'storefront',
    ])
  })

  it('titles a bundled Container’s framework from status.bundle (#1624)', () => {
    const data = demoStory(NOW)
    const api = containerNodes(data, 'webshop-core').find((c) => c.name === 'inventory-api')!
    expect(api.framework).toBe('Python, FastAPI (uv)')
  })
})

describe('crews', () => {
  it("gives a Container its Crew, with the Crew's Agents and the issue it works on", () => {
    const data = sampleStory()
    const meta = (name: string) => ({ name, namespace: 'webshop' })
    data.crews = [
      {
        metadata: meta('orders-api'),
        spec: {
          containerRef: { name: 'orders-api' },
          implementer: { name: 'claude-implementer' },
          reviewer: { name: 'claude-reviewer' },
        },
        status: { phase: 'Working', activeTasks: ['orders-api-4'], ready: [5], waiting: [6, 7] },
      },
      {
        metadata: meta('z-second'),
        spec: { containerRef: { name: 'orders-api' }, implementer: { name: 'x' }, reviewer: { name: 'y' } },
      },
    ]
    data.agents = [
      { metadata: meta('claude-implementer'), spec: { image: 'agent', model: 'claude-sonnet-5-5' } },
      { metadata: meta('claude-reviewer'), spec: { image: 'agent', model: 'claude-opus-5-5' } },
    ]
    data.tasks = [
      {
        metadata: meta('orders-api-4'),
        spec: { containerRef: { name: 'orders-api' }, agentRef: { name: 'claude-implementer' }, issue: 4 },
        status: { issueTitle: 'Validate orders' },
      },
    ]
    const [api] = containerNodes(data, 'webshop-core')
    expect(api.crew).toMatchObject({
      name: 'orders-api',
      phase: 'Working',
      implementer: { name: 'claude-implementer', model: 'claude-sonnet-5-5' },
      reviewer: { name: 'claude-reviewer', model: 'claude-opus-5-5' },
      active: [{ issue: 4, headline: '#4 Validate orders' }],
      ready: [5],
      waiting: [6, 7],
    })
  })

  it('leaves a Container without a Crew alone', () => {
    expect(containerNodes(sampleStory(), 'webshop-core')[0].crew).toBeUndefined()
  })
})

describe('linksOf and systemsForPicker', () => {
  it('lists links both ways with their Applied condition', () => {
    const links = linksOf(demoStory(NOW), 'DataStore', 'cart-cache')
    expect(links).toEqual([
      expect.objectContaining({
        direction: 'in',
        other: { kind: 'Container', name: 'orders-api', title: 'Orders API', exists: true },
        applied: expect.objectContaining({ status: 'False', reason: 'AddedAfterScaffold' }),
      }),
    ])
  })

  it('offers systems with the most containers first', () => {
    expect(systemsForPicker(demoStory(NOW)).map((s) => s.name)).toEqual([
      'webshop-core',
      'payment-provider',
      'warehouse',
    ])
  })
})

describe('two rows with one title (#175)', () => {
  const twins = (kind: 'systems' | 'containers', names: string[]): StoryData => {
    const story = sampleStory()
    const meta = (name: string) => ({ name, namespace: 'webshop' })
    if (kind === 'systems') {
      story.systems = names.map((name) => ({ metadata: meta(name), spec: { displayName: 'Same title' } }))
      story.containers = []
    } else {
      story.containers = names.map((name) => ({
        metadata: meta(name),
        spec: {
          softwareSystemRef: { name: 'webshop-core' },
          displayName: 'Same title',
          type: 'service',
          gitProviderRef: { name: 'gitea' },
          frameworkRef: { name: 'go' },
        },
      }))
    }
    return story
  }

  it('offers systems of one title and one number of Containers in one order', () => {
    const picked = (names: string[]) => systemsForPicker(twins('systems', names)).map((s) => s.name)
    expect(picked(['b-system', 'a-system'])).toEqual(['a-system', 'b-system'])
    expect(picked(['a-system', 'b-system'])).toEqual(['a-system', 'b-system'])
  })

  it('lists Container cards of one title in one order', () => {
    const cards = (names: string[]) => containerNodes(twins('containers', names)).map((c) => c.name)
    expect(cards(['b-api', 'a-api'])).toEqual(['a-api', 'b-api'])
    expect(cards(['a-api', 'b-api'])).toEqual(['a-api', 'b-api'])
  })
})

describe('linksOf for a system', () => {
  it("includes its parts' links with the outside world, and skips links inside it", () => {
    const links = linksOf(demoStory(NOW), 'SoftwareSystem', 'webshop-core')
    expect(links.map((l) => `${l.direction} ${l.other.name}${l.via ? ` via ${l.via}` : ''}`)).toEqual([
      'in customer',
      'out warehouse via Order Worker',
      'out payment-provider via Orders API',
      'in shop-manager',
    ])
  })
})

describe('workingNow', () => {
  it('counts active Sessions and the Tasks that Crews are working on', () => {
    expect(workingNow(demoStory(NOW))).toBe(4)
    expect(workingNow(sampleStory())).toBe(0)
  })
})
