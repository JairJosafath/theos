// @vitest-environment happy-dom

// A Brief's requests on its page: the list with each one's stage, the
// Requests condition when it can't read them, Accept and Decline on a request
// waiting in Ask, which send the releases list with the number added behind a
// test of the resourceVersion, and the settings that turn requests on.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LiveBriefWriter } from '../../k8s/briefs'
import type { UiSession } from '../../k8s/session'
import type { Brief, BriefRelease } from '../../k8s/types'
import { FixedNow, SessionContext } from '../../lib/hooks'
import { BriefPage } from '../objects/BriefPage'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

const NOW = Date.parse('2026-10-09T12:00:00Z')
const ACTING: UiSession = { actions: true, mode: 'service-account', user: null }
const OFF: UiSession = { actions: false, mode: 'off', user: null }
const SIGNED_OUT: UiSession = { actions: true, mode: 'oidc', user: null, login: '/ui/login', logout: '/ui/logout' }

const lens: BriefRelease = {
  container: 'cel-lens',
  github: { repository: 'gray-lenovo/cel-lens', tokenSecretRef: { name: 'gh' } },
  requests: { mode: 'Ask', authors: ['gray-lenovo'], accepted: [3] },
}
const repo = 'gray-lenovo/cel-lens'
const brief: Brief = {
  apiVersion: 'theos.factory.io/v1beta1',
  kind: 'Brief',
  metadata: { name: 'lens', namespace: 'p', resourceVersion: '7' },
  spec: { idea: 'A lens on CEL', releases: [lens] },
  status: {
    phase: 'Planned',
    created: [{ kind: 'Container', name: 'cel-lens' }],
    requests: [
      {
        repository: repo,
        number: 3,
        author: 'gray-lenovo',
        title: 'Export as PNG',
        stage: 'Planned',
        round: 2,
        issues: ['cel-lens#7'],
        release: 'v0.3.0',
      },
      {
        repository: repo,
        number: 12,
        author: 'gray-lenovo',
        title: 'Dark mode',
        url: `https://github.com/${repo}/issues/12`,
        stage: 'Waiting',
      },
    ],
    needsYou: [
      {
        kind: 'Request',
        name: `${repo}#12`,
        question: `Request ${repo}#12 "Dark mode" by gray-lenovo waits for you.`,
      },
    ],
    needsYouWaiting: 1,
    conditions: [
      {
        type: 'Requests',
        status: 'False',
        reason: 'TokenLacksIssues',
        message: 'Resource not accessible by personal access token',
        lastTransitionTime: '2026-10-09T11:00:00Z',
      },
    ],
  },
}

let root: Root | undefined
let host: HTMLElement | undefined

afterEach(async () => {
  await act(async () => root?.unmount())
  host?.remove()
})

/** Renders the page; fetchImpl answers its writes. */
async function render(session: UiSession, fetchImpl = vi.fn(async () => ok()), shown: Brief = brief) {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () =>
    root!.render(
      createElement(
        SessionContext.Provider,
        { value: session },
        createElement(
          FixedNow.Provider,
          { value: NOW },
          createElement(BriefPage, { brief: shown, agents: [], writer: new LiveBriefWriter('k8s', fetchImpl) }),
        ),
      ),
    ),
  )
  return { page: host, fetchImpl }
}

/** Renders the page again, as the watch does with a newer Brief. */
async function rerender(session: UiSession, shown: Brief, fetchImpl = vi.fn(async () => ok())) {
  await act(async () =>
    root!.render(
      createElement(
        SessionContext.Provider,
        { value: session },
        createElement(
          FixedNow.Provider,
          { value: NOW },
          createElement(BriefPage, { brief: shown, agents: [], writer: new LiveBriefWriter('k8s', fetchImpl) }),
        ),
      ),
    ),
  )
}

function ok(): Response {
  return new Response(JSON.stringify(brief), { status: 200, headers: { 'content-type': 'application/json' } })
}

const button = (page: HTMLElement, label: string) =>
  page.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)

