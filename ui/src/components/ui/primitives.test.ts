// @vitest-environment happy-dom

// The primitives' keyboard (docs/plans/ui-shell.md): Modal and Drawer are
// labelled dialogs that keep Tab inside, close on Esc, the close button or the
// backdrop, and give the focus back to what opened them; Tabs move with the
// arrow keys, Home and End over a roving tabindex, and can keep the tab in the
// address; Menu opens from its button and moves with the arrow keys.

import { act, createElement, useState, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { UrlContext, type UrlState } from '../../lib/hooks'
import { Drawer } from './Drawer'
import { Menu } from './Menu'
import { Modal } from './Modal'
import { Tabs } from './Tabs'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

let root: Root | undefined
let host: HTMLElement | undefined

afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  host?.remove()
  document.documentElement.style.overflow = ''
})

async function render(node: ReactNode) {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root!.render(node))
  return host
}

async function key(k: string, init: KeyboardEventInit = {}, target: Element | null = document.activeElement) {
  await act(async () => {
    ;(target ?? document.body).dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, ...init }))
  })
}

const button = (name: string) =>
  [...document.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent === name || b.getAttribute('aria-label') === name,
  )!

/** A page with a button that opens `overlay`, which gets the close callback. */
function Opener({ overlay }: { overlay: (open: boolean, close: () => void) => ReactNode }) {
  const [open, setOpen] = useState(false)
  return createElement(
    'div',
    null,
    createElement('button', { type: 'button', onClick: () => setOpen(true) }, 'Open'),
    overlay(open, () => setOpen(false)),
  )
}

const modal = (open: boolean, close: () => void) =>
  createElement(Modal, {
    open,
    onClose: close,
    title: 'Settings',
    description: 'Kept in this browser only.',
    children: [
      createElement('input', { key: 'first', 'aria-label': 'First' }),
      createElement('button', { key: 'last', type: 'button' }, 'Last'),
    ],
  })

const drawer = (open: boolean, close: () => void) =>
  createElement(Drawer, {
    open,
    onClose: close,
    label: 'Task orders-api-21',
    actions: createElement('a', { href: '?full=1' }, 'Open full page'),
    children: createElement('p', null, 'The Task'),
  })

describe('Modal', () => {
  it('is a labelled modal dialog that locks the page’s scroll', async () => {
    await render(createElement(Opener, { overlay: modal }))
    button('Open').focus()
    await act(async () => button('Open').click())
    const dialog = document.querySelector('[role="dialog"]')!
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog.getAttribute('aria-labelledby')!)!.textContent).toBe('Settings')
    expect(document.getElementById(dialog.getAttribute('aria-describedby')!)!.textContent).toBe(
      'Kept in this browser only.',
    )
    expect(document.documentElement.style.overflow).toBe('hidden')
  })

  it('closes on Esc and gives the focus back to its opener', async () => {
    await render(createElement(Opener, { overlay: modal }))
    button('Open').focus()
    await act(async () => button('Open').click())
    expect(document.querySelector('[role="dialog"]')!.contains(document.activeElement)).toBe(true)
    await key('Escape')
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(button('Open'))
    expect(document.documentElement.style.overflow).toBe('')
  })

  it('keeps Tab inside: from the last control to the first, and Shift+Tab back', async () => {
    await render(createElement(Opener, { overlay: modal }))
    await act(async () => button('Open').click())
    const close = button('Close')
    const last = button('Last')
    last.focus()
    await key('Tab')
    expect(document.activeElement).toBe(close)
    await key('Tab', { shiftKey: true })
    expect(document.activeElement).toBe(last)
  })

  it('closes from its close button and from a click on the backdrop', async () => {
    await render(createElement(Opener, { overlay: modal }))
    await act(async () => button('Open').click())
    await act(async () => button('Close').click())
    expect(document.querySelector('[role="dialog"]')).toBeNull()

    await act(async () => button('Open').click())
    const backdrop = document.querySelector('.overlay')!
    await act(async () => {
      backdrop.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    })
    expect(document.querySelector('[role="dialog"]')).toBeNull()
  })

  it('leaves Esc to a confirmation <dialog> open inside it', async () => {
    const onClose = vi.fn()
    await render(
      createElement(Modal, {
        open: true,
        onClose,
        title: 'A page',
        children: createElement('dialog', { open: true }, createElement('button', { type: 'button' }, 'Confirm')),
      }),
    )
    await key('Escape', {}, button('Confirm'))
    expect(onClose).not.toHaveBeenCalled()
  })
})

