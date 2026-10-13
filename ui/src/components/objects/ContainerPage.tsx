// A Container's page: the object header (its scaffold lifecycle and
// conditions, its Relationships with their Applied reason and its Sessions,
// the repository, and on Failed the retry kubectl line), then its template
// refs and its Standard with the update pull request in flight, and when CI
// runs its full check (ContainerChecks, which sets it with UI actions on);
// with UI actions on, a scaffold retry, an answer to the Standard's files left
// out and a Task from a goal (ContainerActions). The files left out show what
// the Standard changed in each, beside the take and keep buttons, or on their
// own when nobody here may answer them.

import { useContext } from 'react'
import type { ContainerWriter } from '../../k8s/containers'
import type { Brief, CatalogEntry, Container, Crew, Relationship, Session } from '../../k8s/types'
import { describeContainerType, splitTemplateRef } from '../../model/describe'
import { containerNextStep, describeStandardUpdate } from '../../model/lifecycle'
import { sessionsOf } from '../../model/story'
import { SessionContext } from '../../lib/hooks'
import { LeftOutFiles, SessionRow } from '../bits'
import { ExternalLinkIcon } from '../icons'
import { ObjectHeader, type OutLink } from '../ObjectHeader'
import { answersLeftOut, ContainerActions } from './ContainerActions'
import { ContainerChecks } from './ContainerChecks'
import { relationshipsOf } from './rows'
import { pageHref, storyHref } from './links'
import '../briefs.css'

export function ContainerPage({
  container,
  relationships,
  sessions,
  frameworks,
  briefs,
  crews,
  writer,
}: {
  container: Container
  relationships: Relationship[]
  sessions: Session[]
  frameworks: CatalogEntry[]
  briefs: Brief[]
  crews: Crew[]
  writer: ContainerWriter
}) {
  const session = useContext(SessionContext)
  const ns = container.metadata.namespace ?? ''
  const name = container.metadata.name
  const status = container.status
  const behind = status?.standard?.behind ?? []
  const system = container.spec.softwareSystemRef.name
  const own = sessionsOf(sessions, name)
  const madeBy = briefs.find((b) => b.status?.created?.some((c) => c.kind === 'Container' && c.name === name))
  const update = status?.standard?.update
  const described = describeStandardUpdate(container)
  const links: OutLink[] = []
  if (status?.repositoryURL) links.push({ href: status.repositoryURL, label: 'Its repository' })
  if (status?.checks?.url) links.push({ href: status.checks.url, label: `Checks on main: ${status.checks.state}` })
  if (update?.pullRequestURL)
    links.push({
      href: update.pullRequestURL,
      label: `Standard ${update.take ? 'take' : 'update'} pull request #${update.pullRequest ?? ''}`,
    })
  const framework = frameworks.find((f) => f.metadata.name === container.spec.frameworkRef.name)
  return (
    <main className="shell-page object-page">
      <ObjectHeader
        kind="Container"
        name={name}
        namespace={ns}
        title={container.spec.displayName}
        breadcrumb={[
          { label: `Project ${ns}`, href: storyHref(ns) },
          { label: `System ${system}`, href: storyHref(ns, system) },
          ...(madeBy
            ? [{ label: `Made by Brief ${madeBy.metadata.name}`, href: pageHref(ns, 'Brief', madeBy.metadata.name) }]
            : []),
        ]}
        phase={status?.phase}
        conditions={status?.conditions ?? []}
        childrenTitle="Relationships"
        children={relationshipsOf(relationships, name).map((r) => ({
          ...r,
          href: pageHref(ns, 'Relationship', r.name),
        }))}
        links={links}
        next={containerNextStep(container)}
      />

      <ContainerActions
        container={container}
        crew={crews.find((c) => c.spec.containerRef.name === name)}
        writer={writer}
      />

      {behind.length && !answersLeftOut(session, container) ? (
        <section className="panel" aria-labelledby="container-left-out">
          <h2 className="panel-title small-title" id="container-left-out">
            Standard files left out
          </h2>
          <p className="panel-text">
            {status!.standard!.name}’s updates left these out, as the repository changed them in the same lines:
          </p>
          <LeftOutFiles files={behind} className="panel-text" />
        </section>
      ) : null}

      <section className="panel" aria-labelledby="container-templates">
        <h2 className="panel-title small-title" id="container-templates">
          What it is made from
        </h2>
        <dl className="facts">
          <dt>Type</dt>
          <dd>{describeContainerType(container.spec.type)}</dd>
          <dt>Framework</dt>
          <dd className="mono">
            {container.spec.frameworkRef.name}
            {framework ? ` @ ${framework.spec.template.ref}` : ''}
          </dd>
          {container.spec.standardRef ? (
            <>
              <dt>Standard</dt>
              <dd className="mono">
                <a href={pageHref(ns, 'Standard', container.spec.standardRef.name)}>
                  {container.spec.standardRef.name}
                </a>
                {status?.standard ? ` @ ${status.standard.ref}` : ''}
              </dd>
            </>
          ) : null}
          {container.spec.computeRef ? (
            <>
              <dt>Compute</dt>
              <dd className="mono">
                <a href={pageHref(ns, 'Compute', container.spec.computeRef.name)}>{container.spec.computeRef.name}</a>
              </dd>
            </>
          ) : null}
          {container.spec.deploymentRef ? (
            <>
              <dt>Deployment</dt>
              <dd className="mono">
                <a href={pageHref(ns, 'Deployment', container.spec.deploymentRef.name)}>
                  {container.spec.deploymentRef.name}
                </a>
              </dd>
            </>
          ) : null}
          <dt>Git provider</dt>
          <dd className="mono">
            <a href={pageHref(ns, 'GitProvider', container.spec.gitProviderRef.name)}>
              {container.spec.gitProviderRef.name}
            </a>
          </dd>
          <dt>Scaffolded from</dt>
          <dd>
            {status?.scaffoldedFrom?.length ? (
              <ol className="object-templates">
                {status.scaffoldedFrom.map((ref) => {
                  const t = splitTemplateRef(ref)
                  return (
                    <li key={ref} className="mono">
                      {t.name}
                      {t.version ? <span className="muted"> @ {t.version}</span> : null}
                    </li>
                  )
                })}
              </ol>
            ) : (
              <span className="muted">Not scaffolded yet.</span>
            )}
          </dd>
        </dl>
      </section>

      {container.spec.standardRef ? <ContainerChecks container={container} writer={writer} /> : null}

      {update && described ? (
        <section className="panel" aria-labelledby="container-standard">
          <h2 className="panel-title small-title" id="container-standard">
            {described.title}
          </h2>
          <p className="panel-text">{described.text}</p>
          {update.pullRequestURL ? (
            <p className="panel-text">
              <a href={update.pullRequestURL} target="_blank" rel="noreferrer">
                Pull request #{update.pullRequest} <ExternalLinkIcon size={12} />
              </a>
            </p>
          ) : null}
          {update.leftOut?.length ? (
            <p className="panel-text">
              Left out, as the repository changed them: <span className="mono">{update.leftOut.join(', ')}</span>
            </p>
          ) : null}
        </section>
      ) : null}

      {own.length ? (
        <section className="panel" aria-labelledby="container-sessions">
          <h2 className="panel-title small-title" id="container-sessions">
            Sessions
          </h2>
          <ul className="sessions">
            {own.map((s) => (
              <SessionRow key={s.metadata.name} session={s} href={pageHref(ns, 'Session', s.metadata.name)} />
            ))}
          </ul>
        </section>
      ) : null}
    </main>
  )
}
