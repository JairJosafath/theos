// Edge routing for the diagram layout: smooth curves from the bottom of one box
// to the top of the next, stepping around any box in a row they pass through,
// with a label placed where it overlaps nothing else.

import type { Diagram } from './story'
import type { EdgeRoute, Point, Rect, Size } from './layout'

type Side = 'top' | 'bottom' | 'left' | 'right'

function centre(r: Rect): Point {
  return { x: r.x + r.width / 2, y: r.y + r.height / 2 }
}

/** The point at t on a cubic Bezier with vertical tangents from p to q. */
function curvePoint(p: Point, q: Point, t: number): Point {
  const dy = (q.y - p.y) / 2
  const c1 = { x: p.x, y: p.y + dy }
  const c2 = { x: q.x, y: q.y - dy }
  const u = 1 - t
  return {
    x: u * u * u * p.x + 3 * u * u * t * c1.x + 3 * u * t * t * c2.x + t * t * t * q.x,
    y: u * u * u * p.y + 3 * u * u * t * c1.y + 3 * u * t * t * c2.y + t * t * t * q.y,
  }
}

/** A smooth path through points, with vertical tangents at each one. */
function verticalCurve(points: Point[]): string {
  let d = `M ${r1(points[0].x)} ${r1(points[0].y)}`
  for (let i = 1; i < points.length; i++) {
    const p = points[i - 1]
    const q = points[i]
    const dy = (q.y - p.y) / 2
    d += ` C ${r1(p.x)} ${r1(p.y + dy)}, ${r1(q.x)} ${r1(q.y - dy)}, ${r1(q.x)} ${r1(q.y)}`
  }
  return d
}

function r1(v: number): number {
  return Math.round(v * 10) / 10
}

export function routeEdges(
  diagram: Diagram,
  rects: Map<string, Rect>,
  boundary: Rect | undefined,
  labelSize: (text: string, lines: number) => Size,
): Record<string, EdgeRoute> {
  const boundaryId = diagram.boundary?.id
  const rectOf = (id: string) => (id === boundaryId ? boundary : rects.get(id))

  // Decide which side of each box every edge leaves or enters by.
  type End = { edge: string; node: string; side: Side; other: Point }
  const ends: End[] = []
  const plan = new Map<string, { from: Side; to: Side }>()
  for (const e of diagram.edges) {
    const s = rectOf(e.source)
    const t = rectOf(e.target)
    if (!s || !t) continue
    let from: Side
    let to: Side
    if (e.target === boundaryId || e.source === boundaryId) {
      // Links with the system as a whole meet the boundary at its top or bottom.
      const outside = e.target === boundaryId ? s : t
      const outsideAbove = outside.y + outside.height <= boundary!.y
      const outsideSide: Side = outsideAbove ? 'bottom' : 'top'
      const boundarySide: Side = outsideAbove ? 'top' : 'bottom'
      ;[from, to] = e.target === boundaryId ? [outsideSide, boundarySide] : [boundarySide, outsideSide]
    } else if (t.y >= s.y + s.height - 1) {
      ;[from, to] = ['bottom', 'top']
    } else if (t.y + t.height <= s.y + 1) {
      ;[from, to] = ['top', 'bottom']
    } else {
      ;[from, to] = t.x > s.x ? ['right', 'left'] : ['left', 'right']
    }
    plan.set(e.id, { from, to })
    ends.push({ edge: e.id, node: e.source, side: from, other: centre(t) })
    ends.push({ edge: e.id, node: e.target, side: to, other: centre(s) })
  }

  // Spread the edges that share a side of a box, ordered by where they go, so
  // they don't start on top of each other or cross right at the box.
  const port = new Map<string, Point>()
  const groups = new Map<string, End[]>()
  for (const end of ends) {
    const key = `${end.node}|${end.side}`
    groups.set(key, [...(groups.get(key) ?? []), end])
  }
  for (const group of groups.values()) {
    const r = rectOf(group[0].node)!
    const side = group[0].side
    const horizontal = side === 'top' || side === 'bottom'
    group.sort((a, b) => (horizontal ? a.other.x - b.other.x : a.other.y - b.other.y))
    group.forEach((end, i) => {
      const key = `${end.edge}|${end.node}|${end.side}`
      let p: Point
      if (group[0].node === boundaryId) {
        // On the boundary, meet it straight across from the box outside.
        const x = Math.min(r.x + r.width - 24, Math.max(r.x + 24, end.other.x))
        p = { x, y: side === 'top' ? r.y : r.y + r.height }
      } else if (horizontal) {
        const step = Math.min(36, (r.width * 0.7) / group.length)
        const x = r.x + r.width / 2 + (i - (group.length - 1) / 2) * step
        p = { x, y: side === 'top' ? r.y : r.y + r.height }
      } else {
        const step = Math.min(24, (r.height * 0.7) / group.length)
        const y = r.y + r.height / 2 + (i - (group.length - 1) / 2) * step
        p = { x: side === 'left' ? r.x : r.x + r.width, y }
      }
      port.set(key, p)
    })
  }
  const portOf = (edge: string, node: string, side: Side) => port.get(`${edge}|${node}|${side}`)!

  // Boxes an edge must not cross or put its label on. The boundary's title is
  // one too.
  const obstacles = [...rects.entries()]
  const labelObstacles = [...rects.values()]
  if (boundary) labelObstacles.push({ x: boundary.x + 8, y: boundary.y + 6, width: 220, height: 40 })

  const routes: Record<string, EdgeRoute> = {}
  const placedLabels: Rect[] = []
  for (const e of diagram.edges) {
    const p = plan.get(e.id)
    if (!p) continue
    const start = portOf(e.id, e.source, p.from)
    const end = portOf(e.id, e.target, p.to)
    let points: Point[]
    let path: string
    if (p.from === 'left' || p.from === 'right') {
      points = [start, end]
      path = `M ${r1(start.x)} ${r1(start.y)} L ${r1(end.x)} ${r1(end.y)}`
    } else {
      points = [start, ...throughRows(start, end, e.source, e.target, obstacles), end]
      path = verticalCurve(points)
    }

    let label = curvePoint(points[0], points[1], 0.5)
    if (e.label || e.notApplied) {
      const size = labelSize(e.label || ' ', e.notApplied ? 2 : 1)
      // The "added after scaffolding" note stays on one line (see .edge-note).
      if (e.notApplied) size.width = Math.max(size.width, 236)
      // Leaving the system for a box outside it: the label reads best near
      // that box, in the open space below the boundary.
      const leavesBoundary =
        boundary !== undefined &&
        !diagram.boundary!.nodeIds.includes(e.target) &&
        diagram.boundary!.nodeIds.includes(e.source)
      label = placeLabel(points, size, placedLabels, labelObstacles, leavesBoundary)
      placedLabels.push(boxAround(label, size))
    }
    routes[e.id] = { id: e.id, path, points, label }
  }
  return routes
}