describe('Drawer', () => {
  it('opens as a dialog named for its object, focused itself, and closes on Esc back to the opener', async () => {
    await render(createElement(Opener, { overlay: drawer }))
    button('Open').focus()
    await act(async () => button('Open').click())
    const dialog = document.querySelector<HTMLElement>('.drawer')!
    expect(dialog.getAttribute('role')).toBe('dialog')
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(dialog.getAttribute('aria-label')).toBe('Task orders-api-21')
    expect(document.activeElement).toBe(dialog)
    expect(dialog.querySelector('a')!.textContent).toBe('Open full page')
    await key('Escape')
    expect(document.querySelector('.drawer')).toBeNull()
    expect(document.activeElement).toBe(button('Open'))
  })

  it('closes from its × button', async () => {
    await render(createElement(Opener, { overlay: drawer }))
    await act(async () => button('Open').click())
    await act(async () => button('Close').click())
    expect(document.querySelector('.drawer')).toBeNull()
  })

  it('answers Esc only on top: a modal over it closes first', async () => {
    const closeDrawer = vi.fn()
    const closeModal = vi.fn()
    await render(
      createElement(
        'div',
        null,
        createElement(Drawer, { open: true, onClose: closeDrawer, label: 'Crew orders-api', children: 'Its page' }),
        createElement(Modal, { open: true, onClose: closeModal, title: 'Search', children: 'Over it' }),
      ),
    )
    await key('Escape')
    expect(closeModal).toHaveBeenCalledTimes(1)
    expect(closeDrawer).not.toHaveBeenCalled()
  })
})

const TABS = [
  { id: 'board', label: 'Board', panel: 'The board' },
  { id: 'crews', label: 'Crews', panel: 'The crews' },
  { id: 'releases', label: 'Releases', panel: 'The releases', disabled: true },
  { id: 'time', label: 'Time & tokens', panel: 'The time' },
]

