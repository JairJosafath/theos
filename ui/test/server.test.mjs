// Tests for server.mjs against a fake Kubernetes API: the proxy is GET-only,
// strips client credentials, streams watches, and serves the built files.
// Run with: node --test test/server.test.mjs

import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises'
import http from 'node:http'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { createServer, resolveUpstream } from '../server.mjs'

/** Starts a server on a free port and returns its base URL. */
function listen(server) {
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => resolve(`http://127.0.0.1:${server.address().port}`))
  })
}

function close(server) {
  server.closeAllConnections?.()
  return new Promise((resolve) => server.close(() => resolve()))
}

/** A credential the browser sends; the server never passes it on. */
const browserToken = randomBytes(18).toString('base64url')

describe('server.mjs', () => {
  /** Requests the fake API server has seen. */
  const seen = []
  /** Watch responses still open, so the test can write to them. */
  const watches = []
  let upstream
  let server
  let base
  let dist

  before(async () => {
    upstream = http.createServer((req, res) => {
      seen.push({ method: req.method, url: req.url, headers: req.headers })
      const url = new URL(req.url, 'http://x')
      if (url.searchParams.get('watch') === '1' && url.searchParams.get('resourceVersion') === 'old') {
        res.writeHead(410, { 'content-type': 'application/json' })
        return res.end(
          JSON.stringify({ kind: 'Status', code: 410, reason: 'Expired', message: 'too old resource version' }),
        )
      }
      if (url.searchParams.get('watch') === '1') {
        res.writeHead(200, { 'content-type': 'application/json', 'transfer-encoding': 'chunked' })
        res.write('{"type":"ADDED","object":{"metadata":{"name":"a","resourceVersion":"2"}}}\n')
        watches.push(res)
        return
      }
      res.writeHead(200, { 'content-type': 'application/json', 'set-cookie': 'upstream=1', 'www-authenticate': 'x' })
      res.end(JSON.stringify({ kind: 'ContainerList', metadata: { resourceVersion: '1' }, items: [] }))
    })
    const upstreamUrl = await listen(upstream)

    dist = await mkdtemp(path.join(tmpdir(), 'c4ui-dist-'))
    await mkdir(path.join(dist, 'assets'))
    await writeFile(path.join(dist, 'index.html'), '<!doctype html><title>C4 Factory</title>')
    await writeFile(path.join(dist, 'assets', 'app-123.js'), 'console.log(1)')
    await writeFile(path.join(path.dirname(dist), 'c4ui-secret.txt'), 'outside dist')

    server = createServer({
      distDir: dist,
      upstream: resolveUpstream({ K8S_API: upstreamUrl }),
      log: () => {},
    })
    base = await listen(server)
  })

  after(async () => {
    for (const res of watches) res.end()
    await close(server)
    await close(upstream)
    await rm(dist, { recursive: true, force: true })
    await rm(path.join(path.dirname(dist), 'c4ui-secret.txt'), { force: true })
  })

  it('answers /healthz', async () => {
    const res = await fetch(`${base}/healthz`)
    assert.equal(res.status, 200)
    assert.equal(await res.text(), 'ok\n')
  })

  it('answers /factory/health with a summary read by the server itself', async () => {
    seen.length = 0
    const res = await fetch(`${base}/factory/health`, { headers: { authorization: `Bearer ${browserToken}` } })
    assert.equal(res.status, 200)
    const health = await res.json()
    assert.ok(Array.isArray(health.components) && health.components.length > 0)
    assert.equal(typeof health.agents.maxSandboxes, 'number')
    assert.deepEqual(seen.map((r) => r.url).sort(), [
      '/api/v1/nodes',
      '/api/v1/pods',
      '/apis/metrics.k8s.io/v1beta1/nodes',
      '/apis/metrics.k8s.io/v1beta1/pods',
    ])
    assert.ok(seen.every((r) => r.method === 'GET' && !r.headers.authorization))
  })

  it('refuses other methods on /factory/health', async () => {
    const res = await fetch(`${base}/factory/health`, { method: 'POST' })
    assert.equal(res.status, 405)
  })

  it('proxies GET to the API with path and query, and without client credentials', async () => {
    seen.length = 0
    const res = await fetch(`${base}/k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers?limit=5`, {
      headers: {
        authorization: `Bearer ${browserToken}`,
        cookie: `session=${browserToken}`,
        'impersonate-user': 'system:admin',
        'x-remote-user': 'admin',
        accept: 'application/json',
      },
    })
    assert.equal(res.status, 200)
    assert.equal((await res.json()).kind, 'ContainerList')
    assert.equal(seen.length, 1)
    const [req] = seen
    assert.equal(req.method, 'GET')
    assert.equal(req.url, '/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers?limit=5')
    assert.equal(req.headers.authorization, undefined)
    assert.equal(req.headers.cookie, undefined)
    assert.equal(req.headers['impersonate-user'], undefined)
    assert.equal(req.headers['x-remote-user'], undefined)
    assert.equal(req.headers.accept, 'application/json')
    // Nothing from the API that could set state in the browser comes back.
    assert.equal(res.headers.get('set-cookie'), null)
    assert.equal(res.headers.get('www-authenticate'), null)
  })

  it('reads the factory kinds across namespaces: Tasks, Agents and Crews', async () => {
    for (const plural of ['tasks', 'agents', 'crews']) {
      seen.length = 0
      const res = await fetch(`${base}/k8s/apis/c4.factory.io/v1alpha1/${plural}`)
      assert.equal(res.status, 200)
      assert.deepEqual(
        seen.map((r) => [r.method, r.url]),
        [['GET', `/apis/c4.factory.io/v1alpha1/${plural}`]],
      )
    }
  })

  for (const method of ['POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']) {
    it(`rejects ${method} under /k8s/ with 405 and never calls the API`, async () => {
      seen.length = 0
      const res = await fetch(`${base}/k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers/orders-api`, {
        method,
        body: method === 'POST' || method === 'PUT' || method === 'PATCH' ? '{}' : undefined,
      })
      assert.equal(res.status, 405)
      assert.equal(res.headers.get('allow'), 'GET')
      assert.equal(seen.length, 0)
    })
  }

  it('refuses API paths outside c4.factory.io, including through .. and escapes', async () => {
    seen.length = 0
    for (const p of [
      '/k8s/api/v1/namespaces/webshop/secrets',
      '/k8s/apis/c4.factory.io/../../api/v1/secrets',
      '/k8s/apis/c4.factory.io/%2e%2e/%2e%2e/api/v1/secrets',
      '/k8s/apis/c4.factory.io/v1alpha1/..%2f..%2f..%2fapi%2fv1%2fsecrets',
      '/k8s/version',
    ]) {
      const res = await fetch(`${base}${p}`)
      assert.notEqual(res.status, 200, p)
    }
    assert.equal(seen.length, 0)
  })

  it('streams a watch without waiting for it to end', async () => {
    const res = await fetch(
      `${base}/k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers?watch=1&resourceVersion=1`,
    )
    assert.equal(res.status, 200)
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    const first = await reader.read()
    assert.match(decoder.decode(first.value), /"type":"ADDED"/)

    // The upstream is still open; a later event arrives on its own.
    watches.at(-1).write('{"type":"MODIFIED","object":{"metadata":{"name":"a","resourceVersion":"3"}}}\n')
    const second = await reader.read()
    assert.match(decoder.decode(second.value), /"type":"MODIFIED"/)
    await reader.cancel()
  })

  it('multiplexes several watches over one GET /k8s-watch', async () => {
    seen.length = 0
    watches.length = 0
    const q = new URLSearchParams()
    q.append('w', '/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers')
    q.append('rv', '7')
    q.append('w', '/apis/c4.factory.io/v1alpha1/namespaces/webshop/sessions')
    q.append('rv', 'old')
    q.set('timeoutSeconds', '300')
    const res = await fetch(`${base}/k8s-watch?${q}`, {
      headers: { cookie: 'x=1', authorization: `Bearer ${browserToken}` },
    })
    assert.equal(res.status, 200)
    assert.match(res.headers.get('content-type'), /ndjson/)
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let text = ''
    const lines = () =>
      text
        .split('\n')
        .filter(Boolean)
        .map((l) => JSON.parse(l))
    while (lines().length < 2) text += decoder.decode((await reader.read()).value, { stream: true })

    // The first watch streams its event; the second ends at once with 410.
    assert.deepEqual(
      lines().sort((a, b) => a.i - b.i),
      [
        { i: 0, event: { type: 'ADDED', object: { metadata: { name: 'a', resourceVersion: '2' } } } },
        {
          i: 1,
          end: true,
          code: 410,
          status: { kind: 'Status', code: 410, reason: 'Expired', message: 'too old resource version' },
        },
      ],
    )
    const upstreamWatch = seen.find((r) => r.url.includes('containers'))
    assert.match(upstreamWatch.url, /watch=1&resourceVersion=7&allowWatchBookmarks=true&timeoutSeconds=300/)
    assert.equal(upstreamWatch.headers.cookie, undefined)
    assert.equal(upstreamWatch.headers.authorization, undefined)

    // When the API ends the last open watch, the response ends too.
    watches.at(-1).end()
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      text += decoder.decode(value, { stream: true })
    }
    assert.deepEqual(lines().at(-1), { i: 0, end: true, code: 200 })
  })

  it('refuses /k8s-watch for other methods, other API groups and malformed requests', async () => {
    seen.length = 0
    const ok = '/apis/c4.factory.io/v1alpha1/projects'
    const cases = [
      ['POST', `w=${ok}&rv=1`, 405],
      ['GET', `w=/api/v1/secrets&rv=1`, 403],
      ['GET', `w=${encodeURIComponent('/apis/c4.factory.io/../../api/v1/secrets')}&rv=1`, 403],
      ['GET', `w=${encodeURIComponent('/apis/c4.factory.io/v1alpha1/x?watch=0')}&rv=1`, 403],
      ['GET', `w=${ok}&rv=1&timeoutSeconds=abc`, 403],
      ['GET', `w=${ok}`, 400],
      ['GET', '', 400],
    ]
    for (const [method, query, status] of cases) {
      const res = await fetch(`${base}/k8s-watch?${query}`, { method })
      assert.equal(res.status, status, `${method} ${query}`)
    }
    assert.equal(seen.length, 0)
  })

  it('serves the built files, and nothing outside them', async () => {
    const index = await fetch(`${base}/`)
    assert.equal(index.status, 200)
    assert.match(index.headers.get('content-type'), /text\/html/)
    assert.equal(index.headers.get('cache-control'), 'no-cache')

    const asset = await fetch(`${base}/assets/app-123.js`)
    assert.equal(asset.status, 200)
    assert.match(asset.headers.get('cache-control'), /immutable/)

    const traversal = await fetch(`${base}/%2e%2e/c4ui-secret.txt`)
    assert.equal(traversal.status, 404)
    const missing = await fetch(`${base}/nope.js`)
    assert.equal(missing.status, 404)
  })

  it('answers 502 with a Status when the API is unreachable', async () => {
    const dead = createServer({
      distDir: dist,
      upstream: resolveUpstream({ K8S_API: 'http://127.0.0.1:1' }),
      log: () => {},
    })
    const deadBase = await listen(dead)
    try {
      const res = await fetch(`${deadBase}/k8s/apis/c4.factory.io/v1alpha1/projects`)
      assert.equal(res.status, 502)
      assert.equal((await res.json()).kind, 'Status')
    } finally {
      await close(dead)
    }
  })
})

