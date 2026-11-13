// A Crew's page: the object header (its state, its Ready condition, its Tasks,
// and what a person does: re-run a failed release, resume it, or fix what
// keeps it waiting), then who works and how many at once, its issues, and its
// routes by label, its schedules (work on a timer: when each is next due and
// the issue it filed last), the schedules its Container's Standard proposes
// (those it may take, and those whose name it uses with where its own
// differs), and its last release with its runs, Go module,
// publishing and bump issues. With the UI server's actions on, the person pauses or
// resumes it or its running Tasks, sets those and its release settings,
// re-runs a failed release's runs, runs a schedule now and takes a proposal
// (CrewActions). Where
// it publishes says so when its Brief sets that, and the form changes it on
// the Brief. Its tabs: Overview (who works, routes), Status, Activity (the last
// release and its runs) and Related (its Tasks, links).

import type { BriefWriter } from '../../k8s/briefs'
import { RUN_SCHEDULE, briefTarget, type CrewWriter } from '../../k8s/crews'
import type { Agent, ApprovalRule, Brief, Container, Crew, CrewRelease, Standard, Task } from '../../k8s/types'
import { useEffect, useId, useRef, useState } from 'react'
import { useActs } from '../../lib/hooks'
import { approvalAsks, approvalRuleFor, crewSummary, issueList, type CrewRouteSummary } from '../../model/crews'
import { PUBLISH_MIGRATION_URL, RELEASE_RUN, publishFailure } from '../../model/releases'
import type { AgentRole } from '../../model/factory'
import { absoluteTime } from '../../model/format'
import { crewNextStep } from '../../model/lifecycle'
import { scheduleProposals, writtenZone, type ScheduleField, type ScheduleProposals } from '../../model/schedules'
import { AgentName, When } from '../bits'
import { ExternalLinkIcon } from '../icons'
import { ObjectHeader, type OutLink } from '../ObjectHeader'
import {
  CrewPauseButton,
  CrewPauseRunsButton,
  ParallelForm,
  ReleaseForm,
  RerunButton,
  RunScheduleButton,
  SignInToAct,
  TakeProposalForm,
  TimeZoneList,
} from './CrewActions'
import { pageHref, storyHref } from './links'
import { crewTasks } from './rows'

export function CrewPage({
  crew,
  agents,
  tasks,
  containers,
  standards,
  briefs,
  writer,
  briefWriter,
}: {
  crew: Crew
  agents: Agent[]
  tasks: Task[]
  containers: Container[]
  standards: Standard[]
  briefs: Brief[]
  writer: CrewWriter
  briefWriter?: BriefWriter
}) {
  const acts = useActs()
  const ns = crew.metadata.namespace ?? ''
  const name = crew.metadata.name
  const summary = crewSummary(crew, agents, tasks)
  const proposals = scheduleProposals(crew, containers, standards)
  const containerName = crew.spec.containerRef.name
  const container = containers.find((c) => c.metadata.name === containerName)
  const system = container?.spec.softwareSystemRef.name
  const madeBy = briefs.find((b) => b.status?.created?.some((c) => c.kind === 'Crew' && c.name === name))
  const release = crew.status?.lastRelease
  const links: OutLink[] = []
  if (container?.status?.repositoryURL) links.push({ href: container.status.repositoryURL, label: 'Its repository' })
  if (release?.runURL) links.push({ href: release.runURL, label: `Release ${release.tag}’s CI run` })
  const overview = <CrewOverview crew={crew} writer={writer} summary={summary} proposals={proposals} />
  const activity = <CrewReleasePanel crew={crew} writer={writer} briefs={briefs} briefWriter={briefWriter} />
  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="Crew"
        name={name}
        namespace={ns}
        title={crew.spec.displayName}
        breadcrumb={[
          { label: `Project ${ns}`, href: storyHref(ns) },
          ...(system ? [{ label: `System ${system}`, href: storyHref(ns, system) }] : []),
          { label: `Container ${containerName}`, href: pageHref(ns, 'Container', containerName) },
          ...(madeBy
            ? [{ label: `Made by Brief ${madeBy.metadata.name}`, href: pageHref(ns, 'Brief', madeBy.metadata.name) }]
            : []),
        ]}
        phase={crew.status?.phase}
        conditions={crew.status?.conditions ?? []}
        childrenTitle="Its Tasks"
        children={crewTasks(crew, tasks)}
        links={links}
        next={crewNextStep(crew, briefs)}
        actions={
          acts === 'act' ? (
            <>
              <CrewPauseRunsButton crew={crew} writer={writer} />
              <CrewPauseButton crew={crew} writer={writer} />
            </>
          ) : undefined
        }
        sections={{ overview, activity }}
      />
    </div>
  )
}

