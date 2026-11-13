// @vitest-environment happy-dom

// #72, step 6: a person takes a schedule the Standard proposes. The form asks
// for the time zone, sends the proposal as it is written with that zone as one
// append to the Crew's schedules, and shows the API server's refusal.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LiveCrewWriter } from '../../k8s/crews'
import type { Crew, CrewSchedule } from '../../k8s/types'
import { TakeProposalForm } from './CrewActions'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

const crew: Crew = {
  metadata: { name: 'orders-api', namespace: 'webshop', resourceVersion: '5' },
  spec: { containerRef: { name: 'orders-api' }, implementer: { name: 'i' }, reviewer: { name: 'r' } },
}
// As the API server stores a proposal that names no zone: with UTC written in.
const proposal: CrewSchedule = { name: 'dependencies', cron: '0 6 * * 1', timeZone: 'UTC', goal: 'Update the deps.' }

const DUPLICATE =
  'Crew.theos.factory.io "orders-api" is invalid: spec.schedules[1]: Duplicate value: {"name":"dependencies"}'
function answer(code: number): Response {
  const body = code === 200 ? crew : { kind: 'Status', status: 'Failure', code, reason: 'Invalid', message: DUPLICATE }
  return new Response(JSON.stringify(body), { status: code, headers: { 'content-type': 'application/json' } })
}

let root: Root | undefined
let host: HTMLElement | undefined

afterEach(async () => {
  await act(async () => root?.unmount())
  host?.remove()
})

async function show(code = 200) {
  const fetchImpl = vi.fn(async () => answer(code))
  const writer = new LiveCrewWriter('k8s', fetchImpl)
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root!.render(createElement(TakeProposalForm, { crew, proposal, writer })))
  const page = host
  const input = page.querySelector<HTMLInputElement>('input')!
  const button = page.querySelector<HTMLButtonElement>('button[type="submit"]')!
  /** Types `value` into the time zone field, as a person does. */
  const type = (value: string) =>
    act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  const sent = () =>
    fetchImpl.mock.calls.map((call) => {
      const [url, init] = call as unknown as [string, RequestInit]
      return {
        url,
        type: (init.headers as Record<string, string>)['Content-Type'],
        body: JSON.parse(init.body as string),
      }
    })
  return { page, input, button, type, sent }
}

describe('taking a schedule the Standard proposes', () => {
  it('asks for the time zone: the field starts empty and nothing is sent without one', async () => {
    const { page, input, button, type, sent } = await show()
    expect(input.value).toBe('')
    expect(button.textContent).toBe('Take dependencies')
    expect(button.disabled).toBe(true)
    await type('   ')
    expect(button.disabled).toBe(true)
    await act(async () =>
      page.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })),
    )
    expect(sent()).toEqual([])
  })

  it('adds the proposal as it is written, in the zone given, as one append to the Crew’s schedules', async () => {
    const { page, button, type, sent } = await show()
    await type(' Europe/Amsterdam ')
    expect(button.disabled).toBe(false)
    await act(async () => button.click())
    expect(sent()).toEqual([
      {
        url: 'k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/crews/orders-api',
        type: 'application/json-patch+json',
        body: [
          {
            op: 'add',
            path: '/spec/schedules/-',
            value: { name: 'dependencies', cron: '0 6 * * 1', timeZone: 'Europe/Amsterdam', goal: 'Update the deps.' },
          },
        ],
      },
    ])
    expect(page.querySelector('[role="status"]')?.textContent).toBe('Added to the Crew’s schedules')
    expect(page.querySelector('[role="alert"]')).toBeNull()
    // Taken once: the page lists it under the names in use once the Crew has it.
    expect(button.disabled).toBe(true)
  })

  it('sends UTC when the person says UTC, and a zone it can’t check as typed', async () => {
    for (const zone of ['UTC', 'Mars/Olympus']) {
      const { button, type, sent } = await show()
      await type(zone)
      await act(async () => button.click())
      expect(sent()[0].body[0].value.timeZone).toBe(zone)
      await act(async () => root?.unmount())
      host?.remove()
    }
  })

  it('shows the API server’s refusal, and can be sent again', async () => {
    const { page, button, type } = await show(422)
    await type('Europe/Amsterdam')
    await act(async () => button.click())
    expect(page.querySelector('[role="alert"]')?.textContent).toContain('Duplicate value')
    expect(button.disabled).toBe(false)
  })
})
