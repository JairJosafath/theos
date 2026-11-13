// @vitest-environment happy-dom

// The areas' layouts (docs/plans/done/ui-shell.md, step 2): Work's Needs a look
// strip and its tabs, the Loop's ring that selects a stage, Needs you's filter
// chips and folding groups, the Story's side panel that folds, and the Factory
// area's dashboard. Each remembers what it should, where it should: the tab or
// stage in the address, a fold in this browser.

import { act, createElement, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { demoFactory, demoHealth } from '../demo/factoryFixture'
import { demoStory } from '../demo/fixture'
import type { ContainerWriter } from '../k8s/containers'
import type { Connection } from '../k8s/sources'
import { FixedNow, UrlContext, type SetUrl, type UrlState } from '../lib/hooks'
import { resetPrefsForTest, STORED_KEYS, useStoredFlag } from '../lib/prefs'
import { needs } from '../model/needs'
import { FactoryPage } from './Factory'
import { LoopArea } from './Loop'
import { NeedsArea } from './Needs'
import { PanelToggle, StoryWorkspace } from './Panels'
import { FactoryArea } from './Shell'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

const NOW = Date.parse('2026-06-01T12:00:00Z')
const live: Connection = { phase: 'live', loaded: true, problems: [] }

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

/** Renders `node` with the clock stopped and the address `url`, whose setter is returned. */
async function render(node: ReactNode, url: Partial<UrlState> = {}) {
  const setUrl = vi.fn<SetUrl>()
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  const state: UrlState = { demo: true, view: 'context', ...url }
  await act(async () =>
    root!.render(
      createElement(
        FixedNow.Provider,
        { value: NOW },
        createElement(UrlContext.Provider, { value: [state, setUrl] }, node),
      ),
    ),
  )
  return { host, setUrl }
}

const byText = <T extends HTMLElement>(selector: string, text: string | RegExp) =>
  [...document.querySelectorAll<T>(selector)].find((e) =>
    typeof text === 'string' ? e.textContent === text : text.test(e.textContent ?? ''),
  )!

const tabNames = () =>
  [...document.querySelectorAll('[role="tab"]')].map((t) =>
    t.getAttribute('aria-selected') === 'true' ? `[${t.textContent}]` : t.textContent,
  )

describe('Work', () => {
  const work = () =>
    createElement(FactoryPage, {
      data: demoFactory(NOW),
      connection: live,
      health: demoHealth(NOW),
      healthLoaded: true,
      onSelectTask: () => {},
      pageHref: () => undefined,
    })

  it('keeps the headline numbers above a one-line Needs a look strip, then its four tabs', async () => {
    const { host } = await render(work())
    expect(host.querySelector('.stats')?.textContent).toContain('Active tasks')
    const strip = host.querySelector<HTMLElement>('[aria-label="Needs a look"]')!
    const toggle = strip.querySelector('button')!
    expect(toggle.textContent).toMatch(/^Needs a look\d+.+ and \d+ moreShow all$/)
    expect(host.querySelector('.stats')!.compareDocumentPosition(strip)).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
    expect(tabNames().map((t) => t?.replace(/\d+/, ''))).toEqual(['[Board]', 'Crews', 'Releases', 'Time & tokens'])
  })

  it('opens the strip to every item, and folds it again', async () => {
    const { host } = await render(work())
    const toggle = host.querySelector<HTMLButtonElement>('.attention-toggle')!
    const list = host.querySelector<HTMLElement>('.attention-list')!
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(list.hidden).toBe(true)
    expect(toggle.getAttribute('aria-controls')).toBe(list.id)
    await act(async () => toggle.click())
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(list.hidden).toBe(false)
    expect(list.querySelectorAll('li').length).toBeGreaterThan(1)
  })

  it('opens the tab the address names and keeps a new one there, without a history entry', async () => {
    const { host, setUrl } = await render(work(), { area: 'work', tab: 'crews' })
    expect(tabNames()[1]).toMatch(/^\[Crews/)
    expect(host.querySelector('[role="tabpanel"]')!.textContent).toContain('Crews')
    await act(async () => byText<HTMLButtonElement>('[role="tab"]', 'Time & tokens').click())
    expect(setUrl).toHaveBeenCalledWith({ tab: 'time' }, true)
  })
})

describe('Loop', () => {
  const loop = () =>
    createElement(LoopArea, { data: demoStory(NOW), title: 'Webshop', loaded: true, problems: [], setUrl: () => {} })
  const current = () => [...document.querySelectorAll('.loop-node[aria-current="true"]')]

  it('opens on the first review point with items, its node marked and its items beside the ring', async () => {
    const { host } = await render(loop())
    expect(current().map((n) => n.getAttribute('aria-label'))).toEqual(['4. Reviewing the spec: 1'])
    expect(host.querySelector('#loop-stage')!.getAttribute('aria-label')).toBe('4. Reviewing the spec')
    expect(byText('.loop-chips .chip', /^4\. Spec review/).getAttribute('aria-pressed')).toBe('true')
    // One stage at a time: no list of every stage to scroll through.
    expect(host.querySelectorAll('.loop-stage')).toHaveLength(1)
  })

  it('selects a stage from its node, in the address without a history entry', async () => {
    const { setUrl } = await render(loop())
    const node = document.querySelector<HTMLElement>('.loop-node[aria-label^="10. Monitor"]')!
    await act(async () => node.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })))
    expect(setUrl).toHaveBeenCalledWith({ tab: 'monitor' }, true)
  })

  it('shows the stage the address names, and selects one from its chip', async () => {
    const { host, setUrl } = await render(loop(), { area: 'loop', tab: 'ship' })
    expect(current().map((n) => n.getAttribute('aria-label'))).toEqual(['9. Ship: 1'])
    expect(host.querySelector('#loop-stage')!.textContent).toContain('Ship')
    await act(async () => byText<HTMLButtonElement>('.loop-chips .chip', /^1\. Idea/).click())
    expect(setUrl).toHaveBeenCalledWith({ tab: 'idea' }, true)
  })
})

