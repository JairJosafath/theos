import { createContext, useCallback, useContext, useEffect, useState, useSyncExternalStore } from 'react'
import type { UiSession } from '../k8s/session'
import { isArea, type Area } from '../model/areas'
import type { Snapshot, Source } from '../k8s/sources'

/** Subscribes to a data source for as long as the component is mounted. */
export function useSource<T>(source: Source<T>): Snapshot<T> {
  useEffect(() => {
    source.start()
    return () => source.stop()
  }, [source])
  return useSyncExternalStore(
    useCallback((listener) => source.subscribe(listener), [source]),
    () => source.getSnapshot(),
  )
}

/** The UI server's session answer for the page; unset until it comes, and in demo mode. */
export const SessionContext = createContext<UiSession | undefined>(undefined)

/**
 * Whether the page offers its action buttons: 'act' when the UI server takes
 * actions, 'sign-in' when it does so only as a person and nobody is signed
 * in, else 'none' (actions off, demo mode, or before the answer comes).
 */
export function useActs(): 'act' | 'sign-in' | 'none' {
  const session = useContext(SessionContext)
  if (!session?.actions) return 'none'
  return session.mode === 'oidc' && !session.user ? 'sign-in' : 'act'
}

/** Asks the UI server's session again; a no-op outside the page. */
export const RefreshSession = createContext<() => void>(() => undefined)

/** A fixed time for every useNow below it, for tests; unset, useNow follows the clock. */
export const FixedNow = createContext<number | undefined>(undefined)

/**
 * One clock for the whole page, ticking every second while anything shows it,
 * so two panels showing the same heartbeat agree.
 */
const clock = {
  now: Date.now(),
  listeners: new Set<() => void>(),
  timer: undefined as ReturnType<typeof setInterval> | undefined,
  subscribe(listener: () => void): () => void {
    clock.listeners.add(listener)
    if (!clock.timer) {
      clock.now = Date.now()
      clock.timer = setInterval(() => {
        clock.now = Date.now()
        for (const l of clock.listeners) l()
      }, 1000)
    }
    return () => {
      clock.listeners.delete(listener)
      if (!clock.listeners.size) {
        clearInterval(clock.timer)
        clock.timer = undefined
      }
    }
  },
}
const noSubscription = () => () => undefined

/**
 * The current time, updated every second, for "12 s ago" style labels. Only
 * the components that show a running clock call it, so the tick re-renders
 * them and nothing above them.
 */
export function useNow(): number {
  const fixed = useContext(FixedNow)
  const now = useSyncExternalStore(
    fixed === undefined ? clock.subscribe : noSubscription,
    () => clock.now,
    () => clock.now,
  )
  return fixed ?? now
}

/** Story's two diagrams. */
export type View = 'context' | 'containers'

export interface UrlState {
  demo: boolean
  /** The area on screen; unset, the shell picks one. */
  area?: Area
  /** An object page: ns=<namespace>&kind=<Kind>&name=<name>, in the drawer over the area. */
  ns?: string
  kind?: string
  name?: string
  /** The object's page full width instead of in the drawer: full=1. */
  full?: boolean
  /** The area's tab on screen, for the areas with tabs (components/ui/Tabs.tsx). */
  tab?: string
  project?: string
  view: View
  system?: string
}

const VIEWS: View[] = ['context', 'containers']

/**
 * The URL state of a query string. A link from before the areas (view=,
 * system=, project=, task=) still opens what it showed: the factory view is
 * Work, a Task in it is its page, and the story's views are Story. An area=
 * in the link wins.
 */
export function parseUrl(search: string): UrlState {
  const params = new URLSearchParams(search)
  const get = (key: string) => params.get(key) || undefined
  const system = get('system')
  const viewParam = params.get('view')
  const state: UrlState = {
    demo: params.get('demo') === '1',
    area: isArea(params.get('area')) ? (params.get('area') as Area) : undefined,
    ns: get('ns'),
    kind: get('kind'),
    name: get('name'),
    full: params.get('full') === '1' || undefined,
    tab: get('tab'),
    project: get('project'),
    // A system alone was always shown with its containers.
    view: VIEWS.includes(viewParam as View) ? (viewParam as View) : system ? 'containers' : 'context',
    system,
  }
  if (state.area) return state
  if (viewParam === 'factory') {
    const [ns, name] = get('task')?.split('/') ?? []
    const task = ns && name && !(state.kind || state.name) ? { ns, kind: 'Task', name } : {}
    return { ...state, ...task, area: 'work' }
  }
  if (VIEWS.includes(viewParam as View) || system || state.project) return { ...state, area: 'story' }
  return state
}

