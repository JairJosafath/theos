// The read-only pages of the model's kinds: a Project (its counts, conditions,
// versions and catalog), a SoftwareSystem (what it is for and what it holds),
// a Relationship (its ends, and whether its Connector is applied) and a
// Session (who, on which Container, and its heartbeat). Each opens with the
// object header; everything comes from the objects' spec and status.

import type { Connection, StoryData } from '../../k8s/sources'
import {
  GROUP,
  VERSION,
  type ElementRef,
  type Project,
  type Relationship,
  type Session,
  type SoftwareSystem,
} from '../../k8s/types'
import { appliesConnector, relationshipPhase, type NextStep } from '../../model/lifecycle'
import { ObjectHeader, type ChildRef, type OutLink } from '../ObjectHeader'
import { CatalogList } from './CatalogPages'
import { DeployTargetList } from './DeployPages'
import { pageHref, projectCrumb, storyHref } from './links'
import { When } from '../bits'

/** A count from status, or that it has not been reported. */
function count(n: number | undefined): string {
  return n === undefined ? 'not reported yet' : String(n)
}

export function ProjectPage({
  project,
  data,
  connection,
}: {
  project: Project
  data: StoryData
  connection: Connection
}) {
  const ns = project.metadata.namespace ?? data.namespace
  const status = project.status
  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="Project"
        name={project.metadata.name}
        namespace={ns}
        title={project.spec?.displayName}
        breadcrumb={[]}
        conditions={status?.conditions ?? []}
        childrenTitle="Software systems"
        children={data.systems.map((s) => ({
          kind: 'SoftwareSystem',
          name: s.metadata.name,
          href: pageHref(ns, 'SoftwareSystem', s.metadata.name),
          note: s.status?.summary,
        }))}
        links={[]}
      />

      <section className="panel" aria-labelledby="project-counts">
        <h2 className="panel-title small-title" id="project-counts">
          What it holds
        </h2>
        {project.spec?.description ? <p className="panel-text">{project.spec.description}</p> : null}
        <dl className="facts">
          <dt>Software systems</dt>
          <dd>{count(status?.softwareSystems)}</dd>
          <dt>Containers</dt>
          <dd>
            {status?.containers === undefined
              ? 'not reported yet'
              : `${status.containers}, ${status.readyContainers ?? 0} Ready`}
          </dd>
          <dt>Active sessions</dt>
          <dd>{count(status?.activeSessions)}</dd>
          {project.spec?.license ? (
            <>
              <dt>License</dt>
              <dd className="mono">{project.spec.license}</dd>
            </>
          ) : null}
          {status?.summary ? (
            <>
              <dt>Summary</dt>
              <dd>{status.summary}</dd>
            </>
          ) : null}
        </dl>
        <p className="panel-text">
          <a href={storyHref(ns)}>See its story</a>
        </p>
      </section>

      <section className="panel" aria-labelledby="project-versions">
        <h2 className="panel-title small-title" id="project-versions">
          The factory that reconciles it
        </h2>
        <dl className="facts">
          <dt>Operator</dt>
          <dd className="mono">{status?.factory?.operatorVersion ?? 'not reported yet'}</dd>
          <dt>Chart</dt>
          <dd className="mono">{status?.factory?.chartVersion ?? 'not reported yet'}</dd>
          <dt>API</dt>
          <dd className="mono">{project.apiVersion ?? `${GROUP}/${VERSION}`}</dd>
        </dl>
      </section>

      <CatalogList ns={ns} data={data} connection={connection} />

      <DeployTargetList ns={ns} data={data} connection={connection} />
    </div>
  )
}

export function SystemPage({ system, data }: { system: SoftwareSystem; data: StoryData }) {
  const ns = system.metadata.namespace ?? data.namespace
  const name = system.metadata.name
  const status = system.status
  const children: ChildRef[] = [
    ...data.containers
      .filter((c) => c.spec.softwareSystemRef.name === name)
      .map((c) => ({
        kind: 'Container',
        name: c.metadata.name,
        href: pageHref(ns, 'Container', c.metadata.name),
        note: c.status?.phase ?? 'no status yet',
      })),
    ...data.dataStores
      .filter((d) => d.spec.softwareSystemRef.name === name)
      .map((d) => ({ kind: 'DataStore', name: d.metadata.name, note: `Connector ${d.spec.connectorRef.name}` })),
  ]
  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="SoftwareSystem"
        name={name}
        namespace={ns}
        title={system.spec?.displayName}
        breadcrumb={[projectCrumb(ns)]}
        conditions={status?.conditions ?? []}
        childrenTitle="Containers and data stores"
        children={children}
        links={[]}
      />

      <section className="panel" aria-labelledby="system-purpose">
        <h2 className="panel-title small-title" id="system-purpose">
          What it is for
        </h2>
        {system.spec?.description ? <p className="panel-text">{system.spec.description}</p> : null}
        <dl className="facts">
          <dt>Needs</dt>
          <dd>{list(system.spec?.needs, 'None written down.')}</dd>
          <dt>KPIs</dt>
          <dd>{list(system.spec?.kpis, 'None written down.')}</dd>
          <dt>Domains</dt>
          <dd>
            {/* Each linked to its page, with the catalog's pinned ref; a Domain is rendered into its Containers at their scaffold. */}
            {system.spec?.domainRefs?.length ? (
              <ul className="object-list">
                {system.spec.domainRefs.map((d) => {
                  const entry = data.domains.find((e) => e.metadata.name === d)
                  const href = pageHref(ns, 'Domain', d)
                  return (
                    <li key={d} className="mono">
                      {href ? <a href={href}>{d}</a> : d}
                      {entry ? (
                        <span className="muted"> @ {entry.spec.template.ref}</span>
                      ) : (
                        <span className="muted small"> · not in the catalog</span>
                      )}
                    </li>
                  )
                })}
              </ul>
            ) : (
              <span className="muted">None.</span>
            )}
          </dd>
          <dt>Containers</dt>
          <dd>
            {status?.containers === undefined
              ? 'not reported yet'
              : `${status.containers}, ${status.readyContainers ?? 0} Ready`}
          </dd>
          <dt>Data stores</dt>
          <dd>{count(status?.dataStores)}</dd>
          <dt>Active sessions</dt>
          <dd>{count(status?.activeSessions)}</dd>
        </dl>
        <p className="panel-text">
          <a href={storyHref(ns, name)}>See its containers in the story</a>
        </p>
      </section>
    </div>
  )
}

