// The page's shell (docs/plans/done/ui-shell.md, step 1): a sidebar with the areas,
// the project switcher, About, Settings and the versions running; a top bar
// with where you are, the demo chip, how fresh the page is, search, the area's
// primary action and the theme; the area on screen; and an object's page in a
// drawer over it, or full width.

import { useId, useRef, useState, type MouseEvent, type ReactNode } from 'react'
import type { FactoryHealth } from '../k8s/health'
import { problemsOf, readOf, type Connection, type FactoryData, type Problem } from '../k8s/sources'
import { RESOURCES, type Chat } from '../k8s/types'
import { hrefFor, useActs, useNow, type SetUrl, type UrlState } from '../lib/hooks'
import {
  nextTheme,
  resolveTheme,
  setPref,
  THEME_LABELS,
  usePrefersDark,
  usePrefs,
  useMediaQuery,
  type Density,
  type SidebarState,
  type ThemeChoice,
} from '../lib/prefs'
import { AREAS, GROUP_LABELS, groupLabel, inNamespace, type Area, type AreaGroup } from '../model/areas'
import { absoluteTime, formatDuration } from '../model/format'
import type { PaletteItem } from '../model/search'
import { aboutLinks, type Versions } from '../model/versions'
import { Account } from './Account'
import { ChatPanel } from './ChatPanel'
import { HealthDashboard } from './factory/Health'
import { SlotsPanel } from './factory/Slots'
import {
  BoardIcon,
  BriefIcon,
  ChevronDownIcon,
  CrossIcon,
  ExpandIcon,
  ExternalLinkIcon,
  InboxIcon,
  InfoIcon,
  LogoMark,
  LoopIcon,
  MenuIcon,
  MonitorIcon,
  MoonIcon,
  PulseIcon,
  SearchIcon,
  SettingsIcon,
  SidebarIcon,
  StoryIcon,
  SunIcon,
} from './icons'
import { ConnectionStatus } from './Status'
import { VersionLine } from './Versions'
import { CommandPalette } from './ui/CommandPalette'
import { Drawer } from './ui/Drawer'
import { useLayer } from './ui/layer'
import { Menu } from './ui/Menu'
import { Modal } from './ui/Modal'
import { usePopover } from './ui/popover'
import { usePaletteShortcut } from './ui/shortcuts'
import './factory.css'
import './shell.css'

export interface ShellProps {
  url: UrlState
  area: Area
  setUrl: SetUrl
  connection: Connection
  /** The project's namespace the Loop, Briefs and Story areas show. */
  namespace?: string
  versions: Versions
  onSignOut: () => Promise<void>
  /** The projects to switch between, oldest first. */
  projects: { namespace: string; title: string }[]
  /** How many things wait on a person, once known: the count beside Needs you. */
  needsCount?: number
  /** The problem and warning banners. */
  banners: ReactNode
  /** What each area but About shows. */
  areas: Record<'needs' | 'loop' | 'briefs' | 'story' | 'work' | 'factory', ReactNode>
  /** The object page the URL names, for kinds that have one. */
  objectPage?: ReactNode
  /** The area's one primary action, such as New brief, in the top bar. */
  primaryAction?: ReactNode
  /** What Search or jump finds. */
  paletteItems: PaletteItem[]
  /** The project's Chat, on About and in Settings; unset when its namespace has none. */
  chat?: Chat
  /** The failed read of the Chats, if it failed. */
  chatProblems?: Problem[]
}

/** Leaves an object page and the area's tab: the area alone. */
const AREA_ONLY: Partial<UrlState> = {
  ns: undefined,
  kind: undefined,
  name: undefined,
  full: undefined,
  tab: undefined,
}
/** Closes the object in the drawer; the area and its tab stay. */
const NO_OBJECT: Partial<UrlState> = { ns: undefined, kind: undefined, name: undefined, full: undefined }

const AREA_ICONS: Record<Area, (props: { size?: number }) => ReactNode> = {
  needs: InboxIcon,
  loop: LoopIcon,
  briefs: BriefIcon,
  story: StoryIcon,
  work: BoardIcon,
  factory: PulseIcon,
  about: InfoIcon,
}

/** Below this width the sidebar is a drawer behind the menu button. */
const PHONE = '(max-width: 767px)'

/** Whether the page runs on a Mac, for the shortcut's label. */
function onMac(): boolean {
  return typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)
}