function CrewOverview({
  crew,
  writer,
  summary,
  proposals,
}: {
  crew: Crew
  writer: CrewWriter
  summary: ReturnType<typeof crewSummary>
  proposals: ScheduleProposals
}) {
  const acts = useActs()
  const ns = crew.metadata.namespace ?? ''
  return (
    <>
      {acts === 'sign-in' ? (
        <section className="panel">
          <SignInToAct />
        </section>
      ) : null}

      <section className="panel" aria-labelledby="crew-work">
        <h2 className="panel-title small-title" id="crew-work">
          Who works, and how many at once
        </h2>
        {summary.description ? <p className="panel-text">{summary.description}</p> : null}
        <dl className="facts">
          <dt>Implements</dt>
          <dd>
            <AgentName role={summary.implementer} />
          </dd>
          <dt>Reviews</dt>
          <dd>
            <AgentName role={summary.reviewer} />
          </dd>
          <dt>Places</dt>
          <dd>
            {crew.spec.parallel
              ? `${summary.active.length} of ${crew.spec.parallel} busy`
              : `${summary.active.length} busy, of the factory’s default`}
            {crew.spec.paused ? (
              <span className="muted">
                {crew.spec.pauseRuns
                  ? ' · paused: it picks up no new issues, and its running Tasks pause at their next tool call'
                  : ' · paused: it picks up no new issues'}
              </span>
            ) : null}
          </dd>
          <dt>Now</dt>
          <dd>
            {summary.active.length ? (
              summary.active.map((a) => (
                <div key={a.key}>
                  <a className="mono" href={pageHref(ns, 'Task', a.name)}>
                    {a.name}
                  </a>
                  {a.headline ? <span className="muted"> · {a.headline}</span> : null}
                </div>
              ))
            ) : (
              <span className="muted">No Task at work.</span>
            )}
          </dd>
          <dt>Ready issues</dt>
          <dd>{summary.ready.length ? issueList(summary.ready) : <span className="muted">none</span>}</dd>
          <dt>Waiting on others</dt>
          <dd>{summary.waiting.length ? issueList(summary.waiting) : <span className="muted">none</span>}</dd>
          <dt>Read the issues</dt>
          <dd>{summary.lastPolled ? <When at={summary.lastPolled} full /> : 'not yet'}</dd>
        </dl>
        {acts === 'act' ? <ParallelForm crew={crew} writer={writer} /> : null}
      </section>

      {summary.routes.length ? <RoutesPanel routes={summary.routes} /> : null}
      {crew.spec.approval?.length ? <ApprovalPanel rules={crew.spec.approval} /> : null}
      {crew.spec.schedules?.length ? <SchedulesPanel crew={crew} writer={acts === 'act' ? writer : undefined} /> : null}
      {proposals.offered.length || proposals.inUse.length ? (
        <ProposalsPanel crew={crew} proposals={proposals} writer={acts === 'act' ? writer : undefined} />
      ) : null}
    </>
  )
}

