// Test environments (docs/plans/done/test-environments.md, steps 6 to 10): the
// Project page's list of its TestEnvironments, each with its live leases,
// today's spend against its budget, its limits, a remote member's credential
// and CI when they fail, and a deviceFarm environment's test runs; a
// TestEnvironment's page (what it has, its credential, its leases, its test
// runs, its CI and how a run uses it); and the Task page's section on its
// run's leases and why it waits for one. Read only: the operator leases and
// returns, and a page shows the fields it names one by one, never the object
// whole, and of a credential only the Secret's name.

import { unreadOf, type Connection, type StoryData } from '../../k8s/sources'
import {
  RESOURCES,
  type Task,
  type TestEnvironment,
  type TestEnvironmentLease,
  type TestEnvironmentTestRun,
} from '../../k8s/types'
import { useNow } from '../../lib/hooks'
import { formatCost } from '../../model/brief'
import type { Tone } from '../../model/describe'
import {
  AWS_SECRET_KEYS,
  describeCIPool,
  describeDevices,
  describeLeaseCount,
  describeLimits,
  describeMacHosts,
  describeSpend,
  environmentWait,
  leaseHolder,
  leaseHolders,
  leaseTone,
  taskLeases,
  testEnvCI,
  testEnvCredentials,
  testEnvLeases,
  testEnvMember,
  testEnvReadiness,
  testEnvRemote,
  testEnvSecret,
  testEnvSpend,
  testEnvTestRuns,
  testRunBadge,
} from '../../model/testenvs'
import { When } from '../bits'
import { ExternalLinkIcon } from '../icons'
import { CommandLine, ObjectHeader } from '../ObjectHeader'
import { Badge } from './DeployPages'
import { pageHref, projectCrumb } from './links'
import { ReadProblem } from './ReadProblem'

/** Who holds a lease: the Task whose run holds it, linked, or the CI runner a remote machine runs one job for. */
function Holder({ ns, holder }: { ns: string; holder: string | undefined }) {
  const h = leaseHolder(holder)
  if (!h) return <span className="muted">held by no Task it names</span>
  if (h.kind === 'CI')
    return (
      <>
        held by CI runner <span className="mono">{h.runner}</span> of{' '}
        <a className="mono" href={pageHref(ns, 'GitProvider', h.gitProvider)}>
          {h.gitProvider}
        </a>
        <span className="muted">, for one job</span>
      </>
    )
  return (
    <>
      held by{' '}
      <a className="mono" href={pageHref(ns, 'Task', h.name)}>
        {h.name}
      </a>
    </>
  )
}

