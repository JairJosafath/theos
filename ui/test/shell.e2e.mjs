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
  const errors = []

  before(async () => {
    const { demoStory, demoFactory } = await loadFixture()
    const story = demoStory(NOW)
    namespace = story.namespace
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

  it('opens on Story, with the freshness line', async () => {
    await page.goto(`${base}/?shell=next`)
    await heading('System context: the big picture').waitFor({ timeout: 10000 })
    assert.equal(await current().textContent(), 'Story')
    const fresh = page.locator('.freshness')
    await fresh.getByText('Live').waitFor({ timeout: 5000 })
    await fresh.getByText(/^Updated /).waitFor({ timeout: 5000 })
    await fresh.getByText(`Namespace ${namespace}`).waitFor({ timeout: 5000 })
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
    await page.goBack()
    await heading('System context: the big picture').waitFor({ timeout: 10000 })
    assert.equal(areaOf(), null)
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
    await heading('System context: the big picture').waitFor({ timeout: 10000 })
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
