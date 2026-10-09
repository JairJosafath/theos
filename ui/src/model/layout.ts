// A small layered layout for the diagrams. Stories are small (tens of boxes), so
// a few well-known steps are enough and keep the output predictable:
//
//  1. Rank: every arrow points down a row where it can (longest path from the
//     sources, cycles broken, sources pulled down next to what they point to).
//  2. Order: sort each row by the mean position of each box's neighbours
//     (barycenter), a few sweeps up and down, to reduce crossings.
//  3. Place: rows centred on the widest one, each box nudged under its neighbours.
//  4. Route: smooth curves from the bottom of one box to the top of the next,
//     stepping around any box in a row they pass through, with a label placed
//     where it overlaps nothing else.
//
// A container view places the chosen system's parts inside a boundary box, with
// the people and systems that use it in a row above and those it uses below.

import type { Diagram, DiagramNode } from './story'
import { routeEdges } from './routing'

export interface Size {
  width: number
  height: number
}

export interface Rect extends Size {
  x: number
  y: number
}

export interface Point {
  x: number
  y: number
}

export interface EdgeRoute {
  id: string
  /** SVG path data. */
  path: string
  points: Point[]
  label: Point
}

export interface Layout {
  width: number
  height: number
  nodes: Record<string, Rect>
  edges: Record<string, EdgeRoute>
  boundary?: Rect
}

export interface LayoutOptions {
  sizeOf: (node: DiagramNode) => Size
  /** Estimated size of an edge label, used only to keep labels apart. */
  labelSize?: (text: string, lines: number) => Size
  nodeGap?: number
  rowGap?: number
  margin?: number
  /** Space inside the boundary: sides and bottom, and the top (for its title). */
  boundaryPadding?: number
  boundaryTitle?: number
  /** Size of an empty boundary (a system without containers). */
  emptyBoundary?: Size
}

const DEFAULTS = {
  nodeGap: 48,
  rowGap: 116,
  margin: 24,
  boundaryPadding: 24,
  boundaryTitle: 60,
  emptyBoundary: { width: 360, height: 120 },
}

/** Rough size of a 12px label wrapped at 180px; only needs to be close. */
export function estimateLabelSize(text: string, lines = 1): Size {
  const charWidth = 6.4
  const maxWidth = 180
  const textWidth = text.length * charWidth
  const wrapped = Math.max(1, Math.ceil(textWidth / (maxWidth - 12)))
  return {
    width: Math.min(maxWidth, Math.max(40, textWidth + 12)),
    height: (wrapped + lines - 1) * 16 + 6,
  }
}

type Graph = { succ: Map<string, string[]>; pred: Map<string, string[]> }

function buildGraph(ids: string[], edges: { source: string; target: string }[]): Graph {
  const succ = new Map(ids.map((id) => [id, [] as string[]]))
  const pred = new Map(ids.map((id) => [id, [] as string[]]))
  for (const { source, target } of edges) {
    if (source === target || !succ.has(source) || !succ.has(target)) continue
    if (!succ.get(source)!.includes(target)) succ.get(source)!.push(target)
    if (!pred.get(target)!.includes(source)) pred.get(target)!.push(source)
  }
  return { succ, pred }
}

/** Removes cycles by dropping the edges a depth-first search finds going back. */
function acyclic(ids: string[], graph: Graph): Graph {
  const state = new Map<string, 'visiting' | 'done'>()
  const succ = new Map(ids.map((id) => [id, [] as string[]]))
  const pred = new Map(ids.map((id) => [id, [] as string[]]))
  const visit = (id: string) => {
    state.set(id, 'visiting')
    for (const next of graph.succ.get(id) ?? []) {
      if (state.get(next) === 'visiting') continue // back edge: ignore for ranking
      succ.get(id)!.push(next)
      pred.get(next)!.push(id)
      if (!state.has(next)) visit(next)
    }
    state.set(id, 'done')
  }
  for (const id of ids) if (!state.has(id)) visit(id)
  return { succ, pred }
}

/**
 * Assigns each node a row. Sources start at row 0; every other node sits one row
 * below its lowest predecessor. A source that isn't a person then moves down to
 * sit just above the first thing it points to, so arrows stay short.
 */