describe('Tabs', () => {
  const tab = (name: string) =>
    [...document.querySelectorAll<HTMLElement>('[role="tab"]')].find((t) => t.textContent === name)!
  const panel = () => document.querySelector('[role="tabpanel"]')!

  it('wires the tablist, tabs and panel, with only the selected tab in the Tab order', async () => {
    await render(createElement(Tabs, { tabs: TABS, label: 'Work' }))
    expect(document.querySelector('[role="tablist"]')!.getAttribute('aria-label')).toBe('Work')
    expect(tab('Board').getAttribute('aria-selected')).toBe('true')
    expect(tab('Board').tabIndex).toBe(0)
    expect(tab('Crews').tabIndex).toBe(-1)
    expect(panel().getAttribute('aria-labelledby')).toBe(tab('Board').id)
    expect(tab('Board').getAttribute('aria-controls')).toBe(panel().id)
    expect(panel().textContent).toBe('The board')
  })

  it('moves with the arrow keys, skipping a disabled tab and wrapping, and with Home and End', async () => {
    await render(createElement(Tabs, { tabs: TABS, label: 'Work' }))
    tab('Board').focus()
    await key('ArrowRight')
    expect(document.activeElement).toBe(tab('Crews'))
    expect(panel().textContent).toBe('The crews')
    await key('ArrowRight')
    expect(document.activeElement).toBe(tab('Time & tokens'))
    await key('ArrowRight')
    expect(document.activeElement).toBe(tab('Board'))
    await key('ArrowLeft')
    expect(document.activeElement).toBe(tab('Time & tokens'))
    expect(tab('Time & tokens').tabIndex).toBe(0)
    expect(tab('Board').tabIndex).toBe(-1)
    await key('Home')
    expect(document.activeElement).toBe(tab('Board'))
    await key('End')
    expect(panel().textContent).toBe('The time')
  })

  it('keeps the tab in the address as tab=, without a history entry', async () => {
    const setUrl = vi.fn()
    const url: UrlState = { demo: true, view: 'context', area: 'work', tab: 'crews' }
    await render(
      createElement(
        UrlContext.Provider,
        { value: [url, setUrl] },
        createElement(Tabs, { tabs: TABS, label: 'Work', syncUrl: true }),
      ),
    )
    expect(panel().textContent).toBe('The crews')
    tab('Crews').focus()
    await key('ArrowRight')
    expect(setUrl).toHaveBeenCalledWith({ tab: 'time' }, true)
  })

  it('opens the first tab for a tab= it does not have', async () => {
    const url: UrlState = { demo: false, view: 'context', tab: 'nowhere' }
    await render(
      createElement(
        UrlContext.Provider,
        { value: [url, vi.fn()] },
        createElement(Tabs, { tabs: TABS, label: 'Work', syncUrl: true }),
      ),
    )
    expect(panel().textContent).toBe('The board')
  })

  it("keeps an object page's tab as otab=, beside the area's tab=", async () => {
    const setUrl = vi.fn()
    const url: UrlState = { demo: false, view: 'context', area: 'work', tab: 'crews', otab: 'time' }
    await render(
      createElement(
        UrlContext.Provider,
        { value: [url, setUrl] },
        createElement(Tabs, { tabs: TABS, label: 'Task', syncUrl: true, urlKey: 'otab' }),
      ),
    )
    expect(panel().textContent).toBe('The time')
    await act(async () => tab('Board').click())
    expect(setUrl).toHaveBeenCalledWith({ otab: 'board' }, true)
  })

  it('keeps every panel on the page with keepMounted, the others hidden', async () => {
    await render(createElement(Tabs, { tabs: TABS, label: 'Task', keepMounted: true }))
    const panels = [...document.querySelectorAll<HTMLElement>('[role="tabpanel"]')]
    // The disabled tab's panel is not on the page.
    expect(panels.map((p) => [p.textContent, p.hidden])).toEqual([
      ['The board', false],
      ['The crews', true],
      ['The time', true],
    ])
    expect(tab('Crews').getAttribute('aria-controls')).toBe(panels[1].id)
    await act(async () => tab('Crews').click())
    expect(panels[0].hidden).toBe(true)
    expect(panels[1].hidden).toBe(false)
  })
})

describe('Menu', () => {
  const items = (onSelect = vi.fn()) => [
    { id: 'webshop', label: 'Webshop', checked: true, onSelect },
    { id: 'lens', label: 'Lens', checked: false, onSelect },
    { id: 'blog', label: 'Blog', checked: false, onSelect },
  ]
  const menuItem = (name: string) =>
    [...document.querySelectorAll<HTMLElement>('[role="menuitemradio"]')].find((m) => m.textContent === name)!

  it('opens on the chosen item, moves with the arrow keys and closes on Esc back to its button', async () => {
    await render(createElement(Menu, { button: 'Project', label: 'Projects', items: items() }))
    const open = button('Project')
    expect(open.getAttribute('aria-haspopup')).toBe('menu')
    await act(async () => open.click())
    expect(open.getAttribute('aria-expanded')).toBe('true')
    expect(document.activeElement).toBe(menuItem('Webshop'))
    await key('ArrowDown')
    expect(document.activeElement).toBe(menuItem('Lens'))
    await key('End')
    expect(document.activeElement).toBe(menuItem('Blog'))
    await key('ArrowDown')
    expect(document.activeElement).toBe(menuItem('Webshop'))
    await key('Escape')
    expect(document.querySelector('[role="menu"]')).toBeNull()
    expect(document.activeElement).toBe(open)
  })

  it('picks an item and closes', async () => {
    const onSelect = vi.fn()
    await render(createElement(Menu, { button: 'Project', label: 'Projects', items: items(onSelect) }))
    await act(async () => button('Project').click())
    await act(async () => menuItem('Lens').click())
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(document.querySelector('[role="menu"]')).toBeNull()
  })

  it('opens from the keyboard on ArrowUp at the last item', async () => {
    await render(createElement(Menu, { button: 'Project', label: 'Projects', items: items() }))
    button('Project').focus()
    await key('ArrowUp')
    expect(document.activeElement).toBe(menuItem('Blog'))
  })
})
