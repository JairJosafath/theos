// The Work area's "What agent runs use": each agent run's largest session by
// its Task's round and outcome, against its Agent's sessionTokenLimit, with a
// table of the same by outcome.

import { useId, useMemo, useState, type ReactNode } from 'react'
import type { Problem } from '../../k8s/sources'
import type { Agent, Task } from '../../k8s/types'
import { formatTokens } from '../../model/brief'
import { plural } from '../../model/describe'
import {
  agentTokens,
  describeRun,
  OUTCOMES,
  spreadInRounds,
  tokenAxis,
  type AgentTokens,
  type Outcome,
} from '../../model/tokens'
import { useElementWidth } from '../../lib/hooks'
import type { ObjectHref } from '../Factory'
import { PanelState } from '../PanelState'
import './tokens.css'

export function TokensPanel({
  tasks,
  agents,
  loaded,
  problems,
  pageHref,
}: {
  tasks: Task[]
  agents: Agent[]
  loaded: boolean
  problems: Problem[]
  pageHref: ObjectHref
}) {
  const all = useMemo(() => agentTokens(tasks, agents), [tasks, agents])
  const charted = all.filter((a) => a.points.length)
  const unreported = all.reduce((n, a) => n + a.unreported, 0)
  const [picked, setPicked] = useState<string>()
  const shown = charted.find((a) => a.key === picked) ?? charted[0]
  const id = useId()
  const namespaces = new Set(charted.map((a) => a.namespace))
  const label = (a: AgentTokens) =>
    `${a.title}${namespaces.size > 1 ? ` (${a.namespace})` : ''}${a.limit ? ` · limit ${formatTokens(a.limit)}` : ''}`

  return (
    <div className="panel">
      <h2 className="panel-title">What agent runs use</h2>
      <p className="panel-caption">
        How large each agent run got, its biggest turn’s prompt, in each round of its Task, against its Agent’s session
        token limit; the mark says how the Task ended. Point at one for its run, open it for its Task.
      </p>
      <PanelState
        loaded={loaded}
        problems={problems}
        empty={!charted.length}
        loading="Loading the agent runs…"
        emptyText={
          unreported
            ? `None of the ${plural(unreported, 'agent run')} that ended reported its tokens yet; the stand-in agent reports none.`
            : 'No agent run has ended yet: each shows here once its Task records it.'
        }
        small
      >
        {shown ? (
          <>
            {charted.length > 1 ? (
              <div className="tokens-agent">
                <label htmlFor={`${id}-agent`} className="muted">
                  Agent
                </label>
                <select
                  id={`${id}-agent`}
                  className="input"
                  value={shown.key}
                  onChange={(e) => setPicked(e.target.value)}
                >
                  {charted.map((a) => (
                    <option key={a.key} value={a.key}>
                      {label(a)}
                    </option>
                  ))}
                </select>
              </div>
            ) : (
              <p className="tokens-agent muted">{label(shown)}</p>
            )}
            <RunChart agent={shown} pageHref={pageHref} />
            <Legend limit={shown.limit !== undefined} />
            <div className="tokens-table-wrap">
              <OutcomeTable agent={shown} />
            </div>
            <p className="panel-note">
              {limitNote(shown)}
              {shown.unreported
                ? ` ${plural(shown.unreported, 'more run')} of this Agent didn’t report ${shown.unreported > 1 ? 'their' : 'its'} tokens.`
                : ''}
            </p>
          </>
        ) : null}
      </PanelState>
    </div>
  )
}

function limitNote(agent: AgentTokens): string {
  if (!agent.found) return `Agent ${agent.name} is gone, so its limit is unknown.`
  if (agent.limit === undefined) return `Agent ${agent.name} sets no session token limit.`
  return `A run past ${formatTokens(agent.limit)} hands off to a fresh session; spec.sessionTokenLimit on Agent ${agent.name} sets it.`
}

/** The width the chart draws at before it has measured its own, as on the server. */
const WIDTH = 640
const H = 220
const LEFT = 40
const RIGHT = 8
const TOP = 10
const BOTTOM = 24