export function rankNodes(
  ids: string[],
  edges: { source: string; target: string }[],
  isPerson: (id: string) => boolean,
): Map<string, number> {
  const graph = acyclic(ids, buildGraph(ids, edges))
  const rank = new Map<string, number>()
  const visiting = new Set<string>()
  const rankOf = (id: string): number => {
    const known = rank.get(id)
    if (known !== undefined) return known
    visiting.add(id)
    const preds = graph.pred.get(id)!.filter((p) => !visiting.has(p))
    const r = preds.length === 0 ? 0 : Math.max(...preds.map(rankOf)) + 1
    visiting.delete(id)
    rank.set(id, r)
    return r
  }
  ids.forEach(rankOf)

  const connected = (id: string) => graph.succ.get(id)!.length + graph.pred.get(id)!.length > 0
  // Pull sources down, deepest first so chains of sources follow.
  const sources = ids.filter(
    (id) => graph.pred.get(id)!.length === 0 && graph.succ.get(id)!.length > 0 && !isPerson(id),
  )
  sources.sort((a, b) => rank.get(b)! - rank.get(a)!)
  for (const id of sources) {
    const lowest = Math.min(...graph.succ.get(id)!.map((s) => rank.get(s)!))
    rank.set(id, Math.max(0, lowest - 1))
  }

  // Unconnected boxes: people on top, everything else on the row below them.
  const hasPeople = ids.some(isPerson)
  for (const id of ids) {
    if (!connected(id)) rank.set(id, isPerson(id) || !hasPeople ? 0 : 1)
  }

  // Close gaps left by moved nodes, so row numbers are 0..n-1.
  const used = [...new Set(rank.values())].sort((a, b) => a - b)
  const compact = new Map(used.map((r, i) => [r, i]))
  for (const [id, r] of rank) rank.set(id, compact.get(r)!)
  return rank
}

/** Orders the nodes within each row to reduce edge crossings (barycenter heuristic). */
export function orderRows(rows: string[][], edges: { source: string; target: string }[], sweeps = 4): string[][] {
  const neighbours = new Map<string, string[]>()
  const add = (a: string, b: string) => {
    if (!neighbours.has(a)) neighbours.set(a, [])
    neighbours.get(a)!.push(b)
  }
  for (const { source, target } of edges) {
    if (source === target) continue
    add(source, target)
    add(target, source)
  }
  const result = rows.map((row) => [...row])
  const rowOf = new Map<string, number>()
  result.forEach((row, r) => row.forEach((id) => rowOf.set(id, r)))
  const position = () => {
    const pos = new Map<string, number>()
    for (const row of result) row.forEach((id, i) => pos.set(id, (i + 0.5) / row.length))
    return pos
  }
  const sortRow = (r: number, useRow: (other: number) => boolean) => {
    const pos = position()
    const row = result[r]
    const key = new Map<string, number>()
    row.forEach((id) => {
      const ns = (neighbours.get(id) ?? []).filter((n) => rowOf.has(n) && useRow(rowOf.get(n)!))
      key.set(id, ns.length ? ns.reduce((sum, n) => sum + pos.get(n)!, 0) / ns.length : pos.get(id)!)
    })
    // Stable: ties keep their current order.
    result[r] = row
      .map((id, i) => ({ id, i }))
      .sort((a, b) => key.get(a.id)! - key.get(b.id)! || a.i - b.i)
      .map(({ id }) => id)
  }
  for (let s = 0; s < sweeps; s++) {
    for (let r = 1; r < result.length; r++) sortRow(r, (o) => o < r)
    for (let r = result.length - 2; r >= 0; r--) sortRow(r, (o) => o > r)
  }
  return result
}

/**
 * Places the boxes of one row left to right, each as close as it can get to the
 * x it wants (the centre of its neighbours), keeping at least `gap` between boxes
 * and their order.
 */
export function placeRow(
  widths: number[],
  desired: (number | undefined)[],
  gap: number,
  fallbackStart: number,
): number[] {
  const xs: number[] = []
  let cursor = -Infinity
  let x = fallbackStart
  widths.forEach((w, i) => {
    const want = desired[i] !== undefined ? desired[i]! - w / 2 : x
    const placed = Math.max(want, cursor)
    xs.push(placed)
    cursor = placed + w + gap
    x = cursor
  })
  // Everything was pushed right by the gaps; shift back by the mean error of the
  // boxes that had a wish, which keeps the order and the gaps.
  const errors = xs
    .map((placed, i) => (desired[i] !== undefined ? desired[i]! - widths[i] / 2 - placed : undefined))
    .filter((e): e is number => e !== undefined)
  const shift = errors.length ? errors.reduce((a, b) => a + b, 0) / errors.length : 0
  return xs.map((v) => v + Math.min(0, shift))
}

