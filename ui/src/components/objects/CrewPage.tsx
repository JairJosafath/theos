// A Crew's page: the object header (its state, its Ready condition, its Tasks,
// and what a person does: re-run a failed release, resume it, or fix what
// keeps it waiting), then who works and how many at once, its issues, and its
// last release with its runs, Go module, publishing and bump issues.

import type { Agent, Brief, Container, Crew, CrewRelease, Task } from '../../k8s/types'
import { useNow } from '../../lib/hooks'
import { crewSummary, issueList } from '../../model/crews'
import { timeAgo } from '../../model/describe'
import { RELEASE_RUN } from '../../model/releases'
import { crewNextStep } from '../../model/lifecycle'
import { AgentName } from '../bits'
import { ExternalLinkIcon } from '../icons'
import { ObjectHeader, type OutLink } from '../ObjectHeader'
import { pageHref, storyHref } from './links'
import { crewTasks } from './rows'

export function CrewPage({
  crew,
  agents,
  tasks,
  containers,
  briefs,
}: {
  crew: Crew
  agents: Agent[]
  tasks: Task[]
  containers: Container[]
  briefs: Brief[]
}) {
  const now = useNow()
  const ns = crew.metadata.namespace ?? ''
  const name = crew.metadata.name
  const summary = crewSummary(crew, agents, tasks)
  const containerName = crew.spec.containerRef.name
  const container = containers.find((c) => c.metadata.name === containerName)
  const system = container?.spec.softwareSystemRef.name
  const madeBy = briefs.find((b) => b.status?.created?.some((c) => c.kind === 'Crew' && c.name === name))
  const release = crew.status?.lastRelease
  const links: OutLink[] = []
  if (container?.status?.repositoryURL) links.push({ href: container.status.repositoryURL, label: 'Its repository' })
  if (release?.runURL) links.push({ href: release.runURL, label: `Release ${release.tag}’s CI run` })
  return (
    <main className="shell-page object-page">
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
      />

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
            {crew.spec.paused ? <span className="muted"> · paused: it picks up no new issues</span> : null}
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
          <dd>{summary.lastPolled ? timeAgo(summary.lastPolled, now) : 'not yet'}</dd>
        </dl>
      </section>

      <section className="panel" aria-labelledby="crew-release">
        <h2 className="panel-title small-title" id="crew-release">
          {release ? `Last release: ${release.tag}` : 'Releases'}
        </h2>
        <p className="panel-text">
          {crew.spec.tagReleases
            ? 'The Crew tags a release once its milestone is done.'
            : 'The Crew tags no releases: a person tags them.'}
          {crew.spec.prepareReleases ? ' It files each release’s prepare issue.' : ''}
          {crew.spec.publishReleases ? ` It publishes them to ${crew.spec.publishReleases.github.repository}.` : ''}
        </p>
        {release ? <ReleaseFacts release={release} now={now} /> : <p className="muted small">No release yet.</p>}
      </section>
    </main>
  )
}

function ReleaseFacts({ release, now }: { release: CrewRelease; now: number }) {
  return (
    <dl className="facts">
      <dt>Tagged</dt>
      <dd>
        {timeAgo(release.time, now)} <span className="muted mono">at {release.commit.slice(0, 7)}</span>
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
        {release.rerunTime ? <span className="muted"> · re-run {timeAgo(release.rerunTime, now)}</span> : null}
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
            {release.publish.message ? <span className="muted"> · {release.publish.message}</span> : null}
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