describe('resolveUpstream', () => {
  it('prefers K8S_API', () => {
    const up = resolveUpstream({ K8S_API: 'http://127.0.0.1:8001' }, '/nonexistent')
    assert.equal(up.url.href, 'http://127.0.0.1:8001/')
    assert.equal(up.token(), undefined)
  })

  it('uses the ServiceAccount inside a Pod', async () => {
    const dir = await mkdtemp(path.join(tmpdir(), 'c4ui-sa-'))
    try {
      const token = randomBytes(18).toString('base64url')
      await writeFile(path.join(dir, 'token'), `${token}\n`)
      await writeFile(path.join(dir, 'ca.crt'), 'not really a cert')
      const up = resolveUpstream({ KUBERNETES_SERVICE_HOST: '10.96.0.1', KUBERNETES_SERVICE_PORT: '443' }, dir)
      assert.equal(up.url.href, 'https://10.96.0.1/')
      assert.equal(up.mode, 'in-cluster')
      assert.equal(up.token(), token)
      assert.ok(up.ca)
    } finally {
      await rm(dir, { recursive: true, force: true })
    }
  })

  it('falls back to a local kubectl proxy', () => {
    const up = resolveUpstream({}, '/nonexistent')
    assert.equal(up.url.href, 'http://127.0.0.1:8001/')
  })
})
