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
import { actionsFromEnv, allowedHostsFromEnv, createServer, resolveUpstream } from '../server.mjs'

/** Starts a server on a free port and returns its base URL. */
function listen(server) {
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => resolve(`http://127.0.0.1:${server.address().port}`))
  })
}

/** Sends one request with the given Host header, which fetch() won't set. */
function request(base, method, p, host, body) {
  return new Promise((resolve, reject) => {
    const headers = { host }
    if (body !== undefined) headers['content-type'] = 'application/json'
    const req = http.request(`${base}${p}`, { method, headers }, (res) => {
      let text = ''
      res.on('data', (chunk) => (text += chunk))
      res.on('end', () => resolve({ status: res.statusCode, text }))
    })
    req.on('error', reject)
    req.end(body === undefined ? undefined : JSON.stringify(body))
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
  let upstreamUrl
  let server
  let base
  let dist

  before(async () => {
    upstream = http.createServer(async (req, res) => {
      let text = ''
      for await (const chunk of req) text += chunk
      seen.push({ method: req.method, url: req.url, headers: req.headers, body: text ? JSON.parse(text) : undefined })
      const url = new URL(req.url, 'http://x')
      const json = (status, value) => {
        res.writeHead(status, { 'content-type': 'application/json' })
        res.end(JSON.stringify(value))
      }
      if ((req.method === 'POST' || req.method === 'PATCH') && url.pathname.endsWith('/denied')) {
        return json(422, { kind: 'Status', code: 422, reason: 'Invalid', message: 'spec.parallel: must be at least 1' })
      }
      if (req.method === 'POST' || req.method === 'PATCH') {
        return json(req.method === 'POST' ? 201 : 200, { kind: 'Brief', metadata: { name: 'echo' } })
      }
      const briefs = '/apis/c4.factory.io/v1alpha1/namespaces/webshop/briefs/'
      if (url.pathname === '/apis/c4.factory.io/v1alpha1/namespaces/webshop/projects') {
        return json(200, { kind: 'ProjectList', metadata: {}, items: [{ metadata: { name: 'webshop' } }] })
      }
      if (url.pathname === `${briefs}lens`) {
        return json(200, { kind: 'Brief', metadata: { name: 'lens', resourceVersion: '7' }, spec: { prompts: ['a'] } })
      }
      if (url.pathname === `${briefs}fresh`) {
        return json(200, { kind: 'Brief', metadata: { name: 'fresh', resourceVersion: '8' }, spec: { idea: 'x' } })
      }
      if (url.pathname === `${briefs}gone`) {
        return json(404, { kind: 'Status', code: 404, reason: 'NotFound', message: 'briefs "gone" not found' })
      }
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
    upstreamUrl = await listen(upstream)

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

  describe('the three writes', () => {
    const briefsPath = '/k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/briefs'
    const brief = {
      apiVersion: 'c4.factory.io/v1alpha1',
      kind: 'Brief',
      metadata: { name: 'lens', namespace: 'webshop', labels: { x: 'y' }, finalizers: ['f'] },
      spec: { idea: 'A camera app that reads labels aloud' },
    }
    const prompt = (value = 'Add a dark mode') => [{ op: 'add', path: '/spec/prompts/-', value }]
    const write = (
      method,
      p,
      body,
      type = method === 'PATCH' ? 'application/json-patch+json' : 'application/json',
      headers = {},
    ) =>
      fetch(`${base}${p}`, {
        method,
        headers: { 'content-type': type, authorization: `Bearer ${browserToken}`, ...headers },
        body: typeof body === 'string' ? body : JSON.stringify(body),
      })

    it('creates a Brief: only its name and spec go upstream, after checking the namespace holds a Project', async () => {
      seen.length = 0
      const res = await write('POST', briefsPath, brief)
      assert.equal(res.status, 201)
      assert.equal((await res.json()).kind, 'Brief')
      assert.deepEqual(
        seen.map((r) => [r.method, r.url]),
        [
          ['GET', '/apis/c4.factory.io/v1alpha1/namespaces/webshop/projects'],
          ['POST', '/apis/c4.factory.io/v1alpha1/namespaces/webshop/briefs?fieldManager=c4-factory-ui'],
        ],
      )
      const post = seen[1]
      assert.equal(post.headers['content-type'], 'application/json')
      assert.equal(post.headers.authorization, undefined)
      assert.deepEqual(post.body, {
        apiVersion: 'c4.factory.io/v1alpha1',
        kind: 'Brief',
        metadata: { name: 'lens', namespace: 'webshop' },
        spec: { idea: 'A camera app that reads labels aloud' },
      })
    })

    it('adds a prompt to a Brief with exactly the patch it was sent', async () => {
      seen.length = 0
      const res = await write('PATCH', `${briefsPath}/lens`, prompt())
      assert.equal(res.status, 200)
      assert.deepEqual(
        seen.map((r) => [r.method, r.url]),
        [
          ['GET', '/apis/c4.factory.io/v1alpha1/namespaces/webshop/briefs/lens'],
          ['PATCH', '/apis/c4.factory.io/v1alpha1/namespaces/webshop/briefs/lens?fieldManager=c4-factory-ui'],
        ],
      )
      assert.equal(seen[1].headers['content-type'], 'application/json-patch+json')
      assert.deepEqual(seen[1].body, prompt())
    })

    it('adds the list for the first prompt, guarded by the resourceVersion it read', async () => {
      seen.length = 0
      const res = await write('PATCH', `${briefsPath}/fresh`, prompt('First'))
      assert.equal(res.status, 200)
      assert.deepEqual(seen[1].body, [
        { op: 'test', path: '/metadata/resourceVersion', value: '8' },
        { op: 'add', path: '/spec/prompts', value: ['First'] },
      ])
    })

    it('pauses and resumes a Brief with exactly the patch it was sent', async () => {
      for (const value of [true, false]) {
        seen.length = 0
        const res = await write('PATCH', `${briefsPath}/lens`, [{ op: 'add', path: '/spec/paused', value }])
        assert.equal(res.status, 200)
        assert.deepEqual(
          seen.map((r) => [r.method, r.url]),
          [['PATCH', '/apis/c4.factory.io/v1alpha1/namespaces/webshop/briefs/lens?fieldManager=c4-factory-ui']],
        )
        assert.deepEqual(seen[0].body, [{ op: 'add', path: '/spec/paused', value }])
      }
    })

    it("passes the API's refusal back, such as a Brief that isn't there", async () => {
      seen.length = 0
      const res = await write('PATCH', `${briefsPath}/gone`, prompt())
      assert.equal(res.status, 404)
      assert.equal((await res.json()).reason, 'NotFound')
      assert.deepEqual(
        seen.map((r) => r.method),
        ['GET'],
      )
    })

    const refused = [
      ['a patch of another path', 'PATCH', `${briefsPath}/lens`, [{ op: 'add', path: '/spec/idea', value: 'x' }], 403],
      [
        'a patch replacing a prompt',
        'PATCH',
        `${briefsPath}/lens`,
        [{ op: 'replace', path: '/spec/prompts/0', value: 'x' }],
        403,
      ],
      ['a patch removing prompts', 'PATCH', `${briefsPath}/lens`, [{ op: 'remove', path: '/spec/prompts' }], 403],
      ['a patch with a second operation', 'PATCH', `${briefsPath}/lens`, [...prompt(), ...prompt()], 403],
      [
        'a patch pausing with a string',
        'PATCH',
        `${briefsPath}/lens`,
        [{ op: 'add', path: '/spec/paused', value: 'true' }],
        403,
      ],
      ['a patch removing paused', 'PATCH', `${briefsPath}/lens`, [{ op: 'remove', path: '/spec/paused' }], 403],
      [
        'a pause with a prompt',
        'PATCH',
        `${briefsPath}/lens`,
        [{ op: 'add', path: '/spec/paused', value: true }, ...prompt()],
        403,
      ],

      [
        'a patch adding a list as the prompt',
        'PATCH',
        `${briefsPath}/lens`,
        [{ op: 'add', path: '/spec/prompts/-', value: ['x'] }],
        403,
      ],
      ['a patch with an extra field', 'PATCH', `${briefsPath}/lens`, [{ ...prompt()[0], from: '/spec/idea' }], 403],
      ['a patch of the status', 'PATCH', `${briefsPath}/lens/status`, prompt(), 405],
      [
        'a patch of another kind',
        'PATCH',
        '/k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers/orders-api',
        prompt(),
        405,
      ],
      ['a patch of the Brief list', 'PATCH', briefsPath, prompt(), 403],
      [
        'a merge patch',
        'PATCH',
        `${briefsPath}/lens`,
        { spec: { prompts: ['x'] } },
        415,
        'application/merge-patch+json',
      ],
      ['a server-side apply', 'PATCH', `${briefsPath}/lens`, brief, 415, 'application/apply-patch+yaml'],
      ['a POST of another kind to the Brief path', 'POST', briefsPath, { ...brief, kind: 'Container' }, 403],
      ['a POST of another API version', 'POST', briefsPath, { ...brief, apiVersion: 'c4.factory.io/v2' }, 403],
      [
        'a POST of another kind',
        'POST',
        '/k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers',
        { ...brief, kind: 'Container' },
        405,
      ],
      [
        'a POST naming another namespace',
        'POST',
        briefsPath,
        { ...brief, metadata: { name: 'lens', namespace: 'kube-system' } },
        403,
      ],
      [
        'a POST to a namespace without a Project',
        'POST',
        '/k8s/apis/c4.factory.io/v1alpha1/namespaces/empty/briefs',
        brief,
        403,
      ],
      ['a POST to one Brief', 'POST', `${briefsPath}/lens`, brief, 403],
      ['a POST of something else than JSON', 'POST', briefsPath, 'idea=x', 415, 'application/x-www-form-urlencoded'],
      ['a POST of malformed JSON', 'POST', briefsPath, '{', 403],
      [
        'a POST from another site',
        'POST',
        briefsPath,
        brief,
        403,
        'application/json',
        { origin: 'https://evil.example' },
      ],
      ['a POST too big', 'POST', briefsPath, { ...brief, spec: { idea: 'x'.repeat(70_000) } }, 413],
      ['a PUT of a Brief', 'PUT', `${briefsPath}/lens`, brief, 405],
      ['a DELETE of a Brief', 'DELETE', `${briefsPath}/lens`, undefined, 405],
      ['a DELETE of the Brief list', 'DELETE', briefsPath, undefined, 405],
    ]
    for (const [what, method, p, body, status, type, headers] of refused) {
      it(`refuses ${what} with ${status}, and only reads upstream`, async () => {
        seen.length = 0
        const res =
          body === undefined ? await fetch(`${base}${p}`, { method }) : await write(method, p, body, type, headers)
        assert.equal(res.status, status)
        assert.equal((await res.json()).kind, 'Status')
        assert.ok(
          seen.every((r) => r.method === 'GET'),
          `${what}: ${seen.map((r) => r.method).join()}`,
        )
      })
    }
  })

  it('says on /ui/session that actions are off, and lists the three writes', async () => {
    const res = await fetch(`${base}/ui/session`)
    assert.equal(res.status, 200)
    const answer = await res.json()
    assert.equal(answer.actions, false)
    assert.equal(answer.mode, 'off')
    assert.equal(answer.user, null)
    assert.equal(answer.writes.length, 3)
    assert.equal((await fetch(`${base}/ui/session`, { method: 'POST' })).status, 405)
  })

  describe('the action table, over port-forward (UI_ACTIONS=service-account)', () => {
    let actionServer
    let actionBase
    const api = '/apis/c4.factory.io/v1alpha1/namespaces/webshop'
    const write = (method, p, body, type, headers = {}) =>
      fetch(`${actionBase}/k8s${api}${p}`, {
        method,
        headers: { 'content-type': type, authorization: `Bearer ${browserToken}`, ...headers },
        body: typeof body === 'string' ? body : JSON.stringify(body),
      })
    const JP = 'application/json-patch+json'
    const MP = 'application/merge-patch+json'
    const add = (p, value) => ({ op: 'add', path: p, value })
    const task = {
      apiVersion: 'c4.factory.io/v1alpha1',
      kind: 'Task',
      metadata: { generateName: 'orders-api-', labels: { x: 'y' } },
      spec: {
        kind: 'Implement',
        containerRef: { name: 'orders-api' },
        agentRef: { name: 'claude' },
        reviewerRef: { name: 'reviewer' },
        goal: 'Add pagination',
      },
    }

    before(async () => {
      actionServer = createServer({
        distDir: dist,
        upstream: resolveUpstream({ K8S_API: upstreamUrl }),
        log: () => {},
        actions: 'service-account',
      })
      actionBase = await listen(actionServer)
    })
    after(() => close(actionServer))

    it('says on /ui/session that actions are on, made as the ServiceAccount, with every write', async () => {
      const answer = await (await fetch(`${actionBase}/ui/session`)).json()
      assert.deepEqual([answer.actions, answer.mode, answer.user], [true, 'service-account', null])
      assert.equal(answer.writes.length, 8)
      assert.ok(!JSON.stringify(answer).includes(browserToken))
    })

    // [what, method, path, content type, body, what goes upstream (when not the body)]
    const accepted = [
      [
        "a Brief's settings",
        'PATCH',
        '/briefs/lens',
        JP,
        [
          add('/spec/paused', true),
          add('/spec/autopilot', 'Suggest'),
          add('/spec/parallel', 2),
          add('/spec/releases', [{ name: 'v1' }]),
        ],
      ],
      ['a Brief pause alone', 'PATCH', '/briefs/lens', JP, [add('/spec/paused', false)]],
      [
        "a Crew's settings",
        'PATCH',
        '/crews/orders-api',
        JP,
        [
          add('/spec/paused', true),
          add('/spec/parallel', 3),
          add('/spec/tagReleases', true),
          add('/spec/prepareReleases', false),
          add('/spec/publishReleases', { registry: 'ghcr.io' }),
        ],
      ],
      [
        "a Crew's release re-run",
        'PATCH',
        '/crews/orders-api',
        MP,
        { metadata: { annotations: { 'c4.factory.io/rerun-release': '2026-10-09T01:00:00Z' } } },
      ],
      [
        "a Container's scaffold retry",
        'PATCH',
        '/containers/orders-api',
        MP,
        { metadata: { annotations: { 'c4.factory.io/retry': '1' } } },
      ],
      [
        'a Task from a goal, with only its name and spec',
        'POST',
        '/tasks',
        'application/json',
        task,
        { ...task, metadata: { generateName: 'orders-api-', namespace: 'webshop' } },
      ],
      ["a Standard's ref", 'PATCH', '/standards/go-service', JP, [add('/spec/template/ref', 'v1.4.0')]],
    ]
    for (const [what, method, p, type, body, sent = body] of accepted) {
      it(`takes ${what}, and sends exactly that as the UI`, async () => {
        seen.length = 0
        const res = await write(method, p, body, type)
        assert.equal(res.status, method === 'POST' ? 201 : 200)
        assert.deepEqual(
          seen.map((r) => [r.method, r.url]),
          [[method, `${api}${p}?fieldManager=c4-factory-ui`]],
        )
        assert.equal(seen[0].headers['content-type'], type)
        assert.equal(seen[0].headers.authorization, undefined)
        assert.deepEqual(seen[0].body, sent)
      })
    }

    it('still creates a Brief and adds a prompt as with actions off', async () => {
      seen.length = 0
      const brief = {
        apiVersion: 'c4.factory.io/v1alpha1',
        kind: 'Brief',
        metadata: { name: 'b' },
        spec: { idea: 'x' },
      }
      assert.equal((await write('POST', '/briefs', brief, 'application/json')).status, 201)
      assert.equal((await write('PATCH', '/briefs/lens', [add('/spec/prompts/-', 'More')], JP)).status, 200)
      assert.deepEqual(
        seen.map((r) => r.method),
        ['GET', 'POST', 'GET', 'PATCH'],
      )
    })

    it("passes the API server's refusal back as-is", async () => {
      seen.length = 0
      const res = await write('PATCH', '/crews/denied', [add('/spec/parallel', 0)], JP)
      assert.equal(res.status, 422)
      assert.deepEqual(await res.json(), {
        kind: 'Status',
        code: 422,
        reason: 'Invalid',
        message: 'spec.parallel: must be at least 1',
      })
      assert.deepEqual(
        seen.map((r) => r.method),
        ['PATCH'],
      )
    })

    const crewAnn = (key, value = 'x') => ({ metadata: { annotations: { [key]: value } } })
    // [what, method, path, content type, body, status]
    const refused = [
      // Other paths.
      ['a patch of a Brief status', 'PATCH', '/briefs/lens/status', JP, [add('/spec/paused', true)], 405],
      ['a patch of a Crew list', 'PATCH', '/crews', JP, [add('/spec/paused', true)], 405],
      ['a patch of a Container list', 'PATCH', '/containers', MP, crewAnn('c4.factory.io/retry'), 405],
      ['a patch of one Task', 'PATCH', '/tasks/t1', JP, [add('/spec/goal', 'x')], 405],
      ['a patch of a System', 'PATCH', '/softwaresystems/shop', JP, [add('/spec/x', 1)], 405],
      ['a patch of an Agent', 'PATCH', '/agents/claude', JP, [add('/spec/model', 'x')], 405],
      ['a POST of a Crew', 'POST', '/crews', 'application/json', { kind: 'Crew' }, 405],
      // Other methods on a path the table writes.
      ['a POST to one Crew', 'POST', '/crews/orders-api', 'application/json', {}, 403],
      ['a POST to one Container', 'POST', '/containers/orders-api', MP, crewAnn('c4.factory.io/retry'), 403],
      ['a PATCH of the Task list', 'PATCH', '/tasks', JP, [add('/spec/goal', 'x')], 403],
      ['a PUT of a Crew', 'PUT', '/crews/orders-api', 'application/json', {}, 405],
      ['a DELETE of a Container', 'DELETE', '/containers/orders-api', 'application/json', {}, 405],
      ['a DELETE of a Task', 'DELETE', '/tasks/t1', 'application/json', {}, 405],
      // Other content types.
      ['a merge patch of a Crew setting', 'PATCH', '/crews/orders-api', MP, { spec: { paused: true } }, 403],
      [
        'a JSON patch of a Container annotation',
        'PATCH',
        '/containers/orders-api',
        JP,
        [add('/metadata/annotations/x', 'y')],
        415,
      ],
      ['a merge patch of a Standard', 'PATCH', '/standards/go-service', MP, { spec: { template: { ref: 'v2' } } }, 415],
      ['a server-side apply of a Crew', 'PATCH', '/crews/orders-api', 'application/apply-patch+yaml', {}, 415],
      ['a Task as a form', 'POST', '/tasks', 'application/x-www-form-urlencoded', 'goal=x', 415],
      // Other fields.
      ["a Brief's idea", 'PATCH', '/briefs/lens', JP, [add('/spec/idea', 'x')], 403],
      ["a Brief's agents", 'PATCH', '/briefs/lens', JP, [add('/spec/agents', {})], 403],
      [
        'a Brief setting with a prompt',
        'PATCH',
        '/briefs/lens',
        JP,
        [add('/spec/paused', true), add('/spec/prompts/-', 'x')],
        403,
      ],
      ["a Crew's implementer", 'PATCH', '/crews/orders-api', JP, [add('/spec/implementer', { name: 'x' })], 403],
      ["a Crew's paused as a string", 'PATCH', '/crews/orders-api', JP, [add('/spec/paused', 'true')], 403],
      ["a Crew's parallel as a fraction", 'PATCH', '/crews/orders-api', JP, [add('/spec/parallel', 1.5)], 403],
      [
        "a Crew's paused twice",
        'PATCH',
        '/crews/orders-api',
        JP,
        [add('/spec/paused', true), add('/spec/paused', false)],
        403,
      ],
      ['an empty patch', 'PATCH', '/crews/orders-api', JP, [], 403],
      ["a Standard's template url", 'PATCH', '/standards/go-service', JP, [add('/spec/template/url', 'x')], 403],
      [
        "a Standard's whole template",
        'PATCH',
        '/standards/go-service',
        JP,
        [add('/spec/template', { ref: 'v2' })],
        403,
      ],
      ['another Crew annotation', 'PATCH', '/crews/orders-api', MP, crewAnn('c4.factory.io/paused-by'), 403],
      ['the retry annotation on a Crew', 'PATCH', '/crews/orders-api', MP, crewAnn('c4.factory.io/retry'), 403],
      [
        'the re-run annotation on a Container',
        'PATCH',
        '/containers/orders-api',
        MP,
        crewAnn('c4.factory.io/rerun-release'),
        403,
      ],
      ['a retry annotation removed', 'PATCH', '/containers/orders-api', MP, crewAnn('c4.factory.io/retry', null), 403],
      [
        'a retry with a label',
        'PATCH',
        '/containers/orders-api',
        MP,
        { metadata: { annotations: { 'c4.factory.io/retry': '1' }, labels: { a: 'b' } } },
        403,
      ],
      [
        'a retry with a spec change',
        'PATCH',
        '/containers/orders-api',
        MP,
        { ...crewAnn('c4.factory.io/retry'), spec: { x: 1 } },
        403,
      ],
      ['a retry as null', 'PATCH', '/containers/orders-api', MP, 'null', 403],
      [
        'a Task of kind Plan',
        'POST',
        '/tasks',
        'application/json',
        { ...task, spec: { ...task.spec, kind: 'Plan' } },
        403,
      ],
      [
        'a Task without a kind',
        'POST',
        '/tasks',
        'application/json',
        { ...task, spec: { ...task.spec, kind: undefined } },
        403,
      ],
      [
        'a Task with a Brief',
        'POST',
        '/tasks',
        'application/json',
        { ...task, spec: { ...task.spec, briefRef: { name: 'b' } } },
        403,
      ],
      [
        'a Task with protected paths',
        'POST',
        '/tasks',
        'application/json',
        { ...task, spec: { ...task.spec, allowedProtectedPaths: ['x'] } },
        403,
      ],
      [
        'a Task in another namespace',
        'POST',
        '/tasks',
        'application/json',
        { ...task, metadata: { namespace: 'kube-system' } },
        403,
      ],
      ['a Crew posted as a Task', 'POST', '/tasks', 'application/json', { ...task, kind: 'Crew' }, 403],
      // Other patch operations.
      [
        "a Crew's paused replaced",
        'PATCH',
        '/crews/orders-api',
        JP,
        [{ op: 'replace', path: '/spec/paused', value: true }],
        403,
      ],
      ["a Crew's paused removed", 'PATCH', '/crews/orders-api', JP, [{ op: 'remove', path: '/spec/paused' }], 403],
      [
        "a Standard's ref moved",
        'PATCH',
        '/standards/go-service',
        JP,
        [{ op: 'move', from: '/spec/template/url', path: '/spec/template/ref' }],
        403,
      ],
      [
        "a Brief's prompt replaced",
        'PATCH',
        '/briefs/lens',
        JP,
        [{ op: 'replace', path: '/spec/prompts/0', value: 'x' }],
        403,
      ],
      ['a test operation', 'PATCH', '/crews/orders-api', JP, [{ op: 'test', path: '/spec/paused', value: true }], 403],
      [
        'an operation with an extra field',
        'PATCH',
        '/crews/orders-api',
        JP,
        [{ ...add('/spec/paused', true), from: '/x' }],
        403,
      ],
      // Other origins.
      [
        'a write from another site',
        'PATCH',
        '/crews/orders-api',
        JP,
        [add('/spec/paused', true)],
        403,
        { origin: 'https://evil.example' },
      ],
    ]
    for (const [what, method, p, type, body, status, headers] of refused) {
      it(`refuses ${what} with ${status}, and writes nothing upstream`, async () => {
        seen.length = 0
        const res = await write(method, p, body, type, headers)
        assert.equal(res.status, status)
        const answer = await res.json()
        assert.equal(answer.kind, 'Status')
        if (status === 403 && !headers) assert.match(answer.message, /The C4 Factory UI writes/)
        assert.equal(seen.length, 0, `${what}: ${seen.map((r) => r.method).join()}`)
      })
    }
  })

  describe('the action table behind an Ingress (UI_ACTIONS=oidc), before anyone can sign in', () => {
    let oidcServer
    let oidcBase
    before(async () => {
      oidcServer = createServer({
        distDir: dist,
        upstream: resolveUpstream({ K8S_API: upstreamUrl }),
        log: () => {},
        actions: 'oidc',
      })
      oidcBase = await listen(oidcServer)
    })
    after(() => close(oidcServer))

    it('says on /ui/session that actions need a person, and nobody is signed in', async () => {
      const answer = await (await fetch(`${oidcBase}/ui/session`)).json()
      assert.deepEqual([answer.actions, answer.mode, answer.user], [true, 'oidc', null])
    })

    it('refuses every write with 401, the three Brief writes included, and writes nothing upstream', async () => {
      const api = '/k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop'
      for (const [method, p, type, body] of [
        ['POST', '/briefs', 'application/json', { apiVersion: 'c4.factory.io/v1alpha1', kind: 'Brief', spec: {} }],
        ['PATCH', '/briefs/lens', 'application/json-patch+json', [{ op: 'add', path: '/spec/paused', value: true }]],
        [
          'PATCH',
          '/containers/orders-api',
          'application/merge-patch+json',
          { metadata: { annotations: { 'c4.factory.io/retry': '1' } } },
        ],
      ]) {
        seen.length = 0
        const res = await fetch(`${oidcBase}${api}${p}`, {
          method,
          headers: { 'content-type': type },
          body: JSON.stringify(body),
        })
        assert.equal(res.status, 401, `${method} ${p}`)
        assert.equal(seen.length, 0)
      }
    })
  })

  it('refuses an unknown UI_ACTIONS', () => {
    assert.throws(() => actionsFromEnv({ UI_ACTIONS: 'everyone' }), /UI_ACTIONS is everyone/)
    assert.equal(actionsFromEnv({}), false)
    assert.equal(actionsFromEnv({ UI_ACTIONS: 'service-account' }), 'service-account')
  })

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

  describe('the Host it answers (DNS rebinding)', () => {
    const projects = '/k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/projects'
    const briefs = '/k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/briefs'
    const brief = {
      apiVersion: 'c4.factory.io/v1alpha1',
      kind: 'Brief',
      metadata: { name: 'lens' },
      spec: { idea: 'A camera app that reads labels aloud' },
    }
    let named
    let namedBase

    before(async () => {
      named = createServer({
        distDir: dist,
        upstream: resolveUpstream({ K8S_API: `http://127.0.0.1:${upstream.address().port}` }),
        log: () => {},
        allowedHosts: allowedHostsFromEnv({ UI_ALLOWED_HOSTS: ' Factory.Example.com , ,ui.internal' }),
      })
      namedBase = await listen(named)
    })

    after(() => close(named))

    for (const host of ['evil.example', 'evil.example:8080', '127.0.0.1.evil.example', 'localhost.']) {
      it(`refuses reads, writes and files for Host ${host}`, async () => {
        seen.length = 0
        for (const [method, p, body] of [
          ['GET', projects],
          ['GET', '/k8s-watch?w=x'],
          ['GET', '/factory/health'],
          ['GET', '/'],
          ['POST', briefs, brief],
        ]) {
          const res = await request(base, method, p, host, body)
          assert.equal(res.status, 421, `${method} ${p}`)
          assert.doesNotMatch(res.text, /C4 Factory|ProjectList/)
        }
        assert.deepEqual(seen, [])
      })
    }

    it('refuses a name it is not configured for, even beside configured ones', async () => {
      const res = await request(namedBase, 'GET', projects, 'evil.example')
      assert.equal(res.status, 421)
    })

    it('answers /healthz for any Host, for the kubelet', async () => {
      const res = await request(base, 'GET', '/healthz', '10.244.0.7:8080')
      assert.equal(res.status, 200)
    })

    for (const host of ['localhost:8080', 'LOCALHOST', '127.0.0.1:8080', '[::1]:8080', '[::1]']) {
      it(`answers Host ${host}`, async () => {
        const res = await request(base, 'GET', projects, host)
        assert.equal(res.status, 200)
        assert.match(res.text, /ProjectList/)
      })
    }

    for (const host of ['factory.example.com', 'ui.internal:443', 'localhost:8080']) {
      it(`answers a configured or local Host ${host}, for reads and writes`, async () => {
        assert.equal((await request(namedBase, 'GET', projects, host)).status, 200)
        assert.equal((await request(namedBase, 'POST', briefs, host, brief)).status, 201)
      })
    }
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