function RunChart({ agent, pageHref }: { agent: AgentTokens; pageHref: ObjectHref }) {
  const [ref, measured] = useElementWidth<HTMLDivElement>()
  const W = measured || WIDTH
  const { points, limit, maxRound } = agent
  const largest = Math.max(...points.map((p) => p.largest))
  const axis = tokenAxis(largest, limit)
  const band = (W - LEFT - RIGHT) / maxRound
  const across = spreadInRounds(points)
  const y = (v: number) => H - BOTTOM - (Math.min(v, axis.max) / axis.max) * (H - TOP - BOTTOM)
  const rounds = Array.from({ length: maxRound }, (_, i) => i + 1)
  const summary = `${plural(points.length, 'run')} of ${agent.title}, largest ${formatTokens(largest)}${
    limit !== undefined ? ` against a ${formatTokens(limit)} limit` : ''
  }`
  return (
    <div ref={ref}>
      {/* A group, not an img: its runs are links, each named by what it says when pointed at. */}
      <svg className="tokens-chart" viewBox={`0 0 ${W} ${H}`} role="group" aria-label={summary}>
        <g aria-hidden="true">
          {axis.ticks.map((t) => (
            <g key={t}>
              <line className="tokens-grid" x1={LEFT} x2={W - RIGHT} y1={y(t)} y2={y(t)} />
              <text className="tokens-tick" x={LEFT - 6} y={y(t)} textAnchor="end" dominantBaseline="middle">
                {formatTokens(t)}
              </text>
            </g>
          ))}
          {rounds.map((r) => (
            <g key={r}>
              {r > 1 ? (
                <line
                  className="tokens-band"
                  x1={LEFT + (r - 1) * band}
                  x2={LEFT + (r - 1) * band}
                  y1={TOP}
                  y2={H - BOTTOM}
                />
              ) : null}
              <text className="tokens-tick" x={LEFT + (r - 0.5) * band} y={H - 6} textAnchor="middle">
                Round {r}
              </text>
            </g>
          ))}
          {limit !== undefined ? (
            <g>
              <line className="tokens-limit" x1={LEFT} x2={W - RIGHT} y1={y(limit)} y2={y(limit)} />
              <text className="tokens-limit-label" x={W - RIGHT} y={y(limit) - 4} textAnchor="end">
                limit {formatTokens(limit)}
              </text>
            </g>
          ) : null}
        </g>
        {points.map((p, i) => {
          const cx = LEFT + (p.round - 1 + across[i]) * band
          const said = describeRun(p, limit)
          const mark = (
            <>
              <title>{said}</title>
              <Mark outcome={p.outcome} handedOff={p.handoffs.length > 0} x={cx} y={y(p.largest)} />
            </>
          )
          const href = pageHref(p.namespace, 'Task', p.name)
          const key = `${p.task}/${i}`
          return href ? (
            <a key={key} href={href} className="tokens-run" aria-label={said}>
              {mark}
            </a>
          ) : (
            <g key={key} className="tokens-run" role="img" aria-label={said}>
              {mark}
            </g>
          )
        })}
      </svg>
    </div>
  )
}

/** A run's mark: a dot for merged, a cross for failed, a ring for still going; handed off adds a halo. */
function Mark({ outcome, handedOff, x, y }: { outcome: Outcome; handedOff: boolean; x: number; y: number }) {
  return (
    <g className={`tokens-mark tokens-${outcome}`} transform={`translate(${x.toFixed(1)},${y.toFixed(1)})`}>
      <circle className="tokens-hit" r="9" />
      {handedOff ? <circle className="tokens-halo" r="8" /> : null}
      {outcome === 'failed' ? (
        <path className="tokens-cross" d="M-4,-4L4,4M-4,4L4,-4" />
      ) : (
        <circle className="tokens-dot" r="4.5" />
      )}
    </g>
  )
}

function Legend({ limit }: { limit: boolean }) {
  return (
    <div className="spark-legend tokens-legend">
      {OUTCOMES.map((o) => (
        <span key={o.key}>
          <LegendKey>
            <Mark outcome={o.key} handedOff={false} x={9} y={9} />
          </LegendKey>
          {o.label}
        </span>
      ))}
      <span>
        <LegendKey>
          <Mark outcome="merged" handedOff x={9} y={9} />
        </LegendKey>
        Handed off
      </span>
      {limit ? (
        <span>
          <LegendKey>
            <line className="tokens-limit" x1="0" x2="18" y1="9" y2="9" />
          </LegendKey>
          Session token limit
        </span>
      ) : null}
    </div>
  )
}

function LegendKey({ children }: { children: ReactNode }) {
  return (
    <svg className="tokens-key" viewBox="0 0 18 18" aria-hidden="true">
      {children}
    </svg>
  )
}

/** The chart's figures by outcome: also what a screen reader reads in place of the chart. */
function OutcomeTable({ agent }: { agent: AgentTokens }) {
  return (
    <table className="tokens-table">
      <caption className="visually-hidden">What the runs of {agent.title} used, by how their Task ended</caption>
      <thead>
        <tr>
          <th scope="col">Outcome</th>
          <th scope="col">Tasks</th>
          <th scope="col">Rounds</th>
          <th scope="col">Runs</th>
          <th scope="col">Largest run</th>
          <th scope="col">Tokens per run</th>
          <th scope="col">Past the limit</th>
          <th scope="col">Handed off</th>
        </tr>
      </thead>
      <tbody>
        {agent.byOutcome.map((row) => (
          <tr key={row.outcome}>
            <th scope="row">{OUTCOMES.find((o) => o.key === row.outcome)?.label}</th>
            <td>{row.tasks}</td>
            <td title="On average">{row.meanRounds.toFixed(1)}</td>
            <td>{row.runs}</td>
            <td title="The median">{formatTokens(Math.round(row.medianLargest))}</td>
            <td title="On average">{row.meanTotal === undefined ? '–' : formatTokens(Math.round(row.meanTotal))}</td>
            <td>{agent.limit === undefined ? '–' : row.pastLimit}</td>
            <td>{row.handedOff}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
