import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import type { BriefWriter } from '../../k8s/briefs'
import type { ContainerWriter } from '../../k8s/containers'
import type { CrewWriter } from '../../k8s/crews'
import type { Connection, StoryData } from '../../k8s/sources'
import type { GitProvider } from '../../k8s/types'
import { demoStory } from '../../demo/fixture'
import type { LifecycleKind } from '../../model/lifecycle'
import { GitProviderPage } from './CatalogPages'
import { ObjectPage } from './ObjectPage'

const NOW = Date.parse('2026-06-01T12:00:00Z')
const LIVE: Connection = { phase: 'live', loaded: true, problems: [] }
const WRITER = {} as BriefWriter
const CONTAINER_WRITER = {} as ContainerWriter
const CREW_WRITER = {} as CrewWriter

beforeAll(() => {
  // The pages link to each other through the URL the shell is on.
  vi.stubGlobal('window', { location: { pathname: '/', search: '?shell=next', hash: '' } })
})
afterAll(() => {
  vi.unstubAllGlobals()
})

function page(kind: LifecycleKind, name: string, data: StoryData = demoStory(NOW), connection = LIVE) {
  return renderToStaticMarkup(
    createElement(ObjectPage, {
      kind,
      name,
      data,
      connection,
      writer: WRITER,
      containerWriter: CONTAINER_WRITER,
      crewWriter: CREW_WRITER,
    }),
  )
}

/** The page's text, without its markup. */
function text(html: string) {
  return html.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')
}

describe('the read-only object pages', () => {
  it.each([
    ['Project', 'webshop', 'Its catalog and Git hosts'],
    ['SoftwareSystem', 'webshop-core', 'What it is for'],
    ['Relationship', 'orders-api-to-cart-cache', 'Its ends'],
    ['Session', demoStory(NOW).sessions[0].metadata.name, 'Who and when'],
    ['Standard', 'python-standard', 'Frameworks it checks'],
    ['Compute', 'small', 'Its template'],
    ['Deployment', 'k8s-manifests', 'Its template'],
    ['GitProvider', 'gitea', 'Where repositories go'],
  ] as const)('opens a %s page', (kind, name, section) => {
    const html = page(kind, name)
    expect(html).toContain(`aria-label="${kind} ${name}"`)
    expect(text(html)).toContain(section)
  })

  it('shows the Project’s counts and versions from status', () => {
    const data = demoStory(NOW)
    data.project!.status!.factory = { operatorVersion: '0.14.0' }
    const t = text(page('Project', 'webshop', data))
    expect(t).toContain('Containers 4, 1 Ready')
    expect(t).toContain('Operator 0.14.0')
    expect(t).toContain('Chart not reported yet')
  })

  it('says why an AddedAfterScaffold Relationship is not applied, and links its Container’s repository', () => {
    const html = page('Relationship', 'orders-api-to-cart-cache')
    expect(html).toContain('aria-current="step"')
    expect(text(html)).toMatch(/Added after the scaffold/)
    expect(text(html)).toMatch(/by hand, in orders-api’s repository/)
    expect(html).toContain('href="http://gitea.gitea.svc.cluster.local:3000/webshop/orders-api"')
  })

  it('shows no stepper on a Relationship the factory does not apply', () => {
    const html = page('Relationship', 'customer-uses-webshop-core')
    expect(html).not.toContain('aria-label="Lifecycle"')
    expect(text(html)).toMatch(/it describes the model/)
  })

  it('lists the Containers that name a catalog entry, each linked to its page', () => {
    const html = page('Standard', 'python-standard')
    expect(html).toContain('href="/?shell=next&amp;ns=webshop&amp;kind=Container&amp;name=orders-api"')
    expect(text(html)).toContain('ruff.toml, .c4/checks/')
  })
})

describe('the GitProvider page', () => {
  it('names the Secret, and renders none of its data or any field it does not name', () => {
    const provider = demoStory(NOW).gitProviders[0]
    // Whatever else the object carries (a token pasted where it doesn't
    // belong, an annotation), the page shows only the fields it names.
    const leaky = {
      ...provider,
      metadata: { ...provider.metadata, annotations: { token: 'sekrit-annotation' } },
      spec: { ...provider.spec, token: 'sekrit-token', password: 'sekrit-password' },
      data: { token: btoa('sekrit-data') },
    } as GitProvider
    const html = renderToStaticMarkup(createElement(GitProviderPage, { provider: leaky, containers: [] }))
    expect(text(html)).toContain('Secret gitea-credentials')
    expect(text(html)).toContain('the UI never reads Secrets')
    expect(html).not.toMatch(/sekrit|c2Vrcml0/)
  })
})

describe('a refused read of an optional kind', () => {
  const refused: Connection = {
    ...LIVE,
    unread: [{ what: 'standards', code: 403, message: 'standards.c4.factory.io is forbidden', resource: 'standards' }],
  }

  it('shows on the kind’s page instead of “not there”', () => {
    const data = { ...demoStory(NOW), standards: [] }
    const t = text(page('Standard', 'python-standard', data, refused))
    expect(t).toContain('Could not read standards (403): standards.c4.factory.io is forbidden')
    expect(t).not.toContain('There is no Standard')
  })

  it('shows in the Project page’s catalog, beside the kinds it could read', () => {
    const data = { ...demoStory(NOW), standards: [] }
    const t = text(page('Project', 'webshop', data, refused))
    expect(t).toContain('Standards Could not read standards (403)')
    expect(t).toContain('Computes small')
  })

  it('says a missing object is not there when the read succeeded', () => {
    expect(text(page('Compute', 'large'))).toContain('There is no Compute named large in webshop')
  })
})

describe('kinds without conditions', () => {
  it.each([
    ['Standard', 'python-standard'],
    ['GitProvider', 'gitea'],
  ] as const)('a %s page shows no Conditions section', (kind, name) => {
    expect(text(page(kind, name))).not.toContain('Conditions')
  })
})

describe('a failed re-watch of an optional kind', () => {
  it('says the failure above what was loaded, and keeps it', () => {
    const blip: Connection = {
      ...LIVE,
      unread: [{ what: 'computes', message: 'Cannot reach the Kubernetes API', resource: 'computes' }],
    }
    const t = text(page('Project', 'webshop', demoStory(NOW), blip))
    expect(t).toContain('Computes Could not read computes: Cannot reach the Kubernetes API small')
  })
})

describe('an optional kind still loading', () => {
  it('says it is loading, not that the object is not there', () => {
    const loading: Connection = { ...LIVE, settled: false }
    const data = { ...demoStory(NOW), computes: [] }
    expect(text(page('Compute', 'small', data, loading))).toContain('Loading the Compute')
  })
})