/** Which of the Crew's changes wait for a person, and which merge on CI alone. */
function ApprovalPanel({ rules }: { rules: ApprovalRule[] }) {
  return (
    <section className="panel" aria-labelledby="crew-approval">
      <h2 className="panel-title small-title" id="crew-approval">
        Who approves a change
      </h2>
      <p className="panel-text">
        Each file a pull request changes takes the first rule that matches it, and the change needs the strictest of
        what its files need. A file no rule matches merges on the reviewer’s approval.
      </p>
      <ol className="routes">
        {rules.map((rule, i) => (
          <li key={i}>
            <span className="mono">{approvalRuleFor(rule)}</span>
            <span className="muted"> · </span>
            {approvalAsks(rule.require)}
          </li>
        ))}
      </ol>
    </section>
  )
}

/** The Crew's recurring goals: when each is next due, and the issue it filed last. */
function SchedulesPanel({ crew, writer }: { crew: Crew; writer?: CrewWriter }) {
  const scheduled = crew.status?.conditions?.find((c) => c.type === 'Scheduled')
  // The schedule a person asked to run now, while the operator has not filed its issue.
  const asked = crew.metadata.annotations?.[RUN_SCHEDULE]
  return (
    <section className="panel" aria-labelledby="crew-schedules">
      <h2 className="panel-title small-title" id="crew-schedules">
        Work on a timer
      </h2>
      <p className="panel-text">
        When a schedule comes due the Crew files an issue with its goal and works it like any other. While that issue is
        open, the schedule’s next time is passed over{crew.spec.paused ? '; a paused Crew files none' : ''}. Run now
        files a schedule’s issue at once, under the same two rules.
      </p>
      {asked && scheduled?.status === 'True' ? (
        <p className="panel-text" role="status">
          {scheduled.message}
        </p>
      ) : null}
      {scheduled && scheduled.status !== 'True' ? (
        <p className="panel-text" role="alert">
          {scheduled.message}
        </p>
      ) : null}
      <ol className="routes">
        {(crew.spec.schedules ?? []).map((s) => {
          const st = crew.status?.schedules?.find((x) => x.name === s.name)
          return (
            <li key={s.name}>
              <span className="mono">{s.name}</span>
              <span className="muted"> · </span>
              <span className="mono">{s.cron}</span>
              <span className="muted"> ({s.timeZone ?? 'UTC'})</span>
              <div>{s.goal}</div>
              <div className="muted">
                {st?.next ? (
                  <>
                    Next due <time dateTime={st.next}>{absoluteTime(st.next)}</time>
                  </>
                ) : (
                  'No next time'
                )}
                {st?.lastIssue ? (
                  <>
                    {' · last filed issue #'}
                    {st.lastIssue} <When at={st.lastFiled} />
                  </>
                ) : (
                  ' · nothing filed yet'
                )}
              </div>
              {writer ? (
                // A new button once the request is answered: the operator took the annotation off.
                <RunScheduleButton
                  key={`${s.name}|${asked === s.name ? 'asked' : (st?.lastFiled ?? '')}`}
                  crew={crew}
                  schedule={s.name}
                  asked={asked === s.name}
                  writer={writer}
                />
              ) : null}
            </li>
          )
        })}
      </ol>
    </section>
  )
}

const FIELD_NAME: Record<ScheduleField, string> = { cron: 'Cron', timeZone: 'Time zone', goal: 'Goal' }

/**
 * The schedules the Standard of the Crew's Container proposes. Those whose
 * name the Crew doesn't use are offered: with a writer a person takes one,
 * giving the time zone. Those whose name it uses are listed with where the
 * Crew's schedule differs. The name is all that links a schedule to a
 * proposal, so the panel says "in use" and "differs", never that a schedule
 * was taken from here or changed since. Nor does it call UTC a proposal's
 * own zone: the API server writes UTC into one that names none, so only
 * another zone is shown as written (writtenZone).
 *
 * A taken proposal's row, form and all, goes from the offered list when the
 * watch brings the Crew's new schedule. So the panel says the result, in a
 * live region that stays, and focus the row took with it moves there.
 */
