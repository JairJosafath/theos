// End-to-end check of the Brief page's actions against a fake Kubernetes API
// serving the demo fixture, through the UI server's action table: each
// setting (autopilot, parallel, releases) and each one-click answer (take or
// skip a suggestion; retry, close or wait on a failed issue) sends exactly its
// JSON patch, a refused write shows the API server's message, and in OIDC mode
// a signed-out page says to sign in instead of offering the buttons.
//
// Needs a build and Chromium:  npm run test:e2e
// THEOS_UI_SHOTS=DIR also saves pictures of the page in DIR (docs/screenshots).

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

describe('the Brief page’s actions', () => {
  let api
  let ui
  let signedOut
  let browser
  let page
  let base
  let oidcBase
  let namespace
  let apiUrl
  const errors = []

  before(async () => {
    const { demoStory } = await loadFixture()
    const story = demoStory(NOW)
    namespace = story.namespace
    api = fakeApi(namespace)
    const PLURAL = { project: RESOURCES.projects, systems: RESOURCES.softwareSystems }
    for (const [k, v] of Object.entries(story)) {
      const plural = PLURAL[k] ?? RESOURCES[k] ?? (Object.values(RESOURCES).includes(k) ? k : undefined)
      if (!plural || !v) continue
      for (const obj of Array.isArray(v) ? v : [v]) {
        const copy = structuredClone(obj)
        // A failed issue Needs you doesn't list, answered in the failed list.
        if (copy.kind === 'Brief' && copy.metadata.name === 'webshop') {
          copy.status.failed = [
            { container: 'storefront', number: 3, kind: 'failed', title: 'Reorder button on past orders' },
            { container: 'orders-api', number: 15, kind: 'failed', title: "List a customer's past orders" },
          ]
          copy.status.failedWaiting = 2
        }
        api.add(plural, copy)
      }
    }
    apiUrl = await listen(api.server)
    const upstream = resolveUpstream({ K8S_API: apiUrl })
    ui = createServer({ distDir: dist, upstream, actions: 'service-account', log: () => {} })
    base = await listen(ui)
    // OIDC on and nobody signed in; no issuer is needed to show that.
    signedOut = createServer({ distDir: dist, upstream, actions: 'oidc', oidc: undefined, log: () => {} })
    oidcBase = await listen(signedOut)
    browser = await launchChromium()
    page = await browser.newPage({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' })
    await page.clock.setFixedTime(NOW)
    page.on('pageerror', (err) => errors.push(err.message))
  })

  after(async () => {
    await browser?.close()
    for (const s of [ui, signedOut, api?.server]) s?.closeAllConnections()
    await Promise.all([ui, signedOut, api.server].map((s) => new Promise((r) => s.close(r))))
  })

  const open = async (at = base) => {
    await page.goto(`${at}/?ns=${namespace}&kind=Brief&name=webshop`)
    await page.getByRole('region', { name: 'Brief webshop', exact: true }).waitFor({ timeout: 10000 })
  }
  const settings = () => page.getByRole('region', { name: 'Settings of brief webshop' })
  const button = (name) => page.getByRole('button', { name, exact: true })

  /** Resolves to the next write the fake API gets after `count` writes. */
  async function nextWrite(count) {
    for (let i = 0; i < 100 && api.writes.length <= count; i++) await new Promise((r) => setTimeout(r, 50))
    assert.ok(api.writes.length > count, 'the fake API got no write')
    return api.writes[count]
  }

  /** Clicks, and resolves to the JSON patch ops the API got for it. */
  async function patchOf(click) {
    const count = api.writes.length
    await click()
    const write = await nextWrite(count)
    assert.equal(write.method, 'PATCH')
    assert.equal(write.path, `/apis/theos.factory.io/v1beta1/namespaces/${namespace}/briefs/webshop`)
    assert.equal(write.contentType, 'application/json-patch+json')
    return write.body
  }

  it('sets the autopilot mode, and shows the new one pressed', async () => {
    await open()
    const group = settings().getByRole('group', { name: 'Autopilot' })
    assert.equal(await group.getByRole('button', { name: 'Ask', exact: true }).getAttribute('aria-pressed'), 'true')
    const ops = await patchOf(() => group.getByRole('button', { name: 'On', exact: true }).click())
    assert.deepEqual(ops, [{ op: 'add', path: '/spec/autopilot', value: 'On' }])
    await group.locator('[aria-pressed="true"]', { hasText: 'On' }).waitFor({ timeout: 5000 })
  })

  it('sets parallel', async () => {
    await settings()
      .getByLabel(/^Parallel/)
      .fill('4')
    const ops = await patchOf(() => settings().getByRole('button', { name: 'Set parallel' }).click())
    assert.deepEqual(ops, [{ op: 'add', path: '/spec/parallel', value: 4 }])
    await settings()
      .getByText(/works on at once: 4\./)
      .waitFor({ timeout: 5000 })
  })

  it('publishes an app’s releases, and stops', async () => {
    const add = settings().getByRole('form', { name: "Publish an app's releases" })
    await add.getByLabel('App', { exact: true }).selectOption('storefront')
    await add.getByLabel(/^GitHub repository/).fill('webshop-co/storefront')
    await add.getByLabel(/^Token Secret/).fill('github-token')
    await add.getByLabel(/^Tag prefix/).fill('web-')
    const entry = {
      container: 'storefront',
      github: { repository: 'webshop-co/storefront', tokenSecretRef: { name: 'github-token' } },
      tagPrefix: 'web-',
    }
    // Each releases write tests the resourceVersion of the Brief it was worked out from.
    const tested = (ops) => {
      assert.equal(ops[0].op, 'test')
      assert.equal(ops[0].path, '/metadata/resourceVersion')
      assert.match(ops[0].value, /^\d+$/)
      return ops.slice(1)
    }
    let ops = await patchOf(() => add.getByRole('button', { name: 'Publish its releases' }).click())
    assert.deepEqual(tested(ops), [{ op: 'add', path: '/spec/releases', value: [entry] }])
    const remove = settings().getByRole('button', { name: 'Stop publishing storefront' })
    await remove.waitFor({ timeout: 5000 })
    if (process.env.THEOS_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-brief-settings.png'), fullPage: true })
    }
    ops = await patchOf(() => remove.click())
    assert.deepEqual(tested(ops), [{ op: 'add', path: '/spec/releases', value: [] }])
    await remove.waitFor({ state: 'detached', timeout: 5000 })
  })

  it('shows the API server’s message when it refuses a setting', async () => {
    const message =
      'Brief.theos.factory.io "webshop" is invalid: spec.parallel: Invalid value: 9: spec.parallel in body should be less than or equal to 5'
    api.refuseNextWrite(message)
    await settings()
      .getByLabel(/^Parallel/)
      .fill('9')
    await patchOf(() => settings().getByRole('button', { name: 'Set parallel' }).click())
    const alert = settings().getByRole('alert')
    await alert.waitFor({ timeout: 5000 })
    assert.equal((await alert.textContent()).trim(), message)
  })

  // Each answer appends the prompt worded from its item, and the item then says it is answered.
  const ANSWERS = [
    ['Take orders-api #17', 'Take suggestion #17 in orders-api: Reject orders with a negative quantity.'],
    ['Skip storefront #4', 'Skip suggestion #4 in storefront: Escape the product names on the list page.'],
    ['Retry storefront #3', 'Retry issue #3 in storefront: Reorder button on past orders.'],
    ['Close orders-api #15', "Close issue #15 in orders-api: List a customer's past orders."],
  ]
  for (const [name, prompt] of ANSWERS) {
    it(`answers with one click: ${name}`, async () => {
      const ops = await patchOf(() => button(name).click())
      assert.deepEqual(ops, [{ op: 'add', path: '/spec/prompts/-', value: prompt }])
      await button(name).waitFor({ state: 'detached', timeout: 5000 })
      // The Brief's prompts list it, folded away once the list is long.
      await page.getByText(prompt, { exact: true }).first().waitFor({ state: 'attached', timeout: 5000 })
    })
  }

  it('offers the answers again once a round took the prompt, and waits', async () => {
    await page.getByText('Answered: Retry, for the next round').waitFor({ timeout: 5000 })
    // The storefront #3 answer recommended by the tending round is Wait.
    const at = `${apiUrl}/apis/theos.factory.io/v1beta1/namespaces/${namespace}/briefs/webshop`
    const webshop = await (await fetch(at)).json()
    api.modify('briefs', { ...webshop, status: { ...webshop.status, promptsPlanned: webshop.spec.prompts.length } })
    const wait = button('Wait storefront #3, as it would')
    await wait.waitFor({ timeout: 5000 })
    if (process.env.THEOS_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-brief-answers.png'), fullPage: true })
    }
    const ops = await patchOf(() => wait.click())
    assert.deepEqual(ops, [
      { op: 'add', path: '/spec/prompts/-', value: 'Wait on issue #3 in storefront: Reorder button on past orders.' },
    ])
    await page.getByText('Answered: Wait, for the next round').waitFor({ timeout: 5000 })
  })

  it('shows the API server’s message when it refuses an answer', async () => {
    const message = 'Brief.theos.factory.io "webshop" is invalid: spec.prompts: Too many: 51: must have at most 50 items'
    api.refuseNextWrite(message)
    await patchOf(() => button('Retry orders-api #15').click())
    // orders-api #15 was closed above and that prompt was taken; Retry is offered again.
    const alert = page.getByRole('alert').filter({ hasText: 'Too many' })
    await alert.waitFor({ timeout: 5000 })
    assert.equal((await alert.textContent()).trim(), message)
    await button('Retry orders-api #15').waitFor({ timeout: 5000 })
  })

  it('says to sign in, signed out in OIDC mode, instead of offering an answer or a setting', async () => {
    const writes = api.writes.length
    await open(oidcBase)
    await page.getByText('to answer here.').first().waitFor({ timeout: 5000 })
    await settings().getByText('to change these settings.').waitFor({ timeout: 5000 })
    assert.equal(await page.getByRole('button', { name: /^(Retry|Close|Wait|Take|Skip) / }).count(), 0)
    assert.equal(await settings().getByRole('button', { name: 'Set parallel' }).count(), 0)
    for (const button of await settings().getByRole('group', { name: 'Autopilot' }).getByRole('button').all())
      assert.equal(await button.isDisabled(), true)
    const signIn = settings().getByRole('link', { name: 'Sign in' })
    assert.match(await signIn.getAttribute('href'), /^\/ui\/login\?return=/)
    if (process.env.THEOS_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-brief-signed-out.png'), fullPage: true })
    }
    assert.equal(api.writes.length, writes)
  })

  it('raises no page error', () => {
    assert.deepEqual(errors, [])
  })
})
