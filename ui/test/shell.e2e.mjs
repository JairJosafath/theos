// End-to-end check of the new shell (?shell=next) against a fake Kubernetes
// API serving the demo fixture: each area opens by its link and Back returns
// through them, every area is reachable by keyboard, and the old layout links
// nowhere into the shell.
//
// Needs a build and Chromium:  npm run test:e2e
// C4_UI_SHOTS=DIR also saves a picture of each area in DIR (docs/screenshots).

import assert from 'node:assert/strict'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { launchChromium } from '../scripts/browser.mjs'
import { createServer, resolveUpstream } from '../server.mjs'
import { fakeApi, listen } from './fakeApi.mjs'
import { loadFixture } from './fixture.mjs'
import { RESOURCES } from '../src/k8s/types.ts'

const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')
const NOW = Date.parse('2026-06-01T12:00:00Z')

/** Each area's link text, its `area=`, and a heading only that area shows. */
const AREAS = [
  ['Needs you', 'needs', 'Needs you'],
  ['Briefs', 'briefs', 'Briefs'],
  ['Story', 'story', 'System context: the big picture'],
  ['Work', 'work', 'Tasks'],
  ['Factory', 'factory', 'Agent slots'],
  ['About', 'about', 'About this factory'],
]

describe('the new shell', () => {
  let api
  let ui
  let browser
  let page
  let base
  let namespace
  let session
  const errors = []
  const requests = []

  before(async () => {
    const { demoStory, demoFactory } = await loadFixture()
    const story = demoStory(NOW)
    namespace = story.namespace
    session = story.sessions[0].metadata.name
    api = fakeApi(namespace)
    // Story keys that differ from the plural.
    const PLURAL = { project: RESOURCES.projects, systems: RESOURCES.softwareSystems }
    for (const data of [story, demoFactory(NOW)]) {
      for (const [k, v] of Object.entries(data)) {
        const plural = PLURAL[k] ?? RESOURCES[k] ?? (Object.values(RESOURCES).includes(k) ? k : undefined)
        if (!plural || !v) continue
        for (const obj of Array.isArray(v) ? v : [v]) api.add(plural, structuredClone(obj))
      }
    }
    ui = createServer({
      distDir: dist,
      upstream: resolveUpstream({ K8S_API: await listen(api.server) }),
      log: () => {},
    })
    base = await listen(ui)
    browser = await launchChromium()
    page = await browser.newPage({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' })
    // The fixture's clock, so its times read as they do in demo mode.
    await page.clock.setFixedTime(NOW)
    page.on('pageerror', (err) => errors.push(err.message))
    page.on('request', (req) => requests.push(req.url()))
  })

  after(async () => {
    await browser?.close()
    ui?.closeAllConnections()
    api?.server.closeAllConnections()
    await new Promise((r) => ui.close(r))
    await new Promise((r) => api.server.close(r))
  })

  const nav = () => page.getByRole('navigation', { name: 'Areas' })
  const current = () => nav().locator('[aria-current="page"]')
  const areaOf = () => new URL(page.url()).searchParams.get('area')
  const heading = (text) => page.getByRole('heading', { name: text, exact: true }).first()

  it('opens on Needs you while something waits on a person, with the freshness line', async () => {
    await page.goto(`${base}/?shell=next`)
    await page.locator('.need').first().waitFor({ timeout: 10000 })
    assert.equal(await current().textContent(), 'Needs you')
    assert.equal(areaOf(), 'needs')
    const fresh = page.locator('.freshness')
    await fresh.getByText('Live').waitFor({ timeout: 5000 })
    await fresh.getByText(/^Updated /).waitFor({ timeout: 5000 })
    await fresh.getByText('All namespaces').waitFor({ timeout: 5000 })
    await page.goto(`${base}/?shell=next&area=story`)
    await page.locator('.freshness').getByText(`Namespace ${namespace}`).waitFor({ timeout: 5000 })
  })

  it('opens each area by its link, and goes back with Back', async () => {
    for (const [label, area, title] of AREAS) {
      await nav().getByRole('link', { name: label, exact: true }).click()
      await heading(title).waitFor({ timeout: 10000 })
      assert.equal(areaOf(), area)
      assert.equal(await current().textContent(), label)
      assert.match(await page.title(), new RegExp(`^${label} · C4 Factory$`))
      if (process.env.C4_UI_SHOTS) {
        await page.waitForTimeout(300)
        await page.screenshot({ path: path.join(process.env.C4_UI_SHOTS, `ui-shell-${area}.png`) })
      }
    }
    for (const [label, area, title] of AREAS.slice(0, -1).reverse()) {
      await page.goBack()
      await heading(title).waitFor({ timeout: 10000 })
      assert.equal(areaOf(), area)
      assert.equal(await current().textContent(), label)
    }
  })

  it('says every namespace in Work, and none in About', async () => {
    await page.goto(`${base}/?shell=next&area=work`)
    await page.locator('.freshness').getByText('All namespaces').waitFor({ timeout: 5000 })
    await page.goto(`${base}/?shell=next&area=about`)
    await heading('About this factory').waitFor({ timeout: 5000 })
    assert.equal(
      await page
        .locator('.freshness')
        .getByText(/Namespace|All namespaces/)
        .count(),
      0,
    )
  })

  it('reaches every area by keyboard', async () => {
    await page.goto(`${base}/?shell=next`)
    await page.locator('.need').first().waitFor({ timeout: 10000 })
    for (const [label, area, title] of AREAS) {
      // Tab forward from wherever the focus is until the area's link has it.
      let focused = ''
      for (let i = 0; i < 30 && focused !== label; i++) {
        await page.keyboard.press('Tab')
        focused = await page.evaluate(() => {
          const el = globalThis.document.activeElement
          return el?.closest('nav[aria-label="Areas"]') ? el.textContent : ''
        })
      }
      assert.equal(focused, label, `Tab reaches ${label}`)
      await page.keyboard.press('Enter')
      await heading(title).waitFor({ timeout: 10000 })
      assert.equal(areaOf(), area)
    }
  })

  it('shows the versions and where to read about them in About', async () => {
    await page.goto(`${base}/?shell=next&area=about`)
    const about = page.locator('.about')
    await about.getByText('c4.factory.io/v1alpha1').waitFor({ timeout: 5000 })
    const href = (name) => about.getByRole('link', { name }).getAttribute('href')
    // The test build is unreleased, so main's docs and every release.
    assert.equal(await href('Release notes'), 'https://github.com/JairJosafath/c4factory/releases')
    assert.match(await href(/The design/), /\/blob\/main\/docs\/design\.md$/)
    assert.equal(await href('The repository'), 'https://github.com/JairJosafath/c4factory')
  })

  it('keeps an object route in the URL', async () => {
    await page.goto(`${base}/?shell=next&ns=${namespace}&kind=Container&name=orders-api`)
    await heading('orders-api').waitFor({ timeout: 5000 })
    assert.equal(await current().count(), 0)
    await nav().getByRole('link', { name: 'Work', exact: true }).click()
    await heading('Tasks').waitFor({ timeout: 10000 })
    const params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('kind'), params.get('name'), params.get('ns')], [null, null, null])
  })

  it('opens a Brief page from its card, and a Container page from what the Brief made', async () => {
    await page.goto(`${base}/?shell=next&area=briefs`)
    await page.getByRole('link', { name: 'webshop', exact: true }).click()
    const header = page.getByRole('region', { name: 'Brief webshop', exact: true })
    await header.waitFor({ timeout: 10000 })
    let params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('ns'), params.get('kind'), params.get('name')], [namespace, 'Brief', 'webshop'])
    assert.match(await page.title(), /^Brief webshop · C4 Factory$/)
    // The lifecycle with Planned current, the rounds, and its writes: pause and prompt.
    assert.equal(await header.locator('[aria-current="step"]').textContent(), 'Planned')
    await header.getByText(/^Round 1 applied$/).waitFor({ timeout: 5000 })
    await header.getByRole('button', { name: /^(Pause|Resume) brief webshop$/ }).waitFor({ timeout: 5000 })
    await page.getByRole('heading', { name: 'Rounds' }).first().waitFor({ timeout: 5000 })
    if (process.env.C4_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.C4_UI_SHOTS, 'ui-object-brief.png'), fullPage: true })
    }

    // It made a Container and a Crew both named storefront.
    await header.locator('a[href*="kind=Container"]', { hasText: /^storefront$/ }).click()
    const container = page.getByRole('region', { name: 'Container storefront', exact: true })
    await container.waitFor({ timeout: 10000 })
    params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('kind'), params.get('name')], ['Container', 'storefront'])
    // Failed, with the condition's reason and the exact retry line.
    assert.equal(await container.locator('[aria-current="step"]').textContent(), 'Failed')
    await container.getByText('Choices valid: no · UnsupportedType').waitFor({ timeout: 5000 })
    assert.equal(
      await container.locator('.command-line code').textContent(),
      `kubectl -n ${namespace} annotate containers.c4.factory.io storefront c4.factory.io/retry=$(date +%s) --overwrite`,
    )
    await container.getByRole('link', { name: 'Made by Brief webshop' }).waitFor({ timeout: 5000 })
    if (process.env.C4_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.C4_UI_SHOTS, 'ui-object-container.png'), fullPage: true })
    }

    // Back returns through both pages to the Briefs.
    await page.goBack()
    await header.waitFor({ timeout: 10000 })
    await page.goBack()
    await heading('Briefs').waitFor({ timeout: 10000 })
  })

  it('opens a Ready Container page with its relationships and templates', async () => {
    await page.goto(`${base}/?shell=next&ns=${namespace}&kind=Container&name=orders-api`)
    const container = page.getByRole('region', { name: 'Container orders-api', exact: true })
    await container.waitFor({ timeout: 10000 })
    assert.equal(await container.locator('[aria-current="step"]').textContent(), 'Ready')
    await container
      .getByText(/Applied: True, AppliedAtScaffold/)
      .first()
      .waitFor({ timeout: 5000 })
    await page.getByRole('heading', { name: 'What it is made from' }).waitFor({ timeout: 5000 })
    assert.equal(await container.locator('.command-line').count(), 0)
  })

  it('opens a Crew page from Work, and a Task page from the Crew', async () => {
    await page.goto(`${base}/?shell=next&area=work`)
    await page.getByRole('link', { name: 'Orders API crew', exact: true }).click()
    const crew = page.getByRole('region', { name: 'Crew orders-api', exact: true })
    await crew.waitFor({ timeout: 10000 })
    let params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('ns'), params.get('kind'), params.get('name')], [namespace, 'Crew', 'orders-api'])
    assert.match(await page.title(), /^Crew orders-api · C4 Factory$/)
    // Working, its Tasks, the places in use and its last release.
    assert.equal(await crew.locator('[aria-current="step"]').textContent(), 'Working')
    await page.getByText('2 of 2 busy').waitFor({ timeout: 5000 })
    await heading('Last release: v0.2.0').waitFor({ timeout: 5000 })
    await page.locator('dd', { hasText: /^succeeded Open the run/ }).waitFor({ timeout: 5000 })
    if (process.env.C4_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.C4_UI_SHOTS, 'ui-object-crew.png'), fullPage: true })
    }

    await crew.getByRole('link', { name: 'orders-api-13', exact: true }).click()
    const task = page.getByRole('region', { name: 'Task orders-api-13', exact: true })
    await task.waitFor({ timeout: 10000 })
    params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('kind'), params.get('name')], ['Task', 'orders-api-13'])
    // Failed, with its issue to answer, the history, its agent runs and the handoffs.
    assert.equal(await task.locator('[aria-current="step"]').textContent(), 'Failed')
    await task.getByRole('link', { name: 'Open issue #13' }).waitFor({ timeout: 5000 })
    await task.getByText('Failed, round 3 · RoundsExhausted').waitFor({ timeout: 5000 })
    // Three implement runs and the one review of round 1.
    await task.getByText('run 4', { exact: true }).waitFor({ timeout: 5000 })
    assert.equal(await task.getByText('run 5', { exact: true }).count(), 0)
    await page.getByText('3 of 3', { exact: true }).waitFor({ timeout: 5000 })
    await page.getByText('2 times an agent run handed off to a fresh one at the token limit of 130k').waitFor({
      timeout: 5000,
    })
    // The Task recorded its runs, so the page names the one that handed off.
    await page.getByText('Run 3 (round 2, implement) handed off 2 times, at 131k and 138k tokens.').waitFor({
      timeout: 5000,
    })
    await task.getByText(/round 2, implement by claude-implementer · ended .* · handed off 2 times/).waitFor({
      timeout: 5000,
    })
    assert.equal(await page.locator('.command-line code').count(), 0)
    await task.getByRole('link', { name: 'Crew orders-api' }).waitFor({ timeout: 5000 })
    if (process.env.C4_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.C4_UI_SHOTS, 'ui-object-task.png'), fullPage: true })
    }

    await page.goBack()
    await crew.waitFor({ timeout: 10000 })
  })

  it('opens a Task page from the Task panel in Work', async () => {
    await page.goto(`${base}/?shell=next&area=work&task=${namespace}/orders-api-14`)
    await page.getByRole('link', { name: 'Open the Task’s page' }).click()
    const task = page.getByRole('region', { name: 'Task orders-api-14', exact: true })
    await task.waitFor({ timeout: 10000 })
    // The region shows "Loading the Task…" before the Task arrives: textContent
    // waits for the one current step (strict, so two would fail) where count would not.
    assert.equal(await task.locator('[aria-current="step"]').textContent({ timeout: 10000 }), 'Implementing')
  })

  it('gives a paused Crew the patch that resumes it', async () => {
    await page.goto(`${base}/?shell=next&ns=${namespace}&kind=Crew&name=storefront`)
    const crew = page.getByRole('region', { name: 'Crew storefront', exact: true })
    await crew.waitFor({ timeout: 10000 })
    assert.equal(await crew.locator('[aria-current="step"]').textContent(), 'Paused')
    assert.equal(
      await crew.locator('.command-line code').textContent(),
      `kubectl -n ${namespace} patch crews.c4.factory.io storefront --type merge -p '{"spec":{"paused":false}}'`,
    )
  })

  it('opens a read-only page of each remaining kind', async () => {
    const PAGES = [
      ['Project', namespace, 'Its catalog and Git hosts'],
      ['SoftwareSystem', 'webshop-core', 'What it is for'],
      ['Relationship', 'orders-api-to-cart-cache', 'Its ends'],
      ['Session', session, 'Who and when'],
      ['Standard', 'python-standard', 'Its template'],
      ['Compute', 'small', 'Its template'],
      ['Deployment', 'k8s-manifests', 'Its template'],
      ['GitProvider', 'gitea', 'Where repositories go'],
    ]
    for (const [kind, name, section] of PAGES) {
      await page.goto(`${base}/?shell=next&ns=${namespace}&kind=${kind}&name=${name}`)
      await page.getByRole('region', { name: `${kind} ${name}`, exact: true }).waitFor({ timeout: 10000 })
      await page.getByRole('heading', { name: section }).first().waitFor({ timeout: 5000 })
      assert.match(await page.title(), new RegExp(`^${kind} ${name} · C4 Factory$`))
      if (process.env.C4_UI_SHOTS) {
        await page.waitForTimeout(300)
        await page.screenshot({
          path: path.join(process.env.C4_UI_SHOTS, `ui-object-${kind.toLowerCase()}.png`),
          fullPage: true,
        })
      }
    }
  })

  it('walks from a Container to its Standard, and up to the Project', async () => {
    await page.goto(`${base}/?shell=next&ns=${namespace}&kind=Container&name=orders-api`)
    await page.getByRole('link', { name: 'python-standard', exact: true }).click()
    const standard = page.getByRole('region', { name: 'Standard python-standard', exact: true })
    await standard.waitFor({ timeout: 10000 })
    await standard.getByRole('link', { name: 'orders-api', exact: true }).waitFor({ timeout: 5000 })
    await standard.getByRole('link', { name: `Project ${namespace}` }).click()
    const project = page.getByRole('region', { name: `Project ${namespace}`, exact: true })
    await project.waitFor({ timeout: 10000 })
    await project.getByRole('link', { name: 'webshop-core', exact: true }).waitFor({ timeout: 5000 })
  })

  it('shows a GitProvider’s Secret by name, and never reads a Secret', async () => {
    await page.goto(`${base}/?shell=next&ns=${namespace}&kind=GitProvider&name=gitea`)
    const header = page.getByRole('region', { name: 'GitProvider gitea', exact: true })
    await header.waitFor({ timeout: 10000 })
    await page.getByText('the UI never reads Secrets').waitFor({ timeout: 5000 })
    assert.match(await page.locator('main').textContent(), /Secret gitea-credentials/)
    assert.deepEqual(
      requests.filter((u) => /secrets/i.test(u)),
      [],
      'no request for a Secret',
    )
  })

  it('says a refused read of an optional kind in its panel', async () => {
    api.refuse('standards')
    await page.goto(`${base}/?shell=next&ns=${namespace}&kind=Standard&name=python-standard`)
    await page.getByText(/^Could not read standards \(403\)/).waitFor({ timeout: 10000 })
    await page.goto(`${base}/?shell=next&ns=${namespace}&kind=Project&name=${namespace}`)
    const catalog = page.getByRole('region', { name: 'Its catalog and Git hosts' })
    await catalog.getByText(/^Could not read standards \(403\)/).waitFor({ timeout: 10000 })
    await catalog.getByRole('link', { name: 'small', exact: true }).waitFor({ timeout: 5000 })
    if (process.env.C4_UI_SHOTS) {
      await page.waitForTimeout(300)
      await catalog.screenshot({ path: path.join(process.env.C4_UI_SHOTS, 'ui-object-refused-read.png') })
    }
  })

  it('is not linked from the old layout', async () => {
    for (const q of ['', '?view=containers', '?view=factory']) {
      await page.goto(`${base}/${q}`)
      await page.locator('.status', { hasText: 'Live' }).waitFor({ timeout: 10000 })
      assert.equal(await page.locator('nav[aria-label="Areas"]').count(), 0)
      const links = await page.locator('a[href]').evaluateAll((as) => as.map((a) => a.getAttribute('href')))
      assert.deepEqual(
        links.filter((h) => h.includes('shell')),
        [],
        `no link into the shell from ${q || 'the story'}`,
      )
    }
  })

  it('raises no page error', () => {
    assert.deepEqual(errors, [])
  })
})
