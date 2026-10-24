// @vitest-environment happy-dom

// A Brief's releases are sent as the whole list, so each change names the
// Brief it was worked out from: one changed meanwhile (another person, or
// kubectl) refuses it, and the page says so rather than overwrite the change.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LiveBriefWriter } from '../../k8s/briefs'
import type { UiSession } from '../../k8s/session'
import type { Brief } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { BriefSettings } from './BriefSettings'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

const ACTING: UiSession = { actions: true, mode: 'service-account', user: null }
const cli = { container: 'cli', github: { repository: 'me/cli', tokenSecretRef: { name: 'gh' } } }
const brief: Brief = {
  apiVersion: 'c4.factory.io/v1beta1',
  kind: 'Brief',
  metadata: { name: 'tools', namespace: 'webshop', resourceVersion: '5' },
  spec: { idea: 'Tools', releases: [cli] },
}

/** The UI server's answer to a releases write worked out from a Brief that has changed since. */
const CONFLICT =
  'Brief webshop/tools changed since this page showed it, so nothing was saved: check its settings as they are now and make the change again'
function answer(code: number): Response {
  const body =
    code === 409
      ? { kind: 'Status', status: 'Failure', code, reason: 'Conflict', message: CONFLICT }
      : { ...brief, spec: { ...brief.spec, releases: [] } }
  return new Response(JSON.stringify(body), { status: code, headers: { 'content-type': 'application/json' } })
}

let root: Root | undefined
let host: HTMLElement | undefined

afterEach(async () => {
  await act(async () => root?.unmount())
  host?.remove()
})

/** Renders the settings, clicks Remove on the cli release, and resolves to the page and the patch it sent. */
async function removeCli(code: number) {
  const fetchImpl = vi.fn(async () => answer(code))
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () =>
    root!.render(
      createElement(
        SessionContext.Provider,
        { value: ACTING },
        createElement(BriefSettings, { brief, writer: new LiveBriefWriter('k8s', fetchImpl) }),
      ),
    ),
  )
  const remove = host.querySelector<HTMLButtonElement>('button[aria-label="Stop publishing cli"]')!
  await act(async () => remove.click())
  expect(fetchImpl).toHaveBeenCalledTimes(1)
  const [, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
  return { page: host, ops: JSON.parse(init.body as string) as unknown[] }
}

describe("a Brief's releases", () => {
  it('are sent with a test of the resourceVersion the page shows', async () => {
    const { page, ops } = await removeCli(200)
    expect(ops).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '5' },
      { op: 'add', path: '/spec/releases', value: [] },
    ])
    expect(page.querySelector('[role="alert"]')).toBeNull()
  })

  it('refused because the Brief changed meanwhile, say so on the page', async () => {
    const { page } = await removeCli(409)
    expect(page.querySelector('[role="alert"]')?.textContent).toBe(CONFLICT)
    // Nothing changed here: the list still shows what the Brief had.
    expect(page.querySelector('button[aria-label="Stop publishing cli"]')).not.toBeNull()
  })
})

/** Types `value` into the add form's input labelled `label`, as a person does. */
function type(page: HTMLElement, label: string, value: string) {
  const form = page.querySelector('form[aria-label="Publish an app\'s releases"]')!
  const id = [...form.querySelectorAll('label')].find((l) => l.textContent?.startsWith(label))!.htmlFor
  const input = form.querySelector<HTMLInputElement>(`[id="${id}"]`)!
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

/** Renders a Brief that made the Container engine, adds its release with `fields` typed in, and resolves to the page and the releases sent. */
async function addEngine(fields: [string, string][]) {
  const fetchImpl = vi.fn(async () => answer(200))
  const made: Brief = { ...brief, status: { created: [{ kind: 'Container', name: 'engine' }] } }
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () =>
    root!.render(
      createElement(
        SessionContext.Provider,
        { value: ACTING },
        createElement(BriefSettings, { brief: made, writer: new LiveBriefWriter('k8s', fetchImpl) }),
      ),
    ),
  )
  for (const [label, value] of fields) await act(async () => type(host!, label, value))
  const page = host
  const hint = () => page.querySelector('form[aria-label="Publish an app\'s releases"]')!.textContent ?? ''
  const shown = hint()
  const add = [...host.querySelectorAll('button')].find((b) => b.textContent === 'Publish its releases')!
  await act(async () => add.click())
  expect(fetchImpl).toHaveBeenCalledTimes(1)
  const [, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
  const ops = JSON.parse(init.body as string) as { path: string; value: unknown }[]
  return { shown, releases: ops.find((o) => o.path === '/spec/releases')!.value as unknown[] }
}

describe("a Brief's release form", () => {
  const github = { repository: 'me/tools', tokenSecretRef: { name: 'gh' } }
  const base: [string, string][] = [
    ['GitHub repository', 'me/tools'],
    ['Token Secret', 'gh'],
  ]

  it('sends no source when the folder is empty', async () => {
    const { releases } = await addEngine(base)
    expect(releases).toEqual([cli, { container: 'engine', github }])
  })

  it('sends the source folder, showing the tag it defaults to', async () => {
    const { shown, releases } = await addEngine([...base, ['Source folder', 'engine']])
    expect(shown).toContain('The source is tagged engine/vX.Y.Z.')
    expect(releases).toEqual([cli, { container: 'engine', github, source: { path: 'engine' } }])
  })

  it('sends a source tag prefix typed in, and tags with it', async () => {
    const { shown, releases } = await addEngine([...base, ['Source folder', 'engine'], ['Source tag prefix', 'core/']])
    expect(shown).toContain('The source is tagged core/vX.Y.Z.')
    expect(releases).toEqual([cli, { container: 'engine', github, source: { path: 'engine', tagPrefix: 'core/' } }])
  })

  it('lists an entry’s source folder and the tag it makes', () => {
    const withSource: Brief = { ...brief, spec: { ...brief.spec, releases: [{ ...cli, source: { path: '.' } }] } }
    host = document.createElement('div')
    document.body.append(host)
    root = createRoot(host)
    act(() =>
      root!.render(
        createElement(
          SessionContext.Provider,
          { value: ACTING },
          createElement(BriefSettings, { brief: withSource, writer: new LiveBriefWriter('k8s', vi.fn()) }),
        ),
      ),
    )
    expect(host.textContent).toContain('source in ., tags v…')
  })
})