/** The JSON patch the nth write sent. */
function sent(fetchImpl: ReturnType<typeof vi.fn>, n = 0): unknown[] {
  const [, init] = fetchImpl.mock.calls[n] as unknown as [string, RequestInit]
  return JSON.parse(init.body as string) as unknown[]
}

describe("a Brief's requests", () => {
  it('are listed with their stage and what became of them', async () => {
    const { page } = await render(ACTING)
    const list = page.querySelector('section[aria-label="Requests from GitHub"]')!
    expect(list.textContent).toContain('2 open')
    expect(list.textContent).toContain('gray-lenovo/cel-lens #3')
    expect(list.textContent).toContain('Round 2 planned it as cel-lens#7, in release v0.3.0')
    expect(list.textContent).toContain('Dark mode by gray-lenovo')
    expect(list.textContent).toContain('Waits for you')
    expect(list.querySelector(`a[href="https://github.com/${repo}/issues/12"]`)).not.toBeNull()
  })

  it('say why the Brief can’t read them, from the Requests condition', async () => {
    const { page } = await render(ACTING)
    const note = page.querySelector('section[aria-label="Requests from GitHub"] .brief-note-error')!
    expect(note.textContent).toBe(
      'It can’t read or answer requests (TokenLacksIssues). Resource not accessible by personal access token',
    )
  })

  it('accept one waiting in Ask by adding its number to accepted, behind a test of the resourceVersion', async () => {
    const { page, fetchImpl } = await render(ACTING)
    await act(async () => button(page, `Accept ${repo} #12`)!.click())
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    expect(sent(fetchImpl)).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '7' },
      {
        op: 'add',
        path: '/spec/releases',
        value: [{ ...lens, requests: { mode: 'Ask', authors: ['gray-lenovo'], accepted: [3, 12] } }],
      },
    ])
  })

  it('stay off once one is sent, until the watch brings the Brief it made', async () => {
    const { page } = await render(ACTING)
    await act(async () => button(page, `Accept ${repo} #12`)!.click())
    expect(button(page, `Accept ${repo} #12`)!.disabled).toBe(true)
    expect(button(page, `Decline ${repo} #12`)!.disabled).toBe(true)
    await rerender(ACTING, { ...brief, metadata: { ...brief.metadata, resourceVersion: '8' } })
    expect(button(page, `Decline ${repo} #12`)!.disabled).toBe(false)
  })

  it('decline one by adding its number to declined', async () => {
    const { page, fetchImpl } = await render(ACTING)
    await act(async () => button(page, `Decline ${repo} #12`)!.click())
    expect(sent(fetchImpl)[1]).toEqual({
      op: 'add',
      path: '/spec/releases',
      value: [{ ...lens, requests: { mode: 'Ask', authors: ['gray-lenovo'], accepted: [3], declined: [12] } }],
    })
  })

  it('show the API server’s message when it refuses an answer', async () => {
    const message = 'Brief.theos.factory.io "lens" is invalid: spec.releases[0].requests.accepted: Too many: 201'
    const refuse = vi.fn(
      async () =>
        new Response(JSON.stringify({ kind: 'Status', status: 'Failure', code: 422, reason: 'Invalid', message }), {
          status: 422,
          headers: { 'content-type': 'application/json' },
        }),
    )
    const { page } = await render(ACTING, refuse)
    await act(async () => button(page, `Accept ${repo} #12`)!.click())
    expect(page.querySelector('.brief-needs-you [role="alert"]')!.textContent!.trim()).toBe(message)
    expect(button(page, `Accept ${repo} #12`)!.disabled).toBe(false)
  })

  it('say answered once spec.releases has the number, before the operator re-sorts them', async () => {
    const accepted = {
      ...brief,
      spec: { ...brief.spec, releases: [{ ...lens, requests: { ...lens.requests, accepted: [3, 12] } }] },
    }
    const { page } = await render(ACTING, undefined, accepted)
    expect(button(page, `Accept ${repo} #12`)).toBeNull()
    expect(page.querySelector('.brief-needs-you')!.textContent).toContain('Accepted; it waits for a round')
  })

  it('offer no button with actions off or nobody signed in, and say why', async () => {
    let { page } = await render(OFF)
    expect(button(page, `Accept ${repo} #12`)).toBeNull()
    expect(page.querySelector('.brief-needs-you')!.textContent).toContain('answers requests only with actions on')
    await act(async () => root?.unmount())
    host?.remove()
    ;({ page } = await render(SIGNED_OUT))
    expect(button(page, `Decline ${repo} #12`)).toBeNull()
    expect(page.querySelector('.brief-needs-you')!.textContent).toContain('to accept or decline it.')
  })
})

