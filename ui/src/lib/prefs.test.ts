// @vitest-environment happy-dom

// The theme, density and sidebar choices (docs/plans/ui-shell.md): kept in
// localStorage, applied on <html> as data-theme and data-density, System
// following the device's colour scheme, and a browser that refuses storage
// still gets the page and the choice for the visit.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import themeCss from '../theme.css?raw'
import indexHtml from '../../index.html?raw'
import { ThemeToggle } from '../components/Shell'
import {
  applyPrefs,
  nextTheme,
  PREF_KEYS,
  readPrefs,
  resetPrefsForTest,
  resolveTheme,
  setPref,
  usePrefersDark,
} from './prefs'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

let root: Root | undefined
let host: HTMLElement | undefined

async function render(node: ReturnType<typeof createElement>) {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root!.render(node))
  return host
}

/** A device whose colour scheme the test sets, and changes. */
function fakeDevice(dark: boolean) {
  const listeners = new Set<() => void>()
  const query = {
    get matches() {
      return dark
    },
    addEventListener: (_: string, l: () => void) => listeners.add(l),
    removeEventListener: (_: string, l: () => void) => listeners.delete(l),
  }
  vi.stubGlobal(
    'matchMedia',
    vi.fn((q: string) => (q === '(prefers-color-scheme: dark)' ? query : { ...query, matches: false })),
  )
  return {
    set(next: boolean) {
      dark = next
      for (const l of listeners) l()
    },
  }
}

beforeEach(() => {
  localStorage.clear()
  resetPrefsForTest()
  document.documentElement.removeAttribute('data-theme')
  document.documentElement.removeAttribute('data-density')
})

afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  host?.remove()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('the theme choice', () => {
  it('defaults to System, comfortable and an expanded sidebar', () => {
    expect(readPrefs()).toEqual({ theme: 'system', density: 'comfortable', sidebar: 'expanded' })
  })

  it('is stored, read back and applied on <html>', () => {
    setPref('theme', 'dark')
    expect(localStorage.getItem(PREF_KEYS.theme)).toBe('dark')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
    resetPrefsForTest()
    expect(readPrefs().theme).toBe('dark')

    setPref('density', 'compact')
    expect(localStorage.getItem('theos.density')).toBe('compact')
    expect(document.documentElement.getAttribute('data-density')).toBe('compact')
  })

  it('ignores a stored value it does not know', () => {
    localStorage.setItem(PREF_KEYS.theme, 'sepia')
    localStorage.setItem(PREF_KEYS.sidebar, 'sideways')
    expect(readPrefs()).toEqual({ theme: 'system', density: 'comfortable', sidebar: 'expanded' })
  })

  it('applies System as data-theme="system", which theme.css leaves to the media query', () => {
    applyPrefs({ theme: 'system', density: 'comfortable', sidebar: 'expanded' })
    expect(document.documentElement.getAttribute('data-theme')).toBe('system')
  })

  it('follows the device under System, and not under Light or Dark', () => {
    expect(resolveTheme('system', true)).toBe('dark')
    expect(resolveTheme('system', false)).toBe('light')
    expect(resolveTheme('light', true)).toBe('light')
    expect(resolveTheme('dark', false)).toBe('dark')
  })

  it('cycles System, Light, Dark from the top bar', () => {
    expect([nextTheme('system'), nextTheme('light'), nextTheme('dark')]).toEqual(['light', 'dark', 'system'])
  })

  it('survives a browser whose storage throws, keeping the choice for the visit', () => {
    const refusing = {
      getItem: () => {
        throw new DOMException('denied', 'SecurityError')
      },
      setItem: () => {
        throw new DOMException('full', 'QuotaExceededError')
      },
    }
    vi.spyOn(window, 'localStorage', 'get').mockReturnValue(refusing as unknown as Storage)
    expect(readPrefs().theme).toBe('system')
    expect(() => setPref('theme', 'light')).not.toThrow()
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
    expect(readPrefs().theme).toBe('system')
  })

  it('survives a window whose localStorage itself throws', () => {
    vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError')
    })
    expect(readPrefs()).toEqual({ theme: 'system', density: 'comfortable', sidebar: 'expanded' })
    expect(() => setPref('sidebar', 'collapsed')).not.toThrow()
  })
})

describe('the theme toggle', () => {
  it('names the choice and the theme System shows now, and follows the device as it changes', async () => {
    const device = fakeDevice(true)
    const html = await render(createElement(ThemeToggle))
    const button = () => html.querySelector('button')!
    expect(button().getAttribute('aria-label')).toBe('Theme: System (dark now). Switch to Light')
    await act(async () => device.set(false))
    expect(button().getAttribute('aria-label')).toBe('Theme: System (light now). Switch to Light')

    await act(async () => button().click())
    expect(button().getAttribute('aria-label')).toBe('Theme: Light. Switch to Dark')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
    expect(localStorage.getItem(PREF_KEYS.theme)).toBe('light')
    await act(async () => button().click())
    expect(button().getAttribute('aria-label')).toBe('Theme: Dark. Switch to System')
  })

  it('reads the device through usePrefersDark', async () => {
    const device = fakeDevice(false)
    let seen: boolean[] = []
    function Probe() {
      seen = [...seen, usePrefersDark()]
      return null
    }
    await render(createElement(Probe))
    await act(async () => device.set(true))
    expect(seen.at(0)).toBe(false)
    expect(seen.at(-1)).toBe(true)
  })
})

describe('theme.css', () => {
  /** The declarations of the block that opens with `opener`. */
  function block(opener: string): string[] {
    const at = themeCss.indexOf(opener)
    expect(at, opener).toBeGreaterThan(-1)
    const body = themeCss.slice(at + opener.length, themeCss.indexOf('}', at))
    return body
      .split(';')
      .map((d) => d.trim())
      .filter(Boolean)
  }

  it('applies the dark tokens under the media query unless Light is chosen, and whenever Dark is', () => {
    expect(themeCss).toMatch(/@media \(prefers-color-scheme: dark\) \{\s*:root:not\(\[data-theme='light'\]\) \{/)
    const system = block(":root:not([data-theme='light']) {")
    const chosen = block(":root[data-theme='dark'] {")
    expect(system.length).toBeGreaterThan(30)
    expect(chosen).toEqual(system)
    expect(chosen).toContain('color-scheme: dark')
  })

  it('has no other colour-scheme media query to convert', () => {
    expect(themeCss.match(/prefers-color-scheme/g)).toHaveLength(1)
  })
})

describe('index.html', () => {
  it('applies the stored theme and density before the first paint, by the same keys, in a try', () => {
    const script = indexHtml.slice(indexHtml.indexOf('<script>'), indexHtml.indexOf('</script>'))
    expect(script).toContain(`getItem('${PREF_KEYS.theme}')`)
    expect(script).toContain(`getItem('${PREF_KEYS.density}')`)
    expect(script).toMatch(/try \{[\s\S]*getItem[\s\S]*\} catch/)
    expect(script).toContain("setAttribute('data-theme', theme)")
  })
})
