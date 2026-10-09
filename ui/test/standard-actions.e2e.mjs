// The Standard page's ref bump in the browser (design.md, UI actions): the
// dialog that counts the Containers getting an update pull request, cancel,
// confirm, a ref the API server refuses, and in OIDC mode with nobody signed
// in, a page that says to sign in. A fake Kubernetes API, server.mjs with
// actions on in front of it, and the built UI in Chromium.
//
// Needs a build and Chromium:  npm run test:e2e
// C4_UI_SHOTS=DIR also saves the dialog, the refusal and the signed-out page in
// DIR (docs/screenshots).

import assert from 'node:assert/strict'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { launchChromium } from '../scripts/browser.mjs'
import { createServer, resolveUpstream } from '../server.mjs'
import { fakeApi, listen } from './fakeApi.mjs'

const NS = 'webshop'
const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')
const PAGE = `/?project=${NS}&ns=${NS}&kind=Standard&name=python-standard`

function container(name, phase, standard = 'python-standard') {
  return {
    metadata: { name },
    spec: {
      softwareSystemRef: { name: 'webshop-core' },
      type: 'service',
      gitProviderRef: { name: 'gitea' },
      frameworkRef: { name: 'python-fastapi' },
      standardRef: { name: standard },
    },
    status: { phase },
  }
}

function seed(api) {
  api.add('projects', { metadata: { name: NS }, spec: { displayName: 'Webshop' } })
  api.add('softwaresystems', { metadata: { name: 'webshop-core' }, spec: { displayName: 'Webshop Core' } })
  api.add('standards', {
    metadata: { name: 'python-standard' },
    spec: {
      displayName: 'Python standard',
      template: { url: 'https://gitea.example/templates/python-standard', ref: 'v1.2.0' },
      supportedTypes: ['service', 'worker'],
      supportedFrameworks: ['python-fastapi'],
    },
  })
  api.add('containers', container('orders-api', 'Ready'))
  api.add('containers', container('payments', 'Ready'))
  api.add('containers', container('search', 'Failed'))
  api.add('containers', container('storefront', 'Ready', 'node-standard'))
}

/** The fake API, and server.mjs in front of it with UI actions in `actions` mode. */
async function start(actions) {
  const api = fakeApi(NS)
  seed(api)
  const ui = createServer({
    distDir: dist,
    upstream: resolveUpstream({ K8S_API: await listen(api.server) }),
    actions,
    log: () => {},
  })
  return { api, ui, base: await listen(ui) }
}

async function stop({ api, ui }) {
  ui.closeAllConnections()
  api.server.closeAllConnections()
  await new Promise((r) => ui.close(r))
  await new Promise((r) => api.server.close(r))
}

/** A picture of the viewport around the actions panel, in C4_UI_SHOTS. */
async function shot(page, name) {
  if (!process.env.C4_UI_SHOTS) return
  await page.getByRole('region', { name: 'Act on it' }).scrollIntoViewIfNeeded()
  await page.waitForTimeout(200)
  await page.screenshot({ path: path.join(process.env.C4_UI_SHOTS, name) })
}

describe('the Standard page’s ref bump', () => {
  let browser
  before(async () => {
    browser = await launchChromium()
  })
  after(async () => {
    await browser?.close()
  })

  describe('over port-forward, as the UI’s service account', () => {
    let env
    let page
    const panel = () => page.getByRole('region', { name: 'Act on it' })
    const refBox = () => panel().getByLabel('Bump its ref')
    const dialog = (ref) => page.getByRole('dialog', { name: `Bump python-standard to ${ref}?` })

    before(async () => {
      env = await start('service-account')
      page = await browser.newPage({ viewport: { width: 1280, height: 900 }, reducedMotion: 'reduce' })
      await page.goto(`${env.base}${PAGE}`)
      await panel().waitFor({ timeout: 10000 })
    })
    after(async () => {
      await page?.close()
      if (env) await stop(env)
    })

    it('counts the Ready Containers on it in the dialog, and cancelling writes nothing', async () => {
      assert.equal(await refBox().inputValue(), 'v1.2.0')
      await refBox().fill('v1.3.0')
      await panel().getByRole('button', { name: 'Bump the ref…' }).click()
      await dialog('v1.3.0').waitFor({ timeout: 5000 })
      await dialog('v1.3.0')
        .getByText('2 Containers on python-standard will get an update pull request: orders-api, payments.')
        .waitFor({ timeout: 5000 })
      await dialog('v1.3.0').getByText('Not Ready, so no pull request: search.').waitFor({ timeout: 5000 })
      await shot(page, 'ui-standard-bump-dialog.png')
      await dialog('v1.3.0').getByRole('button', { name: 'Cancel' }).click()
      await dialog('v1.3.0').waitFor({ state: 'hidden', timeout: 5000 })
      assert.deepEqual(env.api.writes, [])
    })

    it('sets spec.template.ref once confirmed, and the page shows the new pin', async () => {
      await panel().getByRole('button', { name: 'Bump the ref…' }).click()
      await dialog('v1.3.0').getByRole('button', { name: 'Bump the ref' }).click()
      await panel().getByRole('status').getByText('Pinned python-standard at').waitFor({ timeout: 5000 })
      await dialog('v1.3.0').waitFor({ state: 'hidden', timeout: 5000 })
      assert.equal(env.api.writes.length, 1)
      const [w] = env.api.writes
      assert.equal(w.method, 'PATCH')
      assert.equal(w.path, `/apis/c4.factory.io/v1alpha1/namespaces/${NS}/standards/python-standard`)
      assert.deepEqual(w.body, [{ op: 'add', path: '/spec/template/ref', value: 'v1.3.0' }])
      // The watch brings the new ref to the page's template facts.
      await page.locator('.facts dd', { hasText: /^v1\.3\.0$/ }).waitFor({ timeout: 5000 })
    })

    it('shows the API server’s message when it refuses an empty ref', async () => {
      env.api.writes.length = 0
      await refBox().fill('')
      await panel().getByRole('button', { name: 'Bump the ref…' }).click()
      await dialog('an empty ref').getByRole('button', { name: 'Bump the ref' }).click()
      const alert = panel().getByRole('alert')
      await alert.getByText('spec.template.ref in body should be at least 1 chars long').waitFor({ timeout: 5000 })
      // The UI sent it: the CRD refused it, not the page.
      assert.equal(env.api.writes.length, 1)
      assert.deepEqual(env.api.writes[0].body, [{ op: 'add', path: '/spec/template/ref', value: '' }])
      await shot(page, 'ui-standard-bump-refused.png')
    })
  })

  describe('behind an Ingress, with OIDC and nobody signed in', () => {
    let env
    let page

    before(async () => {
      env = await start('oidc')
      page = await browser.newPage({ viewport: { width: 1280, height: 900 }, reducedMotion: 'reduce' })
    })
    after(async () => {
      await page?.close()
      if (env) await stop(env)
    })

    it('says to sign in instead of offering the button', async () => {
      await page.goto(`${env.base}${PAGE}`)
      const panel = page.getByRole('region', { name: 'Act on it' })
      await panel.getByText('Sign in to bump its ref.').waitFor({ timeout: 10000 })
      const signIn = new URL(await panel.getByRole('link', { name: 'Sign in' }).getAttribute('href'), env.base)
      assert.equal(signIn.pathname, '/ui/login')
      assert.match(signIn.searchParams.get('return'), /kind=Standard&name=python-standard/)
      assert.equal(await panel.getByRole('button').count(), 0)
      await shot(page, 'ui-standard-signed-out.png')
      assert.deepEqual(env.api.writes, [])
    })
  })
})
