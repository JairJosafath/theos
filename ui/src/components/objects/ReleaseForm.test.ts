// @vitest-environment happy-dom

// A Crew's publish settings are sent as one object, so a save that sets them
// names the Crew they were worked out from: one changed meanwhile (another
// person, or kubectl) refuses it, and the form says so rather than write back
// a field someone else changed.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LiveCrewWriter } from '../../k8s/crews'
import type { Crew } from '../../k8s/types'
import { ReleaseForm } from './CrewActions'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

const crew = (resourceVersion: string, secret: string): Crew => ({
  metadata: { name: 'orders-api', namespace: 'webshop', resourceVersion },
  spec: {
    containerRef: { name: 'orders-api' },
    implementer: { name: 'i' },
    reviewer: { name: 'r' },
    tagReleases: true,
    publishReleases: { github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: secret } } },
  },
})

/** The UI server's answer to a publish write worked out from a Crew that has changed since. */
const CONFLICT =
  'Crew webshop/orders-api changed since this page showed it, so nothing was saved: check its settings as they are now and make the change again'
function answer(code: number): Response {
  const body =
    code === 409 ? { kind: 'Status', status: 'Failure', code, reason: 'Conflict', message: CONFLICT } : crew('9', 'gh')
  return new Response(JSON.stringify(body), { status: code, headers: { 'content-type': 'application/json' } })
}

let root: Root | undefined
let host: HTMLElement | undefined

afterEach(async () => {
  await act(async () => root?.unmount())
  host?.remove()
})

/** Types `value` into the form's input labelled `label`, as a person does. */
function type(page: HTMLElement, label: string, value: string) {
  const id = [...page.querySelectorAll('label')].find((l) => l.textContent?.startsWith(label))!.htmlFor
  const input = page.querySelector<HTMLInputElement>(`[id="${id}"]`)!
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

/**
 * Renders the form on the Crew at version 5, changes its tag prefix, then
 * shows it the Crew at version 6, whose token Secret someone else renamed,
 * and saves. Resolves to the page and the patch it sent.
 */
async function savePrefix(code: number) {
  const fetchImpl = vi.fn(async () => answer(code))
  const writer = new LiveCrewWriter('k8s', fetchImpl)
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root!.render(createElement(ReleaseForm, { crew: crew('5', 'gh'), writer })))
  await act(async () => type(host!, 'Tag prefix', 'api-'))
  await act(async () => root!.render(createElement(ReleaseForm, { crew: crew('6', 'gh-renamed'), writer })))
  const save = [...host.querySelectorAll('button')].find((b) => b.textContent === 'Save release settings')!
  await act(async () => save.click())
  expect(fetchImpl).toHaveBeenCalledTimes(1)
  const [, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
  return { page: host, ops: JSON.parse(init.body as string) as unknown[] }
}

describe("a Crew's publish settings", () => {
  it('are sent with a test of the resourceVersion they were worked out from', async () => {
    const { page, ops } = await savePrefix(200)
    expect(ops).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '5' },
      {
        op: 'add',
        path: '/spec/publishReleases',
        value: { github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'gh' } }, tagPrefix: 'api-' },
      },
    ])
    expect(page.querySelector('[role="alert"]')).toBeNull()
  })

  it('refused because the Crew changed meanwhile, say so on the form', async () => {
    const { page } = await savePrefix(409)
    expect(page.querySelector('[role="alert"]')?.textContent).toBe(CONFLICT)
  })
})

/** Ticks the form's checkbox labelled `label`. */
function tick(page: HTMLElement, label: string) {
  const box = [...page.querySelectorAll('label.crew-check')]
    .find((l) => l.textContent?.startsWith(label))!
    .querySelector<HTMLInputElement>('input')!
  box.click()
}

/**
 * Renders the form on `c`, does `steps` as a person, and saves. Resolves to
 * the page's text before the save and the publishReleases the patch sent.
 */
async function saveSource(c: Crew, steps: ((page: HTMLElement) => void)[]) {
  const fetchImpl = vi.fn(async () => answer(200))
  const writer = new LiveCrewWriter('k8s', fetchImpl)
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root!.render(createElement(ReleaseForm, { crew: c, writer })))
  for (const step of steps) await act(async () => step(host!))
  const shown = host.textContent ?? ''
  const save = [...host.querySelectorAll('button')].find((b) => b.textContent === 'Save release settings')!
  await act(async () => save.click())
  expect(fetchImpl).toHaveBeenCalledTimes(1)
  const [, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
  const ops = JSON.parse(init.body as string) as { op: string; path: string; value?: unknown }[]
  return { shown, publish: ops.find((o) => o.path === '/spec/publishReleases')!.value }
}

describe("a Crew's published source", () => {
  const github = { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'gh' } }
  const withSource = (source: { path: string; tagPrefix?: string }): Crew => {
    const c = crew('5', 'gh')
    return { ...c, spec: { ...c.spec, publishReleases: { github, source } } }
  }

  it('is set by ticking it and naming the folder, showing the tag it defaults to', async () => {
    const { shown, publish } = await saveSource(crew('5', 'gh'), [
      (p) => tick(p, 'Publish the source'),
      (p) => type(p, 'Source folder', 'engine'),
    ])
    expect(shown).toContain('The source is tagged engine/vX.Y.Z.')
    expect(publish).toEqual({ github, source: { path: 'engine' } })
  })

  it('takes a changed folder and tag prefix', async () => {
    const { shown, publish } = await saveSource(withSource({ path: 'engine' }), [
      (p) => type(p, 'Source folder', 'cli'),
      (p) => type(p, 'Source tag prefix', 'tools/'),
    ])
    expect(shown).toContain('The source is tagged tools/vX.Y.Z.')
    expect(publish).toEqual({ github, source: { path: 'cli', tagPrefix: 'tools/' } })
  })

  it('goes back to the default tag when the prefix is emptied', async () => {
    const { shown, publish } = await saveSource(withSource({ path: '.', tagPrefix: 'all-' }), [
      (p) => type(p, 'Source tag prefix', ''),
    ])
    expect(shown).toContain('The source is tagged vX.Y.Z.')
    expect(publish).toEqual({ github, source: { path: '.' } })
  })

  it('is removed by unticking it, still publishing the release', async () => {
    const { publish } = await saveSource(withSource({ path: 'engine' }), [(p) => tick(p, 'Publish the source')])
    expect(publish).toEqual({ github })
  })
})
