// @vitest-environment happy-dom

// Where a Crew publishes is its Brief's when the Brief sets it
// (docs/plans/done/brief-release-target.md): the Crew page says so, its release
// form changes the Brief's spec.releases entry, and a Crew changed under its
// Brief shows both targets and the two ways to settle it, each a patch that
// tests the resourceVersion it was worked out from.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { LiveBriefWriter } from '../../k8s/briefs'
import { BRIEF_PUBLISH_RELEASES, LABEL_BRIEF, LiveCrewWriter, type PublishReleases } from '../../k8s/crews'
import type { UiSession } from '../../k8s/session'
import type { Brief, Crew } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { CrewPage } from './CrewPage'

declare global {
  var IS_REACT_ACT_ENVIRONMENT: boolean
}
globalThis.IS_REACT_ACT_ENVIRONMENT = true

const OLD: PublishReleases = {
  github: { repository: 'JairJosafath/cel-lens', tokenSecretRef: { name: 'cel-lens-github' } },
}
const NEW: PublishReleases = {
  github: { repository: 'JairJosafath/private-cel-lens', tokenSecretRef: { name: 'cel-lens-github' } },
}
const REQUESTS = { mode: 'Ask' as const, authors: ['ada'] }

/** A Crew publishing to `publish`; with `brief`, made by Brief cel-lens, which last set `brief` on it. */
function crew(publish: PublishReleases | undefined, brief?: PublishReleases): Crew {
  const held = brief && JSON.stringify(brief) !== JSON.stringify(publish)
  return {
    metadata: {
      name: 'cli',
      namespace: 'cel-lens',
      resourceVersion: '5',
      ...(brief
        ? { labels: { [LABEL_BRIEF]: 'cel-lens' }, annotations: { [BRIEF_PUBLISH_RELEASES]: JSON.stringify(brief) } }
        : {}),
    },
    spec: {
      containerRef: { name: 'cli' },
      implementer: { name: 'i' },
      reviewer: { name: 'r' },
      tagReleases: true,
      ...(publish ? { publishReleases: publish } : {}),
    },
    status: brief
      ? {
          conditions: [
            {
              type: 'ReleaseTarget',
              status: held ? 'False' : 'True',
              reason: held ? 'DiffersFromBrief' : 'SetByBrief',
              message: '',
              lastTransitionTime: '2026-10-12T09:00:00Z',
            },
          ],
        }
      : {},
  }
}

const BRIEF: Brief = {
  metadata: { name: 'cel-lens', namespace: 'cel-lens', resourceVersion: '41' },
  spec: {
    idea: 'A CEL playground',
    releases: [
      { container: 'engine', ...NEW },
      { container: 'cli', ...OLD, requests: REQUESTS },
    ],
  },
}

const ACTING: UiSession = { actions: true, mode: 'service-account', user: null }

let root: Root | undefined
let host: HTMLElement | undefined

afterEach(async () => {
  await act(async () => root?.unmount())
  host?.remove()
})

beforeAll(() => {
  // The page's links read the URL they are on.
  window.history.replaceState(null, '', '/')
})

/** Renders the Crew's page acting, with both writers sending to one faked API; resolves to the page and the calls. */
async function render(c: Crew, briefs: Brief[] = [BRIEF]) {
  const fetchImpl = vi.fn(
    async () => new Response('{}', { status: 200, headers: { 'content-type': 'application/json' } }),
  )
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  const page = createElement(CrewPage, {
    crew: c,
    agents: [],
    tasks: [],
    containers: [],
    standards: [],
    briefs,
    writer: new LiveCrewWriter('k8s', fetchImpl),
    briefWriter: new LiveBriefWriter('k8s', fetchImpl),
  })
  await act(async () => root!.render(createElement(SessionContext.Provider, { value: ACTING }, page)))
  /** The patches sent, as [the object's kind and name, its ops]. */
  const sent = () =>
    (fetchImpl.mock.calls as unknown as [string, RequestInit][]).map(([url, init]) => [
      url.replace(/^.*\/(crews|briefs)\/(.*)$/, '$1/$2'),
      JSON.parse(init.body as string),
    ])
  return { page: host, sent }
}

function button(page: HTMLElement, text: string): HTMLButtonElement {
  const b = [...page.querySelectorAll('button')].find((x) => x.textContent?.startsWith(text))
  if (!b) throw new Error(`no button ${text}`)
  return b
}

