// End-to-end check of a Crew page's actions (design.md, UI actions) against a
// fake Kubernetes API serving the demo fixture: pause and resume, parallel,
// the release settings, the re-run of a failed release run, a write the API
// refuses showing its message, and with OIDC a signed-out page saying to sign
// in instead of offering the buttons.
//
// Needs a build and Chromium:  npm run test:e2e
// THEOS_UI_SHOTS=DIR also saves pictures of the page in DIR (docs/screenshots).

import assert from 'node:assert/strict'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { oidcFromEnv } from '../oidc.mjs'
import { launchChromium } from '../scripts/browser.mjs'
import { createServer, resolveUpstream } from '../server.mjs'
import { fakeApi, listen } from './fakeApi.mjs'
import { loadFixture } from './fixture.mjs'
import { RESOURCES } from '../src/k8s/types.ts'

const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')
const NOW = Date.parse('2026-06-01T12:00:00Z')
const CREW = 'orders-api'

describe('a Crew page’s actions', () => {
  let api
  let ui
  let oidcUi
  let browser
  let page
  let base
  let oidcBase
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
    const upstream = resolveUpstream({ K8S_API: await listen(api.server) })
    ui = createServer({ distDir: dist, upstream, log: () => {}, actions: 'service-account' })
    base = await listen(ui)
    // Behind an Ingress: nobody signs in here, so the issuer is never asked.
    oidcUi = createServer({
      distDir: dist,
      upstream,
      log: () => {},
      actions: 'oidc',
      oidc: oidcFromEnv({
        OIDC_ISSUER_URL: 'https://issuer.invalid',
        OIDC_CLIENT_ID: 'theos-ui',
        OIDC_CLIENT_SECRET: 'not-a-secret',
      }),
    })
    oidcBase = await listen(oidcUi)
    browser = await launchChromium()
    page = await browser.newPage({ viewport: { width: 1280, height: 1000 }, reducedMotion: 'reduce' })
    await page.clock.setFixedTime(NOW)
    page.on('pageerror', (err) => errors.push(err.message))
  })

  after(async () => {
    await browser?.close()
    for (const server of [ui, oidcUi, api?.server]) {
      server?.closeAllConnections()
      await new Promise((r) => server.close(r))
    }
  })

  const crewPage = (at) => `${at}/?ns=${namespace}&kind=Crew&name=${CREW}`
  const crew = () => api.get('crews', CREW)
  const button = (name) => page.getByRole('button', { name, exact: true })
  const releaseForm = () => page.getByRole('form', { name: `Release settings of crew ${CREW}` })

  /** Waits until the fake API's Crew passes `test`. */
  async function until(test, what) {
    for (let i = 0; i < 100; i++) {
      if (test(crew())) return
      await new Promise((r) => setTimeout(r, 50))
    }
    assert.fail(`the Crew never ${what}`)
  }

  it('pauses and resumes the Crew', async () => {
    await page.goto(crewPage(base))
    await button('Pause crew').click()
    await until((c) => c.spec.paused === true, 'paused')
    await button('Resume crew').waitFor()
    await button('Resume crew').click()
    await until((c) => c.spec.paused === false, 'resumed')
    await button('Pause crew').waitFor()
  })

  it('sets how many issues it works on at once, and shows the API server’s refusal', async () => {
    const field = page.getByLabel('Issues at once')
    await field.fill('4')
    await button('Save').click()
    await until((c) => c.spec.parallel === 4, 'took parallel 4')
    await page.getByText(/of 4 busy/).waitFor()

    await field.fill('9')
    await button('Save').click()
    const alert = page.getByRole('alert')
    await alert.waitFor()
    assert.match(await alert.innerText(), /spec\.parallel in body should be less than or equal to 5/)
    assert.equal(crew().spec.parallel, 4)
    await field.fill('4')
  })

  // Each publishReleases write tests the resourceVersion of the Crew it was worked out from.
  const tested = (ops) => {
    assert.equal(ops[0].op, 'test')
    assert.equal(ops[0].path, '/metadata/resourceVersion')
    assert.match(ops[0].value, /^\d+$/)
    return ops.slice(1)
  }

  it('changes the release settings, naming the token Secret only', async () => {
    const form = releaseForm()
    await form.getByLabel('Prepare releases').check()
    await form.getByLabel('Token Secret').fill('orders-api-release-token')
    await form.getByRole('button', { name: 'Save release settings' }).click()
    await until((c) => c.spec.prepareReleases === true, 'took prepareReleases')
    assert.deepEqual(crew().spec.publishReleases, {
      github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'orders-api-release-token' } },
    })
    assert.deepEqual(tested(api.writes.at(-1).body), [
      { op: 'add', path: '/spec/prepareReleases', value: true },
      {
        op: 'add',
        path: '/spec/publishReleases',
        value: {
          github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'orders-api-release-token' } },
        },
      },
    ])
  })

  it('shows the API server’s message when the release settings break a rule', async () => {
    const form = releaseForm()
    await form.getByLabel('Tag releases').uncheck()
    await form.getByRole('button', { name: 'Save release settings' }).click()
    const alert = form.getByRole('alert')
    await alert.waitFor()
    assert.match(await alert.innerText(), /publishReleases needs tagReleases/)
    assert.equal(crew().spec.tagReleases, true)

    // Off with publishing too, which removes it.
    await form.getByLabel('Publish releases to GitHub').uncheck()
    await form.getByRole('button', { name: 'Save release settings' }).click()
    await until((c) => c.spec.tagReleases === false && !c.spec.publishReleases, 'stopped tagging and publishing')
    assert.deepEqual(tested(api.writes.at(-1).body), [
      { op: 'add', path: '/spec/tagReleases', value: false },
      { op: 'remove', path: '/spec/publishReleases' },
    ])
    await page.getByText('The Crew tags no releases: a person tags them.').waitFor()
  })

  it('offers a re-run only once the release run failed, and asks the operator for it', async () => {
    assert.equal(await page.getByRole('button', { name: /^Re-run/ }).count(), 0)
    const failed = structuredClone(crew())
    failed.status.lastRelease.run = 'Failed'
    api.modify('crews', failed)
    const rerun = button('Re-run v0.2.0’s failed runs')
    await rerun.waitFor()
    if (process.env.THEOS_UI_SHOTS) {
      // From the top, so the sticky top bar is where it belongs.
      await page.mouse.wheel(0, -100000)
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-crew-actions.png'), fullPage: true })
    }
    await rerun.click()
    await page.getByText('Asked the operator to re-run them').waitFor()
    assert.equal(await rerun.isDisabled(), true, 'asked once, until the release’s re-run time changes')
    const asked = crew().metadata.annotations?.['theos.factory.io/rerun-release']
    assert.ok(asked && !Number.isNaN(Date.parse(asked)), `the annotation is a time: ${asked}`)
    assert.equal(api.writes.at(-1).contentType, 'application/merge-patch+json')
  })

  it('says to sign in, instead of offering the buttons, when nobody is signed in with OIDC', async () => {
    await page.goto(crewPage(oidcBase))
    await page.getByText('Sign in to pause this Crew or change its settings.').waitFor()
    const link = page.locator('.crew-sign-in').getByRole('link', { name: 'Sign in' })
    assert.match(await link.getAttribute('href'), /^\/ui\/login\?return=/)
    for (const name of ['Pause crew', 'Resume crew', 'Save', 'Save release settings']) {
      assert.equal(await button(name).count(), 0, `no ${name} button`)
    }
    assert.equal(await page.getByRole('button', { name: /^Re-run/ }).count(), 0)
    if (process.env.THEOS_UI_SHOTS) {
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-crew-sign-in.png') })
    }
    assert.deepEqual(errors, [])
  })
})
