import { createContext, useCallback, useContext, useEffect, useState, useSyncExternalStore } from 'react'
import type { UiSession } from '../k8s/session'
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

export type View = 'context' | 'containers' | 'factory'

export interface UrlState {
  demo: boolean
  project?: string
  view: View
  system?: string
  /** The Task selected in the factory view, as namespace/name. */
  task?: string
}

const VIEWS: View[] = ['context', 'containers', 'factory']

function readUrl(): UrlState {
  const params = new URLSearchParams(window.location.search)
  return {
    demo: params.get('demo') === '1',
    project: params.get('project') || undefined,
    view: VIEWS.includes(params.get('view') as View) ? (params.get('view') as View) : 'context',
    system: params.get('system') || undefined,
    task: params.get('task') || undefined,
  }
}

/**
 * Which project, view and system are shown, kept in the query string so a view
 * can be bookmarked or shared, and the back button works.
 */
export function useUrlState(): [UrlState, (next: Partial<UrlState>, replace?: boolean) => void] {
  const [state, setState] = useState(readUrl)
  useEffect(() => {
    const onPop = () => setState(readUrl())
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  const update = useCallback((next: Partial<UrlState>, replace = false) => {
    // The URL is the source of truth, so merge into what it says now.
    const merged = { ...readUrl(), ...next }
    const params = new URLSearchParams()
    if (merged.demo) params.set('demo', '1')
    if (merged.project) params.set('project', merged.project)
    if (merged.view !== 'context') params.set('view', merged.view)
    if (merged.system && merged.view === 'containers') params.set('system', merged.system)
    if (merged.task && merged.view === 'factory') params.set('task', merged.task)
    const query = params.toString()
    const url = `${window.location.pathname}${query ? `?${query}` : ''}${window.location.hash}`
    if (url !== `${window.location.pathname}${window.location.search}${window.location.hash}`) {
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
