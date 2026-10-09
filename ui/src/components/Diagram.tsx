// Draws a laid-out diagram: the boundary and arrows in SVG, the boxes and arrow
// labels as HTML on top (so text wraps and links work). The whole canvas scales
// down to fit narrow screens and scrolls sideways below a readable size.

import { useEffect, useMemo, useState } from 'react'
import { useElementWidth } from '../lib/hooks'
import { describeContainerPhase } from '../model/describe'
import { layoutDiagram, type Layout, type Rect } from '../model/layout'
import { cardSizeVars, nodeSize } from '../model/sizes'
import type { ContainerNode, Diagram as DiagramModel, DiagramEdge } from '../model/story'
import { ConditionList, PhaseBadge } from './bits'
import { WarningIcon } from './icons'
import { NodeCard } from './NodeCard'
import './diagram.css'

export interface DiagramProps {
  diagram: DiagramModel
  selectedId?: string
  onSelect: (id: string) => void
  onOpenSystem?: (name: string) => void
  /** Shown inside an empty boundary. */
  emptyBoundaryText?: string
}

/** Smallest scale before the diagram scrolls sideways instead of shrinking. */
const MIN_SCALE = 0.55

export function Diagram({ diagram, selectedId, onSelect, onOpenSystem, emptyBoundaryText }: DiagramProps) {
  const layout = useMemo(() => layoutDiagram(diagram, { sizeOf: nodeSize }), [diagram])
  const [ref, available, scroller] = useElementWidth<HTMLDivElement>()
  const [hovered, setHovered] = useState<string>()

  const scale = available > 0 ? Math.max(MIN_SCALE, Math.min(1, available / layout.width)) : 1
  const overflows = available > 0 && layout.width * scale > available + 1
  // When the diagram is wider than the screen, start in its middle, where the
  // system usually is, rather than at its left edge.
  useEffect(() => {
    if (scroller && overflows) scroller.scrollTo({ left: (scroller.scrollWidth - scroller.clientWidth) / 2 })
  }, [scroller, overflows, layout.width])
  // While a box is hovered or selected, its arrows stand out and the rest fade.
  const focus = hovered ?? selectedId
  const linked = useMemo(() => {
    if (!focus) return undefined
    const ids = new Set([focus])
    for (const e of diagram.edges) {
      if (e.source === focus) ids.add(e.target)
      if (e.target === focus) ids.add(e.source)
    }
    return ids
  }, [diagram.edges, focus])
  const edgeActive = (e: DiagramEdge) => !focus || e.source === focus || e.target === focus

  // The selected box already has its details in the side panel.
  const hoveredContainer = diagram.nodes.find(
    (n) => n.id === hovered && n.id !== selectedId && n.role === 'container',
  ) as ContainerNode | undefined

  return (
    <>
      <div className="diagram-scroll" ref={ref}>
        <div className="diagram-sizer" style={{ width: layout.width * scale, height: layout.height * scale }}>
          <div
            className="diagram-canvas"
            style={{ ...cardSizeVars, width: layout.width, height: layout.height, transform: `scale(${scale})` }}
          >
            <svg className="diagram-svg" width={layout.width} height={layout.height} aria-hidden="true">
              <defs>
                {(['default', 'active', 'warn'] as const).map((tone) => (
                  <marker
                    key={tone}
                    id={`arrow-${tone}`}
                    viewBox="0 0 10 10"
                    refX="9"
                    refY="5"
                    markerWidth="8"
                    markerHeight="8"
                    markerUnits="userSpaceOnUse"
                    orient="auto-start-reverse"
                  >
                    <path d="M 0 0 L 10 5 L 0 10 z" className={`arrowhead arrowhead-${tone}`} />
                  </marker>
                ))}
              </defs>
              {layout.boundary && diagram.boundary ? (
                <rect
                  className="boundary"
                  x={layout.boundary.x}
                  y={layout.boundary.y}
                  width={layout.boundary.width}
                  height={layout.boundary.height}
                  rx={14}
                />
              ) : null}
              {diagram.edges.map((e) => {
                const route = layout.edges[e.id]
                if (!route) return null
                const active = focus !== undefined && edgeActive(e)
                const tone = e.notApplied ? 'warn' : active ? 'active' : 'default'
                return (
                  <path
                    key={e.id}
                    d={route.path}
                    className={`edge edge-${tone} ${focus && !edgeActive(e) ? 'is-dimmed' : ''}`}
                    markerEnd={`url(#arrow-${tone})`}
                  />
                )
              })}
            </svg>

            {layout.boundary && diagram.boundary ? (
              <BoundaryTitle
                rect={layout.boundary}
                title={diagram.boundary.title}
                empty={!diagram.boundary.nodeIds.length}
                emptyText={emptyBoundaryText}
              />
            ) : null}

            {diagram.edges.map((e) => {
              const route = layout.edges[e.id]
              if (!route || (!e.label && !e.notApplied)) return null
              return (
                <div
                  key={e.id}
                  className={`edge-label ${e.notApplied ? 'edge-label-warn' : ''} ${focus && !edgeActive(e) ? 'is-dimmed' : ''}`}
                  style={{ left: route.label.x, top: route.label.y }}
                  title={
                    e.relationships.length > 1
                      ? `${e.relationships.length} links: ${e.relationships.join(', ')}`
                      : e.relationships[0]
                  }
                >
                  {e.label ? <span>{e.label}</span> : null}
                  {e.notApplied ? (
                    <span className="edge-note">
                      <WarningIcon size={11} /> Added after scaffolding, not applied
                    </span>
                  ) : null}
                </div>
              )
            })}

            {diagram.nodes.map((node) => {
              const rect = layout.nodes[node.id]
              if (!rect) return null
              return (
                <NodeCard
                  key={node.id}
                  node={node}
                  rect={rect}
                  selected={node.id === selectedId}
                  dimmed={Boolean(linked && !linked.has(node.id))}
                  onSelect={onSelect}
                  onHover={setHovered}
                  onOpenSystem={onOpenSystem}
                />
              )
            })}

            {hoveredContainer ? <ContainerPopover node={hoveredContainer} layout={layout} /> : null}
          </div>
        </div>
      </div>
      {overflows ? <p className="diagram-hint">Scroll sideways to see the whole diagram.</p> : null}
    </>
  )
}

