// The shell's Loop area (docs/plans/done/sdlc-loop.md, step 6): one project's
// software-development cycle in ten stages, each with its count and its items.
// It reads the resources and writes nothing; the two stages that wait on a
// person are drawn apart, with a person's icon and a dashed border.
//
// The ring and the selected stage sit side by side (docs/plans/done/ui-shell.md,
// step 2): a node selects its stage, kept in the address as tab=; the Loop
// opens on the first review point with items. On a phone, the ring, then a
// chip per stage, then the selected stage's items.

import { useContext, useState, type MouseEvent } from 'react'
import type { Problem } from '../k8s/sources'
import { hrefFor, UrlContext, type UrlState } from '../lib/hooks'
import { plural } from '../model/describe'
import { hasPage } from '../model/lifecycle'
import { loopStages, selectedStage, type LoopInput, type LoopItem, type LoopStage, type StageKey } from '../model/loop'
import { ExternalLinkIcon, HumanIcon, WarningIcon } from './icons'
import { PanelState } from './PanelState'
import './loop.css'

/** Leaves an object page and any system the URL had selected. */
const PLACE_ONLY = { ns: undefined, kind: undefined, name: undefined, system: undefined }

export function LoopArea({
  data,
  title,
  loaded,
  problems,
  setUrl,
}: {
  data: LoopInput
  /** The project's name. */
  title: string
  loaded: boolean
  /** The failed reads behind the stages: any of them may hide an item. */
  problems: Problem[]
  setUrl: (next: Partial<UrlState>) => void
}) {
  const stages = loopStages(data)
  const url = useContext(UrlContext)
  const [own, setOwn] = useState<string>()
  const selected = selectedStage(stages, url ? url[0].tab : own)
  const stage = stages.find((s) => s.key === selected)
  // Selecting a stage keeps it in the address without a history entry, as a tab does.
  const select = (key: StageKey) => (url ? url[1]({ tab: key }, true) : setOwn(key))
  return (
    <div className="shell-page loop">
      <header className="area-head">
        <div>
          <h1 className="area-title">Loop</h1>
          <p className="area-caption">
            {title}&rsquo;s cycle from an idea or a bug to a monitored release, as the resources say it. The two dashed
            stages wait on a person; a bug found at Monitor starts the cycle again.
          </p>
        </div>
      </header>
      <PanelState loaded={loaded} problems={problems} loading="Loading the loop…">
        <div className="loop-layout">
          <section className="panel loop-ring-panel" aria-label="The loop">
            <Ring stages={stages} selected={selected} onSelect={select} />
          </section>
          <div className="loop-chips chips" role="group" aria-label="Stages">
            {stages.map((s) => (
              <button
                key={s.key}
                type="button"
                className={`chip${s.person ? ' loop-chip-person' : ''}`}
                aria-pressed={s.key === selected}
                onClick={() => select(s.key)}
              >
                {s.number}. {s.short}
                <span className="chip-count">{s.count}</span>
              </button>
            ))}
          </div>
          {stage ? <Stage stage={stage} setUrl={setUrl} /> : null}
        </div>
      </PanelState>
    </div>
  )
}

const CX = 270
const CY = 205
const R = 140
const NODE = 27

function position(i: number, total: number, radius = R) {
  const angle = (-90 + (360 / total) * i) * (Math.PI / 180)
  return { x: CX + radius * Math.cos(angle), y: CY + radius * Math.sin(angle), cos: Math.cos(angle) }
}

