// A Task's page: the object header (its stepper from Pending to Merged, with
// Blocked and Failed off the path; status.history as the timeline; its agent
// runs; its issue, pull request and follow-ups), then its rounds against the
// implementer's maxRounds, what its agent runs used and how often one handed
// off at the token limit, which runs did and at what sizes, and the agents'
// own words.

import { LABEL_CREW, type Agent, type Task } from '../../k8s/types'
import { describeUsage, formatTokens } from '../../model/brief'
import { plural } from '../../model/describe'
import { followUpLinks, taskHeadline, taskRoles } from '../../model/factory'
import { taskNextStep } from '../../model/lifecycle'
import { AgentName, UsageLine, When } from '../bits'
import { CommandLine, ObjectHeader, type ChildRef, type OutLink } from '../ObjectHeader'
import { pageHref, storyHref } from './links'
import { agentRuns, taskTimeline } from './rows'
import { describeHandoffs } from '../../model/tokens'

export function TaskPage({ task, agents }: { task: Task; agents: Agent[] }) {
  const ns = task.metadata.namespace ?? ''
  const name = task.metadata.name
  const status = task.status
  const headline = taskHeadline(task)
  const roles = taskRoles(task, agents)
  const implementer = agents.find(
    (a) => a.metadata.name === task.spec.agentRef.name && (a.metadata.namespace ?? '') === ns,
  )
  const crew = task.metadata.labels?.[LABEL_CREW]
  const container = task.spec.containerRef?.name
  const brief = task.spec.briefRef?.name
  const limit = implementer?.spec.sessionTokenLimit
  const usage = describeUsage(status?.costMilliUSD, status?.runsWithoutCost, status?.tokens, limit)
  const handoffs = status?.tokens?.sessionHandoffs ?? 0

  const links: OutLink[] = []
  if (headline.url) links.push({ href: headline.url, label: `Issue #${headline.issue}` })
  if (status?.pullRequestURL) links.push({ href: status.pullRequestURL, label: `Pull request #${status.pullRequest}` })
  if (status?.checks?.url) links.push({ href: status.checks.url, label: `Checks: ${status.checks.state ?? 'unknown'}` })
  for (const f of followUpLinks(task)) links.push({ href: f.url, label: `Follow-up ${f.label}` })

  const all = agentRuns(task)
  const runs: ChildRef[] = all.map((r) => ({
    kind: 'Agent run',
    name: `run ${r.n}`,
    note: (
      <>
        round {r.round}, {r.role} by {r.agent} · {r.recorded ? 'ended' : 'started'} <When at={r.at} full />
        {r.current ? ' · running now' : ''}
        {r.handoffs?.length ? ` · ${describeHandoffs(r.handoffs)}` : ''}
      </>
    ),
  }))
  // A Task that records its runs names the ones that handed off; an older one
  // has only the count, and the sizes in its events.
  const recorded = all.some((r) => r.recorded)
  const handedOff = all.filter((r) => r.handoffs?.length)

  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="Task"
        name={name}
        namespace={ns}
        title={headline.text}
        breadcrumb={[
          { label: `Project ${ns}`, href: storyHref(ns) },
          ...(container ? [{ label: `Container ${container}`, href: pageHref(ns, 'Container', container) }] : []),
          ...(brief ? [{ label: `Brief ${brief}`, href: pageHref(ns, 'Brief', brief) }] : []),
          ...(crew ? [{ label: `Crew ${crew}`, href: pageHref(ns, 'Crew', crew) }] : []),
        ]}
        phase={status?.phase}
        conditions={status?.conditions ?? []}
        timeline={taskTimeline(task)}
        childrenTitle="Agent runs"
        children={runs}
        links={links}
        next={taskNextStep(task)}
      />

      <section className="panel" aria-labelledby="task-rounds">
        <h2 className="panel-title small-title" id="task-rounds">
          Rounds and agent runs
        </h2>
        <dl className="facts">
          <dt>Round</dt>
          <dd>
            {status?.round ?? 0}
            {implementer?.spec.maxRounds
              ? ` of ${implementer.spec.maxRounds}`
              : ' of the implementer’s maxRounds, not read yet'}
            {status?.reviewAttempts ? (
              <span className="muted"> · {plural(status.reviewAttempts, 'review attempt')} this round</span>
            ) : null}
          </dd>
          <dt>Implements</dt>
          <dd>
            <AgentName role={roles.implementer} />
          </dd>
          <dt>Reviews</dt>
          <dd>
            <AgentName role={roles.reviewer} />
          </dd>
          <dt>Used</dt>
          <dd>{usage ? <UsageLine usage={usage} /> : <span className="muted">Nothing reported yet.</span>}</dd>
          <dt>Handoffs</dt>
          <dd>
            {handoffs
              ? `${plural(handoffs, 'time')} an agent run handed off to a fresh one at the token limit${limit ? ` of ${formatTokens(limit)}` : ''}`
              : 'No agent run handed off.'}
          </dd>
        </dl>
        {handedOff.length ? (
          <ul className="panel-text small">
            {handedOff.map((r) => (
              <li key={r.n}>
                Run {r.n} (round {r.round}, {r.role}) {describeHandoffs(r.handoffs ?? [])}.
              </li>
            ))}
          </ul>
        ) : null}
        {handoffs && !recorded ? (
          <>
            <p className="panel-text small">
              The Task’s status counts handoffs over all runs; which run handed off, and at how many tokens, is in its
              events:
            </p>
            <CommandLine
              command={`kubectl -n ${ns} get events --field-selector involvedObject.kind=Task,involvedObject.name=${name},reason=SessionHandedOff`}
            />
          </>
        ) : null}
      </section>

      {status?.review?.summary || status?.summary || status?.feedback ? (
        <section className="panel" aria-labelledby="task-words">
          <h2 className="panel-title small-title" id="task-words">
            In the agents’ words
          </h2>
          {status.review?.summary ? (
            <>
              <h3 className="object-section">The review{status.review.verdict ? `: ${status.review.verdict}` : ''}</h3>
              <pre className="pre">{status.review.summary}</pre>
            </>
          ) : null}
          {status.summary ? (
            <>
              <h3 className="object-section">The implementer’s account</h3>
              <pre className="pre">{status.summary}</pre>
            </>
          ) : null}
          {status.feedback ? (
            <>
              <h3 className="object-section">Feedback for the next round</h3>
              <pre className="pre">{status.feedback}</pre>
            </>
          ) : null}
        </section>
      ) : null}
    </div>
  )
}