export function Shell({
  url,
  area,
  setUrl,
  connection,
  namespace,
  versions,
  onSignOut,
  projects,
  needsCount,
  banners,
  areas,
  objectPage,
  primaryAction,
  paletteItems,
  chat,
  chatProblems,
}: ShellProps) {
  const prefs = usePrefs()
  const phone = useMediaQuery(PHONE)
  const [navOpen, setNavOpen] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const sidebar = useRef<HTMLDivElement>(null)
  const navShown = phone && navOpen
  useLayer(navShown, sidebar, () => setNavOpen(false))
  usePaletteShortcut(() => setPaletteOpen(true))

  const object = url.kind && url.name ? { ns: url.ns, kind: url.kind, name: url.name } : undefined
  const fullPage = Boolean(object && url.full)
  const collapsed = !phone && prefs.sidebar === 'collapsed'
  const scope = fullPage ? object!.ns : inNamespace(area) ? namespace : area === 'about' ? undefined : ''
  const projectTitle = projects.find((p) => p.namespace === namespace)?.title ?? namespace

  const go = (next: Partial<UrlState>) => {
    setUrl(next)
    setNavOpen(false)
    if (!next.kind || next.full) window.scrollTo(0, 0)
  }
  const closeObject = () => setUrl(NO_OBJECT)
  const pickProject = (ns: string) =>
    go({ ...AREA_ONLY, project: ns, area: inNamespace(area) ? area : 'story', view: 'context', system: undefined })

  const body = fullPage ? (
    (objectPage ?? <ObjectPlaceholder {...object!} />)
  ) : area === 'about' ? (
    <AboutArea versions={versions} chat={chat} chatProblems={chatProblems} />
  ) : (
    areas[area]
  )

  return (
    <div className={`app shell${collapsed ? ' sidebar-collapsed' : ''}${navShown ? ' nav-open' : ''}`}>
      <a className="skip-link" href="#content">
        Skip to content
      </a>
      <div
        id="sidebar"
        ref={sidebar}
        className="sidebar"
        // On a phone, open over the page, it is a dialog.
        {...(navShown ? { role: 'dialog', 'aria-modal': true, 'aria-label': 'Navigation' } : {})}
      >
        <div className="sidebar-head">
          <div className="brand">
            <LogoMark size={22} />
            <span className="brand-name">theos</span>
          </div>
          {phone ? (
            <button
              type="button"
              className="icon-button"
              aria-label="Close navigation"
              onClick={() => setNavOpen(false)}
            >
              <CrossIcon size={18} />
            </button>
          ) : (
            <button
              type="button"
              className="icon-button sidebar-toggle"
              aria-label={collapsed ? 'Expand the sidebar' : 'Collapse the sidebar'}
              title={collapsed ? 'Expand the sidebar' : 'Collapse the sidebar'}
              aria-expanded={!collapsed}
              aria-controls="sidebar"
              onClick={() => setPref('sidebar', collapsed ? 'expanded' : 'collapsed')}
            >
              <SidebarIcon size={18} />
            </button>
          )}
        </div>
        {projects.length > 0 ? (
          <ProjectSwitcher projects={projects} namespace={namespace} collapsed={collapsed} onPick={pickProject} />
        ) : null}
        <nav className="sidebar-nav" aria-label="Areas">
          {(['needs', 'build', 'operate'] as AreaGroup[]).map((group) => (
            <NavGroup key={group} label={GROUP_LABELS[group]}>
              {AREAS.filter((a) => a.group === group).map((a) => (
                <NavLink
                  key={a.key}
                  area={a}
                  current={a.key === area}
                  fullPage={fullPage}
                  count={a.key === 'needs' ? needsCount : undefined}
                  collapsed={collapsed}
                  onGo={() => go({ area: a.key, ...AREA_ONLY })}
                  same={a.key === area && !object}
                />
              ))}
            </NavGroup>
          ))}
          <ul className="nav-list nav-bottom">
            {AREAS.filter((a) => a.group === 'bottom').map((a) => (
              <NavLink
                key={a.key}
                area={a}
                current={a.key === area}
                fullPage={fullPage}
                collapsed={collapsed}
                onGo={() => go({ area: a.key, ...AREA_ONLY })}
                same={a.key === area && !object}
              />
            ))}
            <li>
              <button
                type="button"
                className="nav-link"
                title={collapsed ? 'Settings' : undefined}
                onClick={() => {
                  setNavOpen(false)
                  setSettingsOpen(true)
                }}
              >
                <SettingsIcon size={18} />
                <span className="nav-label">Settings</span>
              </button>
            </li>
          </ul>
        </nav>
        <footer className="sidebar-foot">
          <VersionLine versions={versions} />
        </footer>
      </div>
      {navShown ? <div className="nav-backdrop" onClick={() => setNavOpen(false)} aria-hidden="true" /> : null}

      <div className="shell-main">
        <header className="topbar">
          <button
            type="button"
            className="icon-button topbar-menu"
            aria-label="Open navigation"
            aria-expanded={navShown}
            aria-controls="sidebar"
            onClick={() => setNavOpen(true)}
          >
            <MenuIcon size={20} />
          </button>
          <Crumbs
            area={area}
            project={inNamespace(area) ? projectTitle : undefined}
            object={fullPage ? object : undefined}
          />
          <div className="topbar-tools">
            {url.demo ? <DemoChip /> : null}
            <Freshness connection={connection} scope={scope} />
            <button
              type="button"
              className="search-button"
              onClick={() => setPaletteOpen(true)}
              aria-keyshortcuts="Meta+K Control+K /"
              aria-label="Search or jump"
            >
              <SearchIcon size={16} />
              <span className="search-label" aria-hidden="true">
                Search or jump
              </span>
              <kbd className="kbd search-kbd" aria-hidden="true">
                {onMac() ? '⌘K' : 'Ctrl K'}
              </kbd>
            </button>
            {fullPage ? null : primaryAction}
            <ThemeToggle />
            <Account onSignOut={onSignOut} />
          </div>
        </header>
        {banners}
        <main id="content" className={`content${fullPage ? ' content-object' : ''}`} tabIndex={-1}>
          {body}
        </main>
      </div>

      {object && !fullPage ? (
        <Drawer
          open
          onClose={closeObject}
          label={`${object.kind} ${object.name}`}
          contentKey={`${object.ns}/${object.kind}/${object.name}`}
          actions={
            <a className="drawer-full" href={hrefFor({ ...object, full: true })}>
              <ExpandIcon size={14} /> Open full page
            </a>
          }
        >
          {objectPage ?? <ObjectPlaceholder {...object} />}
        </Drawer>
      ) : null}
      <SettingsModal
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        chat={chat}
        chatProblems={chatProblems}
      />
      <CommandPalette
        open={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        items={paletteItems}
        onPick={(item) => go(item.target)}
      />
    </div>
  )
}

/** A click the page handles itself; with a modifier or another button, the browser opens the link. */
function plainClick(e: MouseEvent) {
  return e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey
}

function NavGroup({ label, children }: { label?: string; children: ReactNode }) {
  const id = useId()
  return (
    <div className="nav-group">
      {label ? (
        <div className="nav-group-label" id={id}>
          {label}
        </div>
      ) : null}
      <ul className="nav-list" aria-labelledby={label ? id : undefined}>
        {children}
      </ul>
    </div>
  )
}

function NavLink({
  area,
  current,
  fullPage,
  count,
  collapsed,
  onGo,
  same,
}: {
  area: (typeof AREAS)[number]
  current: boolean
  fullPage: boolean
  count?: number
  collapsed: boolean
  onGo: () => void
  /** The area is on screen with nothing over it: a click adds no history entry. */
  same: boolean
}) {
  const Icon = AREA_ICONS[area.key]
  return (
    <li>
      <a
        className="nav-link"
        href={hrefFor({ area: area.key, ...AREA_ONLY })}
        title={collapsed ? area.label : area.about}
        // The area under an object's full page is where you are, not the page itself.
        aria-current={current ? (fullPage ? 'true' : 'page') : undefined}
        onClick={(e) => {
          if (!plainClick(e)) return
          e.preventDefault()
          if (!same) onGo()
        }}
      >
        <Icon size={18} />
        <span className="nav-label">{area.label}</span>
        {count ? (
          <span className="nav-count">
            <span className="visually-hidden">, </span>
            {count}
            <span className="visually-hidden"> waiting</span>
          </span>
        ) : null}
      </a>
    </li>
  )
}

/** The project the areas of one project show, and the others to switch to. */
function ProjectSwitcher({
  projects,
  namespace,
  collapsed,
  onPick,
}: {
  projects: { namespace: string; title: string }[]
  namespace?: string
  collapsed: boolean
  onPick: (ns: string) => void
}) {
  const current = projects.find((p) => p.namespace === namespace)
  const title = current?.title ?? namespace ?? 'Pick a project'
  return (
    <div className="project-switcher">
      <Menu
        label="Projects"
        buttonClassName="project-button"
        buttonLabel={`Project: ${title}. Switch project`}
        align="start"
        button={
          <>
            <span className="project-avatar" aria-hidden="true">
              {title.slice(0, 1).toUpperCase()}
            </span>
            {collapsed ? null : (
              <>
                <span className="project-text">
                  <span className="project-eyebrow">Project</span>
                  <span className="project-title">{title}</span>
                </span>
                <ChevronDownIcon size={16} />
              </>
            )}
          </>
        }
        items={projects.map((p) => ({
          id: p.namespace,
          label: p.title,
          hint: p.namespace,
          checked: p.namespace === namespace,
          onSelect: () => onPick(p.namespace),
        }))}
      />
    </div>
  )
}

/** Where you are: the group or project, the area, and an object's full page. */
function Crumbs({
  area,
  project,
  object,
}: {
  area: Area
  project?: string
  object?: { ns?: string; kind: string; name: string }
}) {
  const label = AREAS.find((a) => a.key === area)!.label
  const first = project ?? groupLabel(area)
  return (
    <nav className="topbar-crumbs" aria-label="You are here">
      <ol>
        {first ? <li className="crumb crumb-muted">{first}</li> : null}
        <li className="crumb">
          {object ? (
            <a href={hrefFor({ area, ...NO_OBJECT })}>{label}</a>
          ) : (
            <span className="crumb-current" aria-current="page">
              {label}
            </span>
          )}
        </li>
        {object ? (
          <li className="crumb">
            <span className="crumb-current" aria-current="page">
              {object.kind} <span className="mono">{object.name}</span>
            </span>
          </li>
        ) : null}
      </ol>
    </nav>
  )
}

/** Says, compactly, that the page shows demo data; its popover says what that means and how to leave. */
function DemoChip() {
  const id = useId()
  const [open, setOpen] = useState(false)
  const wrapper = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  usePopover(open, () => setOpen(false), wrapper, button)
  return (
    <div className="demo-chip" ref={wrapper}>
      <button
        ref={button}
        type="button"
        className="status status-demo"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        title="Built-in example data, not a cluster. Nothing here is live."
        onClick={() => setOpen((o) => !o)}
      >
        <span className="status-dot" aria-hidden="true" />
        Demo data
      </button>
      {open ? (
        <div className="popover demo-popover" id={id}>
          <p>
            <strong>Demo mode.</strong> You are looking at built-in example data, not a cluster. Nothing here is live.
          </p>
          <a href="?" className="button demo-leave">
            Leave demo
          </a>
        </div>
      ) : null}
    </div>
  )
}

/**
 * How fresh the page is: the connection, when the data last changed, and the
 * namespace in view. `scope` is a namespace, '' for every namespace, or
 * undefined where the area shows none. In demo mode the demo chip speaks for
 * the connection.
 */
function Freshness({ connection, scope }: { connection: Connection; scope?: string }) {
  const demo = connection.phase === 'demo'
  return (
    <div className="freshness">
      {demo ? null : <ConnectionStatus connection={connection} />}
      <span className="freshness-detail">
        {connection.updatedAt ? (
          <span>
            Updated <Ago at={connection.updatedAt} />
          </span>
        ) : demo ? null : (
          <span>Not loaded yet</span>
        )}
        {scope === undefined ? null : scope ? (
          <span>
            Namespace <code>{scope}</code>
          </span>
        ) : (
          <span>All namespaces</span>
        )}
      </span>
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

const THEME_ICONS: Record<ThemeChoice, (props: { size?: number }) => ReactNode> = {
  system: MonitorIcon,
  light: SunIcon,
  dark: MoonIcon,
}

/** The top bar's theme button: says the theme chosen, and goes to the next. */
export function ThemeToggle() {
  const { theme } = usePrefs()
  const dark = usePrefersDark()
  const next = nextTheme(theme)
  const shown = theme === 'system' ? ` (${resolveTheme(theme, dark)} now)` : ''
  const label = `Theme: ${THEME_LABELS[theme]}${shown}. Switch to ${THEME_LABELS[next]}`
  const Icon = THEME_ICONS[theme]
  return (
    <button
      type="button"
      className="icon-button theme-toggle"
      aria-label={label}
      title={label}
      onClick={() => setPref('theme', next)}
    >
      <Icon size={18} />
    </button>
  )
}

function Segmented<T extends string>({
  legend,
  hint,
  name,
  value,
  options,
  onChange,
  autoFocus,
}: {
  legend: string
  hint: string
  name: string
  value: T
  options: { value: T; label: string; icon?: ReactNode }[]
  onChange: (value: T) => void
  autoFocus?: boolean
}) {
  return (
    <fieldset className="setting">
      <legend className="setting-legend">{legend}</legend>
      <p className="setting-hint">{hint}</p>
      <div className="segmented">
        {options.map((o) => (
          <label key={o.value} className="segment">
            <input
              type="radio"
              name={name}
              value={o.value}
              checked={o.value === value}
              onChange={() => onChange(o.value)}
              data-autofocus={autoFocus && o.value === value ? true : undefined}
            />
            <span className="segment-face">
              {o.icon}
              {o.label}
            </span>
          </label>
        ))}
      </div>
    </fieldset>
  )
}

/** Settings: the theme, the density and how the sidebar opens, kept in this browser; and the project's Chat, read only. */
export function SettingsModal({
  open,
  onClose,
  chat,
  chatProblems,
}: {
  open: boolean
  onClose: () => void
  chat?: Chat
  chatProblems?: Problem[]
}) {
  const prefs = usePrefs()
  return (
    <Modal open={open} onClose={onClose} title="Settings" description="Kept in this browser only." size="narrow">
      <Segmented<ThemeChoice>
        legend="Appearance"
        hint="System follows your device's light or dark setting."
        name="theme"
        value={prefs.theme}
        autoFocus
        options={[
          { value: 'system', label: 'System', icon: <MonitorIcon size={15} /> },
          { value: 'light', label: 'Light', icon: <SunIcon size={15} /> },
          { value: 'dark', label: 'Dark', icon: <MoonIcon size={15} /> },
        ]}
        onChange={(v) => setPref('theme', v)}
      />
      <Segmented<Density>
        legend="Density"
        hint="Compact fits more on the screen."
        name="density"
        value={prefs.density}
        options={[
          { value: 'comfortable', label: 'Comfortable' },
          { value: 'compact', label: 'Compact' },
        ]}
        onChange={(v) => setPref('density', v)}
      />
      <Segmented<SidebarState>
        legend="Sidebar"
        hint="How the sidebar opens on a wide screen; its button changes it too."
        name="sidebar"
        value={prefs.sidebar}
        options={[
          { value: 'expanded', label: 'Expanded' },
          { value: 'collapsed', label: 'Icons only' },
        ]}
        onChange={(v) => setPref('sidebar', v)}
      />
      <ChatPanel chat={chat} problems={chatProblems} />
    </Modal>
  )
}

/** The versions running, what the page can change, where to read about them, and the project's Chat. */
function AboutArea({ versions, chat, chatProblems }: { versions: Versions; chat?: Chat; chatProblems?: Problem[] }) {
  const acts = useActs()
  const links = aboutLinks(versions)
  return (
    <div className="shell-page">
      <section className="panel about">
        <h1 className="panel-title">About this factory</h1>
        <p className="panel-text about-text">
          This page reads the theos resources in the cluster and updates as they change.{' '}
          {acts === 'none'
            ? 'It can change only three things: create a Brief from an app idea, add a prompt to a Brief, and pause or resume a Brief.'
            : "With actions on, it also changes Brief and Crew settings, retries a Container, reruns a release, starts a Task and moves a Standard's template ref."}{' '}
          The cluster decides what it accepts.
        </p>
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
        <ChatPanel chat={chat} problems={chatProblems} />
      </section>
    </div>
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
    <div className="shell-page">
      <section className="panel">
        <div className="eyebrow">
          {kind}
          {ns ? ` · ${ns}` : ''}
        </div>
        <h1 className="panel-title">{name}</h1>
        <p className="panel-text">The UI has no page for a {kind}.</p>
      </section>
    </div>
  )
}

/** The factory's health and agent slots: the Factory area, a dashboard grid. */
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
    <div className="shell-page">
      <header className="area-head">
        <div>
          <h1 className="area-title">Factory</h1>
          <p className="area-caption">
            Whether each part of the factory is healthy, the node’s usage over the last hour, and the agent slots.
          </p>
        </div>
      </header>
      <HealthDashboard
        health={health}
        loaded={healthLoaded}
        problems={healthProblems}
        now={now}
        slots={
          <SlotsPanel
            tasks={data.tasks}
            loaded={readOf(connection, RESOURCES.tasks)}
            problems={problemsOf(connection, RESOURCES.tasks)}
            health={health}
            now={now}
            onSelectTask={onSelectTask}
          />
        }
      />
    </div>
  )
}
