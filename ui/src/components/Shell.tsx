// The new shell (docs/plans/ui-lifecycle.md, decision 2), reached only with
// ?shell=next until it becomes the default: navigation for the six areas, a
// freshness line, and the area on screen or an object's page. Story, Briefs,
// Work and Factory show today's components, and the pages of the kinds that
// have none yet say so.

import type { MouseEvent, ReactNode } from 'react'
import type { FactoryHealth } from '../k8s/health'
import type { Connection, FactoryData } from '../k8s/sources'
import { hrefFor, useNow, type UrlState } from '../lib/hooks'
import { AREAS, inNamespace, type Area } from '../model/areas'
import { formatDuration } from '../model/format'
import { aboutLinks, versionSkew, type Versions } from '../model/versions'
import { Account } from './Account'
import { HealthPanel } from './factory/Health'
import { SlotsPanel } from './factory/Slots'
import { ExternalLinkIcon, LogoMark, WarningIcon } from './icons'
import { ConnectionStatus } from './Status'
import './factory.css'
import './shell.css'

export interface ShellProps {
  url: UrlState
  area: Area
  setUrl: (next: Partial<UrlState>, replace?: boolean) => void
  connection: Connection
  /** The project's namespace the Story and Briefs areas show. */
  namespace?: string
  versions: Versions
  onSignOut: () => Promise<void>
  /** The project picker, shown in the areas of one project. */
  picker: ReactNode
  /** The demo and problem banners. */
  banners: ReactNode
  /** Needs you, and today's components for the other areas of the cluster. */
  areas: Record<'needs' | 'briefs' | 'story' | 'work' | 'factory', ReactNode>
  /** The object page the URL names, for kinds that have one. */
  objectPage?: ReactNode
}

/** Leaves an object page: the area alone. */
const AREA_ONLY = { ns: undefined, kind: undefined, name: undefined }

export function Shell({
  url,
  area,
  setUrl,
  connection,
  namespace,
  versions,
  onSignOut,
  picker,
  banners,
  areas,
  objectPage,
}: ShellProps) {
  const object = url.kind && url.name ? { ns: url.ns, kind: url.kind, name: url.name } : undefined
  const scope = object ? object.ns : inNamespace(area) ? namespace : area === 'about' ? undefined : ''
  return (
    <div className="app shell">
      <header className="topbar">
        <div className="brand">
          <LogoMark size={22} />
          <span className="brand-name">C4 Factory</span>
        </div>
        <nav className="modes areas" aria-label="Areas">
          {AREAS.map((a) => (
            <a
              key={a.key}
              className="mode"
              href={hrefFor({ area: a.key, ...AREA_ONLY })}
              title={a.about}
              aria-current={a.key === area && !object ? 'page' : undefined}
              onClick={(e) => {
                if (!plainClick(e)) return
                e.preventDefault()
                // The area on screen, if only by default, adds no history entry.
                if (a.key !== area || object) setUrl({ area: a.key, ...AREA_ONLY })
              }}
            >
              {a.label}
            </a>
          ))}
        </nav>
        <div className="topbar-right">
          <Account onSignOut={onSignOut} />
          {inNamespace(area) && !object ? picker : null}
        </div>
      </header>

      {banners}
      <Freshness connection={connection} scope={scope} />

      {object ? (
        (objectPage ?? <ObjectPlaceholder {...object} />)
      ) : area === 'about' ? (
        <AboutArea versions={versions} />
      ) : (
        areas[area]
      )}

      <footer className="footer">
        This page reads the C4 Factory resources in the cluster and updates as they change. It can change only three
        things: create a Brief from an app idea, add a prompt to a Brief, and pause or resume a Brief.
      </footer>
    </div>
  )
}

/** A click the page handles itself; with a modifier or another button, the browser opens the link. */
function plainClick(e: MouseEvent) {
  return e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey
}

/**
 * How fresh the page is: the connection, when the data last changed, and the
 * namespace in view. `scope` is a namespace, '' for every namespace, or
 * undefined where the area shows none.
 */
function Freshness({ connection, scope }: { connection: Connection; scope?: string }) {
  return (
    <div className="freshness">
      <ConnectionStatus connection={connection} />
      {connection.updatedAt ? (
        <span>
          Updated <Ago at={connection.updatedAt} />
        </span>
      ) : connection.phase === 'demo' ? (
        <span>Built-in example data</span>
      ) : (
        <span>Not loaded yet</span>
      )}
      {scope === undefined ? null : scope ? (
        <span>
          Namespace <code>{scope}</code>
        </span>
      ) : (
        <span>All namespaces</span>
      )}
    </div>
  )
}

/** "just now", "12 s ago": the one part of the line that follows the clock. */
function Ago({ at }: { at: number }) {
  const now = useNow()
  const ms = now - at
  return (
    <time dateTime={new Date(at).toISOString()} title={new Date(at).toLocaleString()}>
      {ms < 1000 ? 'just now' : `${formatDuration(ms)} ago`}
    </time>
  )
}

/** The versions running, and where to read about them. */
function AboutArea({ versions }: { versions: Versions }) {
  const skew = versionSkew(versions)
  const links = aboutLinks(versions)
  return (
    <main className="shell-page">
      <section className="panel about">
        <h1 className="panel-title">About this factory</h1>
        <dl className="facts about-versions">
          <dt>UI</dt>
          <dd>{versions.ui}</dd>
          <dt>Operator</dt>
          <dd>{versions.operator ?? 'unknown'}</dd>
          <dt>Chart</dt>
          <dd>{versions.chart ?? 'unknown'}</dd>
          <dt>API</dt>
          <dd>{versions.api}</dd>
        </dl>
        {skew ? (
          <p className="versions-skew" role="alert">
            <WarningIcon />
            {skew}
          </p>
        ) : null}
        <ul className="about-links">
          <li>
            <ExternalLink href={links.docs}>The design: what the factory does, and why</ExternalLink>
          </li>
          <li>
            <ExternalLink href={links.releaseNotes}>Release notes</ExternalLink>
          </li>
          <li>
            <ExternalLink href={links.repo}>The repository</ExternalLink>
          </li>
        </ul>
      </section>
    </main>
  )
}

function ExternalLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a href={href} target="_blank" rel="noreferrer">
      {children} <ExternalLinkIcon size={12} />
    </a>
  )
}

function ObjectPlaceholder({ ns, kind, name }: { ns?: string; kind: string; name: string }) {
  return (
    <main className="shell-page">
      <section className="panel">
        <div className="eyebrow">
          {kind}
          {ns ? ` · ${ns}` : ''}
        </div>
        <h1 className="panel-title">{name}</h1>
        <p className="panel-text">
          Object pages come in a later release. Until then, find this {kind} in Story or Work.
        </p>
      </section>
    </main>
  )
}

/** Today's factory health and agent slots, as the Factory area. */
export function FactoryArea({
  data,
  health,
  healthLoaded,
  onSelectTask,
}: {
  data: FactoryData
  health?: FactoryHealth
  healthLoaded: boolean
  onSelectTask: (key: string) => void
}) {
  const now = useNow()
  return (
    <main className="shell-page shell-columns">
      <HealthPanel health={health} loaded={healthLoaded} now={now} />
      <SlotsPanel tasks={data.tasks} health={health} now={now} onSelectTask={onSelectTask} />
    </main>
  )
}