function boxAround(p: Point, size: Size): Rect {
  return { x: p.x - size.width / 2, y: p.y - size.height / 2, width: size.width, height: size.height }
}

function overlapArea(a: Rect, b: Rect, pad: number): number {
  const w = Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x) + pad
  const h = Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y) + pad
  return w > 0 && h > 0 ? w * h : 0
}

/**
 * Picks where an edge's label goes: on the edge in a gap between rows, where it
 * overlaps no other label or box, or else where it overlaps least. Points come
 * in pairs around each row the edge passes, so even segments are the gaps.
 */
function placeLabel(points: Point[], size: Size, placed: Rect[], obstacles: Rect[], preferLast: boolean): Point {
  const segments = points.slice(1).map((q, i) => ({ a: points[i], b: q, i }))
  const gaps = segments.filter((s) => s.i % 2 === 0)
  const rows = segments.filter((s) => s.i % 2 === 1)
  if (preferLast) gaps.reverse()

  const candidates: Point[] = []
  for (const t of [0.5, 0.4, 0.6, 0.3, 0.7]) {
    for (const s of gaps) candidates.push(curvePoint(s.a, s.b, t))
  }
  // Beside the line, for gaps crowded with labels.
  for (const t of [0.5, 0.35, 0.65]) {
    for (const s of gaps) {
      const c = curvePoint(s.a, s.b, t)
      candidates.push({ x: c.x + size.width / 2 + 8, y: c.y }, { x: c.x - size.width / 2 - 8, y: c.y })
    }
  }
  for (const s of rows) candidates.push(curvePoint(s.a, s.b, 0.5))

  let best = candidates[0]
  let bestScore = Infinity
  for (const c of candidates) {
    const box = boxAround(c, size)
    const score =
      placed.reduce((sum, l) => sum + overlapArea(l, box, 6), 0) * 2 +
      obstacles.reduce((sum, r) => sum + overlapArea(r, box, 4), 0)
    if (score === 0) return c
    if (score < bestScore) {
      best = c
      bestScore = score
    }
  }
  return best
}

/**
 * Waypoints for a vertical edge through the rows between its ends: a pair of
 * points just above and below each row, so the edge crosses the row straight
 * down (around any box in its way) and bends only in the gaps between rows.
 */
function throughRows(start: Point, end: Point, source: string, target: string, obstacles: [string, Rect][]): Point[] {
  const down = end.y > start.y
  const [top, bottom] = down ? [start.y, end.y] : [end.y, start.y]
  const between = obstacles
    .filter(([id, r]) => id !== source && id !== target && r.y > top && r.y + r.height < bottom)
    .map(([, r]) => r)
  if (!between.length) return []

  // Group the boxes into rows by overlapping vertical extent.
  const bands: { y0: number; y1: number; boxes: Rect[] }[] = []
  for (const r of [...between].sort((a, b) => a.y - b.y)) {
    const band = bands.find((b) => r.y < b.y1 && r.y + r.height > b.y0)
    if (band) {
      band.boxes.push(r)
      band.y0 = Math.min(band.y0, r.y)
      band.y1 = Math.max(band.y1, r.y + r.height)
    } else bands.push({ y0: r.y, y1: r.y + r.height, boxes: [r] })
  }
  if (!down) bands.reverse()

  const points: Point[] = []
  let from = start
  for (const band of bands) {
    const midY = (band.y0 + band.y1) / 2
    // Where a straight line to the end would cross this row.
    let x = from.x + ((end.x - from.x) * (midY - from.y)) / (end.y - from.y)
    const hit = band.boxes.find((r) => x > r.x - 16 && x < r.x + r.width + 16)
    if (hit) {
      // Go round on the nearer side, in the gap next to the box.
      const others = band.boxes.filter((r) => r !== hit)
      const free = (cx: number) => !others.some((r) => cx > r.x - 12 && cx < r.x + r.width + 12)
      const leftX = hit.x - 26
      const rightX = hit.x + hit.width + 26
      const preferLeft = Math.abs(x - leftX) <= Math.abs(x - rightX)
      x = (preferLeft && free(leftX)) || !free(rightX) ? leftX : rightX
    }
    const [y0, y1] = down ? [band.y0 - 10, band.y1 + 10] : [band.y1 + 10, band.y0 - 10]
    points.push({ x, y: y0 }, { x, y: y1 })
    from = { x, y: y1 }
  }
  return points
}
