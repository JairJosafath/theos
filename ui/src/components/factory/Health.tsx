// The factory's health: its components, the node's usage, and the history
// charts of the last hours.

import type { ComponentHealth, FactoryHealth, NodeHealth, PodHealth } from '../../k8s/health'
import { plural, timeAgo } from '../../model/describe'
import { API_STALL_MS } from '../../model/attention'
import { formatBytes, formatCpu, formatDuration, percent } from '../../model/format'

const COMPONENT_TONE: Record<ComponentHealth['state'], string> = {
  ok: 'ok',
  degraded: 'waiting',
  down: 'error',
  absent: 'unknown',
  unknown: 'unknown',
}

export function HealthPanel({ health, loaded, now }: { health?: FactoryHealth; loaded: boolean; now: number }) {
  return (
    <div className="panel">
      <h2 className="panel-title small-title">Health</h2>
      {!loaded || !health ? (
        <p className="panel-note">Reading the factory’s health…</p>
      ) : (
        <>
          {health.nodes.map((node) => (
            <NodeUsage key={node.name} node={node} metrics={health.metrics} />
          ))}
          <HistoryCharts health={health} now={now} />
          <ul className="components">
            {health.components.map((c) => (
              <ComponentRow key={c.key} component={c} now={now} />
            ))}
          </ul>
          {!health.metrics ? (
            <p className="panel-note">
              No CPU or memory figures: metrics-server is not installed (
              <span className="mono">make metrics-server</span>
              ).
            </p>
          ) : null}
          <p className="panel-note">Checked {timeAgo(health.generatedAt, now)}.</p>
        </>
      )}
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

function ComponentRow({ component, now }: { component: ComponentHealth; now: number }) {
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
          {component.state === 'absent' ? <span className="muted"> · not installed</span> : null}
          {waiting ? <span className="badge badge-sm badge-error">{waiting}</span> : null}
        </div>
        {component.pods.length ? (
          <div className="component-sub muted">
            {component.pods.filter((p) => p.ready).length}/{component.pods.length} ready
            {restarts ? ` · ${plural(restarts, 'restart')}` : ''}
            {recent ? ` · last ${timeAgo(recent.at, now)}` : ''}
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

/** The last hour of the API server's response time, CPU and memory. */
function HistoryCharts({ health, now }: { health: FactoryHealth; now: number }) {
  const history = health.history ?? []
  if (history.length < 2) {
    return <p className="panel-note">The last hour fills in here: the UI samples the factory every 15 seconds.</p>
  }
  const points = history.map((s) => ({ ...s, t: Date.parse(s.at) }))
  const node = health.nodes[0]
  const latest = points[points.length - 1]
  const worstApi = Math.max(...points.map((p) => p.apiMs))
  const stalls = points.filter((p) => p.apiFailed || p.apiMs >= API_STALL_MS)
  return (
    <div className="history">
      <h3 className="section-title">Last hour</h3>
      <Sparkline
        title="API server response"
        summary={`now ${formatDuration(latest.apiMs, { fine: true })} · worst ${formatDuration(worstApi, { fine: true })}`}
        max={Math.max(1000, worstApi)}
        now={now}
        series={[{ label: 'API', className: 'spark-api', values: points.map((p) => ({ t: p.t, v: p.apiMs })) }]}
        marks={stalls.map((p) => ({ t: p.t, className: 'spark-stall' }))}
      />
      {health.metrics && node ? (
        <>
          <Sparkline
            title="CPU"
            summary={`of ${formatCpu(node.cpuAllocatableMilli)}`}
            max={node.cpuAllocatableMilli}
            now={now}
            series={[
              { label: 'node', className: 'spark-node', values: points.map((p) => ({ t: p.t, v: p.cpuMilli })) },
              { label: 'CI', className: 'spark-ci', values: points.map((p) => ({ t: p.t, v: p.ciCpuMilli })) },
              {
                label: 'agents',
                className: 'spark-agents',
                values: points.map((p) => ({ t: p.t, v: p.agentCpuMilli ?? 0 })),
              },
            ]}
          />
          <Sparkline
            title="Memory"
            summary={`now ${formatBytes(latest.memoryBytes)} of ${formatBytes(node.memoryAllocatableBytes)}`}
            max={node.memoryAllocatableBytes}
            now={now}
            series={[
              { label: 'node', className: 'spark-node', values: points.map((p) => ({ t: p.t, v: p.memoryBytes })) },
            ]}
          />
        </>
      ) : null}
    </div>
  )
}
