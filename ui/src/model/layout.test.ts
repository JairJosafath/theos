import { describe, expect, it } from 'vitest'
import { demoStory } from '../demo/fixture'
import { layoutDiagram, orderRows, placeRow, rankNodes, type Rect } from './layout'
import { nodeSize } from './sizes'
import { buildContainerDiagram, buildContextDiagram, type Diagram } from './story'

const NOW = Date.parse('2026-09-30T12:00:00Z')

function overlap(a: Rect, b: Rect): boolean {
  return a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height
}

function inside(inner: Rect, outer: Rect): boolean {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.width <= outer.x + outer.width &&
    inner.y + inner.height <= outer.y + outer.height
  )
}

function expectNoOverlaps(rects: Rect[]) {
  for (let i = 0; i < rects.length; i++) {
    for (let j = i + 1; j < rects.length; j++) expect(overlap(rects[i], rects[j])).toBe(false)
  }
}

describe('rankNodes', () => {
  it('puts sources on top and each node one row below its lowest predecessor', () => {
    const ranks = rankNodes(
      ['p', 'a', 'b', 'c'],
      [
        { source: 'p', target: 'a' },
        { source: 'a', target: 'b' },
        { source: 'p', target: 'c' },
        { source: 'b', target: 'c' },
      ],
      (id) => id === 'p',
    )
    expect(Object.fromEntries(ranks)).toEqual({ p: 0, a: 1, b: 2, c: 3 })
  })

  it('pulls a source that is not a person down next to what it points to', () => {
    const ranks = rankNodes(
      ['a', 'b', 'c', 'w'],
      [
        { source: 'a', target: 'b' },
        { source: 'b', target: 'c' },
        { source: 'w', target: 'c' },
      ],
      () => false,
    )
    expect(ranks.get('w')).toBe(ranks.get('b'))
  })

  it('survives cycles and unconnected nodes', () => {
    const ranks = rankNodes(
      ['p', 'a', 'b', 'lonely'],
      [
        { source: 'a', target: 'b' },
        { source: 'b', target: 'a' },
      ],
      (id) => id === 'p',
    )
    expect(ranks.get('p')).toBe(0)
    expect(ranks.get('lonely')).toBe(1)
    expect(new Set([ranks.get('a'), ranks.get('b')]).size).toBe(2)
  })
})

describe('orderRows', () => {
  it('uncrosses edges between two rows', () => {
    const rows = orderRows(
      [
        ['a', 'b'],
        ['y', 'x'],
      ],
      [
        { source: 'a', target: 'x' },
        { source: 'b', target: 'y' },
      ],
    )
    expect(rows[1]).toEqual(['x', 'y'])
  })
})

describe('placeRow', () => {
  it('keeps order and gaps while getting close to where boxes want to be', () => {
    const xs = placeRow([100, 100], [50, 60], 20, 0)
    expect(xs[1] - xs[0]).toBeGreaterThanOrEqual(120)
    expect(xs[0]).toBeLessThan(0) // shifted left to share the error
  })
})

