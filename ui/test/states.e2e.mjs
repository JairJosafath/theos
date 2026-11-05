// End-to-end check of the shell's per-panel states against a
// fake Kubernetes API serving the demo fixture, with some kinds refused as
// RBAC would: each panel that shows a refused kind says so with the fix, the
// rest of the area still shows, and no banner speaks for the whole page.
//
// Needs a build and Chromium:  npm run test:e2e
// THEOS_UI_SHOTS=DIR also saves pictures of the refused reads in DIR (docs/screenshots).

import assert from 'node:assert/strict'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { launchChromium } from '../scripts/browser.mjs'
import { createServer, resolveUpstream } from '../server.mjs'
import { RESOURCES } from '../src/k8s/types.ts'
import { listen } from './fakeApi.mjs'
import { fixtureApi } from './fixture.mjs'

const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')
const NOW = Date.parse('2026-06-01T12:00:00Z')

describe('the shell with refused reads', () => {
  let api
  let ui
  let browser
  let page
  let base
  const errors = []

  before(async () => {
    const fixture = await fixtureApi(NOW)
    api = fixture.api
    // Tasks are required, Crews and Sessions are read by panels beside others.
    for (const plural of [RESOURCES.tasks, RESOURCES.crews, RESOURCES.sessions]) api.refuse(plural)
    ui = createServer({
      distDir: dist,
      upstream: resolveUpstream({ K8S_API: await listen(api.server) }),
      log: () => {},
    })
    base = await listen(ui)
    browser = await launchChromium()
    page = await browser.newPage({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' })
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

  const shot = async (name) => {
    if (!process.env.THEOS_UI_SHOTS) return
    await page.waitForTimeout(300)
    await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, name), fullPage: true })
  }
  const panel = (title) =>
    page.locator('.panel').filter({ has: page.getByRole('heading', { name: title, exact: true }) })
  const refusal = (what) => page.getByRole('alert').filter({ hasText: `Could not read ${what}.` })

  it('says each refused kind in the Work panel that shows it, and no banner', async () => {
    await page.goto(`${base}/?area=work`)
    await panel('Tasks').getByRole('alert').getByText('Could not read tasks.').waitFor({ timeout: 10000 })
    assert.match(await refusal('tasks').first().textContent(), /The UI is not allowed to read these/)
    // Neither "loading" forever nor "no Tasks yet".
    assert.equal(
      await panel('Tasks')
        .getByText(/Loading the Tasks|No Tasks yet/)
        .count(),
      0,
    )
    // Sessions only feed Needs a look, which says the failure that may hide its items.
    const look = page.getByRole('region', { name: 'Needs a look' })
    await look.getByRole('alert').getByText('Could not read sessions.').waitFor({ timeout: 5000 })
    // The Crews are Work's second tab (docs/plans/done/ui-shell.md, step 2).
    await page.getByRole('tab', { name: /^Crews/ }).click()
    await panel('Crews').getByRole('alert').getByText('Could not read crews.').waitFor({ timeout: 10000 })
    assert.equal(
      await panel('Crews')
        .getByText(/No Crews yet/)
        .count(),
      0,
    )
    assert.equal(await page.locator('.banner-problem').count(), 0)
    await shot('ui-states-work-refused.png')
  })

  it('says it in Needs you, which a refused kind may hide items from', async () => {
    await page.goto(`${base}/?area=needs`)
    const needs = page.getByRole('region', { name: 'Needs you' })
    await needs.getByRole('alert').waitFor({ timeout: 10000 })
    const said = await needs.getByRole('alert').textContent()
    for (const what of ['tasks', 'sessions', 'crews']) assert.match(said, new RegExp(`Could not read ${what}\\.`))
    assert.equal(await needs.getByText('Nothing waits on you.').count(), 0)
    assert.equal(await page.locator('.banner-problem').count(), 0)
  })

  it('says it in Agent slots, and still shows the health beside it', async () => {
    await page.goto(`${base}/?area=factory`)
    await panel('Agent slots').getByRole('alert').getByText('Could not read tasks.').waitFor({ timeout: 10000 })
    await panel('Health').waitFor({ timeout: 5000 })
    assert.equal(await panel('Agent slots').getByText('No sandbox is running.').count(), 0)
  })

  it('says it beside the diagram in Story, which still draws', async () => {
    await page.goto(`${base}/?area=story`)
    await page.getByRole('region', { name: 'Who is working right now' }).getByRole('alert').waitFor({ timeout: 10000 })
    const working = await page.getByRole('region', { name: 'Who is working right now' }).textContent()
    for (const what of ['sessions', 'crews', 'tasks']) assert.match(working, new RegExp(`Could not read ${what}\\.`))
    await page.locator('.node').first().waitFor({ timeout: 5000 })
    assert.equal(await page.locator('.banner-problem').count(), 0)
    await shot('ui-states-story-refused.png')
  })

  it('had no page errors', () => {
    assert.deepEqual(errors, [])
  })
})
