import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import type { BriefWriter } from '../../k8s/briefs'
import type { ContainerWriter } from '../../k8s/containers'
import type { CrewWriter } from '../../k8s/crews'
import type { Connection, StoryData } from '../../k8s/sources'
import type { StandardWriter } from '../../k8s/standards'
import { demoStory } from '../../demo/fixture'
import { absoluteTime } from '../../model/format'
import { ObjectPage } from './ObjectPage'

const NOW = Date.parse('2026-06-01T12:00:00Z')
const LIVE: Connection = { phase: 'live', loaded: true, problems: [] }

beforeAll(() => {
  vi.stubGlobal('window', { location: { pathname: '/', search: '', hash: '' } })
  absoluteTime(NOW)
})
afterAll(() => {
  vi.unstubAllGlobals()
})

/** The Container's page text, without its markup. */
function containerPage(name: string, data: StoryData) {
  return renderToStaticMarkup(
    createElement(ObjectPage, {
      kind: 'Container',
      name,
      data,
      connection: LIVE,
      writer: {} as BriefWriter,
      containerWriter: {} as ContainerWriter,
      crewWriter: {} as CrewWriter,
      standardWriter: {} as StandardWriter,
    }),
  )
    .replace(/<[^>]+>/g, ' ')
    .replace(/\s+/g, ' ')
}

describe('a Container’s Domain versions', () => {
  it('reads a Domain’s ref from the last scaffoldedFrom entry of its name, not a Connector’s before it (#1933)', () => {
    const data = demoStory(NOW)
    const container = data.containers.find((c) => c.metadata.name === 'orders-api')!
    // A Connector named like the Domain, at another ref, scaffolded before it.
    container.status!.scaffoldedFrom = ['python-fastapi@v1.3.0', 'webshop@v0.3.0', 'webshop@v1.0.0']
    container.status!.scaffoldedDomains = ['webshop']
    const t = containerPage('orders-api', data)
    expect(t).toMatch(/Domains webshop @ v1\.0\.0 Scaffolded from/)
    expect(t).not.toMatch(/Domains webshop @ v0\.3\.0/)
  })
})
