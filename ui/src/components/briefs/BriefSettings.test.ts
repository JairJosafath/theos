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
  apiVersion: 'c4.factory.io/v1alpha1',
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
