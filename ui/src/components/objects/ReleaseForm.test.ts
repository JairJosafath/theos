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
