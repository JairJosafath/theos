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

/** The Container's page as markup. */
function containerMarkup(name: string, data: StoryData) {
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
}

/** The Container's page text, without its markup. */
function containerPage(name: string, data: StoryData) {
  return containerMarkup(name, data)
    .replace(/<[^>]+>/g, ' ')
    .replace(/\s+/g, ' ')
}

/** The addresses the page links to. */
function hrefs(markup: string) {
  return [...markup.matchAll(/href="([^"]*)"/g)].map((m) => m[1])
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

// #172: the Standard of an adopted repository arrives as a pull request a
// person merges or closes (#97); status.standardAdoption is where it is.
describe('a Container whose Standard waits to come into the repository it adopted (#172)', () => {
  const PR = 'https://git.example/webshop/orders-api/pulls/12'
  const open = {
    name: 'python-standard',
    ref: 'v1.2.0',
    branch: 'standard-adopt/python-standard-v1.2.0',
    pullRequest: 12,
    pullRequestURL: PR,
    paths: ['AGENTS.md', '.gitea/workflows/check.yaml'],
  }
  /** orders-api as a Ready Container that adopted its repository, whose Standard status doesn't record. */
  function adopting(reason: string, message: string, stopped?: string) {
    const data = demoStory(NOW)
    const container = data.containers.find((c) => c.metadata.name === 'orders-api')!
    container.spec.adopt = true
    container.status = {
      phase: 'Ready',
      repositoryURL: container.status!.repositoryURL,
      standardAdoption: { ...open, stopped },
      conditions: [{ type: 'StandardUpdated', status: 'False', reason, message }],
    }
    return data
  }

  it('links the pull request and lists the files it writes while it is open', () => {
    const data = adopting('PullRequestOpen', `Pull request ${PR} brings the files of python-standard at v1.2.0`)
    const markup = containerMarkup('orders-api', data)
    // The next step, the page's links and the Status tab's section each link it.
    expect(hrefs(markup).filter((h) => h === PR)).toHaveLength(3)
    const t = containerPage('orders-api', data)
    expect(t).toMatch(/What you do now Its Standard python-standard arrives as a pull request\./)
    expect(t).toMatch(/Open pull request #12/)
    expect(t).toMatch(/Standard pull request #12/)
    expect(t).toMatch(/Bringing in python-standard at v1\.2\.0 Its pull request writes the Standard’s files/)
    expect(t).toMatch(/The files it writes: AGENTS\.md, \.gitea\/workflows\/check\.yaml/)
    // The condition reads as a pull request that waits, not as a failure.
    expect(t).toMatch(/A pull request brings its Standard’s files into the adopted repository/)
    expect(t).not.toMatch(/then retry/)
  })

  it.each([
    ['UpdateClosed', /Its pull request was closed without merging, so no agent works on the repository/],
    ['StandardFilesDiffer', /Its pull request merged, but main holds a file the Standard protects/],
  ])('links no pull request once it stopped with %s', (stopped, sentence) => {
    const data = adopting(stopped, `Pull request ${PR} stopped`, stopped)
    const markup = containerMarkup('orders-api', data)
    expect(hrefs(markup)).not.toContain(PR)
    const t = containerPage('orders-api', data)
    expect(t).toMatch(/Bringing in python-standard at v1\.2\.0/)
    expect(t).toMatch(sentence)
    expect(t).not.toMatch(/Standard pull request #12|Open pull request #12|The files it writes/)
    expect(t).not.toMatch(/then retry/)
  })
})
