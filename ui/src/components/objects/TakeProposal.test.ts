// @vitest-environment happy-dom

// #72, step 6: a person takes a schedule the Standard proposes. The form asks
// for the time zone, sends the proposal as it is written with that zone as one
// append to the Crew's schedules, and shows the API server's refusal.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LiveCrewWriter } from '../../k8s/crews'
import type { Crew, CrewSchedule } from '../../k8s/types'
import type { ScheduleProposals } from '../../model/schedules'
import { TakeProposalForm } from './CrewActions'
import { ProposalsPanel } from './CrewPage'

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
    expect(input.required).toBe(true)
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

// The review of #72's step 6: the watch brings the Crew's new schedule, the
// taken proposal's row leaves the offered list, and the result and the
// keyboard focus must not leave with it.
describe('the proposals panel once a proposal is taken', () => {
  const other: CrewSchedule = { name: 'flakes', cron: '0 2 * * *', timeZone: 'UTC', goal: 'Find the flaky tests.' }
  const before: ScheduleProposals = { standard: 'python-standard', offered: [proposal, other], inUse: [] }
  const taken = { ...proposal, timeZone: 'Europe/Amsterdam' }
  const after: ScheduleProposals = {
    standard: 'python-standard',
    offered: [other],
    inUse: [{ proposal, schedule: taken, differs: [{ field: 'timeZone', crew: 'Europe/Amsterdam', proposal: 'UTC' }] }],
  }

  async function showPanel() {
    const writer = new LiveCrewWriter(
      'k8s',
      vi.fn(async () => answer(200)),
    )
    host = document.createElement('div')
    document.body.append(host)
    root = createRoot(host)
    const page = host
    const render = (proposals: ScheduleProposals) =>
      act(async () => root!.render(createElement(ProposalsPanel, { crew, proposals, writer })))
    await render(before)
    const result = page.querySelector<HTMLElement>('p[role="status"]')!
    /** Takes "dependencies" in Europe/Amsterdam with the keyboard's focus on its button. */
    const take = async () => {
      const input = page.querySelector<HTMLInputElement>('input')!
      const button = [...page.querySelectorAll<HTMLButtonElement>('button')].find(
        (b) => b.textContent === 'Take dependencies',
      )!
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Europe/Amsterdam')
        input.dispatchEvent(new Event('input', { bubbles: true }))
      })
      button.focus()
      await act(async () => button.click())
      return button
    }
    return { page, render, result, take }
  }

  it('has a live region before anything is taken, and says the result there, once', async () => {
    const { page, result, take } = await showPanel()
    expect(result.textContent).toBe('')
    await take()
    expect(result.textContent).toBe('Added dependencies to the Crew’s schedules.')
    expect(page.querySelectorAll('[role="status"]')).toHaveLength(1)
  })

  it('keeps the result, and moves the focus to it, when the taken proposal’s row leaves the list', async () => {
    const { page, render, result, take } = await showPanel()
    const button = await take()
    await render(after)
    expect(button.isConnected).toBe(false)
    expect(page.textContent).not.toContain('Take dependencies')
    expect(page.querySelector('p[role="status"]')).toBe(result)
    expect(result.textContent).toBe('Added dependencies to the Crew’s schedules.')
    expect(document.activeElement).toBe(result)
  })

  it('leaves the focus where a person moved it meanwhile', async () => {
    const { page, render, take } = await showPanel()
    await take()
    const elsewhere = [...page.querySelectorAll<HTMLInputElement>('input')].at(-1)!
    elsewhere.focus()
    await render(after)
    expect(elsewhere.isConnected).toBe(true)
    expect(document.activeElement).toBe(elsewhere)
  })
})
