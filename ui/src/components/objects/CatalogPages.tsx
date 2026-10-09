// The pages of the catalog and the Git hosts: a Standard, Compute or
// Deployment (its template, what it supports, the Containers that name it;
// with UI actions on, a Standard's ref bump, StandardActions), a
// GitProvider (where repositories go, and its Secret's name: the UI never
// reads Secrets), and the Project page's list of them, where a refused read
// of any of these kinds says so.

import { unreadOf, type Connection, type StoryData } from '../../k8s/sources'
import type { ReactNode } from 'react'
import { RESOURCES, type CatalogEntry, type Container, type GitProvider, type Standard } from '../../k8s/types'
import { describeContainerType } from '../../model/describe'
import { ObjectHeader, type ChildRef } from '../ObjectHeader'
import { pageHref, projectCrumb } from './links'
import { ReadProblem } from './ReadProblem'

type CatalogKind = 'Standard' | 'Compute' | 'Deployment'

/** Which Container field names an entry of each kind. */
const REF: Record<CatalogKind | 'GitProvider', (c: Container) => string | undefined> = {
  Standard: (c) => c.spec.standardRef?.name,
  Compute: (c) => c.spec.computeRef?.name,
  Deployment: (c) => c.spec.deploymentRef?.name,
  GitProvider: (c) => c.spec.gitProviderRef.name,
}

/** The Containers that name this entry, each linked to its page. */
function usedBy(kind: CatalogKind | 'GitProvider', name: string, containers: Container[]): ChildRef[] {
  return containers
    .filter((c) => REF[kind](c) === name)
    .map((c) => ({
      kind: 'Container',
      name: c.metadata.name,
      href: pageHref(c.metadata.namespace ?? '', 'Container', c.metadata.name),
      note: c.status?.phase ?? 'no status yet',
    }))
}

export function CatalogPage({
  kind,
  entry,
  containers,
  actions,
}: {
  kind: CatalogKind
  entry: CatalogEntry | Standard
  containers: Container[]
  /** What a person may do on it, under the header. */
  actions?: ReactNode
}) {
  const ns = entry.metadata.namespace ?? ''
  const name = entry.metadata.name
  const standard = kind === 'Standard' ? (entry as Standard) : undefined
  return (
    <main className="shell-page object-page">
      <ObjectHeader
        kind={kind}
        name={name}
        namespace={ns}
        title={entry.spec.displayName}
        breadcrumb={[projectCrumb(ns)]}
        childrenTitle="Containers that name it"
        children={usedBy(kind, name, containers)}
        links={[]}
      />

      {actions}

      <section className="panel" aria-labelledby="catalog-template">
        <h2 className="panel-title small-title" id="catalog-template">
          Its template
        </h2>
        {entry.spec.description ? <p className="panel-text">{entry.spec.description}</p> : null}
        <dl className="facts">
          <dt>Template</dt>
          <dd className="mono">{entry.spec.template.url}</dd>
          <dt>Pinned at</dt>
          <dd className="mono">{entry.spec.template.ref}</dd>
          <dt>Container types</dt>
          <dd>{entry.spec.supportedTypes.map(describeContainerType).join(', ')}</dd>
          {standard ? (
            <>
              <dt>Frameworks it checks</dt>
              <dd className="mono">{standard.spec.supportedFrameworks.join(', ')}</dd>
              <dt>Files agents must not change</dt>
              <dd className="mono">
                {standard.spec.protectedPaths?.length ? (
                  standard.spec.protectedPaths.join(', ')
                ) : (
                  <span className="muted">None besides the platform’s own.</span>
                )}
              </dd>
            </>
          ) : null}
        </dl>
      </section>
    </main>
  )
}

/**
 * A GitProvider's page. It shows the fields it names one by one, never the
 * object whole, and of its credentials only the Secret's name.
 */
export function GitProviderPage({ provider, containers }: { provider: GitProvider; containers: Container[] }) {
  const ns = provider.metadata.namespace ?? ''
  const name = provider.metadata.name
  return (
    <main className="shell-page object-page">
      <ObjectHeader
        kind="GitProvider"
        name={name}
        namespace={ns}
        breadcrumb={[projectCrumb(ns)]}
        childrenTitle="Containers whose repositories it holds"
        children={usedBy('GitProvider', name, containers)}
        links={[]}
      />

      <section className="panel" aria-labelledby="gitprovider-where">
        <h2 className="panel-title small-title" id="gitprovider-where">
          Where repositories go
        </h2>
        <dl className="facts">
          <dt>Provider</dt>
          <dd>{provider.spec.provider ?? <span className="muted">not set</span>}</dd>
          <dt>Address</dt>
          <dd className="mono">{provider.spec.url}</dd>
          <dt>Owner</dt>
          <dd className="mono">{provider.spec.owner}</dd>
          <dt>Credentials</dt>
          <dd>
            Secret <span className="mono">{provider.spec.credentialsSecretRef.name}</span>
            <span className="muted small"> · its name only: the UI never reads Secrets</span>
          </dd>
        </dl>
      </section>
    </main>
  )
}

/** The kinds the Project page lists, with their pages where they have one. */
const CATALOG: {
  title: string
  kind: string
  resource: string
  list: (d: StoryData) => { metadata: { name: string } }[]
}[] = [
  { title: 'Frameworks', kind: 'Framework', resource: RESOURCES.frameworks, list: (d) => d.frameworks },
  { title: 'Standards', kind: 'Standard', resource: RESOURCES.standards, list: (d) => d.standards },
  { title: 'Computes', kind: 'Compute', resource: RESOURCES.computes, list: (d) => d.computes },
  { title: 'Deployments', kind: 'Deployment', resource: RESOURCES.deployments, list: (d) => d.deployments },
  { title: 'Connectors', kind: 'Connector', resource: RESOURCES.connectors, list: (d) => d.connectors },
  { title: 'Git providers', kind: 'GitProvider', resource: RESOURCES.gitProviders, list: (d) => d.gitProviders },
]

/** The Project's catalog and Git hosts, each kind's refused read said in its place. */
export function CatalogList({ ns, data, connection }: { ns: string; data: StoryData; connection: Connection }) {
  return (
    <section className="panel" aria-labelledby="project-catalog">
      <h2 className="panel-title small-title" id="project-catalog">
        Its catalog and Git hosts
      </h2>
      <dl className="facts">
        {CATALOG.map(({ title, kind, resource, list }) => {
          const unread = unreadOf(connection, resource)
          const items = list(data)
          return (
            <div key={kind} className="facts-row">
              <dt>{title}</dt>
              <dd>
                {/* A failed re-watch keeps what it had loaded: say the failure, above the list. */}
                {unread ? <ReadProblem problem={unread} /> : null}
                {items.length ? (
                  <ul className="object-list">
                    {items.map((o) => {
                      const href = pageHref(ns, kind, o.metadata.name)
                      return (
                        <li key={o.metadata.name} className="mono">
                          {href ? <a href={href}>{o.metadata.name}</a> : o.metadata.name}
                        </li>
                      )
                    })}
                  </ul>
                ) : unread ? null : (
                  <span className="muted">None.</span>
                )}
              </dd>
            </div>
          )
        })}
      </dl>
    </section>
  )
}