function list(items: string[] | undefined, empty: string) {
  return items?.length ? (
    <ul className="object-list">
      {items.map((i) => (
        <li key={i}>{i}</li>
      ))}
    </ul>
  ) : (
    <span className="muted">{empty}</span>
  )
}

/** One end of a Relationship, linked to its page when its kind has one. */
function end(ns: string, ref: ElementRef, note: string): ChildRef {
  return { kind: ref.kind, name: ref.name, href: pageHref(ns, ref.kind, ref.name), note }
}

export function RelationshipPage({ relationship, data }: { relationship: Relationship; data: StoryData }) {
  const ns = relationship.metadata.namespace ?? data.namespace
  const { source, target, description } = relationship.spec
  const phase = relationshipPhase(relationship)
  const applies = appliesConnector(relationship)
  const container = data.containers.find((c) => source.kind === 'Container' && c.metadata.name === source.name)
  const repo = container?.status?.repositoryURL
  let next: NextStep | undefined
  if (!phase && !applies)
    next = {
      text: 'Nothing: it describes the model. Only a Container to DataStore relationship adds a Connector to a scaffold.',
    }
  else if (phase === 'AddedAfterScaffold' && repo)
    next = {
      text: `Add what the Connector would have, by hand, in ${source.name}’s repository: the factory scaffolds a Container once and never again.`,
      link: { href: repo, label: 'Open the repository' },
    }
  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="Relationship"
        name={relationship.metadata.name}
        namespace={ns}
        title={description}
        breadcrumb={[projectCrumb(ns)]}
        phase={phase}
        phased={Boolean(phase) || applies}
        conditions={relationship.status?.conditions ?? []}
        childrenTitle="Its ends"
        children={[end(ns, source, 'from'), end(ns, target, 'to')]}
        links={[]}
        next={next}
      />
    </div>
  )
}

export function SessionPage({ session, data }: { session: Session; data: StoryData }) {
  const ns = session.metadata.namespace ?? data.namespace
  const containerName = session.spec.containerRef.name
  const container = data.containers.find((c) => c.metadata.name === containerName)
  const system = container?.spec.softwareSystemRef.name
  const heartbeat = session.status?.lastHeartbeat
  const links: OutLink[] = session.status?.openInEditor
    ? [{ href: session.status.openInEditor, label: 'Open in editor' }]
    : []
  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="Session"
        name={session.metadata.name}
        namespace={ns}
        title={session.spec.summary}
        breadcrumb={[
          projectCrumb(ns),
          ...(system ? [{ label: `System ${system}`, href: pageHref(ns, 'SoftwareSystem', system) }] : []),
          { label: `Container ${containerName}`, href: pageHref(ns, 'Container', containerName) },
        ]}
        phase={session.status?.phase}
        childrenTitle="Where it works"
        children={[{ kind: 'Container', name: containerName, href: pageHref(ns, 'Container', containerName) }]}
        links={links}
      />

      <section className="panel" aria-labelledby="session-who">
        <h2 className="panel-title small-title" id="session-who">
          Who and when
        </h2>
        <dl className="facts">
          <dt>Who</dt>
          <dd>{session.spec.actor === 'agent' ? 'An agent' : 'A person'}</dd>
          <dt>What</dt>
          <dd>{session.spec.summary || <span className="muted">No summary given.</span>}</dd>
          <dt>Last heartbeat</dt>
          <dd>{heartbeat ? <When at={heartbeat} full /> : <span className="muted">None yet.</span>}</dd>
          {session.metadata.creationTimestamp ? (
            <>
              <dt>Started</dt>
              <dd>
                <When at={session.metadata.creationTimestamp} full />
              </dd>
            </>
          ) : null}
        </dl>
      </section>
    </div>
  )
}