/** Sets an input's or select's value as a person typing would, so React sees it. */
function type(el: HTMLInputElement | HTMLSelectElement, value: string) {
  const proto = el instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el, value)
  el.dispatchEvent(new Event(el instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
}

/** The labelled control of a form, by its label's first words. */
function field(form: Element, label: string): HTMLInputElement | HTMLSelectElement {
  const l = [...form.querySelectorAll('label')].find((x) => x.textContent!.startsWith(label))!
  return form.ownerDocument.getElementById(l.htmlFor) as HTMLInputElement | HTMLSelectElement
}

describe("a releases entry's requests in the settings", () => {
  it('are summed up on the entry', async () => {
    const { page } = await render(ACTING)
    expect(page.querySelector('.brief-setting-releases')!.textContent).toContain('requests Ask, from gray-lenovo')
  })

  it('change mode, authors and teams, keeping the numbers answered', async () => {
    const { page, fetchImpl } = await render(ACTING)
    const form = page.querySelector('form[aria-label="Requests of cel-lens"]')!
    await act(async () => {
      type(field(form, 'Requests from'), 'On')
      type(field(form, 'Authors'), 'gray-lenovo, a-colleague')
      type(field(form, 'Teams'), 'gray-org/maintainers')
    })
    await act(async () => form.querySelector<HTMLButtonElement>('button[type="submit"]')!.click())
    expect(sent(fetchImpl)).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '7' },
      {
        op: 'add',
        path: '/spec/releases',
        value: [
          {
            ...lens,
            requests: {
              mode: 'On',
              authors: ['gray-lenovo', 'a-colleague'],
              teams: ['gray-org/maintainers'],
              accepted: [3],
            },
          },
        ],
      },
    ])
  })

  it('start again from the entry when it changed meanwhile', async () => {
    const { page } = await render(ACTING)
    const authors = () => field(page.querySelector('form[aria-label="Requests of cel-lens"]')!, 'Authors').value
    expect(authors()).toBe('gray-lenovo')
    const changed = { ...lens, requests: { ...lens.requests, authors: ['gray-lenovo', 'b'] } }
    await rerender(ACTING, {
      ...brief,
      metadata: { ...brief.metadata, resourceVersion: '8' },
      spec: { ...brief.spec, releases: [changed] },
    })
    expect(authors()).toBe('gray-lenovo, b')
  })

  it('are set when an app’s releases are first published', async () => {
    const two = {
      ...brief,
      status: { ...brief.status, created: [...brief.status!.created!, { kind: 'Container', name: 'cli' }] },
    }
    const { page, fetchImpl } = await render(ACTING, undefined, two)
    const form = page.querySelector('form[aria-label="Publish an app\'s releases"]')!
    await act(async () => {
      type(field(form, 'GitHub repository'), 'gray-lenovo/cel-cli')
      type(field(form, 'Token Secret'), 'gh')
      type(field(form, 'Requests from'), 'Ask')
      type(field(form, 'Teams'), 'gray-org/maintainers')
    })
    await act(async () => form.querySelector<HTMLButtonElement>('button[type="submit"]')!.click())
    expect(sent(fetchImpl)[1]).toEqual({
      op: 'add',
      path: '/spec/releases',
      value: [
        lens,
        {
          container: 'cli',
          github: { repository: 'gray-lenovo/cel-cli', tokenSecretRef: { name: 'gh' } },
          requests: { mode: 'Ask', teams: ['gray-org/maintainers'] },
        },
      ],
    })
  })
})