describe('Needs you', () => {
  const writer = {} as ContainerWriter
  const story = demoStory(NOW)
  const items = needs({ ...story, projects: story.project ? [story.project] : [] })
  const area = () =>
    createElement(NeedsArea, {
      items,
      projects: story.project ? [story.project] : [],
      loaded: true,
      problems: [],
      setUrl: () => {},
      writer,
    })
  const shown = () => [...document.querySelectorAll('.need')].map((n) => n.getAttribute('aria-label'))

  it('has a chip per kind with its count, after All', async () => {
    await render(area())
    const chips = [...document.querySelectorAll('.chips .chip')].map(
      (c) => `${c.textContent}${c.getAttribute('aria-pressed') === 'true' ? '*' : ''}`,
    )
    expect(chips[0]).toBe(`All${items.length}*`)
    expect(chips.slice(1).map((c) => c.replace(/\d+$/, ''))).toEqual(
      [...new Set(items.map((i) => i.kind))].sort((a, b) => order(a) - order(b)),
    )
  })

  it('shows only the items of the chip pressed, and all again from All', async () => {
    await render(area())
    await act(async () => byText<HTMLButtonElement>('.chips .chip', /^Container/).click())
    expect(shown().every((s) => s?.startsWith('Container '))).toBe(true)
    expect(shown().length).toBe(items.filter((i) => i.kind === 'Container').length)
    expect(byText('.chips .chip', /^Container/).getAttribute('aria-pressed')).toBe('true')
    await act(async () => byText<HTMLButtonElement>('.chips .chip', /^All/).click())
    expect(shown()).toHaveLength(items.length)
  })

  it('folds a project’s group and remembers it in this browser', async () => {
    await render(area())
    const fold = document.querySelector<HTMLButtonElement>('.needs-project .fold-button')!
    expect(fold.getAttribute('aria-expanded')).toBe('true')
    await act(async () => fold.click())
    expect(fold.getAttribute('aria-expanded')).toBe('false')
    expect(document.querySelector<HTMLElement>('.needs-list')!.hidden).toBe(true)
    expect(JSON.parse(localStorage.getItem(STORED_KEYS.needsFolded)!)).toEqual(['webshop'])
    // Back on the page, it is still folded.
    await act(async () => root!.unmount())
    await render(area())
    expect(document.querySelector('.needs-project .fold-button')!.getAttribute('aria-expanded')).toBe('false')
  })

  it('opens folded groups when storage refuses, and keeps the fold for the visit', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    try {
      await render(area())
      const fold = document.querySelector<HTMLButtonElement>('.needs-project .fold-button')!
      expect(fold.getAttribute('aria-expanded')).toBe('true')
      await act(async () => fold.click())
      expect(fold.getAttribute('aria-expanded')).toBe('false')
    } finally {
      vi.restoreAllMocks()
    }
  })
})