export function ProposalsPanel({
  crew,
  proposals,
  writer,
}: {
  crew: Crew
  proposals: ScheduleProposals
  writer?: CrewWriter
}) {
  const zones = useId()
  const ns = crew.metadata.namespace ?? ''
  const { standard = '', offered, inUse } = proposals
  const [taken, setTaken] = useState<string>()
  const result = useRef<HTMLParagraphElement>(null)
  const listed = taken !== undefined && inUse.some((u) => u.proposal.name === taken)
  useEffect(() => {
    if (!listed) return
    // Only focus that went with the row: a person who moved on meanwhile stays where they are.
    const active = document.activeElement
    if (!active || active === document.body || !active.isConnected) result.current?.focus()
  }, [listed])
  return (
    <section className="panel" aria-labelledby="crew-proposals">
      <h2 className="panel-title small-title" id="crew-proposals">
        Schedules its Standard proposes
      </h2>
      <p className="panel-text">
        <a href={pageHref(ns, 'Standard', standard)}>Standard {standard}</a> proposes these to the Crews of its
        Containers. A proposal files nothing: the Crew works one once it has a schedule of that name, which is then the
        Crew’s own to change.
      </p>
      {writer ? (
        // Always there, so what it comes to say is announced.
        <p className="form-done proposals-result" role="status" tabIndex={-1} ref={result}>
          {taken ? `Added ${taken} to the Crew’s schedules.` : null}
        </p>
      ) : null}
      {offered.length ? (
        <>
          <h3 className="object-section">Not in use by this Crew</h3>
          <p className="panel-text">
            {writer
              ? 'Taking one adds it to the Crew’s schedules as it is written here. A proposal is written for no one place, so you say which time zone its cron is read in.'
              : `To take one, add it to this Crew’s spec.schedules under its name, with the time zone its cron is read in (kubectl edit crew ${crew.metadata.name} -n ${ns}).`}
          </p>
          {writer ? <TimeZoneList id={zones} /> : null}
          <ol className="routes">
            {offered.map((p) => (
              <li key={p.name}>
                <span className="mono">{p.name}</span>
                <span className="muted"> · </span>
                <span className="mono">{p.cron}</span>
                <div>{p.goal}</div>
                {writtenZone(p) ? (
                  <div className="muted">
                    Written for the time zone <span className="mono">{writtenZone(p)}</span>. The zone this Crew reads
                    it in is still the one you give.
                  </div>
                ) : null}
                {writer ? (
                  <TakeProposalForm crew={crew} proposal={p} zones={zones} writer={writer} onTaken={setTaken} />
                ) : null}
              </li>
            ))}
          </ol>
        </>
      ) : null}
      {inUse.length ? (
        <>
          <h3 className="object-section">Names this Crew uses</h3>
          <ol className="routes">
            {inUse.map(({ proposal, differs }) => (
              <li key={proposal.name}>
                <span className="mono">{proposal.name}</span>
                <span className="muted">
                  {differs.length
                    ? ' · the Crew’s schedule of this name differs from the proposal:'
                    : writtenZone(proposal)
                      ? ' · the Crew’s schedule of this name has the proposal’s cron, time zone and goal'
                      : ' · the Crew’s schedule of this name has the proposal’s cron and goal, and is in UTC, which a proposal that names no time zone also reads as'}
                </span>
                {differs.length ? (
                  <dl className="facts">
                    {differs.map((d) => (
                      <div key={d.field} className="facts-row">
                        <dt>{FIELD_NAME[d.field]}</dt>
                        <dd>
                          <div>
                            <span className="muted">The Crew’s: </span>
                            <span className={d.field === 'goal' ? undefined : 'mono'}>{d.crew}</span>
                          </div>
                          {d.field === 'timeZone' && !writtenZone(proposal) ? (
                            <div>
                              <span className="muted">The proposal: </span>
                              <span className="mono">{d.proposal}</span>
                              <span className="muted">, which one that names no time zone also reads as</span>
                            </div>
                          ) : (
                            <div>
                              <span className="muted">The proposal’s: </span>
                              <span className={d.field === 'goal' ? undefined : 'mono'}>{d.proposal}</span>
                            </div>
                          )}
                        </dd>
                      </div>
                    ))}
                  </dl>
                ) : null}
              </li>
            ))}
          </ol>
        </>
      ) : null}
    </section>
  )
}

