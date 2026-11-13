// The factory's health: its components, each node's usage, and the history
// charts of the last hour, as the Factory area's dashboard grid
// (docs/plans/done/ui-shell.md, step 2): a card per part of the factory, each
// node's usage, the last hour's charts of the nodes together and of the
// fullest of them side by side, and the agent slots beside them.

import type { ReactNode } from 'react'
import type { ComponentHealth, FactoryHealth, FullestNode, NodeHealth, PodHealth } from '../../k8s/health'
import { plural } from '../../model/describe'
import { API_STALL_MS } from '../../model/attention'
import { formatBytes, formatCpu, formatDuration, percent } from '../../model/format'
import type { Problem } from '../../k8s/sources'
import { ReadProblems } from '../Status'
import './health.css'
import { When } from '../bits'

const COMPONENT_TONE: Record<ComponentHealth['state'], string> = {
  ok: 'ok',
  degraded: 'waiting',
  down: 'error',
  absent: 'unknown',
  unknown: 'unknown',
}

export function HealthDashboard({
  health,
  loaded,
  problems = [],
  now,
  slots,
}: {
  health?: FactoryHealth
  loaded: boolean
  /** The health check that failed; what it read before still shows. */
  problems?: Problem[]
  now: number
  /** The agent slots' panel, beside the charts. */
  slots: ReactNode
}) {
  const reading = !loaded || !health
  return (
    <div className="dash">
      <section className="panel dash-parts" aria-labelledby="dash-parts">
        <div className="dash-head">
          <h2 className="panel-title small-title" id="dash-parts">
            Health
          </h2>
          {health ? (
            <span className="panel-note">
              Checked <When at={health.generatedAt} />
            </span>
          ) : null}
        </div>
        <ReadProblems problems={problems} />
        {problems.length && !health ? null : reading ? (
          <p className="panel-note">Reading the factory’s health…</p>
        ) : (
          <ul className="components">
            {health.components.map((c) => (
              <ComponentRow key={c.key} component={c} />
            ))}
          </ul>
        )}
      </section>
      <section className="panel dash-node" aria-labelledby="dash-node">
        <h2 className="panel-title small-title" id="dash-node">
          Nodes
        </h2>
        {reading ? (
          <p className="panel-note">{problems.length ? 'Not read.' : 'Reading the nodes’ usage…'}</p>
        ) : (
          <>
            {health.nodes.map((node) => (
              <NodeUsage key={node.name} node={node} metrics={health.metrics} />
            ))}
            {!health.metrics ? (
              <p className="panel-note">
                No CPU or memory figures: metrics-server is not installed (
                <span className="mono">make metrics-server</span>
                ).
              </p>
            ) : null}
          </>
        )}
      </section>
      <section className="panel dash-charts" aria-labelledby="dash-charts">
        <h2 className="panel-title small-title" id="dash-charts">
          Last hour
        </h2>
        {reading ? (
          <p className="panel-note">{problems.length ? 'Not read.' : 'Reading the last hour…'}</p>
        ) : (
          <HistoryCharts health={health} now={now} />
        )}
      </section>
      <div className="dash-slots">{slots}</div>
    </div>
  )
}

function Meter({ label, used, total, text }: { label: string; used?: number; total: number; text: string }) {
  const pct = percent(used, total)
  const tone = pct === undefined ? 'unknown' : pct > 85 ? 'error' : pct > 70 ? 'waiting' : 'ok'
  return (
    <div className="meter">
      <div className="meter-head">
        <span>{label}</span>
        <span className="muted">{text}</span>
      </div>
      <div
        className={`meter-track tone-${tone}`}
        role="meter"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct ?? 0}
      >
        <span className="meter-fill" style={{ width: `${pct ?? 0}%` }} />
      </div>
    </div>
  )
}

function NodeUsage({ node, metrics }: { node: NodeHealth; metrics: boolean }) {
  return (
    <div className="node-usage">
      <div className="node-usage-name">
        <span className="mono">{node.name}</span>
        {node.ready ? null : <span className="badge badge-sm badge-error">Not ready</span>}
        {node.pressure.map((p) => (
          <span key={p} className="badge badge-sm badge-error">
            {p}
          </span>
        ))}
      </div>
      {metrics ? (
        <>
          <Meter
            label="CPU"
            used={node.cpuMilli}
            total={node.cpuAllocatableMilli}
            text={`${formatCpu(node.cpuMilli)} of ${formatCpu(node.cpuAllocatableMilli)}`}
          />
          <Meter
            label="Memory"
            used={node.memoryBytes}
            total={node.memoryAllocatableBytes}
            text={`${formatBytes(node.memoryBytes)} of ${formatBytes(node.memoryAllocatableBytes)}`}
          />
        </>
      ) : null}
    </div>
  )
}

