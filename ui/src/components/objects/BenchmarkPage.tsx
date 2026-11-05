// A Benchmark's page (docs/plans/done/measure-and-improve.md, step 7): what it
// compares and from where, each variant's approval rate, rounds, time and
// cost, its Scorers' criteria by variant, every rate with its 95% interval,
// and the comparison in a sentence; its runs link to their Tasks.

import type { Benchmark, Task } from '../../k8s/types'
import { benchmarkRuns, describeBenchmarkRate } from '../../model/benchmark'
import { formatCost } from '../../model/brief'
import { describeTaskPhase, plural } from '../../model/describe'
import { ObjectHeader } from '../ObjectHeader'
import { pageHref, projectCrumb } from './links'
import '../factory/tokens.css'

export function BenchmarkPage({ bench, tasks }: { bench: Benchmark; tasks: Task[] }) {
  const ns = bench.metadata.namespace ?? ''
  const name = bench.metadata.name
  const spec = bench.spec
  const status = bench.status
  const runs = benchmarkRuns(bench, tasks)
  const variants = status?.variants ?? []
  const criteria = [
    ...new Map(
      variants.flatMap((v) => v.criteria ?? []).map((c) => [`${c.scorer}\u0000${c.criterion}`, c] as const),
    ).values(),
  ]
  const goals = status?.goals?.length ? status.goals : spec.goals.map((g) => g.goal ?? `Issue #${g.issue}`)
  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="Benchmark"
        name={name}
        namespace={ns}
        breadcrumb={[
          projectCrumb(ns),
          { label: `Container ${spec.containerRef.name}`, href: pageHref(ns, 'Container', spec.containerRef.name) },
        ]}
        conditions={status?.conditions ?? []}
        childrenTitle="Its runs"
        children={runs.map((t) => ({
          kind: 'Task',
          name: t.metadata.name,
          href: pageHref(ns, 'Task', t.metadata.name),
          note: `${t.spec.benchmark?.variant}, goal ${(t.spec.benchmark?.goal ?? 0) + 1}, run ${t.spec.benchmark?.repetition}: ${describeTaskPhase(t.status?.phase).label}`,
        }))}
        links={[]}
        sections={{
          overview: (
            <>
              <section className="panel" aria-labelledby="benchmark-found">
                <h2 className="panel-title small-title" id="benchmark-found">
                  What it found
                </h2>
                <p className="panel-text">{status?.comparison || 'No run has finished yet.'}</p>
                <div className="tokens-table-wrap">
                  <table className="tokens-table">
                    <caption className="visually-hidden">Each variant’s runs, with 95% intervals</caption>
                    <thead>
                      <tr>
                        <th scope="col">Variant</th>
                        <th scope="col">Finished</th>
                        <th scope="col">Approved</th>
                        <th scope="col">Rounds</th>
                        <th scope="col">Time</th>
                        <th scope="col">Cost</th>
                      </tr>
                    </thead>
                    <tbody>
                      {variants.map((v) => (
                        <tr key={v.name}>
                          <th scope="row">{v.name}</th>
                          <td>
                            {v.finished ?? 0} of {v.tasks ?? 0}
                          </td>
                          <td>{describeBenchmarkRate(v.approval)}</td>
                          <td title="On average">{v.meanRounds ?? '–'}</td>
                          <td title="The median">{v.medianMinutes ? `${v.medianMinutes} min` : '–'}</td>
                          <td title="The median">{v.medianCostMilliUSD ? formatCost(v.medianCostMilliUSD) : '–'}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {criteria.length ? (
                  <div className="tokens-table-wrap">
                    <table className="tokens-table">
                      <caption>What its Scorers found, by variant</caption>
                      <thead>
                        <tr>
                          <th scope="col">Criterion</th>
                          {variants.map((v) => (
                            <th scope="col" key={v.name}>
                              {v.name}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {criteria.map((c) => (
                          <tr key={`${c.scorer}/${c.criterion}`}>
                            <th scope="row" className="score-criterion">
                              {c.criterion} <span className="muted">({c.scorer})</span>
                            </th>
                            {variants.map((v) => {
                              const r = v.criteria?.find((x) => x.scorer === c.scorer && x.criterion === c.criterion)
                              return (
                                <td key={v.name}>
                                  {describeBenchmarkRate(r?.rate)}
                                  {r?.unclear ? `, ${r.unclear} unclear` : ''}
                                </td>
                              )
                            })}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : null}
                <p className="panel-note">
                  A variant is ahead only when its 95% interval is wholly above another’s. Its runs never merge: an
                  approved one ends Approved.
                </p>
              </section>
              <section className="panel" aria-labelledby="benchmark-what">
                <h2 className="panel-title small-title" id="benchmark-what">
                  What it compares
                </h2>
                <dl className="facts">
                  <dt>From</dt>
                  <dd className="mono">{spec.base}</dd>
                  <dt>Variants</dt>
                  <dd>
                    {spec.variants
                      .map(
                        (v) =>
                          `${v.name}: ${v.implementerRef.name} implements, ${(v.reviewerRef ?? v.implementerRef).name} reviews${v.verifierRef ? `, ${v.verifierRef.name} verifies` : ''}`,
                      )
                      .join('; ')}
                  </dd>
                  <dt>Runs</dt>
                  <dd>
                    {plural(goals.length, 'goal')} × {plural(spec.variants.length, 'variant')} ×{' '}
                    {plural(spec.repetitions ?? 3, 'repetition')}, {spec.parallel ?? 1} at a time
                  </dd>
                  <dt>Spent</dt>
                  <dd>
                    {formatCost(status?.costMilliUSD ?? 0)} of ${spec.budgetUSD}
                  </dd>
                  <dt>Judged by</dt>
                  <dd>{spec.scorerRefs?.length ? spec.scorerRefs.map((s) => s.name).join(', ') : 'no Scorer'}</dd>
                </dl>
                <ol className="panel-text small">
                  {goals.map((g, i) => (
                    <li key={i}>{g.split('\n')[0]}</li>
                  ))}
                </ol>
              </section>
            </>
          ),
        }}
      />
    </div>
  )
}
