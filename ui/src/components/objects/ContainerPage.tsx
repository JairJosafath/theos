// A Container's page: the object header (its scaffold lifecycle and
// conditions, its Relationships with their Applied reason and its Sessions,
// the repository, and on Failed the retry kubectl line), then its template
// refs and its Standard with the update pull request in flight, and when CI
// runs its full check (ContainerChecks, which sets it with UI actions on);
// where it ships and what runs there (ContainerDeploys, which approves a
// version waiting with UI actions on);
// with UI actions on, a scaffold retry, an answer to the Standard's files left
// out and a Task from a goal (ContainerActions). The files left out show what
// the Standard changed in each, beside the take and keep buttons, or on their
// own when nobody here may answer them.

import { useContext } from 'react'
import type { ContainerWriter } from '../../k8s/containers'
import type { Brief, CatalogEntry, Container, Crew, DeployTarget, Domain, Relationship, Session } from '../../k8s/types'
import { choicesOf } from '../../model/choices'
import { describeContainerType, splitTemplateRef } from '../../model/describe'
import { containerNextStep, describeStandardUpdate } from '../../model/lifecycle'
import { sessionsOf } from '../../model/story'
import { SessionContext } from '../../lib/hooks'
import { LeftOutFiles, SessionRow } from '../bits'
import { ExternalLinkIcon } from '../icons'
import { ObjectHeader, type OutLink } from '../ObjectHeader'
import { answersLeftOut, ContainerActions } from './ContainerActions'
import { ContainerChecks } from './ContainerChecks'
import { ContainerOperations } from './ContainerOperations'
import { ContainerDeploys } from './DeployPages'
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
  deployTargets,
  domains,
  writer,
}: {
  container: Container
  relationships: Relationship[]
  sessions: Session[]
  frameworks: CatalogEntry[]
  briefs: Brief[]
  crews: Crew[]
  deployTargets: DeployTarget[]
  /** The catalog's Domains, for their pinned refs; the ones in the repository are status.scaffoldedDomains. */
  domains: Domain[]
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
  // Its spec's refs, or what its Bundle expanded to (status.bundle); the UI enforces nothing.
  const choices = choicesOf(container)
  const framework = frameworks.find((f) => f.metadata.name === choices.framework)
  const fromBundle = choices.bundle ? <span className="muted small"> · from bundle {choices.bundle}</span> : null
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
          {choices.bundle ? (
            <>
              <dt>Bundle</dt>
              <dd className="mono">
                <a href={pageHref(ns, 'Bundle', choices.bundle)}>{choices.bundle}</a>
                {status?.bundle ? null : <span className="muted small"> · not expanded yet</span>}
              </dd>
            </>
          ) : null}
          {choices.framework ? (
            <>
              <dt>Framework</dt>
              <dd className="mono">
                {choices.framework}
                {framework ? ` @ ${framework.spec.template.ref}` : ''}
                {fromBundle}
              </dd>
            </>
          ) : null}
          {choices.standard ? (
            <>
              <dt>Standard</dt>
              <dd className="mono">
                <a href={pageHref(ns, 'Standard', choices.standard)}>{choices.standard}</a>
                {status?.standard ? ` @ ${status.standard.ref}` : ''}
                {fromBundle}
              </dd>
            </>
          ) : null}
          {choices.compute ? (
            <>
              <dt>Compute</dt>
              <dd className="mono">
                <a href={pageHref(ns, 'Compute', choices.compute)}>{choices.compute}</a>
                {fromBundle}
              </dd>
            </>
          ) : null}
          {choices.deployment ? (
            <>
              <dt>Deployment</dt>
              <dd className="mono">
                <a href={pageHref(ns, 'Deployment', choices.deployment)}>{choices.deployment}</a>
                {fromBundle}
              </dd>
            </>
          ) : null}
          <dt>Git provider</dt>
          <dd className="mono">
            <a href={pageHref(ns, 'GitProvider', container.spec.gitProviderRef.name)}>
              {container.spec.gitProviderRef.name}
            </a>
          </dd>
          <dt>Domains</dt>
          <dd>
            {/* What the repository holds (status.scaffoldedDomains), not what its system names now:
                a Domain is rendered once, at the scaffold; DomainsApplied above says when they differ. */}
            {!status?.scaffoldedFrom?.length ? (
              <span className="muted">Not scaffolded yet.</span>
            ) : status.scaffoldedDomains?.length ? (
              <ul className="object-list">
                {status.scaffoldedDomains.map((d) => {
                  const from = status.scaffoldedFrom?.map(splitTemplateRef).find((t) => t.name === d)
                  const version = from?.version ?? domains.find((e) => e.metadata.name === d)?.spec.template.ref
                  return (
                    <li key={d} className="mono">
                      <a href={pageHref(ns, 'Domain', d)}>{d}</a>
                      {version ? <span className="muted"> @ {version}</span> : null}
                    </li>
                  )
                })}
              </ul>
            ) : (
              <span className="muted">None.</span>
            )}
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

      {choices.standard ? <ContainerChecks container={container} writer={writer} /> : null}

      <ContainerOperations container={container} />

      <ContainerDeploys container={container} targets={deployTargets} writer={writer} />

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
