// A Crew's page: the object header (its state, its Ready condition, its Tasks,
// and what a person does: re-run a failed release, resume it, or fix what
// keeps it waiting), then who works and how many at once, its issues, and its
// routes by label, and its last release with its runs, Go module, publishing
// and bump issues. With the UI server's actions on, the person pauses or
// resumes it or its running Tasks, sets those and its release settings, and
// re-runs a failed release's runs (CrewActions). Where
// it publishes says so when its Brief sets that, and the form changes it on
// the Brief.

import type { BriefWriter } from '../../k8s/briefs'
import { briefTarget, type CrewWriter } from '../../k8s/crews'
import type { Agent, Brief, Container, Crew, CrewRelease, Task } from '../../k8s/types'
import { useActs } from '../../lib/hooks'
import { crewSummary, issueList, type CrewRouteSummary } from '../../model/crews'
import { PUBLISH_MIGRATION_URL, RELEASE_RUN, publishFailure } from '../../model/releases'
import type { AgentRole } from '../../model/factory'
import { crewNextStep } from '../../model/lifecycle'
import { AgentName, When } from '../bits'
import { ExternalLinkIcon } from '../icons'
import { ObjectHeader, type OutLink } from '../ObjectHeader'
import {
  CrewPauseButton,
  CrewPauseRunsButton,
  ParallelForm,
  ReleaseForm,
  RerunButton,
  SignInToAct,
} from './CrewActions'
import { pageHref, storyHref } from './links'
import { crewTasks } from './rows'

export function CrewPage({
  crew,
  agents,
  tasks,
  containers,
  briefs,
  writer,
  briefWriter,
}: {
  crew: Crew
  agents: Agent[]
  tasks: Task[]
  containers: Container[]
  briefs: Brief[]
  writer: CrewWriter
  briefWriter?: BriefWriter
}) {
  const acts = useActs()
  const ns = crew.metadata.namespace ?? ''
  const name = crew.metadata.name
  const summary = crewSummary(crew, agents, tasks)
  const containerName = crew.spec.containerRef.name
  const container = containers.find((c) => c.metadata.name === containerName)
  const system = container?.spec.softwareSystemRef.name
  const madeBy = briefs.find((b) => b.status?.created?.some((c) => c.kind === 'Crew' && c.name === name))
  const release = crew.status?.lastRelease
  const target = briefTarget(crew)
  const setBy = target && briefs.find((b) => (b.metadata.namespace ?? '') === ns && b.metadata.name === target.brief)
  const links: OutLink[] = []
  if (container?.status?.repositoryURL) links.push({ href: container.status.repositoryURL, label: 'Its repository' })
  if (release?.runURL) links.push({ href: release.runURL, label: `Release ${release.tag}’s CI run` })
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
      />
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
    </div>
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
