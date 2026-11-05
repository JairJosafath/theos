// The pages of the catalog and the Git hosts: a Standard, Compute or
// Deployment (its template, what it supports, the Containers that name it;
// with UI actions on, a Standard's ref bump, StandardActions), a Bundle (its
// four entries, the types they share, the Containers that name it), a Domain
// (its template, the systems that name it and how many of their Containers
// were scaffolded with it), a GitProvider (where repositories go, and its Secret's name: the UI never
// reads Secrets), and the Project page's list of them, where a refused read
// of any of these kinds says so.

import { unreadOf, type Connection, type StoryData } from '../../k8s/sources'
import type { ReactNode } from 'react'
import {
  RESOURCES,
  type Bundle,
  type CatalogEntry,
  type Container,
  type Domain,
  type GitProvider,
  type SoftwareSystem,
  type Standard,
} from '../../k8s/types'
import { bundleEntries, bundleTypes, choicesOf, type BundleSide, type Catalog } from '../../model/choices'
import { describeContainerType } from '../../model/describe'
import { ObjectHeader, type ChildRef } from '../ObjectHeader'
import { pageHref, projectCrumb } from './links'
import { ReadProblem } from './ReadProblem'

type CatalogKind = 'Standard' | 'Compute' | 'Deployment'

/** Which Container choice names an entry of each kind: its spec's ref, or what its Bundle expanded to. */
const REF: Record<CatalogKind | 'Bundle' | 'GitProvider', (c: Container) => string | undefined> = {
  Standard: (c) => choicesOf(c).standard,
  Compute: (c) => choicesOf(c).compute,
  Deployment: (c) => choicesOf(c).deployment,
  Bundle: (c) => choicesOf(c).bundle,
  GitProvider: (c) => c.spec.gitProviderRef.name,
}

/** The Containers that name this entry, each linked to its page. */
function usedBy(kind: CatalogKind | 'Bundle' | 'GitProvider', name: string, containers: Container[]): ChildRef[] {
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
    <div className="shell-page object-page">
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
    </div>
  )
}

const SIDE_TITLE: Record<BundleSide, string> = {
  framework: 'Framework',
  standard: 'Standard',
  compute: 'Compute',
  deployment: 'Deployment',
}

/**
 * A Bundle's page: the entries it names, each linked to its page where it has
 * one and said to be missing when the catalog lacks it, the Container types
 * every entry supports (computed here, as the operator does), and the
 * Containers that name it. It has no status, so no conditions and no actions.
 */
export function BundlePage({
  bundle,
  catalog,
  containers,
  connection,
}: {
  bundle: Bundle
  catalog: Catalog
  containers: Container[]
  connection: Connection
}) {
  const ns = bundle.metadata.namespace ?? ''
  const name = bundle.metadata.name
  const entries = bundleEntries(bundle, catalog)
  const types = bundleTypes(bundle, catalog)
  // A refused read of a catalog kind would show its entries as missing: say the refusal first.
  const unread = (['frameworks', 'standards', 'computes', 'deployments'] as const)
    .map((k) => unreadOf(connection, RESOURCES[k]))
    .filter((p) => p !== undefined)
  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="Bundle"
        name={name}
        namespace={ns}
        title={bundle.spec.displayName}
        breadcrumb={[projectCrumb(ns)]}
        childrenTitle="Containers that name it"
        children={usedBy('Bundle', name, containers)}
        links={[]}
      />

      <section className="panel" aria-labelledby="bundle-entries">
        <h2 className="panel-title small-title" id="bundle-entries">
          Its entries
        </h2>
        {bundle.spec.description ? <p className="panel-text">{bundle.spec.description}</p> : null}
        {unread.map((p) => (
          <ReadProblem key={p.what} problem={p} />
        ))}
        <dl className="facts">
          {entries.map(({ side, name: entryName, entry }) => {
            const href = pageHref(ns, SIDE_TITLE[side], entryName)
            return (
              <div key={side} className="facts-row">
                <dt>{SIDE_TITLE[side]}</dt>
                <dd className="mono">
                  {href ? <a href={href}>{entryName}</a> : entryName}
                  {entry ? (
                    <span className="muted"> @ {entry.spec.template.ref}</span>
                  ) : (
                    <span className="muted small"> · not in the catalog</span>
                  )}
                </dd>
              </div>
            )
          })}
          <div className="facts-row">
            <dt>Container types</dt>
            <dd>
              {types.length ? (
                types.map(describeContainerType).join(', ')
              ) : entries.some((e) => !e.entry) ? (
                <span className="muted">None: an entry is not in the catalog.</span>
              ) : (
                <span className="muted">None: its entries share no Container type.</span>
              )}
            </dd>
          </div>
        </dl>
      </section>
    </div>
  )
}

/** The systems that name this Domain, each with how many of its Containers were scaffolded with it. */
function namedBy(name: string, systems: SoftwareSystem[], containers: Container[]): ChildRef[] {
  return systems
    .filter((s) => s.spec?.domainRefs?.includes(name))
    .map((s) => {
      const own = containers.filter((c) => c.spec.softwareSystemRef.name === s.metadata.name)
      const scaffolded = own.filter((c) => c.status?.scaffoldedDomains?.includes(name)).length
      return {
        kind: 'SoftwareSystem',
        name: s.metadata.name,
        href: pageHref(s.metadata.namespace ?? '', 'SoftwareSystem', s.metadata.name),
        note: `${scaffolded} of ${own.length} Containers scaffolded with it`,
      }
    })
}

/**
 * A Domain's page: its template, and the systems that name it, each noted
 * with how many of its Containers were scaffolded with it. A Domain is
 * rendered once, at the scaffold (docs/plans/domains.md, decision 5), so
 * the Container page says which Domains a repository holds, and its
 * DomainsApplied condition says when its system's differ.
 */
export function DomainPage({
  domain,
  systems,
  containers,
}: {
  domain: Domain
  systems: SoftwareSystem[]
  containers: Container[]
}) {
  const ns = domain.metadata.namespace ?? ''
  const name = domain.metadata.name
  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="Domain"
        name={name}
        namespace={ns}
        title={domain.spec.displayName}
        breadcrumb={[projectCrumb(ns)]}
        childrenTitle="Systems that name it"
        children={namedBy(name, systems, containers)}
        links={[]}
      />

      <section className="panel" aria-labelledby="domain-template">
        <h2 className="panel-title small-title" id="domain-template">
          Its template
        </h2>
        {domain.spec.description ? <p className="panel-text">{domain.spec.description}</p> : null}
        <dl className="facts">
          <dt>Template</dt>
          <dd className="mono">{domain.spec.template.url}</dd>
          <dt>Pinned at</dt>
          <dd className="mono">{domain.spec.template.ref}</dd>
          <dt>Lands under</dt>
          <dd>
            <span className="mono">docs/agents/domain/{name}/</span>
            <span className="muted small"> · in each of its systems’ Containers, once, at their scaffold</span>
          </dd>
        </dl>
      </section>
    </div>
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
    <div className="shell-page object-page">
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
    </div>
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
  { title: 'Bundles', kind: 'Bundle', resource: RESOURCES.bundles, list: (d) => d.bundles },
  { title: 'Domains', kind: 'Domain', resource: RESOURCES.domains, list: (d) => d.domains },
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
