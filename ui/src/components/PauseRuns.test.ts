// @vitest-environment happy-dom

// Pause runs and Resume on a Brief's page and a Crew's page (#1995): each
// button patches spec.pauseRuns, pausing with spec.paused as the CRD needs, and
// shows its state from spec.pauseRuns. A Resume of a Brief or Crew whose runs
// are paused unsets pauseRuns too, so the API takes it.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LiveBriefWriter } from '../k8s/briefs'
import { LiveCrewWriter } from '../k8s/crews'
import type { Brief, Crew } from '../k8s/types'
import { BriefPage } from './objects/BriefPage'
import { CrewPauseButton, CrewPauseRunsButton } from './objects/CrewActions'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

let root: Root | undefined
let host: HTMLElement | undefined

afterEach(async () => {
  await act(async () => root?.unmount())
  host?.remove()
})

function ok(): Response {
  return new Response('{}', { status: 200, headers: { 'content-type': 'application/json' } })
}

/** Renders `node`, clicks the button named `label`, and resolves to the JSON patch it sent. */
async function click(node: ReturnType<typeof createElement>, label: string, fetchImpl: ReturnType<typeof vi.fn>) {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root!.render(node))
  const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find((b) => b.textContent === label)
  expect(button, `a button "${label}"`).toBeDefined()
  await act(async () => button!.click())
  expect(fetchImpl).toHaveBeenCalledTimes(1)
  const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
  return { url, ops: JSON.parse(init.body as string) as unknown }
}

const brief = (spec: Partial<Brief['spec']> = {}): Brief => ({
  metadata: { name: 'lens', namespace: 'webshop' },
  spec: { idea: 'A CEL lens', ...spec },
})

// The buttons were on the Brief's card; the card now opens the page that has them (docs/plans/done/ui-shell.md).
function briefCard(b: Brief, fetchImpl: ReturnType<typeof vi.fn>) {
  return createElement(BriefPage, {
    brief: b,
    agents: [],
    writer: new LiveBriefWriter('k8s', fetchImpl as unknown as typeof fetch),
  })
}

describe("a Brief card's Pause runs and Resume", () => {
  it('Pause runs patches pauseRuns with paused', async () => {
    const fetchImpl = vi.fn(async () => ok())
    const { url, ops } = await click(briefCard(brief(), fetchImpl), 'Pause runs', fetchImpl)
    expect(url).toBe('k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/briefs/lens')
    expect(ops).toEqual([
      { op: 'add', path: '/spec/paused', value: true },
      { op: 'add', path: '/spec/pauseRuns', value: true },
    ])
  })

  it('Resume runs, shown while spec.pauseRuns is set, unsets only pauseRuns', async () => {
    const fetchImpl = vi.fn(async () => ok())
    const { ops } = await click(
      briefCard(brief({ paused: true, pauseRuns: true }), fetchImpl),
      'Resume runs',
      fetchImpl,
    )
    expect(ops).toEqual([{ op: 'add', path: '/spec/pauseRuns', value: false }])
  })

  it('Resume of a Brief whose runs are paused unsets pauseRuns too', async () => {
    const fetchImpl = vi.fn(async () => ok())
    const { ops } = await click(briefCard(brief({ paused: true, pauseRuns: true }), fetchImpl), 'Resume', fetchImpl)
    expect(ops).toEqual([
      { op: 'add', path: '/spec/paused', value: false },
      { op: 'add', path: '/spec/pauseRuns', value: false },
    ])
  })

  it('Resume of a Brief only drained patches paused alone, as before', async () => {
    const fetchImpl = vi.fn(async () => ok())
    const { ops } = await click(briefCard(brief({ paused: true }), fetchImpl), 'Resume', fetchImpl)
    expect(ops).toEqual([{ op: 'add', path: '/spec/paused', value: false }])
  })
})

const crew = (spec: Partial<Crew['spec']> = {}): Crew => ({
  metadata: { name: 'orders-api', namespace: 'webshop' },
  spec: { containerRef: { name: 'orders-api' }, implementer: { name: 'i' }, reviewer: { name: 'r' }, ...spec },
})

function crewButtons(c: Crew, fetchImpl: ReturnType<typeof vi.fn>) {
  const writer = new LiveCrewWriter('k8s', fetchImpl as unknown as typeof fetch)
  return createElement('div', null, [
    createElement(CrewPauseRunsButton, { key: 'runs', crew: c, writer }),
    createElement(CrewPauseButton, { key: 'crew', crew: c, writer }),
  ])
}

describe("a Crew's Pause runs and Resume", () => {
  it('Pause runs patches pauseRuns with paused', async () => {
    const fetchImpl = vi.fn(async () => ok())
    const { url, ops } = await click(crewButtons(crew(), fetchImpl), 'Pause runs', fetchImpl)
    expect(url).toBe('k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/crews/orders-api')
    expect(ops).toEqual([
      { op: 'add', path: '/spec/paused', value: true },
      { op: 'add', path: '/spec/pauseRuns', value: true },
    ])
  })

  it('Resume runs, shown while spec.pauseRuns is set, unsets only pauseRuns', async () => {
    const fetchImpl = vi.fn(async () => ok())
    const { ops } = await click(
      crewButtons(crew({ paused: true, pauseRuns: true }), fetchImpl),
      'Resume runs',
      fetchImpl,
    )
    expect(ops).toEqual([{ op: 'add', path: '/spec/pauseRuns', value: false }])
  })

  it('Resume crew of a Crew whose runs are paused unsets pauseRuns too', async () => {
    const fetchImpl = vi.fn(async () => ok())
    const { ops } = await click(
      crewButtons(crew({ paused: true, pauseRuns: true }), fetchImpl),
      'Resume crew',
      fetchImpl,
    )
    expect(ops).toEqual([
      { op: 'add', path: '/spec/paused', value: false },
      { op: 'add', path: '/spec/pauseRuns', value: false },
    ])
  })
})