interface Block {
  rows: string[][]
  rects: Map<string, Rect>
  width: number
  height: number
}

/** Lays out rows of boxes: centred on the widest row, then aligned with neighbours. */
function placeRows(
  rows: string[][],
  sizes: Map<string, Size>,
  edges: { source: string; target: string }[],
  gap: number,
  rowGap: number,
): Block {
  const rowWidth = (row: string[]) =>
    row.reduce((sum, id) => sum + sizes.get(id)!.width, 0) + gap * Math.max(0, row.length - 1)
  const width = Math.max(0, ...rows.map(rowWidth))
  const rowHeights = rows.map((row) => Math.max(0, ...row.map((id) => sizes.get(id)!.height)))
  const rects = new Map<string, Rect>()
  let y = 0
  rows.forEach((row, r) => {
    let x = (width - rowWidth(row)) / 2
    for (const id of row) {
      const { width: w, height: h } = sizes.get(id)!
      rects.set(id, { x, y, width: w, height: h })
      x += w + gap
    }
    y += rowHeights[r] + rowGap
  })

  // Nudge each row (except the widest, which anchors the rest) towards the
  // centres of its neighbours, twice so both directions settle.
  const neighbours = new Map<string, string[]>()
  for (const { source, target } of edges) {
    if (!rects.has(source) || !rects.has(target) || source === target) continue
    neighbours.set(source, [...(neighbours.get(source) ?? []), target])
    neighbours.set(target, [...(neighbours.get(target) ?? []), source])
  }
  const widest = rows.findIndex((row) => rowWidth(row) === width)
  for (let pass = 0; pass < 2; pass++) {
    rows.forEach((row, r) => {
      if (r === widest) return
      const desired = row.map((id) => {
        const ns = (neighbours.get(id) ?? []).filter((n) => !row.includes(n))
        if (!ns.length) return undefined
        return ns.reduce((sum, n) => sum + rects.get(n)!.x + rects.get(n)!.width / 2, 0) / ns.length
      })
      const start = rects.get(row[0])!.x
      const xs = placeRow(
        row.map((id) => sizes.get(id)!.width),
        desired,
        gap,
        start,
      )
      row.forEach((id, i) => (rects.get(id)!.x = xs[i]))
    })
  }
  return normalise(rows, rects, rowHeights.reduce((a, b) => a + b, 0) + rowGap * Math.max(0, rows.length - 1))
}

/** Shifts a block so its left edge is at 0 and measures it. */
function normalise(rows: string[][], rects: Map<string, Rect>, height: number): Block {
  const all = [...rects.values()]
  const minX = Math.min(0, ...all.map((r) => r.x))
  for (const r of all) r.x -= minX
  const width = Math.max(0, ...all.map((r) => r.x + r.width))
  return { rows, rects, width, height }
}

function toRows(ids: string[], rank: Map<string, number>): string[][] {
  const rows: string[][] = []
  for (const id of ids) {
    const r = rank.get(id) ?? 0
    ;(rows[r] ??= []).push(id)
  }
  return rows.filter((row) => row && row.length)
}

/** Lays out a diagram: a plain layered layout, or a container view with a boundary. */
export function layoutDiagram(diagram: Diagram, options: LayoutOptions): Layout {
  const opts = { ...DEFAULTS, labelSize: estimateLabelSize, ...options }
  const sizes = new Map(diagram.nodes.map((n) => [n.id, opts.sizeOf(n)]))
  const isPerson = (id: string) => diagram.nodes.find((n) => n.id === id)?.role === 'person'
  const edges = diagram.edges

  let rects: Map<string, Rect>
  let boundary: Rect | undefined

  if (!diagram.boundary) {
    const ids = diagram.nodes.map((n) => n.id)
    const rows = orderRows(toRows(ids, rankNodes(ids, edges, isPerson)), edges)
    rects = placeRows(rows, sizes, edges, opts.nodeGap, opts.rowGap).rects
  } else {
    ;({ rects, boundary } = layoutWithBoundary(diagram, sizes, opts))
  }

  // Put everything behind a margin and measure the canvas.
  const all = [...rects.values(), ...(boundary ? [boundary] : [])]
  const minX = Math.min(...all.map((r) => r.x), 0)
  const minY = Math.min(...all.map((r) => r.y), 0)
  for (const r of all) {
    r.x += opts.margin - minX
    r.y += opts.margin - minY
  }
  const width = Math.max(...all.map((r) => r.x + r.width), 0) + opts.margin
  const height = Math.max(...all.map((r) => r.y + r.height), 0) + opts.margin

  const routes = routeEdges(diagram, rects, boundary, opts.labelSize)
  return {
    width: Math.ceil(width),
    height: Math.ceil(height),
    nodes: Object.fromEntries(rects),
    edges: routes,
    boundary,
  }
}

