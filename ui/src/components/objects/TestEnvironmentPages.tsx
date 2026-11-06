// Test environments (docs/plans/done/test-environments.md, step 6): the Project
// page's list of its TestEnvironments, each with its live leases, today's
// spend and its limits; a TestEnvironment's page (what it has, its leases,
// its CI pool and how a run uses it); and the Task page's section on its
// run's leases and why it waits for one. Read only: the operator leases and
// returns, and a page shows the fields it names one by one, never the object
// whole.

import { unreadOf, type Connection, type StoryData } from '../../k8s/sources'
import { RESOURCES, type Task, type TestEnvironment, type TestEnvironmentLease } from '../../k8s/types'
import { useNow } from '../../lib/hooks'
import { formatCost } from '../../model/brief'
import {
  describeCIPool,
  describeDevices,
  describeLeaseCount,
  describeLimits,
  describeSpend,
  environmentWait,
  leaseHolders,
  leaseTone,
  taskLeases,
  testEnvLeases,
  testEnvMember,
  testEnvReadiness,
} from '../../model/testenvs'
import { When } from '../bits'
import { CommandLine, ObjectHeader } from '../ObjectHeader'
import { Badge } from './DeployPages'
import { pageHref, projectCrumb } from './links'
import { ReadProblem } from './ReadProblem'

/** One live lease: its state, the Task whose run holds it, since when and why it waits or failed. */
function LeaseLine({ ns, lease, cost }: { ns: string; lease: TestEnvironmentLease; cost: boolean }) {
  const holder = lease.holder
  return (
    <li className="test-env-lease">
      <Badge tone={leaseTone(lease.state)} label={lease.state ?? 'Unknown'} />{' '}
      {holder ? (
        <>
          held by{' '}
          <a className="mono" href={pageHref(ns, 'Task', holder)}>
            {holder}
          </a>
        </>
      ) : (
        <span className="muted">held by no Task it names</span>
      )}
      {lease.since ? (
        <span className="muted small">
          {' '}
          · since <When at={lease.since} />
        </span>
      ) : null}
      {cost ? <span className="muted small"> · {formatCost(lease.costMilliUSD ?? 0)}</span> : null}
      <span className="muted small mono"> · {lease.id}</span>
      {lease.message ? <div className="panel-text small">{lease.message}</div> : null}
    </li>
  )
}

/** The Project page's test environments, each with Ready, its leases against its limits and today's spend. */
export function TestEnvironmentList({ ns, data, connection }: { ns: string; data: StoryData; connection: Connection }) {
  const now = useNow()
  const unread = unreadOf(connection, RESOURCES.testEnvironments)
  const envs = data.testEnvironments
  return (
    <section className="panel" aria-labelledby="project-test-environments">
      <h2 className="panel-title small-title" id="project-test-environments">
        Where its runs test
      </h2>
      {/* A failed re-watch keeps what it had loaded: say the failure, above the list. */}
      {unread ? <ReadProblem problem={unread} /> : null}
      {envs.length ? (
        <ul className="test-envs">
          {envs.map((env) => {
            const name = env.metadata.name
            const ready = testEnvReadiness(env)
            const leases = testEnvLeases(env)
            const count = describeLeaseCount(env)
            return (
              <li key={name} className="test-env">
                <a className="mono" href={pageHref(ns, 'TestEnvironment', name)}>
                  {name}
                </a>{' '}
                <Badge tone={ready.tone} label={ready.label} title={ready.condition?.message} />
                <span className="muted small">
                  {' '}
                  · {count.text}
                  {count.full ? ', all taken' : ''} · {describeSpend(env, now)} · {describeLimits(env)}
                </span>
                {env.spec.description ? <div className="panel-text small">{env.spec.description}</div> : null}
                {leases.length ? (
                  <ul className="object-list test-env-leases">
                    {leases.map((l) => (
                      <LeaseLine key={l.id} ns={ns} lease={l} cost={!env.spec.local} />
                    ))}
                  </ul>
                ) : null}
              </li>
            )
          })}
        </ul>
      ) : unread ? null : (
        <p className="panel-text muted">None: no TestEnvironment in this project, so a run labelled env/… waits.</p>
      )}
    </section>
  )
}

