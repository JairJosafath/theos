// The Work area's "What the Scorers found" (docs/plans/done/measure-and-improve.md,
// step 5): each Scorer's pass rate per criterion and per implementer
// configuration, from its Score Tasks of its newest criteria. A table, so a
// screen reader reads what a sighted reader sees.

import { useMemo } from 'react'
import type { Problem } from '../../k8s/sources'
import type { Task } from '../../k8s/types'
import { NOT_RECORDED } from '../../model/metrics'
import { describeRate, scorerRates } from '../../model/scores'
import { PanelState } from '../PanelState'
import './tokens.css'

export function ScoresPanel({ tasks, loaded, problems }: { tasks: Task[]; loaded: boolean; problems: Problem[] }) {
  const all = useMemo(() => scorerRates(tasks), [tasks])
  const namespaces = new Set(all.map((s) => s.namespace))
  return (
    <div className="panel metrics-panel">
      <h2 className="panel-title">What the Scorers found</h2>
      <p className="panel-caption">
        Each Scorer’s criteria, and how often its judge found a change meets one, by what the change’s implementer ran
        on. Unclear verdicts are counted apart, not against. Only the newest criteria count.
      </p>
      <PanelState
        loaded={loaded}
        problems={problems}
        empty={!all.length}
        loading="Loading the Tasks…"
        emptyText="No Scorer has judged a Task yet: make one, and its judge runs show here."
        small
      >
        {all.map((s) => (
          <div className="tokens-table-wrap" key={s.key}>
            <table className="tokens-table">
              <caption>
                Scorer {s.scorer}
                {namespaces.size > 1 ? ` (${s.namespace})` : ''} · {s.scored} judged
              </caption>
              <thead>
                <tr>
                  <th scope="col">Criterion</th>
                  {s.configs.map((c) => (
                    <th scope="col" key={c}>
                      {c === NOT_RECORDED ? 'Not recorded' : c}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {s.criteria.map((criterion, i) => (
                  <tr key={i}>
                    <th scope="row" className="score-criterion">
                      {criterion}
                    </th>
                    {s.configs.map((c) => (
                      <td key={c}>{describeRate(s.rates[i]?.[c])}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ))}
      </PanelState>
    </div>
  )
}