function ComponentRow({ component }: { component: ComponentHealth }) {
  const cpu = sumOf(component.pods, 'cpuMilli')
  const memory = sumOf(component.pods, 'memoryBytes')
  const restarts = component.pods.reduce((n, p) => n + p.restarts, 0)
  const recent = component.pods.find((p) => p.restartedRecently)?.lastRestart
  const waiting = component.pods.find((p) => p.waiting)?.waiting
  return (
    <li className={`component tone-${COMPONENT_TONE[component.state]}`} title={component.role}>
      <span className="badge-dot" aria-hidden="true" />
      <div className="component-main">
        <div className="component-name">
          {component.name}
          {waiting ? <span className="badge badge-sm badge-error">{waiting}</span> : null}
        </div>
        {component.state === 'absent' ? <div className="component-sub muted">not installed</div> : null}
        {component.pods.length ? (
          <div className="component-sub muted">
            {component.pods.filter((p) => p.ready).length}/{component.pods.length} ready
            {restarts ? ` · ${plural(restarts, 'restart')}` : ''}
            {recent ? (
              <>
                {' '}
                · last <When at={recent.at} />
              </>
            ) : null}
          </div>
        ) : null}
      </div>
      {cpu !== undefined || memory !== undefined ? (
        <div className="component-usage">
          <span>{formatCpu(cpu)}</span>
          <span>{formatBytes(memory)}</span>
        </div>
      ) : null}
    </li>
  )
}

function sumOf(pods: PodHealth[], field: 'cpuMilli' | 'memoryBytes'): number | undefined {
  const values = pods.map((p) => p[field]).filter((v): v is number => v !== undefined)
  return values.length ? values.reduce((a, b) => a + b, 0) : undefined
}

const HOUR_MS = 60 * 60_000

interface Series {
  label: string
  className: string
  values: { t: number; v: number | undefined }[]
}

/** A small line chart of the last hour. Points without a value break the line. */
function Sparkline({
  title,
  series,
  max,
  now,
  summary,
  marks = [],
}: {
  title: string
  series: Series[]
  max: number
  now: number
  summary: string
  marks?: { t: number; className: string }[]
}) {
  const width = 300
  const height = 44
  const x = (t: number) => ((t - (now - HOUR_MS)) / HOUR_MS) * width
  const y = (v: number) => height - 2 - (Math.min(v, max) / max) * (height - 4)
  const paths = series.map((s) => {
    let d = ''
    let open = false
    for (const { t, v } of s.values) {
      if (v === undefined) {
        open = false
        continue
      }
      d += `${open ? 'L' : 'M'}${x(t).toFixed(1)},${y(v).toFixed(1)}`
      open = true
    }
    return { ...s, d }
  })
  return (
    <figure className="spark">
      <figcaption className="meter-head">
        <span>{title}</span>
        <span className="muted">{summary}</span>
      </figcaption>
      <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label={`${title}: ${summary}`}>
        <line className="spark-base" x1="0" x2={width} y1={height - 1} y2={height - 1} />
        {paths.map((p) => (p.d ? <path key={p.label} className={`spark-line ${p.className}`} d={p.d} /> : null))}
        {marks.map((m, i) => (
          <line key={i} className={`spark-mark ${m.className}`} x1={x(m.t)} x2={x(m.t)} y1="0" y2={height} />
        ))}
      </svg>
      {series.length > 1 ? (
        <div className="spark-legend">
          {series.map((s) => (
            <span key={s.label} className={s.className}>
              <span className="spark-key" aria-hidden="true" />
              {s.label}
            </span>
          ))}
        </div>
      ) : null}
    </figure>
  )
}

/** Used over allocatable, 1 being full; nothing when either is missing or there is nothing to allocate. */
function shareOf(used: number | undefined, allocatable: number | undefined): number | undefined {
  return used === undefined || !allocatable ? undefined : used / allocatable
}

/** A share as the summary says it. Not cut at 100: usage can pass what a node allocates. */
const asPercent = (share: number) => `${Math.round(share * 100)}%`

/**
 * The fullest node's line of one chart, and its highest point of the hour for
 * the summary (#141): one full node among idle ones is a small part of the
 * nodes' sum. Nothing when the line would lie on the nodes' own at every
 * point, as it does on one node: the chart then draws one line. The two lie
 * on each other within half a percent, less than a pixel of the chart and
 * more than the rounding of the share the server sends.
 */