/** A TestEnvironment's page: what it has, its leases against its limits, its CI pool and how a run uses it. */
export function TestEnvironmentPage({ env }: { env: TestEnvironment }) {
  const now = useNow()
  const ns = env.metadata.namespace ?? ''
  const name = env.metadata.name
  const member = testEnvMember(env)
  const ready = testEnvReadiness(env)
  const local = env.spec.local
  const leases = testEnvLeases(env)
  const count = describeLeaseCount(env)
  const devices = describeDevices(env)
  const placement = [
    ...Object.entries(local?.nodeSelector ?? {}).map(([k, v]) => `node ${k}=${v}`),
    ...(local?.tolerations ?? []).map(
      (t) =>
        `tolerates ${t.key ?? 'every taint'}${t.operator === 'Equal' && t.value ? `=${t.value}` : ''}${t.effect ? `:${t.effect}` : ''}`,
    ),
  ]
  const resources = (['requests', 'limits'] as const).flatMap((kind) => {
    const list = Object.entries(local?.resources?.[kind] ?? {}).sort(([a], [b]) => a.localeCompare(b))
    return list.length ? [`${kind}: ${list.map(([k, v]) => `${k} ${v}`).join(', ')}`] : []
  })
  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="TestEnvironment"
        name={name}
        namespace={ns}
        title={env.spec.description}
        breadcrumb={[projectCrumb(ns)]}
        conditions={env.status?.conditions ?? []}
        childrenTitle="Tasks whose runs hold it"
        children={leaseHolders(env).map((holder) => ({
          kind: 'Task',
          name: holder,
          href: pageHref(ns, 'Task', holder),
          note: leases
            .filter((l) => l.holder === holder)
            .map((l) => l.state ?? 'Unknown')
            .join(', '),
        }))}
        links={[]}
        sections={{
          overview: (
            <>
              <section className="panel" aria-labelledby="testenv-has">
                <h2 className="panel-title small-title" id="testenv-has">
                  What it has
                </h2>
                <dl className="facts">
                  <dt>Provider</dt>
                  <dd>
                    {member.label} <span className="mono muted">({member.member})</span>
                  </dd>
                  {local ? (
                    <>
                      <dt>Image</dt>
                      <dd className="mono">{local.image}</dd>
                      <dt>Devices</dt>
                      <dd>
                        {devices.length ? (
                          <span className="mono">{devices.join(', ')}</span>
                        ) : (
                          <span className="muted">None.</span>
                        )}
                      </dd>
                      <dt>Runtime class</dt>
                      <dd>
                        {local.runtimeClassName ? (
                          <span className="mono">{local.runtimeClassName}</span>
                        ) : (
                          <span className="muted">The cluster’s default.</span>
                        )}
                      </dd>
                      <dt>Placement</dt>
                      <dd>{placement.length ? placement.join('; ') : <span className="muted">Any node.</span>}</dd>
                      <dt>Resources</dt>
                      <dd>{resources.length ? resources.join('; ') : <span className="muted">Not set.</span>}</dd>
                    </>
                  ) : null}
                  <dt>Ready</dt>
                  <dd>
                    <Badge tone={ready.tone} label={ready.label} />
                    {ready.condition?.message ? <span className="panel-text"> {ready.condition.message}</span> : null}
                  </dd>
                </dl>
              </section>

              <section className="panel" aria-labelledby="testenv-leases">
                <h2 className="panel-title small-title" id="testenv-leases">
                  Its leases
                </h2>
                <dl className="facts">
                  <dt>Live</dt>
                  <dd>
                    {count.text}
                    {count.full ? <span className="muted">: a run that wants one more waits, AllLeased</span> : null}
                  </dd>
                  <dt>Limits</dt>
                  <dd>{describeLimits(env)}</dd>
                  <dt>Spent</dt>
                  <dd>{describeSpend(env, now)}</dd>
                </dl>
                {leases.length ? (
                  <ul className="object-list test-env-leases">
                    {leases.map((l) => (
                      <LeaseLine key={l.id} ns={ns} lease={l} cost={!local} />
                    ))}
                  </ul>
                ) : (
                  <p className="panel-text muted">No live lease: no run holds it now.</p>
                )}
              </section>

              <section className="panel" aria-labelledby="testenv-use">
                <h2 className="panel-title small-title" id="testenv-use">
                  How a run uses it
                </h2>
                <p className="panel-text">
                  An issue labelled <span className="mono">env/{name}</span>, or a Container whose Standard or Framework
                  names it, gets a lease for each implementer’s and verifier’s run, which runs commands there:
                </p>
                <CommandLine command={`theos-env run ${name} -- <command>`} />
                <dl className="facts">
                  <dt>CI</dt>
                  <dd>
                    {describeCIPool(env)}
                    {env.spec.ci?.runnerImage ? (
                      <span className="muted small">
                        {' '}
                        · registers with <span className="mono">{env.spec.ci.runnerImage}</span>
                      </span>
                    ) : null}
                  </dd>
                </dl>
              </section>
            </>
          ),
        }}
      />
    </div>
  )
}

/** The Task page's test environments: why its run waits for one, the leases it holds, and those its runs held. */
export function TaskEnvironments({ task }: { task: Task }) {
  const ns = task.metadata.namespace ?? ''
  const leases = taskLeases(task)
  const wait = environmentWait(task)
  const leased = task.status?.leasedEnvironments ?? []
  if (!leases.length && !wait && !leased.length) return null
  const envLink = (name: string) => (
    <a className="mono" href={pageHref(ns, 'TestEnvironment', name)}>
      {name}
    </a>
  )
  return (
    <section className="panel" aria-labelledby="task-environments">
      <h2 className="panel-title small-title" id="task-environments">
        Test environments
      </h2>
      {wait ? (
        <p className="panel-text">
          <Badge tone="waiting" label={wait.why || 'Waiting'} /> Its run waits for a test environment, at no cost of a
          round: {wait.message}
        </p>
      ) : null}
      {leases.length ? (
        <>
          <h3 className="object-section">Its run holds</h3>
          <ul className="object-list">
            {leases.map((l) => (
              <li key={l.lease}>
                {envLink(l.name)}
                <span className="muted small">
                  {' '}
                  · lease <span className="mono">{l.lease}</span> · run <span className="mono">{l.sandbox}</span>
                  {l.since ? (
                    <>
                      {' '}
                      · since <When at={l.since} />
                    </>
                  ) : null}
                </span>
              </li>
            ))}
          </ul>
          <p className="panel-note">
            The run reaches each with <span className="mono">theos-env run &lt;name&gt; -- &lt;command&gt;</span>; the
            lease is returned when the run ends or is suspended.
          </p>
        </>
      ) : null}
      {leased.length ? (
        <p className="panel-text">
          Its runs leased{' '}
          {leased.map((name, i) => (
            <span key={name}>
              {i ? ', ' : ''}
              {envLink(name)}
            </span>
          ))}
          <span className="muted">: a criterion they could check is never left to a person.</span>
        </p>
      ) : null}
    </section>
  )
}
