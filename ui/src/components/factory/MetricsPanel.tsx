// The Work area's "How well it works" (docs/plans/measure-and-improve.md, step
// 3): over the last 7 or 30 days, what merged, how much with no person's code,
// how long each stage took and what a merged Task cost, for every Task and per
// Agent configuration, and the cost by the change's size. Tables, so a screen
// reader reads what a sighted reader sees.

import { useMemo, useState } from 'react'
import type { Problem } from '../../k8s/sources'
import type { Task } from '../../k8s/types'
import { formatCost } from '../../model/brief'
import { plural } from '../../model/describe'
import { formatDuration } from '../../model/format'
import { factoryMetrics, NOT_RECORDED, PERIODS, SIZES, type MetricsRow, type Period } from '../../model/metrics'
import { PanelState } from '../PanelState'
import './tokens.css'

const share = (v: number | undefined) => (v === undefined ? '–' : `${Math.round(v * 100)}%`)
const span = (ms: number | undefined) => (ms === undefined ? '–' : formatDuration(ms))
const cost = (milli: number | undefined) => (milli === undefined ? '–' : formatCost(milli))

export function MetricsPanel({
  tasks,
  now,
  loaded,
  problems,
}: {
  tasks: Task[]
  now: number
  loaded: boolean
  problems: Problem[]
}) {
  const [period, setPeriod] = useState<Period>(7)
  const m = useMemo(() => factoryMetrics(tasks, now, period), [tasks, now, period])
  const rows = m.byConfig.length > 1 ? [m.all, ...m.byConfig] : m.byConfig.length ? m.byConfig : [m.all]
  return (
    <div className="panel metrics-panel">
      <h2 className="panel-title">How well it works</h2>
      <p className="panel-caption">
        The Tasks that merged in the last {period} days, by what their implementer ran on: how many merged with no
        person’s commit, how long from the first run to the pull request, its first review and the merge, and what a
        merged Task cost. Judge a change to an Agent, a route or a guide by these.
      </p>
      <div className="tokens-agent" role="group" aria-label="Period">
        {PERIODS.map((p) => (
          <button key={p} type="button" className="chip" aria-pressed={p === period} onClick={() => setPeriod(p)}>
            {p} days
          </button>
        ))}
      </div>
      <PanelState
        loaded={loaded}
        problems={problems}
        empty={!m.all.merged}
        loading="Loading the Tasks…"
        emptyText={`No Task merged a pull request in the last ${period} days.`}
        small
      >
        <div className="tokens-table-wrap">
          <table className="tokens-table">
            <caption className="visually-hidden">
              The Tasks merged in the last {period} days, by their implementer’s configuration
            </caption>
            <thead>
              <tr>
                <th scope="col">Configuration</th>
                <th scope="col">Merged</th>
                <th scope="col" title="Merged with no person’s commit, of those whose commits were read">
                  Autonomy
                </th>
                <th scope="col">Round 1</th>
                <th scope="col" title="The median, from the first run">
                  To pull request
                </th>
                <th scope="col" title="The median, from the first run">
                  To review
                </th>
                <th scope="col" title="The median, from the first run">
                  To merge
                </th>
                <th scope="col" title="The median, of those that reported their cost">
                  Cost
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <MetricsTableRow key={r.key} row={r} />
              ))}
            </tbody>
          </table>
        </div>
        <div className="tokens-table-wrap">
          <table className="tokens-table">
            <caption className="visually-hidden">What a merged Task cost, by the size of its change</caption>
            <thead>
              <tr>
                <th scope="col">Size</th>
                <th scope="col">Merged</th>
                <th scope="col" title="The median, of those that reported their cost">
                  Cost
                </th>
              </tr>
            </thead>
            <tbody>
              {SIZES.map(({ key, label }) => {
                const s = m.bySize.find((b) => b.size === key)
                return (
                  <tr key={key}>
                    <th scope="row">{label}</th>
                    <td>{s?.tasks ?? 0}</td>
                    <td>{cost(s?.costMilliUSD)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
        <p className="panel-note">
          {notes(
            m.all,
            m.withoutSize,
            m.byConfig.some((r) => r.key === NOT_RECORDED),
          )}
        </p>
      </PanelState>
    </div>
  )
}

function MetricsTableRow({ row }: { row: MetricsRow }) {
  const label = row.key === 'all' ? 'All' : row.key === NOT_RECORDED ? 'Not recorded' : row.key
  return (
    <tr>
      <th scope="row">{label}</th>
      <td>{row.merged}</td>
      <td title={`Of ${plural(row.commitsRead, 'Task')} whose commits were read`}>{share(row.autonomy)}</td>
      <td>{share(row.firstRound)}</td>
      <td>{span(row.toPullMs)}</td>
      <td>{span(row.toReviewMs)}</td>
      <td>{span(row.toMergeMs)}</td>
      <td>{cost(row.costMilliUSD)}</td>
    </tr>
  )
}

/** What the tables leave out, in a sentence each. */
function notes(all: MetricsRow, withoutSize: number, unrecorded: boolean): string {
  const out: string[] = [
    'Autonomy counts the code, not the decisions: approving a spec or a deploy is a person’s designed part.',
  ]
  if (all.commitsRead < all.merged) {
    out.push(`${plural(all.merged - all.commitsRead, 'merged Task')} had no commits read: left out of Autonomy.`)
  }
  if (withoutSize) out.push(`${plural(withoutSize, 'merged Task')} had no size read: left out of the sizes.`)
  if (unrecorded) out.push('Not recorded: Tasks whose runs ended before each run recorded its Agent.')
  return out.join(' ')
}
