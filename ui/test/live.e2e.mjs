// End-to-end check of the live path: a fake Kubernetes API, server.mjs in
// front of it, and the built UI in Chromium. Changes pushed through watch
// events must show up in the page without a reload, and an expired watch
// (410 Gone) must recover by relisting.
//
// Needs a build and Chromium:  npm run test:e2e
// THEOS_UI_SHOTS=DIR also saves the footer's version line, with and without the
// skew warning, in DIR (docs/screenshots).
// (npx playwright install chromium-headless-shell, once)

import assert from 'node:assert/strict'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { launchChromium } from '../scripts/browser.mjs'
import { createServer, resolveUpstream } from '../server.mjs'
import { fakeApi, listen } from './fakeApi.mjs'
// The UI's own list of kinds, so the fake API serves and the test expects every one.
import { RESOURCES, resourcePath } from '../src/k8s/types.ts'

const NS = 'webshop'
const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')

describe('live updates in the browser', () => {
  let api
  let ui
  let browser
  let page
  let base
  /** Every request the page made to the API gateway. */
  const requests = []

  before(async () => {
    api = fakeApi(NS)
    const apiUrl = await listen(api.server)
    api.add('projects', { metadata: { name: NS }, spec: { displayName: 'Webshop' } })
    api.add('people', { metadata: { name: 'customer' }, spec: { displayName: 'Customer' } })
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
    })
    api.add('datastores', {
      metadata: { name: 'orders-db' },
      spec: {
        softwareSystemRef: { name: 'webshop-core' },
        displayName: 'Orders DB',
        connectorRef: { name: 'postgres' },
      },
    })

    ui = createServer({ distDir: dist, upstream: resolveUpstream({ K8S_API: apiUrl }), log: () => {} })
    base = await listen(ui)
    browser = await launchChromium()
    page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    page.on('request', (r) => {
      if (new URL(r.url()).pathname.startsWith('/k8s')) requests.push({ url: new URL(r.url()), method: r.method() })
    })
    await page.goto(`${base}/?view=containers`)
    await page.locator('[data-node-id="Container/orders-api"]').waitFor()
  })

  after(async () => {
    await browser?.close()
    ui?.closeAllConnections()
    api?.server.closeAllConnections()
    await new Promise((r) => ui.close(r))
    await new Promise((r) => api.server.close(r))
  })

  const card = () => page.locator('[data-node-id="Container/orders-api"]')

  it('starts from the list, with no status yet', async () => {
    await assert.doesNotReject(card().getByText('No status yet').waitFor({ timeout: 5000 }))
    assert.equal(await page.locator('.status').textContent(), 'Live')
  })

  it('shows a new phase as soon as it is written', async () => {
    api.modify('containers', {
      metadata: { name: 'orders-api' },
      spec: {
        softwareSystemRef: { name: 'webshop-core' },
        displayName: 'Orders API',
        type: 'service',
        gitProviderRef: { name: 'gitea' },
        frameworkRef: { name: 'python-fastapi' },
      },
      status: {
        phase: 'Ready',
        repositoryURL: 'http://gitea.example/webshop/orders-api',
        scaffoldedFrom: ['python-fastapi@v1.2.0'],
        conditions: [
          { type: 'Scaffolded', status: 'True', reason: 'Scaffolded', message: 'Pushed the initial commit' },
        ],
      },
    })
    await card().getByText('Ready', { exact: true }).waitFor({ timeout: 5000 })
    await card().getByText('python-fastapi v1.2.0').waitFor({ timeout: 5000 })
  })

  it('shows a Session that starts, and fades it when it goes Stale', async () => {
    const session = {
      metadata: { name: 'orders-api-k7f3q' },
      spec: { containerRef: { name: 'orders-api' }, actor: 'agent', summary: 'Adding order validation' },
      status: { phase: 'Active', lastHeartbeat: new Date().toISOString() },
    }
    api.add('sessions', session)
    await card().getByText('1 working').waitFor({ timeout: 5000 })
    await page.locator('.session', { hasText: 'Adding order validation' }).waitFor({ timeout: 5000 })

    api.modify('sessions', { ...session, status: { ...session.status, phase: 'Stale' } })
    await page.locator('.session-stale', { hasText: 'Adding order validation' }).waitFor({ timeout: 5000 })
    await card().getByText('Nobody working').waitFor({ timeout: 5000 })
  })

  it('removes a deleted DataStore', async () => {
    api.remove('datastores', 'orders-db')
    await page.locator('[data-node-id="DataStore/orders-db"]').waitFor({ state: 'detached', timeout: 5000 })
  })

  it('recovers from an expired watch by listing again', async () => {
    // A change the browser misses: made while no watch is open.
    api.expire('containers')
    api.modify('containers', {
      metadata: { name: 'orders-api' },
      spec: {
        softwareSystemRef: { name: 'webshop-core' },
        displayName: 'Orders API (v2)',
        type: 'service',
        gitProviderRef: { name: 'gitea' },
        frameworkRef: { name: 'python-fastapi' },
      },
      status: { phase: 'Ready' },
    })
    await card().getByText('Orders API (v2)').waitFor({ timeout: 10000 })
    // And it is watching again afterwards.
    for (let i = 0; i < 50 && api.watching('containers') === 0; i++) await new Promise((r) => setTimeout(r, 100))
    assert.ok(api.watching('containers') > 0, 'a new watch is open')
  })

  it('watches all kinds over a few connections, not one each', () => {
    // Browsers allow six connections per host, and each /k8s-watch request
    // holds one. The page watches the project picker's Projects and every kind
    // in the project's namespace; each watch opens when its list returns, so
    // on a busy device they may come in a few batches, never one each.
    const want = new Set([
      resourcePath(RESOURCES.projects),
      ...Object.values(RESOURCES).map((k) => resourcePath(k, NS)),
    ])
    const watches = requests.filter((r) => r.url.pathname === '/k8s-watch')
    const seen = new Set()
    let used = 0
    for (const r of watches) {
      if ([...want].every((k) => seen.has(k))) break
      used++
      for (const k of r.url.searchParams.getAll('w')) seen.add(k)
    }
    assert.deepEqual(seen, want, `watch requests carry every kind: ${[...seen].join(', ')}`)
    assert.ok(used < 6, `${used} watch requests for ${want.size} kinds leave no connection for the rest`)
    assert.ok(requests.every((r) => r.method === 'GET'))
  })

  it('shows the versions in the footer, and warns when the operator is another release', async () => {
    const line = page.locator('.versions-line')
    const skew = page.locator('.versions-skew')
    // The test build stamps no release, and no Project has reported the operator yet.
    await line
      .getByText('UI dev · operator unknown · chart unknown · API theos.factory.io/v1beta1')
      .waitFor({ timeout: 5000 })
    assert.equal(await skew.count(), 0)

    const project = { metadata: { name: NS }, spec: { displayName: 'Webshop' } }
    api.modify('projects', { ...project, status: { factory: { operatorVersion: '0.14.0', chartVersion: '0.14.0' } } })
    await line.getByText('operator 0.14.0 · chart 0.14.0').waitFor({ timeout: 5000 })
    await skew.getByText('This UI is dev and the operator 0.14.0').waitFor({ timeout: 5000 })
    if (process.env.THEOS_UI_SHOTS)
      await page.locator('footer').screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-versions-skew.png') })

    api.modify('projects', { ...project, status: { factory: { operatorVersion: 'dev' } } })
    await line.getByText('operator dev · chart unknown').waitFor({ timeout: 5000 })
    await skew.waitFor({ state: 'detached', timeout: 5000 })
    if (process.env.THEOS_UI_SHOTS)
      await page.locator('footer').screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-versions.png') })
  })

  it('never sends anything but GET', async () => {
    const res = await page.evaluate(
      async () =>
        (
          await fetch('k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/containers/orders-api', {
            method: 'DELETE',
          })
        ).status,
    )
    assert.equal(res, 405)
  })
})
