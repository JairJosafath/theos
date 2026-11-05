// @vitest-environment happy-dom

// The shell (docs/plans/done/ui-shell.md, step 1): the sidebar's areas with the
// Needs you count, folded to icons and remembered per browser; the demo chip;
// Search or jump from ⌘K / Ctrl+K or /; the area's primary action; an object's
// page in the drawer over the area; and About with what the footer said.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { emptyFactory, type Connection } from '../k8s/sources'
import type { Chat } from '../k8s/types'
import type { UrlState } from '../lib/hooks'
import { PREF_KEYS, resetPrefsForTest } from '../lib/prefs'
import { paletteItems } from '../model/search'
import { Shell, type ShellProps } from './Shell'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

let root: Root | undefined
let host: HTMLElement | undefined

beforeEach(() => {
  localStorage.clear()
  resetPrefsForTest()
})

afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  host?.remove()
})

const live: Connection = { phase: 'live', loaded: true, problems: [] }
const chat: Chat = {
  metadata: { name: 'webshop', namespace: 'webshop' },
  spec: { credentialsSecretRef: { name: 'matrix-bot' } },
  status: {
    homeserver: 'http://theos-matrix.theos-system.svc:8008',
    rooms: [{ purpose: 'general', id: '!g:theos.internal', alias: '#theos-webshop-general:theos.internal' }],
    conditions: [{ type: 'Ready', status: 'True', reason: 'Ready', message: 'The Space and its 6 rooms are there' }],
  },
}
const project = { metadata: { name: 'webshop', namespace: 'webshop' }, spec: { displayName: 'Webshop' } }

function props(url: Partial<UrlState>, more: Partial<ShellProps> = {}): ShellProps {
  const area = url.area ?? 'needs'
  return {
    url: { demo: false, view: 'context', ...url },
    area,
    setUrl: vi.fn(),
    connection: live,
    namespace: 'webshop',
    versions: { ui: '1.4.0', operator: '1.4.0', chart: '1.4.0', api: 'theos.factory.io/v1beta1' },
    onSignOut: async () => undefined,
    projects: [{ namespace: 'webshop', title: 'Webshop' }],
    needsCount: 3,
    banners: null,
    areas: {
      needs: createElement('h1', null, 'Needs you'),
      loop: createElement('h1', null, 'Loop'),
      briefs: createElement('h1', null, 'Briefs'),
      story: createElement('h1', null, 'Story'),
      work: createElement('h1', null, 'Work'),
      factory: createElement('h1', null, 'Factory'),
    },
    paletteItems: paletteItems({
      projects: [project] as never,
      factory: { ...emptyFactory(), tasks: [{ metadata: { name: 'orders-api-21', namespace: 'webshop' } }] } as never,
      area,
    }),
    ...more,
  }
}

async function render(p: ShellProps) {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root!.render(createElement(Shell, p)))
  return host
}

async function rerender(p: ShellProps) {
  await act(async () => root!.render(createElement(Shell, p)))
}

const button = (name: string) =>
  [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.getAttribute('aria-label') === name || b.textContent?.trim() === name,
  )

async function key(k: string, init: KeyboardEventInit = {}, target: Element = document.activeElement ?? document.body) {
  await act(async () => {
    target.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init }))
  })
}