describe('layoutDiagram', () => {
  const opts = { sizeOf: nodeSize }

  function checkEdges(diagram: Diagram, layout: ReturnType<typeof layoutDiagram>) {
    for (const e of diagram.edges) {
      const route = layout.edges[e.id]
      expect(route, e.id).toBeDefined()
      expect(route.path).toMatch(/^M [\d.]+ [\d.]+/)
      expect(Number.isFinite(route.label.x) && Number.isFinite(route.label.y)).toBe(true)
    }
  }

  it('lays out the context view: people above systems, nothing overlapping, all on the canvas', () => {
    const diagram = buildContextDiagram(demoStory(NOW))
    const layout = layoutDiagram(diagram, opts)
    const rects = Object.values(layout.nodes)
    expectNoOverlaps(rects)
    for (const r of rects) expect(inside(r, { x: 0, y: 0, width: layout.width, height: layout.height })).toBe(true)
    const y = (id: string) => layout.nodes[id].y
    expect(y('Person/customer')).toBeLessThan(y('SoftwareSystem/webshop-core'))
    expect(y('SoftwareSystem/webshop-core')).toBeLessThan(y('SoftwareSystem/payment-provider'))
    checkEdges(diagram, layout)
  })

  it('lays out the container view: parts inside the boundary, people above, used systems below', () => {
    const diagram = buildContainerDiagram(demoStory(NOW), 'webshop-core')!
    const layout = layoutDiagram(diagram, opts)
    const boundary = layout.boundary!
    for (const id of diagram.boundary!.nodeIds) expect(inside(layout.nodes[id], boundary), id).toBe(true)
    for (const n of diagram.nodes.filter((n) => !diagram.boundary!.nodeIds.includes(n.id))) {
      const r = layout.nodes[n.id]
      expect(overlap(r, boundary), n.id).toBe(false)
      if (n.role === 'person') expect(r.y + r.height).toBeLessThanOrEqual(boundary.y)
      else expect(r.y).toBeGreaterThanOrEqual(boundary.y + boundary.height)
    }
    expectNoOverlaps(Object.values(layout.nodes))
    checkEdges(diagram, layout)
    // An arrow into the system as a whole ends on the boundary's top edge.
    const route = layout.edges['Person/customer->SoftwareSystem/webshop-core']
    expect(route.points.at(-1)!.y).toBe(boundary.y)
  })

  it('routes an edge around a box in a row it passes through', () => {
    const diagram = buildContainerDiagram(demoStory(NOW), 'webshop-core')!
    const layout = layoutDiagram(diagram, opts)
    const others = Object.entries(layout.nodes)
    // The demo has arrows that cross the data store row to reach systems below.
    expect(diagram.edges.filter((e) => layout.edges[e.id].points.length > 2).length).toBeGreaterThan(0)
    for (const e of diagram.edges) {
      const points = layout.edges[e.id].points
      // Waypoints come in pairs around each row the edge crosses; the vertical
      // run between them must not go through any box.
      for (let i = 1; i + 1 < points.length - 1; i += 2) {
        const [a, b] = [points[i], points[i + 1]]
        for (const [id, r] of others) {
          if (id === e.source || id === e.target) continue
          const hits =
            a.x > r.x && a.x < r.x + r.width && Math.min(a.y, b.y) < r.y + r.height && Math.max(a.y, b.y) > r.y
          expect(hits, `${e.id} through ${id}`).toBe(false)
        }
      }
    }
  })

  it('keeps edge labels apart', () => {
    const diagram = buildContainerDiagram(demoStory(NOW), 'webshop-core')!
    const layout = layoutDiagram(diagram, opts)
    const labels = diagram.edges.map((e) => {
      const { x, y } = layout.edges[e.id].label
      return { x: x - 50, y: y - 8, width: 100, height: 16 }
    })
    expectNoOverlaps(labels)
  })

  it('is deterministic', () => {
    const a = layoutDiagram(buildContainerDiagram(demoStory(NOW), 'webshop-core')!, opts)
    const b = layoutDiagram(buildContainerDiagram(demoStory(NOW), 'webshop-core')!, opts)
    expect(a).toEqual(b)
  })

  it('lays out an empty system as an empty boundary', () => {
    const diagram = buildContainerDiagram(demoStory(NOW), 'warehouse')!
    const layout = layoutDiagram(diagram, opts)
    expect(layout.boundary!.width).toBeGreaterThan(0)
    checkEdges(diagram, layout)
  })

  it('lays out nothing as an empty canvas', () => {
    const layout = layoutDiagram({ nodes: [], edges: [], looseEnds: [] }, opts)
    expect(layout.nodes).toEqual({})
    expect(layout.width).toBeGreaterThan(0)
  })
})