function fullestOf(
  points: { t: number; share: number | undefined; fullest: FullestNode | undefined }[],
): { series: Series; peak: FullestNode } | undefined {
  let peak: FullestNode | undefined
  let apart = false
  for (const { share, fullest } of points) {
    if (!fullest) continue
    // The later of two equal peaks: the one nearer to now.
    if (!peak || fullest.share >= peak.share) peak = fullest
    if (share === undefined || Math.abs(fullest.share - share) >= 0.005) apart = true
  }
  if (!peak || !apart) return undefined
  return {
    peak,
    series: {
      label: 'fullest node',
      className: 'spark-fullest',
      values: points.map((p) => ({ t: p.t, v: p.fullest?.share })),
    },
  }
}

/**
 * The last hour of the API server's response time, CPU and memory. The CPU and
 * memory are the cluster's: every node's usage added up, over what those nodes
 * could allocate together (#122). Each point is drawn as the share it was when
 * it was sampled, against the sample's own allocatable (#141): a node that
 * joined or left since moves no earlier point. A sample that carries no
 * allocatable (no node had figures, or an older server took it) has no point,
 * and the line breaks there: drawn against the nodes of now it would say what
 * nobody measured. A second line is the fullest node's share of itself, where
 * it differs from the nodes'. Each node as it is now is in the panel above.
 */
function HistoryCharts({ health, now }: { health: FactoryHealth; now: number }) {
  const history = health.history ?? []
  if (history.length < 2) {
    return <p className="panel-note">The last hour fills in here: the UI samples the factory every 15 seconds.</p>
  }
  const points = history.map((s) => ({
    ...s,
    t: Date.parse(s.at),
    cpuShare: shareOf(s.cpuMilli, s.cpuAllocatableMilli),
    memoryShare: shareOf(s.memoryBytes, s.memoryAllocatableBytes),
  }))
  const latest = points[points.length - 1]
  const worstApi = Math.max(...points.map((p) => p.apiMs))
  const stalls = points.filter((p) => p.apiFailed || p.apiMs >= API_STALL_MS)
  const cpuFullest = fullestOf(points.map((p) => ({ t: p.t, share: p.cpuShare, fullest: p.cpuFullest })))
  const memoryFullest = fullestOf(points.map((p) => ({ t: p.t, share: p.memoryShare, fullest: p.memoryFullest })))
  /** "now 8.5 cores of 16.0 cores · fullest worker-b 94%": the latest sample, and the hour's fullest node. */
  const summary = (used: string, allocatable: string | undefined, fullest: typeof cpuFullest) =>
    `now ${used}` +
    (allocatable ? ` of ${allocatable}` : '') +
    (fullest ? ` · fullest ${fullest.peak.node} ${asPercent(fullest.peak.share)}` : '')
  return (
    <div className="history">
      <Sparkline
        title="API server response"
        summary={`now ${formatDuration(latest.apiMs, { fine: true })} · worst ${formatDuration(worstApi, { fine: true })}`}
        max={Math.max(1000, worstApi)}
        now={now}
        series={[{ label: 'API', className: 'spark-api', values: points.map((p) => ({ t: p.t, v: p.apiMs })) }]}
        marks={stalls.map((p) => ({ t: p.t, className: 'spark-stall' }))}
      />
      {health.metrics && points.some((p) => p.cpuShare !== undefined) ? (
        <Sparkline
          title="CPU"
          summary={summary(
            formatCpu(latest.cpuMilli),
            latest.cpuAllocatableMilli ? formatCpu(latest.cpuAllocatableMilli) : undefined,
            cpuFullest,
          )}
          max={1}
          now={now}
          series={[
            { label: 'nodes', className: 'spark-node', values: points.map((p) => ({ t: p.t, v: p.cpuShare })) },
            ...(cpuFullest ? [cpuFullest.series] : []),
            {
              label: 'CI',
              className: 'spark-ci',
              values: points.map((p) => ({ t: p.t, v: shareOf(p.ciCpuMilli, p.cpuAllocatableMilli) })),
            },
            {
              label: 'agents',
              className: 'spark-agents',
              values: points.map((p) => ({ t: p.t, v: shareOf(p.agentCpuMilli ?? 0, p.cpuAllocatableMilli) })),
            },
          ]}
        />
      ) : null}
      {health.metrics && points.some((p) => p.memoryShare !== undefined) ? (
        <Sparkline
          title="Memory"
          summary={summary(
            formatBytes(latest.memoryBytes),
            latest.memoryAllocatableBytes ? formatBytes(latest.memoryAllocatableBytes) : undefined,
            memoryFullest,
          )}
          max={1}
          now={now}
          series={[
            { label: 'nodes', className: 'spark-node', values: points.map((p) => ({ t: p.t, v: p.memoryShare })) },
            ...(memoryFullest ? [memoryFullest.series] : []),
          ]}
        />
      ) : null}
    </div>
  )
}