function type(page: HTMLElement, label: string, value: string) {
  const id = [...page.querySelectorAll('label')].find((l) => l.textContent?.startsWith(label))!.htmlFor
  const input = page.querySelector<HTMLInputElement>(`[id="${id}"]`)!
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

const releasesText = (page: HTMLElement) =>
  page.querySelector('[aria-labelledby="crew-release"] .panel-text')!.textContent

describe('a Crew with no Brief setting its target', () => {
  it('says where it publishes and saves the target on the Crew', async () => {
    const { page, sent } = await render(crew(OLD), [])
    expect(releasesText(page)).toContain('It publishes them to JairJosafath/cel-lens.')
    expect(page.textContent).not.toContain('set by')
    expect(page.textContent).toContain('Publish releases to GitHub')
    await act(async () => type(page, 'Repository', 'JairJosafath/private-cel-lens'))
    await act(async () => button(page, 'Save release settings').click())
    expect(sent()).toEqual([
      [
        'crews/cli',
        [
          { op: 'test', path: '/metadata/resourceVersion', value: '5' },
          { op: 'add', path: '/spec/publishReleases', value: NEW },
        ],
      ],
    ])
  })
})

describe('a Crew whose Brief sets its target', () => {
  it('says so with a link to the Brief', async () => {
    const { page } = await render(crew(OLD, OLD))
    expect(releasesText(page)).toContain('It publishes them to JairJosafath/cel-lens, set by Brief cel-lens')
    const link = page.querySelector<HTMLAnchorElement>('[aria-labelledby="crew-release"] .panel-text a')!
    expect(link.textContent).toBe('Brief cel-lens')
    expect(link.getAttribute('href')).toContain('cel-lens')
    expect(page.textContent).not.toContain('Publish releases to GitHub')
    expect(page.textContent).toContain('saving changes its spec.releases entry for cli')
  })

  it('saves a new target in the Brief’s entry, keeping its requests, and tag and prepare on the Crew', async () => {
    const { page, sent } = await render(crew(OLD, OLD))
    await act(async () => type(page, 'Repository', 'JairJosafath/private-cel-lens'))
    await act(async () => page.querySelector<HTMLInputElement>('.crew-check input')!.click())
    await act(async () => button(page, 'Save release settings').click())
    expect(sent()).toEqual([
      ['crews/cli', [{ op: 'add', path: '/spec/tagReleases', value: false }]],
      [
        'briefs/cel-lens',
        [
          { op: 'test', path: '/metadata/resourceVersion', value: '41' },
          {
            op: 'add',
            path: '/spec/releases',
            value: [
              { container: 'engine', ...NEW },
              { container: 'cli', ...NEW, requests: REQUESTS },
            ],
          },
        ],
      ],
    ])
  })

  it('shows no publish fields when the page doesn’t have the Brief', async () => {
    const { page } = await render(crew(OLD, OLD), [])
    expect(page.textContent).toContain('change it in the Brief’s spec.releases')
    expect(page.textContent).not.toContain('Repository')
  })
})

describe('a Crew changed under its Brief', () => {
  it('shows both targets and the two answers instead of the publish fields', async () => {
    const { page } = await render(crew(NEW, OLD))
    expect(releasesText(page)).toContain(
      'It publishes nothing for now: the Crew was changed to publish to JairJosafath/private-cel-lens, but Brief cel-lens says JairJosafath/cel-lens.',
    )
    expect(page.textContent).not.toContain('Repository')
    expect(button(page, 'Use the Crew’s target').textContent).toContain('JairJosafath/private-cel-lens')
    expect(button(page, 'Put the Brief’s back').textContent).toContain('JairJosafath/cel-lens')
  })

  it('uses the Crew’s target by writing the Brief’s entry', async () => {
    const { page, sent } = await render(crew(NEW, OLD))
    await act(async () => button(page, 'Use the Crew’s target').click())
    expect(sent()).toEqual([
      [
        'briefs/cel-lens',
        [
          { op: 'test', path: '/metadata/resourceVersion', value: '41' },
          {
            op: 'add',
            path: '/spec/releases',
            value: [
              { container: 'engine', ...NEW },
              { container: 'cli', ...NEW, requests: REQUESTS },
            ],
          },
        ],
      ],
    ])
  })

  it('puts the Brief’s back by writing the Crew', async () => {
    const { page, sent } = await render(crew(NEW, OLD))
    await act(async () => button(page, 'Put the Brief’s back').click())
    expect(sent()).toEqual([
      [
        'crews/cli',
        [
          { op: 'test', path: '/metadata/resourceVersion', value: '5' },
          { op: 'add', path: '/spec/publishReleases', value: OLD },
        ],
      ],
    ])
  })

  it('removes the Brief’s entry when the Crew was changed to publish nowhere', async () => {
    const { page, sent } = await render(crew(undefined, OLD))
    expect(releasesText(page)).toContain('the Crew was changed to publish nowhere')
    await act(async () => button(page, 'Publish nowhere').click())
    expect(sent()).toEqual([
      [
        'briefs/cel-lens',
        [
          { op: 'test', path: '/metadata/resourceVersion', value: '41' },
          { op: 'add', path: '/spec/releases', value: [{ container: 'engine', ...NEW }] },
        ],
      ],
    ])
  })
})