/** One live lease: its state, who holds it, since when, its cost and why it waits or failed. */
function LeaseLine({ ns, lease, cost }: { ns: string; lease: TestEnvironmentLease; cost: boolean }) {
  return (
    <li className="test-env-lease">
      <Badge tone={leaseTone(lease.state)} label={lease.state ?? 'Unknown'} /> <Holder ns={ns} holder={lease.holder} />
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

/**
 * One test run a lease's run asked for: its result, or its state while there
 * is none; the test package's type and commit; the Task and Container; since
 * when, its cost and the run on the provider's console; its outcome, and
 * whether it still waits to be commented on the Task's pull request.
 */
function TestRunLine({ ns, run }: { ns: string; run: TestEnvironmentTestRun }) {
  const badge = testRunBadge(run)
  return (
    <li className="test-env-lease">
      <Badge tone={badge.tone} label={badge.label} /> <span className="mono">{run.type ?? 'test'}</span> run
      {run.commit ? (
        <>
          {' '}
          of <span className="mono">{run.commit.slice(0, 12)}</span>
        </>
      ) : null}
      {run.holder ? (
        <>
          , asked for by{' '}
          <a className="mono" href={pageHref(ns, 'Task', run.holder)}>
            {run.holder}
          </a>
        </>
      ) : null}
      {run.container ? (
        <>
          {' '}
          on{' '}
          <a className="mono" href={pageHref(ns, 'Container', run.container)}>
            {run.container}
          </a>
        </>
      ) : null}
      {run.since ? (
        <span className="muted small">
          {' '}
          · since <When at={run.since} />
        </span>
      ) : null}
      {run.costMilliUSD ? <span className="muted small"> · {formatCost(run.costMilliUSD)}</span> : null}
      {run.url ? (
        // A link keeps its own colour in muted text.
        <span className="muted small">
          {' '}
          ·{' '}
          <a href={run.url} target="_blank" rel="noreferrer">
            the run on Device Farm <ExternalLinkIcon size={12} />
          </a>
        </span>
      ) : null}
      {run.outcome ? <div className="panel-text small">{run.outcome}</div> : null}
      {run.comment && run.holder ? (
        <div className="muted small">
          Its outcome goes on {run.holder}’s pull request once there is one; the report is on the lease’s branch.
        </div>
      ) : null}
    </li>
  )
}

/** A condition that isn't True, as one line under an environment in the Project page's list. */
function ProblemLine({
  what,
  badge,
}: {
  what: string
  badge: { label: string; tone: Tone; condition?: { message?: string } }
}) {
  return (
    <div className="panel-text small">
      {what} <Badge tone={badge.tone} label={badge.label} />
      {badge.condition?.message ? ` ${badge.condition.message}` : null}
    </div>
  )
}

/**
 * The Project page's test environments, each with Ready, where a remote one
 * runs, its leases against its limits, today's spend against its budget, its
 * credential and CI when they fail, and its test runs.
 */
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
            const credentials = testEnvCredentials(env)
            const ci = testEnvCI(env)
            const leases = testEnvLeases(env)
            const runs = testEnvTestRuns(env)
            const count = describeLeaseCount(env)
            const remote = testEnvRemote(env)
            return (
              <li key={name} className="test-env">
                <a className="mono" href={pageHref(ns, 'TestEnvironment', name)}>
                  {name}
                </a>{' '}
                <Badge tone={ready.tone} label={ready.label} title={ready.condition?.message} />
                <span className="muted small">
                  {' '}
                  · {remote ? `${testEnvMember(env).where} · ` : ''}
                  {count.text}
                  {count.full ? ', all taken' : ''} · {describeSpend(env, now)} · {describeLimits(env)}
                </span>
                {credentials && credentials.tone !== 'ok' && credentials.condition ? (
                  <ProblemLine what="Credentials" badge={credentials} />
                ) : null}
                {ci && ci.tone !== 'ok' && ci.condition ? <ProblemLine what="CI" badge={ci} /> : null}
                {env.spec.description ? <div className="panel-text small">{env.spec.description}</div> : null}
                {leases.length ? (
                  <ul className="object-list test-env-leases">
                    {leases.map((l) => (
                      <LeaseLine key={l.id} ns={ns} lease={l} cost={remote} />
                    ))}
                  </ul>
                ) : null}
                {runs.length ? (
                  <ul className="object-list test-env-leases" aria-label={`Test runs on ${name}`}>
                    {runs.map((r) => (
                      <TestRunLine key={r.id} ns={ns} run={r} />
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

/** A remote member's facts: where its machines run, or its Device Farm project and devices. */
function RemoteFacts({ env }: { env: TestEnvironment }) {
  const { aws, deviceFarm } = env.spec
  const account = (aws ?? deviceFarm)?.account
  const mac = describeMacHosts(env)
  return (
    <>
      {aws ? (
        <>
          <dt>Region</dt>
          <dd className="mono">{aws.region}</dd>
        </>
      ) : null}
      {deviceFarm ? (
        <>
          <dt>Project</dt>
          <dd className="mono">{deviceFarm.projectArn}</dd>
          <dt>Device pool</dt>
          <dd className="mono">{deviceFarm.devicePoolArn}</dd>
        </>
      ) : null}
      <dt>Account</dt>
      <dd>
        {account ? (
          <span className="mono">{account}</span>
        ) : (
          <span className="muted">Not set: whichever account the credential is in.</span>
        )}
      </dd>
      {aws ? (
        <>
          <dt>Instance type</dt>
          <dd className="mono">{aws.instanceType}</dd>
          {mac ? (
            <>
              <dt>Mac hosts</dt>
              <dd>
                {mac}
                {aws.mac?.keepHostHours === undefined ? (
                  <span className="muted"> (keepHostHours not set: EC2’s minimum)</span>
                ) : null}
              </dd>
            </>
          ) : null}
          <dt>Machine image</dt>
          <dd className="mono">{aws.imageId}</dd>
          <dt>Subnet</dt>
          <dd className="mono">{aws.subnetId}</dd>
        </>
      ) : null}
    </>
  )
}

/** A remote member's credential: the Secret's name only, the keys the operator reads from it, and CredentialsValid. */
function CredentialFacts({ env }: { env: TestEnvironment }) {
  const secret = testEnvSecret(env)
  const credentials = testEnvCredentials(env)
  if (!credentials) return null
  const [id, key, session] = AWS_SECRET_KEYS
  return (
    <>
      <dt>Credentials</dt>
      <dd>
        Secret <span className="mono">{secret}</span>
        <span className="muted small"> · its name only: the UI never reads Secrets</span>
        <p className="muted small">
          The operator alone reads its keys <span className="mono">{id}</span>, <span className="mono">{key}</span> and,
          when there, <span className="mono">{session}</span>: they never reach a run, an environment or a CI job.
        </p>
      </dd>
      <dt>Credentials valid</dt>
      <dd>
        <Badge tone={credentials.tone} label={credentials.label} />
        {credentials.condition?.message ? <span className="panel-text"> {credentials.condition.message}</span> : null}
      </dd>
    </>
  )
}

/**
 * A TestEnvironment's page: what it has, its credential, its leases against
 * its limits and budget, its test runs, its CI and how a run uses it.
 */
export function TestEnvironmentPage({ env }: { env: TestEnvironment }) {
  const now = useNow()
  const ns = env.metadata.namespace ?? ''
  const name = env.metadata.name
  const member = testEnvMember(env)
  const ready = testEnvReadiness(env)
  const ci = testEnvCI(env)
  const spend = testEnvSpend(env, now)
  const local = env.spec.local
  const deviceFarm = env.spec.deviceFarm
  const remote = testEnvRemote(env)
  const leases = testEnvLeases(env)
  const runs = testEnvTestRuns(env)
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
                  {remote ? (
                    <>
                      <RemoteFacts env={env} />
                      <CredentialFacts env={env} />
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
                  <dd>
                    {describeSpend(env, now)}
                    {spend.over ? (
                      <span className="muted">
                        : no lease starts until tomorrow (UTC), or a higher limits.budgetUSDPerDay, OverBudget; the
                        leases held finish their run
                      </span>
                    ) : remote && spend.budget ? (
                      <span className="muted small"> · by the provider’s count, the ended leases’ included</span>
                    ) : null}
                  </dd>
                </dl>
                {leases.length ? (
                  <ul className="object-list test-env-leases">
                    {leases.map((l) => (
                      <LeaseLine key={l.id} ns={ns} lease={l} cost={remote} />
                    ))}
                  </ul>
                ) : (
                  <p className="panel-text muted">No live lease: no run holds it now.</p>
                )}
              </section>

              {deviceFarm ? (
                <section className="panel" aria-labelledby="testenv-test-runs">
                  <h2 className="panel-title small-title" id="testenv-test-runs">
                    Its test runs
                  </h2>
                  {runs.length ? (
                    <ul className="object-list test-env-leases">
                      {runs.map((r) => (
                        <TestRunLine key={r.id} ns={ns} run={r} />
                      ))}
                    </ul>
                  ) : (
                    <p className="panel-text muted">None yet: no run that holds a lease asked for one.</p>
                  )}
                </section>
              ) : null}

              <section className="panel" aria-labelledby="testenv-use">
                <h2 className="panel-title small-title" id="testenv-use">
                  How a run uses it
                </h2>
                {deviceFarm ? (
                  <>
                    <p className="panel-text">
                      An issue labelled <span className="mono">env/{name}</span>, or a Container whose Standard or
                      Framework names it, gets a lease for each implementer’s and verifier’s run, which asks for a test
                      run of an app and its test package over its Git host:
                    </p>
                    <CommandLine command={`theos-env test ${name} --type <type> --app <file> --tests <file>`} />
                    <p className="panel-note">
                      The operator, which alone holds the credential, runs it on the device pool, gives the report,
                      screenshots and logs back to the run’s working tree, and comments the outcome on the Task’s pull
                      request.
                    </p>
                  </>
                ) : (
                  <>
                    <p className="panel-text">
                      An issue labelled <span className="mono">env/{name}</span>, or a Container whose Standard or
                      Framework names it, gets a lease for each implementer’s and verifier’s run, which runs commands
                      there:
                    </p>
                    <CommandLine command={`theos-env run ${name} -- <command>`} />
                  </>
                )}
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
                    {ci ? (
                      <div>
                        <Badge tone={ci.tone} label={ci.label} />
                        {ci.condition?.message ? <span className="panel-text"> {ci.condition.message}</span> : null}
                      </div>
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
            The run reaches each with <span className="mono">theos-env run &lt;name&gt; -- &lt;command&gt;</span>, or
            asks a Device Farm one for a test run with <span className="mono">theos-env test &lt;name&gt;</span>; the
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
