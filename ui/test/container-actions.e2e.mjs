// The Container page's actions in the browser (design.md, UI actions): the
// scaffold retry behind its dialog, a Task from a goal, a write the API
// server refuses, and in OIDC mode with nobody signed in, a page that says to
// sign in. A fake Kubernetes API, server.mjs with actions on in front of it,
// and the built UI in Chromium.
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
const REPO = 'http://gitea.example/webshop/orders-api'

function seed(api) {
  api.add('projects', { metadata: { name: NS }, spec: { displayName: 'Webshop' } })
  api.add('softwaresystems', { metadata: { name: 'webshop-core' }, spec: { displayName: 'Webshop Core' } })
  api.add('containers', {
    metadata: { name: 'orders-api' },
    spec: {
      softwareSystemRef: { name: 'webshop-core' },
      displayName: 'Orders API',
      type: 'service',
      gitProviderRef: { name: 'gitea' },
      frameworkRef: { name: 'python-fastapi' },
    },
    status: {
      phase: 'Failed',
      repositoryURL: REPO,
      conditions: [
        { type: 'RepositoryCreated', status: 'True', reason: 'Created' },
        {
          type: 'Scaffolded',
          status: 'False',
          reason: 'ForeignCommits',
          message: 'the repository has commits the factory did not make',
        },
      ],
    },
  })
  api.add('crews', {
    metadata: { name: 'orders-api' },
    spec: { containerRef: { name: 'orders-api' }, implementer: { name: 'claude' }, reviewer: { name: 'codex' } },
  })
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

describe('the Container page’s actions', () => {
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
    const dialog = () => page.getByRole('dialog', { name: 'Retry the scaffold of orders-api?' })

    before(async () => {
      env = await start('service-account')
      page = await browser.newPage({ viewport: { width: 1280, height: 900 }, reducedMotion: 'reduce' })
      await page.goto(`${env.base}/?shell=next&project=${NS}&ns=${NS}&kind=Container&name=orders-api`)
      await panel().waitFor({ timeout: 10000 })
    })
    after(async () => {
      await page?.close()
      if (env) await stop(env)
    })

    it('asks to empty the repository first, and cancelling writes nothing', async () => {
      await panel().getByRole('button', { name: 'Retry the scaffold…' }).click()
      await dialog().waitFor({ timeout: 5000 })
      await dialog().getByText('Empty the repository first').waitFor({ timeout: 5000 })
      assert.equal(await dialog().getByRole('link', { name: 'Open the repository' }).getAttribute('href'), REPO)
      await shot(page, 'ui-container-retry-dialog.png')
      await dialog().getByRole('button', { name: 'Cancel' }).click()
      await dialog().waitFor({ state: 'hidden', timeout: 5000 })
      assert.deepEqual(env.api.writes, [])
    })

    it('sets the retry annotation once confirmed', async () => {
      await panel().getByRole('button', { name: 'Retry the scaffold…' }).click()
      await dialog().getByRole('button', { name: 'It is empty: retry' }).click()
      await panel().getByText('Asked the factory to retry').waitFor({ timeout: 5000 })
      await dialog().waitFor({ state: 'hidden', timeout: 5000 })
      assert.equal(env.api.writes.length, 1)
      const [w] = env.api.writes
      assert.equal(w.method, 'PATCH')
      assert.equal(w.path, `/apis/c4.factory.io/v1alpha1/namespaces/${NS}/containers/orders-api`)
      assert.deepEqual(Object.keys(w.body.metadata.annotations), ['c4.factory.io/retry'])
      assert.ok(!Number.isNaN(Date.parse(w.body.metadata.annotations['c4.factory.io/retry'])))
    })

    it('starts a Task from a goal with the Crew’s Agents, and links to it', async () => {
      env.api.writes.length = 0
      await panel().getByLabel('Start a Task').fill('Add pagination to GET /orders')
      await panel().getByRole('button', { name: 'Start the Task' }).click()
      const link = panel().getByRole('status').getByRole('link')
      await link.waitFor({ timeout: 5000 })
      const name = await link.textContent()
      assert.match(name, /^orders-api-/)
      const href = new URL(await link.getAttribute('href'), env.base).searchParams
      assert.deepEqual([href.get('area'), href.get('task')], ['work', `${NS}/${name}`])
      assert.equal(env.api.writes.length, 1)
      assert.deepEqual(env.api.writes[0].body.spec, {
        kind: 'Implement',
        containerRef: { name: 'orders-api' },
        agentRef: { name: 'claude' },
        reviewerRef: { name: 'codex' },
        goal: 'Add pagination to GET /orders',
      })
      assert.equal(await panel().getByLabel('Start a Task').inputValue(), '')
      await link.click()
      // Work opens with the new Task selected.
      await page.locator('.task-card[aria-pressed="true"]', { hasText: name }).waitFor({ timeout: 10000 })
    })

    it('shows the API server’s message when it refuses an empty goal', async () => {
      await page.goto(`${env.base}/?shell=next&project=${NS}&ns=${NS}&kind=Container&name=orders-api`)
      await panel().waitFor({ timeout: 10000 })
      env.api.writes.length = 0
      await panel().getByRole('button', { name: 'Start the Task' }).click()
      const alert = panel().getByRole('alert')
      await alert.getByText('spec.goal in body should be at least 1 chars long').waitFor({ timeout: 5000 })
      // The UI sent it: the CRD refused it, not the page.
      assert.equal(env.api.writes.length, 1)
      assert.equal(env.api.writes[0].body.spec.goal, '')
      await shot(page, 'ui-container-task-refused.png')
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

    it('says to sign in instead of offering the buttons', async () => {
      await page.goto(`${env.base}/?shell=next&project=${NS}&ns=${NS}&kind=Container&name=orders-api`)
      const panel = page.getByRole('region', { name: 'Act on it' })
      await panel.getByText('Sign in to retry its scaffold or start a Task on it.').waitFor({ timeout: 10000 })
      const signIn = new URL(await panel.getByRole('link', { name: 'Sign in' }).getAttribute('href'), env.base)
      assert.equal(signIn.pathname, '/ui/login')
      assert.match(signIn.searchParams.get('return'), /kind=Container&name=orders-api/)
      assert.equal(await panel.getByRole('button').count(), 0)
      await shot(page, 'ui-container-signed-out.png')
      assert.deepEqual(env.api.writes, [])
    })
  })
})