describe('the sidebar', () => {
  it('lists the areas in their groups, the current one marked, with the Needs you count in its name', async () => {
    const html = await render(props({ area: 'work' }))
    const nav = html.querySelector('nav[aria-label="Areas"]')!
    const links = [...nav.querySelectorAll('a')].map((a) => a.textContent)
    expect(links).toEqual(['Needs you, 3 waiting', 'Loop', 'Briefs', 'Story', 'Work', 'Factory', 'About'])
    expect([...nav.querySelectorAll('.nav-group-label')].map((g) => g.textContent)).toEqual(['Build', 'Operate'])
    expect(nav.querySelector('[aria-current="page"]')!.textContent).toBe('Work')
    expect(button('Settings')).toBeDefined()
    expect(html.querySelector('.sidebar-foot')!.textContent).toContain('UI 1.4.0 · operator 1.4.0')
  })

  it('folds to its icons and remembers it in this browser', async () => {
    const html = await render(props({ area: 'work' }))
    expect(html.querySelector('.shell')!.classList.contains('sidebar-collapsed')).toBe(false)
    await act(async () => button('Collapse the sidebar')!.click())
    expect(html.querySelector('.shell')!.classList.contains('sidebar-collapsed')).toBe(true)
    expect(localStorage.getItem(PREF_KEYS.sidebar)).toBe('collapsed')
    // The names stay, for screen readers and the tooltips.
    expect(html.querySelector('nav[aria-label="Areas"] a[title="Work"]')!.textContent).toBe('Work')

    // Another visit in the same browser opens it folded.
    await act(async () => root!.unmount())
    resetPrefsForTest()
    const again = await render(props({ area: 'work' }))
    expect(again.querySelector('.shell')!.classList.contains('sidebar-collapsed')).toBe(true)
    await act(async () => button('Expand the sidebar')!.click())
    expect(localStorage.getItem(PREF_KEYS.sidebar)).toBe('expanded')
  })

  it('goes to an area on a plain click, closing any object', async () => {
    const p = props({ area: 'work', ns: 'webshop', kind: 'Task', name: 'orders-api-21' })
    const html = await render(p)
    const loop = [...html.querySelectorAll<HTMLAnchorElement>('nav[aria-label="Areas"] a')].find(
      (a) => a.textContent === 'Loop',
    )!
    await act(async () => loop.click())
    expect(p.setUrl).toHaveBeenCalledWith({
      area: 'loop',
      ns: undefined,
      kind: undefined,
      name: undefined,
      full: undefined,
      tab: undefined,
    })
  })

  it('opens Settings with the theme, the density and the sidebar', async () => {
    await render(props({}))
    await act(async () => button('Settings')!.click())
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.textContent).toContain('Settings')
    expect([...dialog.querySelectorAll('legend')].map((l) => l.textContent)).toEqual([
      'Appearance',
      'Density',
      'Sidebar',
    ])
    const compact = dialog.querySelector<HTMLInputElement>('input[name="density"][value="compact"]')!
    await act(async () => compact.click())
    expect(document.documentElement.getAttribute('data-density')).toBe('compact')
    expect(localStorage.getItem(PREF_KEYS.density)).toBe('compact')
  })
})

describe('the top bar', () => {
  it('says demo data in a chip, whose popover explains it and leaves demo mode', async () => {
    const html = await render(props({ area: 'needs', demo: true }, { connection: { ...live, phase: 'demo' } }))
    const chip = button('Demo data')!
    expect(chip.getAttribute('title')).toMatch(/not a cluster/)
    expect(html.querySelector('.banner-demo')).toBeNull()
    await act(async () => chip.click())
    expect(chip.getAttribute('aria-expanded')).toBe('true')
    const popover = document.getElementById(chip.getAttribute('aria-controls')!)!
    expect(popover.textContent).toContain('You are looking at built-in example data, not a cluster.')
    expect(popover.querySelector('a')!.textContent).toBe('Leave demo')
    expect(popover.querySelector('a')!.getAttribute('href')).toBe('?')
  })

  it('shows the area’s primary action, and the freshness of the page', async () => {
    const html = await render(
      props({ area: 'briefs' }, { primaryAction: createElement('button', { type: 'button' }, 'New brief') }),
    )
    expect(html.querySelector('.topbar')!.textContent).toContain('New brief')
    expect(html.querySelector('.freshness')!.textContent).toContain('Live')
    expect(html.querySelector('.freshness')!.textContent).toContain('Namespace webshop')
    expect(html.querySelector('.topbar-crumbs')!.textContent).toBe('WebshopBriefs')
  })
})

