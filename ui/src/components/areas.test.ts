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
import type { FactoryHealth, HealthSample } from '../k8s/health'
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
  it('lays out the health cards, the nodes, the last hour and the agent slots as a dashboard', async () => {
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

  const GiB = 2 ** 30
  const renderHealth = (health: FactoryHealth) =>
    render(
      createElement(FactoryArea, {
        data: demoFactory(NOW),
        connection: live,
        health,
        healthLoaded: true,
        healthProblems: [],
        onSelectTask: () => {},
      }),
    )
  /** The last hour's charts by title, each with its summary and legend as text and its lines' paths by class. */
  const chartsOf = (host: HTMLElement) =>
    Object.fromEntries(
      [...host.querySelectorAll('.dash-charts .spark')].map((chart) => [
        chart.querySelector('figcaption span:first-child')!.textContent,
        {
          text: chart.textContent,
          lines: Object.fromEntries(
            [...chart.querySelectorAll('path.spark-line')].map((path) => [
              path.getAttribute('class')!.replace('spark-line ', ''),
              path.getAttribute('d'),
            ]),
          ),
        },
      ]),
    )
  const worker = (name: string, cpuAllocatableMilli: number, memoryAllocatableBytes: number) => ({
    name,
    ready: true,
    pressure: [],
    cpuAllocatableMilli,
    memoryAllocatableBytes,
  })
  const sampleAt = (minutesAgo: number, figures: Partial<HealthSample>): HealthSample => ({
    at: new Date(NOW - minutesAgo * 60_000).toISOString(),
    apiMs: 5,
    ...figures,
  })

  it("says what the latest sample's nodes could allocate together, and lists each node of now (#122)", async () => {
    const health = demoHealth(NOW)
    const first = health.nodes[0]
    health.nodes = [
      { ...first, name: 'worker-a', cpuAllocatableMilli: 4000, memoryAllocatableBytes: 8 * GiB },
      { ...first, name: 'worker-b', cpuAllocatableMilli: 12000, memoryAllocatableBytes: 24 * GiB },
    ]
    // What the server sampled of those two: each sample carries what they could allocate together.
    health.history = health.history!.map((s) => ({
      ...s,
      cpuAllocatableMilli: 16000,
      memoryAllocatableBytes: 32 * GiB,
    }))
    const { host } = await renderHealth(health)
    const charts = chartsOf(host)
    expect(charts.CPU.text).toContain('of 16.0 cores')
    expect(charts.CPU.text).toContain('nodes')
    expect(charts.Memory.text).toContain('of 32.0 GiB')
    expect(host.querySelector('#dash-node')!.textContent).toBe('Nodes')
    expect(host.querySelectorAll('.dash-node .node-usage').length).toBe(2)
  })

  // A point is the share it was when it was sampled: 7 of one node's 8 cores is
  // 88% (y 7.0 of the chart's 44), and stays there when a second node of 8
  // joins, where the nodes of now would draw it at 44% (y 24.5). A node that
  // left does the reverse: 8 of 16 cores is half (y 22.0), not the one
  // remaining node's ceiling (y 2.0).
  it.each([
    [
      'a node that joined',
      [worker('worker-a', 8000, 16 * GiB), worker('worker-b', 8000, 16 * GiB)],
      [
        sampleAt(30, {
          cpuMilli: 7000,
          cpuAllocatableMilli: 8000,
          memoryBytes: 14 * GiB,
          memoryAllocatableBytes: 16 * GiB,
        }),
        sampleAt(15, {
          cpuMilli: 7000,
          cpuAllocatableMilli: 8000,
          memoryBytes: 14 * GiB,
          memoryAllocatableBytes: 16 * GiB,
        }),
        sampleAt(0, {
          cpuMilli: 8000,
          cpuAllocatableMilli: 16000,
          memoryBytes: 16 * GiB,
          memoryAllocatableBytes: 32 * GiB,
        }),
      ],
      'M150.0,7.0L225.0,7.0L300.0,22.0',
      'now 8.0 cores of 16.0 cores',
    ],
    [
      'a node that left',
      [worker('worker-a', 8000, 16 * GiB)],
      [
        sampleAt(30, {
          cpuMilli: 8000,
          cpuAllocatableMilli: 16000,
          memoryBytes: 16 * GiB,
          memoryAllocatableBytes: 32 * GiB,
        }),
        sampleAt(15, {
          cpuMilli: 8000,
          cpuAllocatableMilli: 16000,
          memoryBytes: 16 * GiB,
          memoryAllocatableBytes: 32 * GiB,
        }),
        sampleAt(0, {
          cpuMilli: 7000,
          cpuAllocatableMilli: 8000,
          memoryBytes: 14 * GiB,
          memoryAllocatableBytes: 16 * GiB,
        }),
      ],
      'M150.0,22.0L225.0,22.0L300.0,7.0',
      'now 7.0 cores of 8.0 cores',
    ],
  ])(
    'leaves the earlier points of the last hour where they were after %s (#141)',
    async (_, nodes, history, line, now) => {
      const { host } = await renderHealth({ ...demoHealth(NOW), nodes, history })
      const charts = chartsOf(host)
      expect(charts.CPU.lines['spark-node']).toBe(line)
      expect(charts.Memory.lines['spark-node']).toBe(line)
      expect(charts.CPU.text).toContain(now)
    },
  )

  // CI's and the agents' CPU are drawn against the same sample's allocatable as the nodes'.
  it("draws CI's and the agents' CPU against what the nodes could allocate when sampled (#141)", async () => {
    const history = [
      sampleAt(30, { cpuMilli: 7000, cpuAllocatableMilli: 8000, ciCpuMilli: 4000, agentCpuMilli: 2000 }),
      sampleAt(0, { cpuMilli: 8000, cpuAllocatableMilli: 16000, ciCpuMilli: 4000 }),
    ]
    const { host } = await renderHealth({ ...demoHealth(NOW), history })
    const { lines } = chartsOf(host).CPU
    expect(lines['spark-ci']).toBe('M150.0,22.0L300.0,32.0')
    expect(lines['spark-agents']).toBe('M150.0,32.0L300.0,42.0')
  })

  it('shows a full node among idle ones as full, and names it (#141)', async () => {
    // Three nodes of 20 cores and 40 GiB together: worker-b used 95% of its own
    // 4 cores, worker-c 88% of its own memory, and the sums are a quarter and
    // less than half.
    const quiet = {
      cpuMilli: 1200,
      cpuAllocatableMilli: 20000,
      cpuFullest: { node: 'worker-a', share: 0.05 },
      memoryBytes: 6 * GiB,
      memoryAllocatableBytes: 40 * GiB,
      memoryFullest: { node: 'worker-a', share: 0.25 },
    }
    const history = [
      sampleAt(30, quiet),
      sampleAt(20, {
        cpuMilli: 4600,
        cpuAllocatableMilli: 20000,
        cpuFullest: { node: 'worker-b', share: 0.95 },
        memoryBytes: 17 * GiB,
        memoryAllocatableBytes: 40 * GiB,
        memoryFullest: { node: 'worker-c', share: 0.875 },
      }),
      sampleAt(0, quiet),
    ]
    const { host } = await renderHealth({ ...demoHealth(NOW), history })
    const charts = chartsOf(host)
    // The nodes together never pass a quarter (y 32.8); the fullest node's line reaches 95% (y 4.0).
    expect(charts.CPU.lines['spark-node']).toBe('M150.0,39.6L200.0,32.8L300.0,39.6')
    expect(charts.CPU.lines['spark-fullest']).toBe('M150.0,40.0L200.0,4.0L300.0,40.0')
    expect(charts.CPU.text).toContain('now 1.2 cores of 20.0 cores · peak worker-b 95%')
    expect(charts.CPU.text).toContain('fullest node')
    expect(charts.Memory.lines['spark-fullest']).toBe('M150.0,32.0L200.0,7.0L300.0,32.0')
    expect(charts.Memory.text).toContain('now 6.0 GiB of 40.0 GiB · peak worker-c 88%')
    expect(charts.Memory.text).toContain('fullest node')
  })

  // One rule for a tie, within a sample (the server's) and across the hour:
  // the first by name. Neither the earliest of the equal peaks (worker-b's
  // here) nor the latest (worker-b's again) decides.
  it('names the first by name of the nodes that peaked equally within the hour (#141)', async () => {
    const at = (node: string, share: number) => ({
      cpuMilli: 2000,
      cpuAllocatableMilli: 20000,
      cpuFullest: { node, share },
      memoryBytes: 6 * GiB,
      memoryAllocatableBytes: 40 * GiB,
      memoryFullest: { node, share },
    })
    const history = [
      sampleAt(30, at('worker-b', 0.9)),
      sampleAt(20, at('worker-a', 0.9)),
      sampleAt(10, at('worker-c', 0.5)),
      sampleAt(0, at('worker-b', 0.9)),
    ]
    const { host } = await renderHealth({ ...demoHealth(NOW), history })
    const charts = chartsOf(host)
    expect(charts.CPU.text).toContain('now 2.0 cores of 20.0 cores · peak worker-a 90%')
    expect(charts.Memory.text).toContain('now 6.0 GiB of 40.0 GiB · peak worker-a 90%')
  })

  it('draws one line on one node, where the fullest node is the nodes together (#141)', async () => {
    const { host } = await renderHealth(demoHealth(NOW))
    const charts = chartsOf(host)
    expect(Object.keys(charts.CPU.lines)).toEqual(['spark-node', 'spark-ci', 'spark-agents'])
    expect(Object.keys(charts.Memory.lines)).toEqual(['spark-node'])
    expect(charts.CPU.text).not.toContain('fullest')
    expect(charts.CPU.text).not.toContain('peak')
    expect(charts.Memory.text).not.toContain('fullest')
    expect(charts.Memory.text).not.toContain('peak')
  })

  it('draws one line on one node whose share the server rounded to the next percent (#141)', async () => {
    // 2499m of 20 cores is 12.495%, 12 to the summary, and its share rounded to
    // four decimals is 0.125, 13 to the summary: the same line, not a second one.
    const one = { cpuMilli: 2499, cpuAllocatableMilli: 20000, cpuFullest: { node: 'worker-a', share: 0.125 } }
    const { host } = await renderHealth({ ...demoHealth(NOW), history: [sampleAt(30, one), sampleAt(0, one)] })
    const { CPU } = chartsOf(host)
    expect(Object.keys(CPU.lines)).toEqual(['spark-node', 'spark-agents'])
    expect(CPU.text).not.toContain('fullest')
    expect(CPU.text).not.toContain('peak')
  })

  // Nothing to divide by: a chart of shares is left out when no sample of the
  // hour carries what its nodes could allocate, so no NaN reaches the page
  // (#122), and no point is drawn against the nodes of now instead (#141).
  it.each([
    [
      'no node had figures',
      (health: FactoryHealth) => {
        health.nodes = []
        health.history = health.history!.map(({ at, apiMs }) => ({ at, apiMs }))
      },
    ],
    [
      'the samples carry nothing the nodes could allocate (an older server sampled)',
      (health: FactoryHealth) => {
        health.history = health.history!.map(({ at, apiMs, cpuMilli, memoryBytes, ciCpuMilli, agentCpuMilli }) => ({
          at,
          apiMs,
          cpuMilli,
          memoryBytes,
          ciCpuMilli,
          agentCpuMilli,
        }))
      },
    ],
  ])('hides the last hour of CPU and memory when %s, and renders no NaN (#122, #141)', async (_, change) => {
    const health = demoHealth(NOW)
    change(health)
    const { host } = await renderHealth(health)
    expect(Object.keys(chartsOf(host))).toEqual(['API server response'])
    expect(host.innerHTML).not.toContain('NaN')
  })

  it('breaks the line at a sample that carries no allocatable, and draws the rest (#141)', async () => {
    const full = { cpuMilli: 4000, cpuAllocatableMilli: 8000, memoryBytes: 8 * GiB, memoryAllocatableBytes: 16 * GiB }
    const history = [
      sampleAt(45, full),
      sampleAt(30, full),
      sampleAt(20, { cpuMilli: 4000, memoryBytes: 8 * GiB }),
      sampleAt(10, full),
      sampleAt(0, full),
    ]
    const { host } = await renderHealth({ ...demoHealth(NOW), history })
    expect(chartsOf(host).CPU.lines['spark-node']).toBe('M75.0,22.0L150.0,22.0M250.0,22.0L300.0,22.0')
    expect(host.innerHTML).not.toContain('NaN')
  })
})