/** The ten stages as a ring of nodes, an arrow from each to the next; a node selects its stage. */
function Ring({
  stages,
  selected,
  onSelect,
}: {
  stages: LoopStage[]
  selected?: StageKey
  onSelect: (key: StageKey) => void
}) {
  const n = stages.length
  return (
    <svg className="loop-ring" viewBox="0 0 540 420" role="group" aria-label="The loop, stage by stage">
      <defs>
        <marker id="loop-arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto">
          <path d="M1 1.5 8.5 5 1 8.5z" className="loop-arrowhead" />
        </marker>
      </defs>
      {stages.map((s, i) => {
        const a = position(i, n)
        const b = position((i + 1) % n, n)
        const dx = b.x - a.x
        const dy = b.y - a.y
        const len = Math.hypot(dx, dy)
        const pad = NODE + 4
        return (
          <line
            key={`edge-${s.key}`}
            className="loop-edge"
            x1={a.x + (dx / len) * pad}
            y1={a.y + (dy / len) * pad}
            x2={b.x - (dx / len) * (pad + 3)}
            y2={b.y - (dy / len) * (pad + 3)}
            markerEnd="url(#loop-arrow)"
          />
        )
      })}
      {stages.map((s, i) => {
        const p = position(i, n)
        const label = position(i, n, R + NODE + 8)
        const anchor = label.cos > 0.3 ? 'start' : label.cos < -0.3 ? 'end' : 'middle'
        const top = label.y < CY - R + 1
        const bottom = label.y > CY + R - 1
        const ly = top ? p.y - NODE - 8 : bottom ? p.y + NODE + 16 : label.y + 4
        const lx = top || bottom ? p.x : label.x
        const problem = s.key === 'monitor' && s.count > 0
        const state = s.count === 0 ? 'idle' : problem ? 'problem' : s.person ? 'person' : 'busy'
        const on = s.key === selected
        return (
          <a
            key={s.key}
            href={hrefFor({ tab: s.key })}
            className={`loop-node loop-node-${state}${s.person ? ' loop-node-review' : ''}`}
            aria-label={`${s.number}. ${s.label}: ${s.count}`}
            aria-current={on ? 'true' : undefined}
            aria-controls="loop-stage"
            onClick={(e) => {
              if (!plainClick(e)) return
              e.preventDefault()
              onSelect(s.key)
            }}
          >
            {on ? <circle cx={p.x} cy={p.y} r={NODE + 6} className="loop-halo" /> : null}
            <circle cx={p.x} cy={p.y} r={NODE} className="loop-disc" />
            {s.person ? (
              <g transform={`translate(${p.x - 8} ${p.y - 23}) scale(0.67)`} className="loop-person-icon">
                <circle cx="12" cy="8" r="4" />
                <path d="M4 21c0-4.4 3.6-7 8-7s8 2.6 8 7" />
              </g>
            ) : null}
            <text x={p.x} y={s.person ? p.y + 14 : p.y + 6} textAnchor="middle" className="loop-count">
              {s.count}
            </text>
            <text x={lx} y={ly} textAnchor={top || bottom ? 'middle' : anchor} className="loop-label">
              {s.number}. {s.short}
            </text>
          </a>
        )
      })}
      <text x={CX} y={CY - 4} textAnchor="middle" className="loop-centre">
        the loop
      </text>
      <text x={CX} y={CY + 14} textAnchor="middle" className="loop-centre-sub">
        {stages.reduce((sum, s) => sum + s.count, 0)} in flight
      </text>
    </svg>
  )
}

function Stage({ stage, setUrl }: { stage: LoopStage; setUrl: (next: Partial<UrlState>) => void }) {
  const monitor = stage.key === 'monitor'
  return (
    <section
      id="loop-stage"
      className={`panel loop-stage${stage.person ? ' loop-stage-person' : ''}`}
      aria-label={`${stage.number}. ${stage.label}`}
      aria-live="polite"
    >
      <h2 className="loop-stage-head">
        <span className="loop-stage-number">{stage.number}</span>
        <span className="loop-stage-name">{stage.label}</span>
        {stage.person ? (
          <span className="loop-person-tag">
            <HumanIcon size={13} /> waits on a person
          </span>
        ) : null}
        <span className="loop-stage-count">
          {monitor ? plural(stage.count, 'problem') : stage.count}
          {monitor ? `, ${stage.healthy ?? 0} healthy` : ''}
        </span>
      </h2>
      <p className="loop-stage-about">{stage.about}</p>
      {stage.items.length > 0 ? (
        <ul className="loop-items">
          {stage.items.map((item) => (
            <Item key={item.key} item={item} setUrl={setUrl} />
          ))}
        </ul>
      ) : (
        <p className="loop-none">{monitor && stage.healthy ? 'No problems.' : 'Nothing here.'}</p>
      )}
    </section>
  )
}

function Item({ item, setUrl }: { item: LoopItem; setUrl: (next: Partial<UrlState>) => void }) {
  const open = { ...PLACE_ONLY, ns: item.page.ns, kind: item.page.kind, name: item.page.name }
  const linked = hasPage(item.page.kind)
  return (
    <li className={`loop-item${item.problem ? ' loop-item-problem' : ''}`}>
      <div className="loop-item-head">
        <span className="loop-item-kind">{item.kind}</span>{' '}
        {linked ? (
          <a
            className="loop-item-name"
            href={hrefFor(open)}
            onClick={(e) => {
              if (!plainClick(e)) return
              e.preventDefault()
              setUrl(open)
            }}
          >
            {item.name}
          </a>
        ) : (
          <strong className="loop-item-name">{item.name}</strong>
        )}
      </div>
      <p className="loop-item-title">
        {item.problem ? <WarningIcon size={13} /> : null} {item.title}
      </p>
      {item.detail ? <p className="loop-item-detail">{item.detail}</p> : null}
      {item.links.length > 0 ? (
        <div className="loop-item-links">
          {item.links.map((l) => (
            <a key={l.url} href={l.url} target="_blank" rel="noreferrer">
              {l.label} <ExternalLinkIcon size={12} />
            </a>
          ))}
        </div>
      ) : null}
    </li>
  )
}

function plainClick(e: MouseEvent) {
  return e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey
}