function BoundaryTitle({
  rect,
  title,
  empty,
  emptyText,
}: {
  rect: Rect
  title: string
  empty: boolean
  emptyText?: string
}) {
  return (
    <>
      <div className="boundary-title" style={{ left: rect.x + 18, top: rect.y + 14 }}>
        <span className="boundary-name">{title}</span>
        <span className="boundary-kind">Software system</span>
      </div>
      {empty ? (
        <div
          className="boundary-empty"
          style={{ left: rect.x, top: rect.y + 44, width: rect.width, height: rect.height - 44 }}
        >
          {emptyText ?? 'No containers or data stores yet.'}
        </div>
      ) : null}
    </>
  )
}

/** On hover over a container: its phase and checks in plain words. */
function ContainerPopover({ node, layout }: { node: ContainerNode; layout: Layout }) {
  const rect = layout.nodes[node.id]
  if (!rect) return null
  const width = 300
  const right = rect.x + rect.width + 12
  const left = right + width > layout.width ? Math.max(4, rect.x - width - 12) : right
  const phase = describeContainerPhase(node.phase)
  // Keep it inside the canvas, which clips: about 120px plus 56px per check.
  const height = 120 + 56 * Math.max(1, node.conditions.length)
  const top = Math.max(4, Math.min(rect.y, layout.height - height - 4))
  return (
    <div className="popover" style={{ left, top, width }} role="tooltip">
      <div className="popover-head">
        <span className="popover-title">{node.title}</span>
        <PhaseBadge phase={node.phase} size="sm" />
      </div>
      <p className="popover-text">{phase.explanation}</p>
      <ConditionList conditions={node.conditions} empty="The factory has not checked anything yet." />
      <p className="popover-hint">Click for details and sessions.</p>
    </div>
  )
}
