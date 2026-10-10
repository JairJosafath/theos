// @vitest-environment happy-dom

// The Brief forms check none of the CRD's rules: they send what the person
// typed, and a write the API server refuses shows the server's own message.

import { act, createElement, type ReactElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LiveBriefWriter } from '../../k8s/briefs'
import { NewBrief } from './NewBrief'
import { PromptForm } from './PromptForm'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

/** The API server's answer to a write a CRD rule refuses. */
function refused(message: string): Response {
  return new Response(JSON.stringify({ kind: 'Status', status: 'Failure', code: 422, reason: 'Invalid', message }), {
    status: 422,
    headers: { 'content-type': 'application/json' },
  })
}

let root: Root | undefined
let host: HTMLElement | undefined

async function render(element: ReactElement): Promise<HTMLElement> {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root!.render(element))
  return host
}

afterEach(async () => {
  await act(async () => root?.unmount())
  host?.remove()
})

/** Types value into a React-controlled field, as a person would. */
async function type(field: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const proto = Object.getPrototypeOf(field) as object
  await act(async () => {
    Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(field, value)
    field.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

async function submit(form: HTMLFormElement) {
  await act(async () => {
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  })
}

describe('a refused Brief create', () => {
  const message =
    'Brief.c4.factory.io "plant-care" is invalid: spec.parallel: Invalid value: 9: spec.parallel in body should be less than or equal to 5'

  it('sends what was typed and shows the API server’s message', async () => {
    const fetchImpl = vi.fn(async () => refused(message))
    const page = await render(
      createElement(NewBrief, { namespace: 'webshop', taken: [], writer: new LiveBriefWriter('k8s', fetchImpl) }),
    )
    await type(page.querySelector('textarea')!, 'Plant care reminders')
    await type(page.querySelector<HTMLInputElement>('input[id$="-parallel"]')!, '9')
    const button = page.querySelector<HTMLButtonElement>('button[type="submit"]')!
    expect(button.disabled).toBe(false)
    await submit(page.querySelector('form')!)

    expect(fetchImpl).toHaveBeenCalledTimes(1)
    const [, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
    expect(JSON.parse(init.body as string).spec).toEqual({ idea: 'Plant care reminders', parallel: 9 })
    expect(page.querySelector('[role="alert"]')?.textContent).toBe(message)
    expect(page.querySelector('[role="status"]')).toBeNull()
  })

  it.each([
    ['', undefined],
    [' 3 ', 3],
    ['abc', 'abc'],
    ['2.5', '2.5'],
  ])('sends parallel %j as %j, for the server to judge', async (typed, sent) => {
    const fetchImpl = vi.fn(async () => new Response('{}', { status: 201 }))
    const page = await render(
      createElement(NewBrief, { namespace: 'webshop', taken: [], writer: new LiveBriefWriter('k8s', fetchImpl) }),
    )
    await type(page.querySelector('textarea')!, 'Plant care reminders')
    await type(page.querySelector<HTMLInputElement>('input[id$="-parallel"]')!, typed)
    await submit(page.querySelector('form')!)
    const [, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
    expect(JSON.parse(init.body as string).spec.parallel).toBe(sent)
  })

  it('takes any name and leaves its rule to the server', async () => {
    const fetchImpl = vi.fn(async () =>
      refused(
        'Brief.c4.factory.io "Plant Care" is invalid: metadata.name: Invalid value: "Plant Care": a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters',
      ),
    )
    const page = await render(
      createElement(NewBrief, { namespace: 'webshop', taken: [], writer: new LiveBriefWriter('k8s', fetchImpl) }),
    )
    await type(page.querySelector('textarea')!, 'Plant care reminders')
    await type(page.querySelector<HTMLInputElement>('input[id$="-name"]')!, 'Plant Care')
    expect(page.querySelector<HTMLButtonElement>('button[type="submit"]')!.disabled).toBe(false)
    await submit(page.querySelector('form')!)
    expect(page.querySelector('[role="alert"]')?.textContent).toMatch(
      /^Brief.c4.factory.io "Plant Care" is invalid: metadata.name/,
    )
  })
})

describe('a refused prompt', () => {
  it('shows the API server’s message and keeps the prompt', async () => {
    const message =
      'Brief.c4.factory.io "returns" is invalid: spec.prompts: Invalid value: a Brief lists at most 50 prompts: in the write that appends, drop the oldest prompts rounds have planned (status.promptsPlanned counts them) and raise promptsRetired by as many'
    const fetchImpl = vi.fn(async () => refused(message))
    const page = await render(
      createElement(PromptForm, {
        namespace: 'webshop',
        name: 'returns',
        writer: new LiveBriefWriter('k8s', fetchImpl),
        needsInput: false,
      }),
    )
    const input = page.querySelector('input')!
    await type(input, 'Only unopened items')
    await submit(page.querySelector('form')!)
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    expect(page.querySelector('[role="alert"]')?.textContent).toBe(message)
    expect(input.value).toBe('Only unopened items')
  })
})