describe('Search or jump', () => {
  it('opens on Ctrl+K, finds an object by name and goes to it on Enter', async () => {
    const p = props({ area: 'work' })
    await render(p)
    await key('k', { ctrlKey: true }, document.body)
    const input = document.querySelector<HTMLInputElement>('input[role="combobox"]')!
    expect(document.activeElement).toBe(input)
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(input, 'api 21')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const options = [...document.querySelectorAll('[role="option"]')]
    expect(options[0].textContent).toContain('orders-api-21')
    expect(options[0].getAttribute('aria-selected')).toBe('true')
    await key('Enter', {}, input)
    expect(p.setUrl).toHaveBeenCalledWith({ ns: 'webshop', kind: 'Task', name: 'orders-api-21', full: undefined })
    expect(document.querySelector('input[role="combobox"]')).toBeNull()
  })

  it('moves with the arrow keys and closes on Esc', async () => {
    const p = props({ area: 'work' })
    await render(p)
    await key('k', { metaKey: true }, document.body)
    const input = document.querySelector<HTMLInputElement>('input[role="combobox"]')!
    const selected = () => document.querySelector('[role="option"][aria-selected="true"]')!.textContent
    expect(selected()).toContain('Needs you')
    await key('ArrowDown', {}, input)
    expect(selected()).toContain('Loop')
    await key('ArrowUp', {}, input)
    await key('ArrowUp', {}, input)
    expect(selected()).toContain('orders-api-21')
    expect(input.getAttribute('aria-activedescendant')).toBe(
      document.querySelector('[role="option"][aria-selected="true"]')!.id,
    )
    await key('Escape', {}, input)
    expect(document.querySelector('input[role="combobox"]')).toBeNull()
    expect(p.setUrl).not.toHaveBeenCalled()
  })

  it('opens on / outside a field, and not while typing in one', async () => {
    const html = await render(props({ area: 'work' }))
    const field = document.createElement('input')
    html.append(field)
    await key('/', {}, field)
    expect(document.querySelector('input[role="combobox"]')).toBeNull()
    await key('/', {}, document.body)
    expect(document.querySelector('input[role="combobox"]')).not.toBeNull()
  })
})

describe('an object', () => {
  const task = { area: 'work' as const, ns: 'webshop', kind: 'Task', name: 'orders-api-21' }
  const page = createElement('h1', null, 'Task orders-api-21')

  it('opens in the drawer over the area, which stays on screen and current', async () => {
    const html = await render(props(task, { objectPage: page }))
    const drawer = document.querySelector('.drawer')!
    expect(drawer.getAttribute('aria-label')).toBe('Task orders-api-21')
    expect(drawer.textContent).toContain('Task orders-api-21')
    expect(html.querySelector('main')!.textContent).toBe('Work')
    expect(html.querySelector('nav[aria-label="Areas"] [aria-current="page"]')!.textContent).toBe('Work')
    expect(drawer.querySelector<HTMLAnchorElement>('a.drawer-full')!.getAttribute('href')).toContain('full=1')
  })

  it('closes on Esc, leaving the area and its tab', async () => {
    const p = props(task, { objectPage: page })
    await render(p)
    await key('Escape')
    expect(p.setUrl).toHaveBeenCalledWith({ ns: undefined, kind: undefined, name: undefined, full: undefined })
  })

  it('shows full width with full=1, the area its breadcrumb', async () => {
    const html = await render(props({ ...task, full: true }, { objectPage: page }))
    expect(document.querySelector('.drawer')).toBeNull()
    expect(html.querySelector('main')!.textContent).toBe('Task orders-api-21')
    const crumbs = html.querySelector('.topbar-crumbs')!
    expect(crumbs.querySelector('a')!.textContent).toBe('Work')
    expect(crumbs.querySelector('[aria-current="page"]')!.textContent).toBe('Task orders-api-21')
    await rerender(props({ area: 'work' }))
    expect(html.querySelector('main')!.textContent).toBe('Work')
  })
})

describe('About', () => {
  it('says what the footer said: what the page reads and the three things it changes', async () => {
    const html = await render(props({ area: 'about' }))
    const about = html.querySelector('.about')!
    expect(about.textContent).toContain('This page reads the theos resources in the cluster')
    expect(about.textContent).toContain(
      'It can change only three things: create a Brief from an app idea, add a prompt to a Brief, and pause or resume a Brief.',
    )
    expect(html.querySelector('footer.footer')).toBeNull()
  })

  it("shows the project's Chat, read only, when it has one, and nothing of it when not", async () => {
    let html = await render(props({ area: 'about' }))
    expect(html.querySelector('.chat-panel')).toBeNull()
    await act(async () => root!.unmount())
    html = await render(props({ area: 'about' }, { chat }))
    const panel = html.querySelector('.about .chat-panel')!
    expect(panel.textContent).toContain('http://theos-matrix.theos-system.svc:8008')
    expect(
      panel.querySelector('a[href="https://matrix.to/#/%23theos-webshop-general%3Atheos.internal"]'),
    ).not.toBeNull()
    expect(panel.querySelectorAll('button, input')).toHaveLength(0)
  })
})

describe('Settings', () => {
  it("shows the project's Chat below the preferences", async () => {
    await render(props({}, { chat }))
    await act(async () => button('Settings')!.click())
    const panel = document.querySelector('[role="dialog"] .chat-panel')!
    expect(panel.textContent).toContain('Yes: The Space and its 6 rooms are there')
  })
})