function CrewReleasePanel({
  crew,
  writer,
  briefs,
  briefWriter,
}: {
  crew: Crew
  writer: CrewWriter
  briefs: Brief[]
  briefWriter?: BriefWriter
}) {
  const acts = useActs()
  const ns = crew.metadata.namespace ?? ''
  const release = crew.status?.lastRelease
  const target = briefTarget(crew)
  const setBy = target && briefs.find((b) => (b.metadata.namespace ?? '') === ns && b.metadata.name === target.brief)
  return (
    <section className="panel" aria-labelledby="crew-release">
      <h2 className="panel-title small-title" id="crew-release">
        {release ? `Last release: ${release.tag}` : 'Releases'}
      </h2>
      <p className="panel-text">
        {crew.spec.tagReleases
          ? 'The Crew tags a release once its milestone is done.'
          : 'The Crew tags no releases: a person tags them.'}
        {crew.spec.prepareReleases ? ' It files each release’s prepare issue.' : ''}
        {target
          ? null
          : crew.spec.publishReleases
            ? ` It publishes them to ${crew.spec.publishReleases.github.repository}.`
            : ''}
        {target ? (
          <PublishSetBy
            ns={ns}
            crew={crew}
            brief={target.brief}
            held={target.held}
            briefRepo={target.target?.github.repository}
          />
        ) : null}
      </p>
      {crew.spec.publishReleases ? (
        <p className="panel-text muted small">
          <span className="mono">publishReleases</span> is deprecated: a <span className="mono">github</span>{' '}
          DeployTarget and a <span className="mono">spec.deploy</span> entry on the Container ship the same release.{' '}
          <a href={PUBLISH_MIGRATION_URL} target="_blank" rel="noreferrer">
            How to move over <ExternalLinkIcon size={12} />
          </a>
        </p>
      ) : null}
      {release ? (
        <ReleaseFacts release={release} failure={publishFailure(crew)} />
      ) : (
        <p className="muted small">No release yet.</p>
      )}
      {acts === 'act' && release?.run === 'Failed' ? (
        <RerunButton key={`${release.tag}|${release.rerunTime ?? ''}`} crew={crew} writer={writer} />
      ) : null}
      {acts === 'act' ? <ReleaseForm crew={crew} writer={writer} brief={setBy} briefWriter={briefWriter} /> : null}
    </section>
  )
}

/**
 * The Crew's routes by label, in order: for each role, the first route whose
 * label the issue has and that names an Agent decides, else the Crew's own
 * Agent, shown here as "the Crew's".
 */
function RoutesPanel({ routes }: { routes: CrewRouteSummary[] }) {
  return (
    <section className="panel" aria-labelledby="crew-routes">
      <h2 className="panel-title small-title" id="crew-routes">
        Routes by label
      </h2>
      <p className="panel-text">
        Issues with a label go to these Agents instead; the first route an issue matches decides each role.
      </p>
      <ol className="routes">
        {routes.map((r) => (
          <li key={r.label}>
            <span className="mono">{r.label}</span>
            <span className="muted"> · implemented by </span>
            <RouteAgent role={r.implementer} named={r.names.implementer} />
            <span className="muted"> · reviewed by </span>
            <RouteAgent role={r.reviewer} named={r.names.reviewer} />
          </li>
        ))}
      </ol>
    </section>
  )
}

