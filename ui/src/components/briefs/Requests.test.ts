// @vitest-environment happy-dom

// A Brief's requests on its page: the list with each one's stage, the
// Requests condition when it can't read them, Accept and Decline on a request
// waiting in Ask, which send the releases list with the number added behind a
// test of the resourceVersion, and the settings that turn requests on.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { demoStory } from '../../demo/fixture'
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
  github: { repository: 'ada/cel-lens', tokenSecretRef: { name: 'gh' } },
  requests: { mode: 'Ask', authors: ['ada'], accepted: [3] },
}
const repo = 'ada/cel-lens'
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
        author: 'ada',
        title: 'Export as PNG',
        stage: 'Planned',
        round: 2,
        issues: ['cel-lens#7'],
        release: 'v0.3.0',
      },
      {
        repository: repo,
        number: 12,
        author: 'ada',
        title: 'Dark mode',
        url: `https://github.com/${repo}/issues/12`,
        stage: 'Waiting',
      },
    ],
    needsYou: [
      {
        kind: 'Request',
        name: `${repo}#12`,
        question: `Request ${repo}#12 "Dark mode" by ada waits for you.`,
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
    expect(list.textContent).toContain('ada/cel-lens #3')
    expect(list.textContent).toContain('Round 2 planned it as cel-lens#7, in release v0.3.0')
    expect(list.textContent).toContain('Dark mode by ada')
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
        value: [{ ...lens, requests: { mode: 'Ask', authors: ['ada'], accepted: [3, 12] } }],
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
      value: [{ ...lens, requests: { mode: 'Ask', authors: ['ada'], accepted: [3], declined: [12] } }],
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

// The demo's Brief with a Linear source: ENG-409 planned, ENG-412 waiting, ENG-418 waiting and with no author.
const feedback = demoStory(NOW).briefs.find((b) => b.metadata.name === 'product-feedback')!
const source = feedback.spec.requestSources![0]

describe("a Brief's requests from a request source (#163)", () => {
  it('read as their source and key, never as undefined #412', async () => {
    const { page } = await render(ACTING, undefined, feedback)
    const list = page.querySelector('section[aria-label="Requests"]')!
    expect(list.textContent).toContain('3 open')
    expect(list.textContent).toContain('product:ENG-412')
    expect(list.textContent).toContain('Add a dark mode to the storefront by Sam Sample')
    expect(list.textContent).toContain('Round 2 planned it as feedback-api#4')
    expect(page.querySelector('section[aria-label="Requests from GitHub"]')).toBeNull()
    expect(list.querySelector('a[href="https://linear.example/acme/issue/ENG-412"]')!.textContent).toContain(
      'product:ENG-412',
    )
    expect(page.textContent).not.toContain('undefined')
  })

  it('show no author, and never undefined, for an item that names nobody', async () => {
    const { page } = await render(ACTING, undefined, feedback)
    const line = [...page.querySelectorAll('.brief-request')].find((li) => li.textContent!.includes('product:ENG-418'))!
    expect(line.textContent).toContain('Let customers export their order history')
    expect(line.textContent).not.toContain(' by ')
    expect(page.querySelector('.brief-needs-you')!.textContent).toContain('product:ENG-418')
    expect(page.textContent).not.toContain('undefined')
    expect(page.textContent).not.toContain(' by undefined')
  })

  it('show their text as text, never as HTML', async () => {
    const hostile = {
      ...feedback,
      status: {
        ...feedback.status,
        requests: [
          { ...feedback.status!.requests![1], title: '<img src=x onerror=alert(1)>', author: '<b>Sam</b>' },
          feedback.status!.requests![2],
        ],
      },
    }
    const { page } = await render(ACTING, undefined, hostile)
    expect(page.querySelector('.brief-request img, .brief-request b')).toBeNull()
    expect(page.querySelector('.brief-request')!.textContent).toContain('<img src=x onerror=alert(1)> by <b>Sam</b>')
  })

  it('are accepted by adding the number to the source’s accepted, and never by a patch of spec.releases', async () => {
    const { page, fetchImpl } = await render(ACTING, undefined, feedback)
    await act(async () => button(page, 'Accept product:ENG-412')!.click())
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/briefs/product-feedback')
    expect(init.method).toBe('PATCH')
    expect(sent(fetchImpl)).toEqual([
      { op: 'test', path: '/spec/requestSources/0', value: source },
      { op: 'add', path: '/spec/requestSources/0/accepted', value: [409, 412] },
    ])
    expect(JSON.stringify(sent(fetchImpl))).not.toContain('/spec/releases')
  })

  it('are declined by adding the number to the source’s declined', async () => {
    const { page, fetchImpl } = await render(ACTING, undefined, feedback)
    await act(async () => button(page, 'Decline product:ENG-418')!.click())
    expect(sent(fetchImpl)).toEqual([
      { op: 'test', path: '/spec/requestSources/0', value: source },
      { op: 'add', path: '/spec/requestSources/0/declined', value: [418] },
    ])
  })

  it('answer for the right source when there are two', async () => {
    const support = {
      name: 'support',
      jira: { site: 'https://acme.example', cloudID: 'c1', project: 'SHOP', label: 'factory' },
      credentialsSecretRef: { name: 'acme-jira' },
    }
    const two: Brief = { ...feedback, spec: { ...feedback.spec, requestSources: [support, source] } }
    const { page, fetchImpl } = await render(ACTING, undefined, two)
    await act(async () => button(page, 'Accept product:ENG-412')!.click())
    expect(sent(fetchImpl)).toEqual([
      { op: 'test', path: '/spec/requestSources/1', value: source },
      { op: 'add', path: '/spec/requestSources/1/accepted', value: [409, 412] },
    ])
  })

  it('stay off once one is sent, until the watch brings the Brief it made', async () => {
    const { page } = await render(ACTING, undefined, feedback)
    await act(async () => button(page, 'Accept product:ENG-412')!.click())
    expect(button(page, 'Accept product:ENG-412')!.disabled).toBe(true)
    expect(button(page, 'Decline product:ENG-412')!.disabled).toBe(true)
  })

  it('offer no button for a source the Brief no longer has, and with it off', async () => {
    const gone: Brief = { ...feedback, spec: { ...feedback.spec, requestSources: [{ ...source, mode: 'Off' }] } }
    const { page } = await render(ACTING, undefined, gone)
    expect(button(page, 'Accept product:ENG-412')!.disabled).toBe(true)
    expect(button(page, 'Decline product:ENG-412')!.disabled).toBe(true)
  })

  it('say answered once the source has the number', async () => {
    const answered: Brief = {
      ...feedback,
      spec: { ...feedback.spec, requestSources: [{ ...source, accepted: [409, 412] }] },
    }
    const { page } = await render(ACTING, undefined, answered)
    expect(button(page, 'Accept product:ENG-412')).toBeNull()
    expect(page.querySelector('.brief-needs-you')!.textContent).toContain('Accepted; it waits for a round')
  })

  it('show the API server’s message when it refuses an answer', async () => {
    const message =
      'Brief.theos.factory.io "product-feedback" is invalid: spec.requestSources[0].accepted: Too many: 201'
    const refuse = vi.fn(
      async () =>
        new Response(JSON.stringify({ kind: 'Status', status: 'Failure', code: 422, reason: 'Invalid', message }), {
          status: 422,
          headers: { 'content-type': 'application/json' },
        }),
    )
    const { page } = await render(ACTING, refuse, feedback)
    await act(async () => button(page, 'Accept product:ENG-412')!.click())
    expect(page.querySelector('.brief-needs-you [role="alert"]')!.textContent!.trim()).toBe(message)
    expect(button(page, 'Accept product:ENG-412')!.disabled).toBe(false)
  })

  it('offer no button with actions off or nobody signed in, and say why', async () => {
    let { page } = await render(OFF, undefined, feedback)
    expect(button(page, 'Accept product:ENG-412')).toBeNull()
    expect(page.querySelector('.brief-needs-you')!.textContent).toContain('answers requests only with actions on')
    await act(async () => root?.unmount())
    host?.remove()
    ;({ page } = await render(SIGNED_OUT, undefined, feedback))
    expect(button(page, 'Decline product:ENG-412')).toBeNull()
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
    expect(page.querySelector('.brief-setting-releases')!.textContent).toContain('requests Ask, from ada')
  })

  it('change mode, authors and teams, keeping the numbers answered', async () => {
    const { page, fetchImpl } = await render(ACTING)
    const form = page.querySelector('form[aria-label="Requests of cel-lens"]')!
    await act(async () => {
      type(field(form, 'Requests from'), 'On')
      type(field(form, 'Authors'), 'ada, a-colleague')
      type(field(form, 'Teams'), 'acme/maintainers')
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
              authors: ['ada', 'a-colleague'],
              teams: ['acme/maintainers'],
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
    expect(authors()).toBe('ada')
    const changed = { ...lens, requests: { ...lens.requests, authors: ['ada', 'b'] } }
    await rerender(ACTING, {
      ...brief,
      metadata: { ...brief.metadata, resourceVersion: '8' },
      spec: { ...brief.spec, releases: [changed] },
    })
    expect(authors()).toBe('ada, b')
  })

  it('are set when an app’s releases are first published', async () => {
    const two = {
      ...brief,
      status: { ...brief.status, created: [...brief.status!.created!, { kind: 'Container', name: 'cli' }] },
    }
    const { page, fetchImpl } = await render(ACTING, undefined, two)
    const form = page.querySelector('form[aria-label="Publish an app\'s releases"]')!
    await act(async () => {
      type(field(form, 'GitHub repository'), 'ada/cel-cli')
      type(field(form, 'Token Secret'), 'gh')
      type(field(form, 'Requests from'), 'Ask')
      type(field(form, 'Teams'), 'acme/maintainers')
    })
    await act(async () => form.querySelector<HTMLButtonElement>('button[type="submit"]')!.click())
    expect(sent(fetchImpl)[1]).toEqual({
      op: 'add',
      path: '/spec/releases',
      value: [
        lens,
        {
          container: 'cli',
          github: { repository: 'ada/cel-cli', tokenSecretRef: { name: 'gh' } },
          requests: { mode: 'Ask', teams: ['acme/maintainers'] },
        },
      ],
    })
  })
})
