// The page's shell (docs/plans/done/ui-lifecycle.md, decisions 1 and 2): navigation
// for the six areas, a freshness line, the area on screen or an object's page,
// and a footer with the versions running.

import type { MouseEvent, ReactNode } from 'react'
import type { FactoryHealth } from '../k8s/health'
import { problemsOf, readOf, type Connection, type FactoryData, type Problem } from '../k8s/sources'
import { RESOURCES } from '../k8s/types'
import { hrefFor, useActs, useNow, type UrlState } from '../lib/hooks'
import { AREAS, inNamespace, type Area } from '../model/areas'
import { absoluteTime, formatDuration } from '../model/format'
import { aboutLinks, type Versions } from '../model/versions'
import { Account } from './Account'
import { HealthPanel } from './factory/Health'
import { SlotsPanel } from './factory/Slots'
import { ExternalLinkIcon, LogoMark } from './icons'
import { ConnectionStatus } from './Status'
import { VersionLine } from './Versions'
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
  /** What each area but About shows. */
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
  const acts = useActs()
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
        This page reads the C4 Factory resources in the cluster and updates as they change.{' '}
        {acts === 'none'
          ? 'It can change only three things: create a Brief from an app idea, add a prompt to a Brief, and pause or resume a Brief.'
          : "With actions on, it also changes Brief and Crew settings, retries a Container, reruns a release, starts a Task and moves a Standard's template ref."}{' '}
        The cluster decides what it accepts.
        <VersionLine versions={versions} />
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
    <time dateTime={new Date(at).toISOString()} title={absoluteTime(at)}>
      {ms < 1000 ? 'just now' : `${formatDuration(ms)} ago`}
      <span className="visually-hidden"> ({absoluteTime(at)})</span>
    </time>
  )
}

/** The versions running, and where to read about them; the footer warns of a skew. */
function AboutArea({ versions }: { versions: Versions }) {
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

/** The page of a kind the UI shows no page for, such as a link typed by hand. */
function ObjectPlaceholder({ ns, kind, name }: { ns?: string; kind: string; name: string }) {
  return (
    <main className="shell-page">
      <section className="panel">
        <div className="eyebrow">
          {kind}
          {ns ? ` · ${ns}` : ''}
        </div>
        <h1 className="panel-title">{name}</h1>
        <p className="panel-text">The UI has no page for a {kind}.</p>
      </section>
    </main>
  )
}

/** The factory's health and agent slots: the Factory area. */
export function FactoryArea({
  data,
  connection,
  health,
  healthLoaded,
  healthProblems,
  onSelectTask,
}: {
  data: FactoryData
  connection: Connection
  health?: FactoryHealth
  healthLoaded: boolean
  healthProblems: Problem[]
  onSelectTask: (key: string) => void
}) {
  const now = useNow()
  return (
    <main className="shell-page shell-columns">
      <HealthPanel health={health} loaded={healthLoaded} problems={healthProblems} now={now} />
      <SlotsPanel
        tasks={data.tasks}
        loaded={readOf(connection, RESOURCES.tasks)}
        problems={problemsOf(connection, RESOURCES.tasks)}
        health={health}
        now={now}
        onSelectTask={onSelectTask}
      />
    </main>
  )
}
