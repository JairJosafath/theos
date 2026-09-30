// End-to-end check of the live path: a fake Kubernetes API, server.mjs in
// front of it, and the built UI in Chromium. Changes pushed through watch
// events must show up in the page without a reload, and an expired watch
// (410 Gone) must recover by relisting.
//
// Needs a build and Chromium:  npm run test:e2e
// (npx playwright install --with-deps chromium, once)

import assert from 'node:assert/strict'
import http from 'node:http'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'
import { createServer, resolveUpstream } from '../server.mjs'

const NS = 'webshop'
const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')

/** A tiny in-memory API server for the c4.factory.io kinds, with watches. */
function fakeApi() {
  let rv = 1
  const store = new Map() // plural -> Map(name -> object)
  const watchers = new Map() // plural -> Set(res)
  const kinds = [
    'projects',
    'people',
    'softwaresystems',
    'containers',
    'datastores',
    'relationships',
    'sessions',
    'frameworks',
    'connectors',
  ]
  for (const k of kinds) {
    store.set(k, new Map())
    watchers.set(k, new Set())
  }
  const put = (plural, obj) => {
    obj.metadata = { namespace: NS, ...obj.metadata, resourceVersion: String(++rv) }
    store.get(plural).set(obj.metadata.name, obj)
    return obj
  }
  // Every event, so a watch from resourceVersion R first gets what happened
  // after R, as the real API server does.
  const history = []
  const send = (plural, type, obj) => {
    history.push({
      plural,
      rv: Number(obj.metadata.resourceVersion),
      line: `${JSON.stringify({ type, object: obj })}\n`,
    })
    for (const res of watchers.get(plural)) res.write(history.at(-1).line)
  }
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://x')
    const m = url.pathname.match(/^\/apis\/c4\.factory\.io\/v1alpha1(?:\/namespaces\/([^/]+))?\/([a-z]+)$/)
    if (!m || !store.has(m[2])) {
      res.writeHead(404, { 'content-type': 'application/json' })
      return res.end(JSON.stringify({ kind: 'Status', code: 404, message: 'not found' }))
    }
    const plural = m[2]
    if (url.searchParams.get('watch') === '1') {
      res.writeHead(200, { 'content-type': 'application/json' })
      res.flushHeaders()
      const from = Number(url.searchParams.get('resourceVersion') || 0)
      for (const h of history) if (h.plural === plural && h.rv > from) res.write(h.line)
      watchers.get(plural).add(res)
      res.on('close', () => watchers.get(plural).delete(res))
      return
    }
    res.writeHead(200, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ metadata: { resourceVersion: String(rv) }, items: [...store.get(plural).values()] }))
  })
  return {
    server,
    watchers,
    add(plural, obj) {
      send(plural, 'ADDED', put(plural, obj))
    },
    modify(plural, obj) {
      send(plural, 'MODIFIED', put(plural, obj))
    },
    remove(plural, name) {
      const obj = store.get(plural).get(name)
      store.get(plural).delete(name)
      send(plural, 'DELETED', { ...obj, metadata: { ...obj.metadata, resourceVersion: String(++rv) } })
    },
    /** Ends every open watch of a kind with an "expired" error, as the API does after compaction. */
    expire(plural) {
      for (const res of [...watchers.get(plural)]) {
        watchers.get(plural).delete(res)
        res.end(
          `${JSON.stringify({ type: 'ERROR', object: { kind: 'Status', code: 410, reason: 'Expired', message: 'too old resource version' } })}\n`,
        )
      }
    },
  }
}

function listen(server) {
  return new Promise((resolve) =>
    server.listen(0, '127.0.0.1', () => resolve(`http://127.0.0.1:${server.address().port}`)),
  )
}

describe('live updates in the browser', () => {
  let api
  let ui
  let browser
  let page
  let base
  /** Every request the page made to the API gateway. */
  const requests = []

  before(async () => {
    api = fakeApi()
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
    browser = await chromium.launch()
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
    for (let i = 0; i < 50 && api.watchers.get('containers').size === 0; i++)
      await new Promise((r) => setTimeout(r, 100))
    assert.ok(api.watchers.get('containers').size > 0, 'a new watch is open')
  })

  it('watches all kinds over one or two connections, not one each', () => {
    // Browsers allow six connections per host; the page watches ten kinds.
    const first = requests.filter((r) => r.url.pathname === '/k8s-watch').slice(0, 2)
    const kinds = first.flatMap((r) => r.url.searchParams.getAll('w'))
    assert.ok(first.length >= 1)
    assert.equal(new Set(kinds).size, 10, `first watch requests carry all kinds: ${kinds.join(', ')}`)
    assert.ok(requests.every((r) => r.method === 'GET'))
  })

  it('never sends anything but GET', async () => {
    const res = await page.evaluate(
      async () =>
        (await fetch('k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers/orders-api', { method: 'DELETE' }))
          .status,
    )
    assert.equal(res, 405)
  })
})
