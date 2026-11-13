// One box in a diagram. People are drawn as a head and body, data stores as a
// cylinder, everything else as a card, following the usual C4 shapes and colours.

import type { KeyboardEvent, MouseEvent } from 'react'
import { plural, splitTemplateRef } from '../model/describe'
import type { Rect } from '../model/layout'
import { MAX_LIST_ITEMS } from '../model/sizes'
import type { ContainerNode, DataStoreNode, DiagramNode, PersonNode, SystemNode } from '../model/story'
import { findCondition } from '../k8s/types'
import { ActorIcon, PhaseBadge } from './bits'
import { ArrowRightIcon, ExternalLinkIcon, WarningIcon } from './icons'

export interface NodeCardProps {
  node: DiagramNode
  rect: Rect
  selected: boolean
  dimmed: boolean
  onSelect: (id: string) => void
  onHover: (id: string | undefined) => void
  /** Opens the container view of a system ("Look inside"). */
  onOpenSystem?: (name: string) => void
  /** The box's page: the box is a link to it, and onSelect opens it. */
  href?: string
}

export function NodeCard({ node, rect, selected, dimmed, onSelect, onHover, onOpenSystem, href }: NodeCardProps) {
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      onSelect(node.id)
    }
  }
  const className = ['node', `node-${node.role}`, selected ? 'is-selected' : '', dimmed ? 'is-dimmed' : '']
    .filter(Boolean)
    .join(' ')
  return (
    <div
      className={className}
      style={{ left: rect.x, top: rect.y, width: rect.width, height: rect.height }}
      role={href ? 'link' : 'button'}
      tabIndex={0}
      aria-pressed={href ? undefined : selected}
      // Its text names it, the title and then the kind, which every body shows (WCAG 2.5.3, label in name).
      data-node-id={node.id}
      onClick={(e) => {
        // With a modifier, a page opens in a new tab, as a link's would.
        if (href && (e.metaKey || e.ctrlKey || e.shiftKey)) window.open(href, '_blank', 'noopener')
        else onSelect(node.id)
      }}
      onAuxClick={(e) => {
        if (href && e.button === 1) window.open(href, '_blank', 'noopener')
      }}
      onKeyDown={onKeyDown}
      onMouseEnter={() => onHover(node.id)}
      onMouseLeave={() => onHover(undefined)}
      onFocus={() => onHover(node.id)}
      onBlur={() => onHover(undefined)}
    >
      {node.role === 'person' ? <PersonCard node={node} rect={rect} /> : null}
      {node.role === 'system' ? <SystemCard node={node} onOpenSystem={onOpenSystem} /> : null}
      {node.role === 'external-system' ? <ExternalCard node={node} /> : null}
      {node.role === 'container' ? <ContainerCard node={node} /> : null}
      {node.role === 'datastore' ? <DataStoreCard node={node} rect={rect} /> : null}
    </div>
  )
}

function PersonCard({ node, rect }: { node: PersonNode; rect: Rect }) {
  const w = rect.width
  const h = rect.height
  return (
    <>
      <svg className="node-shape" width={w} height={h} viewBox={`0 0 ${w} ${h}`} aria-hidden="true">
        <circle cx={w / 2} cy={29} r={27} />
        <rect x={1} y={50} width={w - 2} height={h - 51} rx={26} />
      </svg>
      <div className="node-body person-body">
        <div className="node-title">{node.title}</div>
        <div className="node-kind">Person</div>
        {node.description ? <p className="node-desc clamp-3">{node.description}</p> : null}
      </div>
    </>
  )
}

function ListBlock({ label, items }: { label: string; items: string[] }) {
  if (!items.length) return null
  const shown = items.slice(0, MAX_LIST_ITEMS)
  const more = items.length - shown.length
  return (
    <div className="node-list">
      <div className="node-list-label">{label}</div>
      <ul>
        {shown.map((item, i) => (
          <li key={i} title={item}>
            {i === shown.length - 1 && more > 0 ? `${item} (+${more} more)` : item}
          </li>
        ))}
      </ul>
    </div>
  )
}