function readUrl(): UrlState {
  return parseUrl(window.location.search)
}

/** The link to the page with `next` merged into what the URL says now. */
export function hrefFor(next: Partial<UrlState>): string {
  const merged = { ...readUrl(), ...next }
  const params = new URLSearchParams()
  if (merged.demo) params.set('demo', '1')
  if (merged.area) params.set('area', merged.area)
  if (merged.tab) params.set('tab', merged.tab)
  if (merged.kind && merged.name) {
    if (merged.ns) params.set('ns', merged.ns)
    params.set('kind', merged.kind)
    params.set('name', merged.name)
    if (merged.full) params.set('full', '1')
  }
  if (merged.project) params.set('project', merged.project)
  if (merged.view === 'containers') {
    params.set('view', merged.view)
    if (merged.system) params.set('system', merged.system)
  }
  const query = params.toString()
  return `${window.location.pathname}${query ? `?${query}` : ''}${window.location.hash}`
}

/** The page's address as it is now. */
function currentHref(): string {
  return `${window.location.pathname}${window.location.search}${window.location.hash}`
}

/** Sets what the address says; replace adds no history entry. */
export type SetUrl = (next: Partial<UrlState>, replace?: boolean) => void

/** The page's URL state and its setter, for components deep in the tree such as Tabs. */
export const UrlContext = createContext<[UrlState, SetUrl] | undefined>(undefined)

/**
 * The link a click follows inside the page, as a URL, or undefined for one the
 * browser should follow itself: with a modifier or another button, to another
 * page or target, a download, a jump within the page, or into or out of demo
 * mode (which loads the page again).
 */
export function inPageLink(e: MouseEvent, location: Location): URL | undefined {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return undefined
  const target = e.target as Element | null
  const a = target?.closest?.('a[href]') as HTMLAnchorElement | null
  if (!a || (a.target && a.target !== '_self') || a.hasAttribute('download')) return undefined
  const to = new URL(a.getAttribute('href')!, location.href)
  if (to.origin !== location.origin || to.pathname !== location.pathname) return undefined
  if (to.search === location.search) return undefined
  if (parseUrl(to.search).demo !== parseUrl(location.search).demo) return undefined
  return to
}

/**
 * Which area, object, project, view and system are shown, kept in the query
 * string so a page can be bookmarked or shared, and the back button works.
 * An old link is rewritten in place to the area it opens, so the address bar
 * names the area. A link to another address of the page is followed in place,
 * so an object opens in the drawer over the area it was opened from.
 */
export function useUrlState(): [UrlState, SetUrl] {
  const [state, setState] = useState(readUrl)
  useEffect(() => {
    const canonical = hrefFor({})
    if (canonical !== currentHref()) window.history.replaceState(null, '', canonical)
    const onPop = () => setState(readUrl())
    const onClick = (e: MouseEvent) => {
      const to = inPageLink(e, window.location)
      if (!to) return
      e.preventDefault()
      const next = parseUrl(to.search)
      window.history.pushState(null, '', `${to.pathname}${to.search}${to.hash}`)
      setState(readUrl())
      // A new page starts at its top; an object in the drawer keeps the area where it was.
      if (!next.kind || next.full) window.scrollTo(0, 0)
    }
    window.addEventListener('popstate', onPop)
    document.addEventListener('click', onClick)
    return () => {
      window.removeEventListener('popstate', onPop)
      document.removeEventListener('click', onClick)
    }
  }, [])
  const update = useCallback((next: Partial<UrlState>, replace = false) => {
    // The URL is the source of truth, so merge into what it says now.
    const merged = { ...readUrl(), ...next }
    const url = hrefFor(next)
    if (url !== currentHref()) {
      window.history[replace ? 'replaceState' : 'pushState'](null, '', url)
    }
    setState(merged)
  }, [])
  return [state, update]
}

/**
 * Width of an element, kept up to date as it resizes. Pass the returned ref to
 * the element; the element itself is returned too, for effects that need it.
 */
export function useElementWidth<T extends HTMLElement>(): [
  (el: T | null) => (() => void) | undefined,
  number,
  T | null,
] {
  const [width, setWidth] = useState(0)
  const [element, setElement] = useState<T | null>(null)
  const ref = useCallback((el: T | null) => {
    setElement(el)
    if (!el) return undefined
    setWidth(el.clientWidth)
    const observer = new ResizeObserver(() => setWidth(el.clientWidth))
    observer.observe(el)
    return () => observer.disconnect()
  }, [])
  return [ref, width, element]
}