function order(kind: string) {
  return ['Brief', 'Container', 'Task', 'Crew'].indexOf(kind)
}

describe('Story', () => {
  /** The Story's workspace as App wires it: the toggle and the panel share the remembered flag. */
  function Story() {
    const [open, setOpen] = useStoredFlag(STORED_KEYS.storyPanel, true)
    return createElement(
      'div',
      null,
      createElement(PanelToggle, { open, onToggle: setOpen }),
      createElement(StoryWorkspace, {
        open,
        stage: createElement('section', { className: 'stage' }, 'The diagram'),
        side: createElement('p', null, 'Who’s working'),
      }),
    )
  }

  it('folds the side panel so the diagram takes the width, and remembers it', async () => {
    const { host } = await render(createElement(Story))
    const toggle = byText<HTMLButtonElement>('button', 'Hide the panel')
    const panel = host.querySelector<HTMLElement>('#story-panel')!
    expect(toggle.getAttribute('aria-controls')).toBe('story-panel')
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(panel.hidden).toBe(false)
    await act(async () => toggle.click())
    expect(toggle.textContent).toBe('Show the panel')
    expect(panel.hidden).toBe(true)
    expect(host.querySelector('.workspace')!.className).toContain('panel-folded')
    expect(localStorage.getItem(STORED_KEYS.storyPanel)).toBe('false')
    await act(async () => root!.unmount())
    await render(createElement(Story))
    expect(document.querySelector<HTMLElement>('#story-panel')!.hidden).toBe(true)
  })
})

describe('Factory', () => {
  it('lays out the health cards, the node, the last hour and the agent slots as a dashboard', async () => {
    const { host } = await render(
      createElement(FactoryArea, {
        data: demoFactory(NOW),
        connection: live,
        health: demoHealth(NOW),
        healthLoaded: true,
        healthProblems: [],
        onSelectTask: () => {},
      }),
    )
    const dash = host.querySelector('.dash')!
    expect([...dash.children].map((c) => c.className)).toEqual([
      'panel dash-parts',
      'panel dash-node',
      'panel dash-charts',
      'dash-slots',
    ])
    expect(dash.querySelectorAll('.dash-parts .component').length).toBe(demoHealth(NOW).components.length)
    expect(dash.querySelector('.dash-slots')!.textContent).toContain('Agent slots')
  })

  it('draws the last hour against every node together, not the first one listed (#122)', async () => {
    const health = demoHealth(NOW)
    const first = health.nodes[0]
    health.nodes = [
      { ...first, name: 'worker-a', cpuAllocatableMilli: 4000, memoryAllocatableBytes: 8 * 2 ** 30 },
      { ...first, name: 'worker-b', cpuAllocatableMilli: 12000, memoryAllocatableBytes: 24 * 2 ** 30 },
    ]
    const { host } = await render(
      createElement(FactoryArea, {
        data: demoFactory(NOW),
        connection: live,
        health,
        healthLoaded: true,
        healthProblems: [],
        onSelectTask: () => {},
      }),
    )
    const charts = [...host.querySelectorAll('.dash-charts .spark')].map((c) => c.textContent)
    expect(charts[1]).toContain('of 16.0 cores')
    expect(charts[1]).toContain('nodes')
    expect(charts[2]).toContain('of 32.0 GiB')
    expect(host.querySelector('#dash-node')!.textContent).toBe('Nodes')
    expect(host.querySelectorAll('.dash-node .node-usage').length).toBe(2)
  })
})