function SystemCard({ node, onOpenSystem }: { node: SystemNode; onOpenSystem?: (name: string) => void }) {
  const open = (e: MouseEvent) => {
    e.stopPropagation()
    onOpenSystem?.(node.name)
  }
  return (
    <div className="node-body card-body">
      <div className="node-title">{node.title}</div>
      <div className="node-kind">Software system</div>
      {node.description ? <p className="node-desc clamp-2">{node.description}</p> : null}
      <ListBlock label="Needs" items={node.needs} />
      <ListBlock label="KPIs" items={node.kpis} />
      <div className="node-footer">
        <span
          className="node-summary"
          title={node.summaryReported ? 'Reported by the factory' : 'Counted from the model'}
        >
          {node.summary}
        </span>
        {onOpenSystem ? (
          <button type="button" className="look-inside" onClick={open} title="Show the containers of this system">
            Look inside <ArrowRightIcon size={13} />
          </button>
        ) : null}
      </div>
    </div>
  )
}

function ExternalCard({ node }: { node: SystemNode }) {
  return (
    <div className="node-body card-body">
      <div className="node-title">{node.title}</div>
      <div className="node-kind">{node.external ? 'Software system · external' : 'Software system · outside'}</div>
      {node.description ? <p className="node-desc clamp-2">{node.description}</p> : null}
    </div>
  )
}

function ContainerCard({ node }: { node: ContainerNode }) {
  const templates = node.scaffoldedFrom.map((ref) => {
    const { name, version } = splitTemplateRef(ref)
    return version ? `${name} ${version}` : name
  })
  const kind = ['Container', node.typeLabel, node.framework].filter(Boolean).join(' · ')
  return (
    <div className="node-body card-body">
      <div className="node-head">
        <div className="node-title">{node.title}</div>
        <PhaseBadge phase={node.phase} size="sm" />
      </div>
      <div className="node-kind" title={kind}>
        {kind}
      </div>
      {node.description ? <p className="node-desc clamp-2">{node.description}</p> : null}
      <div className="node-templates">
        <div className="node-list-label">Started from</div>
        <div className="node-templates-text clamp-3" title={node.scaffoldedFrom.join(', ')}>
          {templates.length ? templates.join(' · ') : 'Not scaffolded yet'}
        </div>
      </div>
      <div className="node-footer">
        <span className="node-sessions" title={`${plural(node.activeSessions, 'active session')}`}>
          {node.sessions.length ? (
            <span className="avatars">
              {node.sessions.slice(0, 4).map((s) => (
                <span
                  key={s.metadata.name}
                  className={`avatar avatar-${s.spec.actor} ${s.status?.phase === 'Stale' ? 'avatar-stale' : ''}`}
                >
                  <ActorIcon actor={s.spec.actor} size={12} />
                </span>
              ))}
            </span>
          ) : null}
          {node.activeSessions ? `${node.activeSessions} working` : 'Nobody working'}
        </span>
        {node.repositoryURL ? (
          <a
            className="repo-link"
            href={node.repositoryURL}
            target="_blank"
            rel="noreferrer"
            onClick={(e) => e.stopPropagation()}
            title={node.repositoryURL}
          >
            Repository <ExternalLinkIcon size={12} />
          </a>
        ) : (
          <span className="repo-none">No repository yet</span>
        )}
      </div>
    </div>
  )
}

function DataStoreCard({ node, rect }: { node: DataStoreNode; rect: Rect }) {
  const w = rect.width
  const h = rect.height
  const ry = 13
  const missing = findCondition(node.conditions, 'ReferencesResolved')?.status === 'False'
  return (
    <>
      <svg className="node-shape" width={w} height={h} viewBox={`0 0 ${w} ${h}`} aria-hidden="true">
        <path
          d={`M1 ${ry} A ${w / 2 - 1} ${ry - 1} 0 0 1 ${w - 1} ${ry} V ${h - ry} A ${w / 2 - 1} ${ry - 1} 0 0 1 1 ${h - ry} Z`}
        />
        <ellipse className="rim" cx={w / 2} cy={ry} rx={w / 2 - 1} ry={ry - 1} />
      </svg>
      <div className="node-body datastore-body">
        <div className="node-title">{node.title}</div>
        <div className="node-kind">Data store · {node.connector || 'no connector'}</div>
        {node.description ? <p className="node-desc clamp-2">{node.description}</p> : null}
        {missing ? (
          <div className="node-warning">
            <WarningIcon size={13} /> Connector not found
          </div>
        ) : null}
      </div>
    </>
  )
}
