// The person's own choices for this browser (docs/plans/done/ui-shell.md): the theme,
// the density and whether the sidebar opens expanded. They live in localStorage,
// every read and write wrapped so a browser that refuses storage (a private
// window, blocked site data) still gets the page and the choice for this visit.
// index.html applies the stored theme and density before the first paint.

import { useCallback, useState, useSyncExternalStore } from 'react'

export type ThemeChoice = 'system' | 'light' | 'dark'
export type Density = 'comfortable' | 'compact'
export type SidebarState = 'expanded' | 'collapsed'

export interface Prefs {
  theme: ThemeChoice
  density: Density
  sidebar: SidebarState
}

/** The storage keys; index.html's inline script reads the first two by the same names. */
export const PREF_KEYS: Record<keyof Prefs, string> = {
  theme: 'theos.theme',
  density: 'theos.density',
  sidebar: 'theos.sidebar',
}

export const THEMES: ThemeChoice[] = ['system', 'light', 'dark']
const DENSITIES: Density[] = ['comfortable', 'compact']
const SIDEBARS: SidebarState[] = ['expanded', 'collapsed']

export const DEFAULT_PREFS: Prefs = { theme: 'system', density: 'comfortable', sidebar: 'expanded' }

/** A stored value, or null when there is none or storage refuses. */
export function readStored(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

/** Stores a value; false when storage refuses, and the page goes on without it. */
export function writeStored(key: string, value: string): boolean {
  try {
    window.localStorage.setItem(key, value)
    return true
  } catch {
    return false
  }
}

function oneOf<T extends string>(value: string | null, allowed: T[], fallback: T): T {
  return allowed.includes(value as T) ? (value as T) : fallback
}

/** The stored choices, each falling back to its default. */
export function readPrefs(): Prefs {
  return {
    theme: oneOf(readStored(PREF_KEYS.theme), THEMES, DEFAULT_PREFS.theme),
    density: oneOf(readStored(PREF_KEYS.density), DENSITIES, DEFAULT_PREFS.density),
    sidebar: oneOf(readStored(PREF_KEYS.sidebar), SIDEBARS, DEFAULT_PREFS.sidebar),
  }
}

/** Puts the theme and density on <html>, where theme.css and the density rules read them. */
export function applyPrefs(prefs: Prefs, root: HTMLElement = document.documentElement): void {
  root.setAttribute('data-theme', prefs.theme)
  root.setAttribute('data-density', prefs.density)
}

/** The theme the page shows: System follows the device's colour scheme. */
export function resolveTheme(choice: ThemeChoice, prefersDark: boolean): 'light' | 'dark' {
  return choice === 'system' ? (prefersDark ? 'dark' : 'light') : choice
}

/** The top bar's toggle: System, then Light, then Dark, then System again. */
export function nextTheme(choice: ThemeChoice): ThemeChoice {
  return THEMES[(THEMES.indexOf(choice) + 1) % THEMES.length]
}

export const THEME_LABELS: Record<ThemeChoice, string> = { system: 'System', light: 'Light', dark: 'Dark' }

/**
 * The choices of this page, shared by every component that shows or changes
 * one. Read from storage once; a change is kept here even when storage
 * refuses it, so it holds for the visit.
 */
const store = {
  prefs: undefined as Prefs | undefined,
  listeners: new Set<() => void>(),
  get(): Prefs {
    store.prefs ??= typeof window === 'undefined' ? DEFAULT_PREFS : readPrefs()
    return store.prefs
  },
  subscribe(listener: () => void): () => void {
    store.listeners.add(listener)
    return () => store.listeners.delete(listener)
  },
}

/** Changes one choice: stored, applied to <html> and shown everywhere. */
export function setPref<K extends keyof Prefs>(key: K, value: Prefs[K]): void {
  store.prefs = { ...store.get(), [key]: value }
  writeStored(PREF_KEYS[key], value)
  applyPrefs(store.prefs)
  for (const l of store.listeners) l()
}

/** Forgets the choices read, so the next read goes to storage again; for tests. */
export function resetPrefsForTest(): void {
  store.prefs = undefined
}

export function usePrefs(): Prefs {
  return useSyncExternalStore(store.subscribe, store.get, () => DEFAULT_PREFS)
}

const DARK_QUERY = '(prefers-color-scheme: dark)'

/** Whether the device asks for a dark colour scheme, following it as it changes. */
export function usePrefersDark(): boolean {
  return useMediaQuery(DARK_QUERY)
}

/** Whether the viewport matches a media query, following it as it changes. */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (listener: () => void) => {
      const list = mediaQuery(query)
      list?.addEventListener('change', listener)
      return () => list?.removeEventListener('change', listener)
    },
    [query],
  )
  return useSyncExternalStore(
    subscribe,
    () => mediaQuery(query)?.matches ?? false,
    () => false,
  )
}

/** What else the page keeps in this browser: the Needs you groups folded, and the Story's side panel. */
export const STORED_KEYS = {
  needsFolded: 'theos.needs.folded',
  storyPanel: 'theos.story.panel',
}

/**
 * A set of names kept in this browser under `key`, such as the Needs you
 * groups folded; like the choices above, it holds for the visit when storage
 * refuses it.
 */
export function useStoredSet(key: string): [ReadonlySet<string>, (name: string, on: boolean) => void] {
  const [set, setSet] = useState<ReadonlySet<string>>(() => new Set(readList(key)))
  const toggle = useCallback(
    (name: string, on: boolean) =>
      setSet((prev) => {
        const next = new Set(prev)
        if (on) next.add(name)
        else next.delete(name)
        writeStored(key, JSON.stringify([...next]))
        return next
      }),
    [key],
  )
  return [set, toggle]
}

/** A yes or no kept in this browser under `key`, such as the Story's side panel folded. */
export function useStoredFlag(key: string, fallback: boolean): [boolean, (on: boolean) => void] {
  const [flag, setFlag] = useState(() => {
    const stored = readStored(key)
    return stored === 'true' ? true : stored === 'false' ? false : fallback
  })
  const set = useCallback(
    (on: boolean) => {
      setFlag(on)
      writeStored(key, String(on))
    },
    [key],
  )
  return [flag, set]
}

function readList(key: string): string[] {
  try {
    const list: unknown = JSON.parse(readStored(key) ?? '[]')
    return Array.isArray(list) ? list.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}

function mediaQuery(query: string): MediaQueryList | undefined {
  try {
    return typeof window !== 'undefined' && window.matchMedia ? window.matchMedia(query) : undefined
  } catch {
    return undefined
  }
}
