// A tiny in-memory Kubernetes API for the c4.factory.io kinds, with lists and
// watches, for the end-to-end test and the screenshots of every view. It
// answers GETs of collections and of one object, a JSON patch of one object
// (add and test ops, as the UI server sends them), and the factory health's
// Pods and Nodes as empty lists; everything else is 404. refuse() makes the
// next write fail as the API server refuses one.

import http from 'node:http'
// The UI's own list of kinds, so the fake API serves every one.
import { RESOURCES } from '../src/k8s/types.ts'

/** A fake API whose objects default to `namespace` when they name none. */
export function fakeApi(namespace) {
  let rv = 1
  const store = new Map() // plural -> Map(namespace/name -> object)
  const watchers = new Map() // plural -> Set(res)
  for (const k of Object.values(RESOURCES)) {
    store.set(k, new Map())
    watchers.set(k, new Set())
  }
  const key = (obj) => `${obj.metadata.namespace}/${obj.metadata.name}`
  const put = (plural, obj) => {
    obj.metadata = { namespace, ...obj.metadata, resourceVersion: String(++rv) }
    store.get(plural).set(key(obj), obj)
    return obj
  }
  // Every event, so a watch from resourceVersion R first gets what happened
  // after R, as the real API server does.
  const history = []
  const send = (plural, type, obj) => {
    history.push({
      plural,
      namespace: obj.metadata.namespace,
      rv: Number(obj.metadata.resourceVersion),
      line: `${JSON.stringify({ type, object: obj })}\n`,
    })
    for (const w of watchers.get(plural))
      if (!w.namespace || w.namespace === obj.metadata.namespace) w.res.write(history.at(-1).line)
  }
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://x')
    if (req.method === 'GET' && (url.pathname === '/api/v1/pods' || url.pathname === '/api/v1/nodes')) {
      res.writeHead(200, { 'content-type': 'application/json' })
      return res.end(JSON.stringify({ metadata: { resourceVersion: String(rv) }, items: [] }))
    }
    const one = url.pathname.match(/^\/apis\/c4\.factory\.io\/v1alpha1\/namespaces\/([^/]+)\/([a-z]+)\/([^/]+)$/)
    if (one && store.has(one[2]) && (req.method === 'GET' || req.method === 'PATCH'))
      return object(req, res, ...one.slice(1))
    const m = url.pathname.match(/^\/apis\/c4\.factory\.io\/v1alpha1(?:\/namespaces\/([^/]+))?\/([a-z]+)$/)
    if (req.method !== 'GET' || !m || !store.has(m[2])) {
      res.writeHead(404, { 'content-type': 'application/json' })
      return res.end(JSON.stringify({ kind: 'Status', code: 404, message: 'not found' }))
    }
    const [, ns, plural] = m
    const inScope = (objNs) => !ns || objNs === ns
    if (url.searchParams.get('watch') === '1') {
      res.writeHead(200, { 'content-type': 'application/json' })
      res.flushHeaders()
      const from = Number(url.searchParams.get('resourceVersion') || 0)
      for (const h of history) if (h.plural === plural && h.rv > from && inScope(h.namespace)) res.write(h.line)
      const w = { res, namespace: ns }
      watchers.get(plural).add(w)
      res.on('close', () => watchers.get(plural).delete(w))
      return
    }
    const items = [...store.get(plural).values()].filter((o) => inScope(o.metadata.namespace))
    res.writeHead(200, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ metadata: { resourceVersion: String(rv) }, items }))
  })
  /** GET or PATCH one object; the patch is recorded in `writes`, or refused when refuse() asked. */
  function object(req, res, ns, plural, name) {
    const reply = (code, body) => {
      res.writeHead(code, { 'content-type': 'application/json' })
      res.end(JSON.stringify(body))
    }
    const obj = store.get(plural).get(`${ns}/${name}`)
    if (!obj) return reply(404, { kind: 'Status', code: 404, message: `${plural} "${name}" not found` })
    if (req.method === 'GET') return reply(200, obj)
    let body = ''
    req.on('data', (chunk) => (body += chunk))
    req.on('end', () => {
      const ops = JSON.parse(body)
      writes.push({ plural, name, contentType: req.headers['content-type'], ops })
      if (refusal) {
        const message = refusal
        refusal = undefined
        return reply(422, { kind: 'Status', status: 'Failure', code: 422, reason: 'Invalid', message })
      }
      const next = structuredClone(obj)
      for (const op of ops) {
        const keys = op.path.split('/').slice(1)
        const last = keys.pop()
        const parent = keys.reduce((o, k) => o[k], next)
        if (op.op === 'test' && parent[last] !== op.value)
          return reply(409, { kind: 'Status', code: 409, message: `the object has been modified` })
        if (op.op === 'add') {
          if (last === '-') parent.push(op.value)
          else parent[last] = op.value
        }
      }
      send(plural, 'MODIFIED', put(plural, next))
      reply(200, next)
    })
  }

  const writes = []
  let refusal
  return {
    server,
    /** Every PATCH the API was sent, oldest first: plural, name, content type and the ops. */
    writes,
    /** Refuses the next write with 422 and `message`, as the API server does when the CRD refuses a value. */
    refuse(message) {
      refusal = message
    },
    /** How many watches of a kind are open. */
    watching(plural) {
      return watchers.get(plural).size
    },
    add(plural, obj) {
      send(plural, 'ADDED', put(plural, obj))
    },
    modify(plural, obj) {
      send(plural, 'MODIFIED', put(plural, obj))
    },
    remove(plural, name) {
      const k = `${namespace}/${name}`
      const obj = store.get(plural).get(k)
      store.get(plural).delete(k)
      send(plural, 'DELETED', { ...obj, metadata: { ...obj.metadata, resourceVersion: String(++rv) } })
    },
    /** Ends every open watch of a kind with an "expired" error, as the API does after compaction. */
    expire(plural) {
      for (const w of [...watchers.get(plural)]) {
        watchers.get(plural).delete(w)
        w.res.end(
          `${JSON.stringify({ type: 'ERROR', object: { kind: 'Status', code: 410, reason: 'Expired', message: 'too old resource version' } })}\n`,
        )
      }
    },
  }
}

/** Listens on a free port of 127.0.0.1 and resolves to the server's base URL. */
export function listen(server) {
  return new Promise((resolve) =>
    server.listen(0, '127.0.0.1', () => resolve(`http://127.0.0.1:${server.address().port}`)),
  )
}