function RouteAgent({ role, named }: { role: AgentRole; named: boolean }) {
  return (
    <>
      <AgentName role={role} />
      {named ? null : <span className="muted"> (the Crew’s)</span>}
    </>
  )
}

/**
 * Where a Crew publishes when its Brief sets it: "set by Brief <name>" with a
 * link, or, for a Crew changed under its Brief, both targets and that it
 * publishes nothing until they agree.
 */
function PublishSetBy({
  ns,
  crew,
  brief,
  held,
  briefRepo,
}: {
  ns: string
  crew: Crew
  brief: string
  held: boolean
  briefRepo?: string
}) {
  const link = <a href={pageHref(ns, 'Brief', brief)}>Brief {brief}</a>
  const own = crew.spec.publishReleases?.github.repository
  if (!held) {
    return (
      <>
        {own ? ` It publishes them to ${own}, set by ` : ' Where it publishes them is set by '}
        {link}: change it there.
      </>
    )
  }
  return (
    <>
      {' '}
      It publishes nothing for now: the Crew was changed to {own ? `publish to ${own}` : 'publish nowhere'}, but {link}{' '}
      says {briefRepo ?? 'another target'}. Use one or the other; until they agree its releases wait.
    </>
  )
}

function ReleaseFacts({ release, failure }: { release: CrewRelease; failure?: string }) {
  return (
    <dl className="facts">
      <dt>Tagged</dt>
      <dd>
        <When at={release.time} full /> <span className="muted mono">at {release.commit.slice(0, 7)}</span>
      </dd>
      <dt>CI runs</dt>
      <dd>
        {release.run ? RELEASE_RUN[release.run] : 'not reported yet'}
        {release.runURL ? (
          <>
            {' '}
            <a href={release.runURL} target="_blank" rel="noreferrer">
              Open the run <ExternalLinkIcon size={11} />
            </a>
          </>
        ) : null}
        {release.rerunTime ? (
          <span className="muted">
            {' '}
            · re-run <When at={release.rerunTime} full />
          </span>
        ) : null}
      </dd>
      {release.goModule ? (
        <>
          <dt>Go module</dt>
          <dd>
            {release.goModule.state}
            {release.goModule.path ? (
              <span className="mono">
                {' '}
                {release.goModule.path}@{release.goModule.version ?? release.tag}
              </span>
            ) : null}
            {release.goModule.message ? <span className="muted"> · {release.goModule.message}</span> : null}
          </dd>
        </>
      ) : null}
      {release.publish ? (
        <>
          <dt>Published</dt>
          <dd>
            {release.publish.url ? (
              <a href={release.publish.url} target="_blank" rel="noreferrer">
                {release.publish.state} <ExternalLinkIcon size={11} />
              </a>
            ) : (
              release.publish.state
            )}
            {failure ? `: ${failure}` : null}
            {release.publish.message ? <span className="muted"> · {release.publish.message}</span> : null}
          </dd>
        </>
      ) : null}
      {release.publish?.source ? (
        <>
          <dt>Source</dt>
          <dd>
            {release.publish.source.url ? (
              <a className="mono" href={release.publish.source.url} target="_blank" rel="noreferrer">
                {release.publish.source.tag} <ExternalLinkIcon size={11} />
              </a>
            ) : (
              <span className="mono">{release.publish.source.tag}</span>
            )}
            <span className="muted mono"> at {release.publish.source.commit.slice(0, 7)}</span>
          </dd>
        </>
      ) : null}
      {release.bumps?.length ? (
        <>
          <dt>Bump issues</dt>
          <dd>
            {release.bumps.map((b) => (
              <div key={b.container}>
                {b.issueURL ? (
                  <a href={b.issueURL} target="_blank" rel="noreferrer">
                    in {b.container} <ExternalLinkIcon size={11} />
                  </a>
                ) : (
                  `in ${b.container}`
                )}
              </div>
            ))}
          </dd>
        </>
      ) : null}
    </dl>
  )
}