function layoutWithBoundary(
  diagram: Diagram,
  sizes: Map<string, Size>,
  opts: typeof DEFAULTS,
): { rects: Map<string, Rect>; boundary: Rect } {
  const b = diagram.boundary!
  const inside = new Set(b.nodeIds)
  const insideIds = diagram.nodes.filter((n) => inside.has(n.id)).map((n) => n.id)
  const outsideNodes = diagram.nodes.filter((n) => !inside.has(n.id))
  const edges = diagram.edges
  const innerEdges = edges.filter((e) => inside.has(e.source) && inside.has(e.target))

  // The system's parts, as a block of rows.
  const innerRows = orderRows(
    toRows(
      insideIds,
      rankNodes(insideIds, innerEdges, () => false),
    ),
    innerEdges,
  )
  const inner = insideIds.length
    ? placeRows(innerRows, sizes, innerEdges, opts.nodeGap, opts.rowGap)
    : {
        rows: [],
        rects: new Map<string, Rect>(),
        width: opts.emptyBoundary.width - 2 * opts.boundaryPadding,
        height: opts.emptyBoundary.height - opts.boundaryPadding - opts.boundaryTitle,
      }

  const boundary: Rect = {
    x: 0,
    y: 0,
    width: inner.width + 2 * opts.boundaryPadding,
    height: inner.height + opts.boundaryPadding + opts.boundaryTitle,
  }
  const rects = new Map<string, Rect>()
  for (const [id, r] of inner.rects) {
    rects.set(id, { ...r, x: r.x + opts.boundaryPadding, y: r.y + opts.boundaryTitle })
  }

  // People and other systems: above when they use this system, below when it
  // uses them. People always go above, as in any C4 diagram.
  const above: DiagramNode[] = []
  const below: DiagramNode[] = []
  for (const node of outsideNodes) {
    const sends = edges.some((e) => e.source === node.id)
    const receives = edges.some((e) => e.target === node.id)
    if (node.role === 'person' || (sends && !receives)) above.push(node)
    else below.push(node)
  }

  // Each outside box wants to sit over (or under) the parts it is linked with;
  // a link to the system as a whole points at the boundary's centre.
  const centreOf = (id: string) => {
    if (id === b.id) return boundary.x + boundary.width / 2
    const r = rects.get(id)
    return r ? r.x + r.width / 2 : undefined
  }
  const placeOutside = (nodes: DiagramNode[], y: (height: number) => number) => {
    const wishes = nodes.map((n) => {
      const others = edges
        .filter((e) => e.source === n.id || e.target === n.id)
        .map((e) => centreOf(e.source === n.id ? e.target : e.source))
        .filter((x): x is number => x !== undefined)
      return { node: n, want: others.length ? others.reduce((a, c) => a + c, 0) / others.length : undefined }
    })
    // Sort by where they want to be; stable for ties.
    wishes.sort((p, q) => (p.want ?? boundary.width / 2) - (q.want ?? boundary.width / 2))
    const widths = wishes.map((w) => sizes.get(w.node.id)!.width)
    const rowWidth = widths.reduce((a, c) => a + c, 0) + opts.nodeGap * Math.max(0, widths.length - 1)
    const xs = placeRow(
      widths,
      wishes.map((w) => w.want),
      opts.nodeGap,
      boundary.width / 2 - rowWidth / 2,
    )
    const rowHeight = Math.max(0, ...wishes.map((w) => sizes.get(w.node.id)!.height))
    wishes.forEach((w, i) => {
      const size = sizes.get(w.node.id)!
      rects.set(w.node.id, {
        x: xs[i],
        y: y(rowHeight) + (above.includes(w.node) ? rowHeight - size.height : 0),
        width: size.width,
        height: size.height,
      })
    })
  }
  placeOutside(above, (h) => -opts.rowGap - h)
  placeOutside(below, () => boundary.height + opts.rowGap)
  return { rects, boundary }
}
