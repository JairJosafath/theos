// A tiny in-memory Kubernetes API for the c4.factory.io kinds, with lists and
// watches, for the end-to-end test and the screenshots of every view. It
// answers GETs of collections, the factory health's Pods and Nodes as empty
// lists, and PATCHes of one object (a JSON patch of add and remove, or a merge
// patch), refused with 422 when a rule given to refuse() says so, as a CRD's
// rule would; everything else is 404.

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
  const rules = [] // (plural, obj) => a refusal's message, or undefined
  const patches = [] // { plural, name, type, body }, each PATCH as it came
  const status = (res, code, message) => {
    res.writeHead(code, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ kind: 'Status', code, message }))
  }
  const patch = async (req, res, ns, plural, name) => {
    const old = store.get(plural)?.get(`${ns}/${name}`)
    if (!old) return status(res, 404, `${plural} "${name}" not found`)
    let text = ''
    for await (const chunk of req) text += chunk
    const body = JSON.parse(text)
    const type = (req.headers['content-type'] ?? '').split(';')[0]
    patches.push({ plural, name, type, body })
    const obj = structuredClone(old)
    if (type === 'application/json-patch+json') {
      for (const op of body) {
        const keys = op.path.split('/').slice(1)
        const last = keys.pop()
        const parent = keys.reduce((o, k) => (o[k] ??= {}), obj)
        if (op.op === 'add') parent[last] = op.value
        else if (op.op === 'remove' && last in parent) delete parent[last]
        else return status(res, 422, `the ${op.op} of ${op.path} failed`)
      }
    } else if (type === 'application/merge-patch+json') {
      const merge = (into, from) => {
        for (const [k, v] of Object.entries(from)) {
          if (v === null) delete into[k]
          else if (typeof v === 'object' && !Array.isArray(v)) merge((into[k] ??= {}), v)
          else into[k] = v
        }
      }
      merge(obj, body)
    } else {
      return status(res, 415, `unsupported media type ${type}`)
    }
    const refusal = rules.map((rule) => rule(plural, obj)).find(Boolean)
    if (refusal) return status(res, 422, refusal)
    send(plural, 'MODIFIED', put(plural, obj))
    res.writeHead(200, { 'content-type': 'application/json' })
    res.end(JSON.stringify(obj))
  }
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://x')
    const one = url.pathname.match(/^\/apis\/c4\.factory\.io\/v1alpha1\/namespaces\/([^/]+)\/([a-z]+)\/([^/]+)$/)
    if (req.method === 'PATCH' && one) {
      patch(req, res, one[1], one[2], decodeURIComponent(one[3])).catch((err) => status(res, 500, err.message))
      return
    }
    if (req.method === 'GET' && (url.pathname === '/api/v1/pods' || url.pathname === '/api/v1/nodes')) {
      res.writeHead(200, { 'content-type': 'application/json' })
      return res.end(JSON.stringify({ metadata: { resourceVersion: String(rv) }, items: [] }))
    }
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
  return {
    server,
    /** Every PATCH so far, in order. */
    patches,
    /** Refuses a PATCH whose result `rule(plural, obj)` returns a message for, with that message. */
    refuse(rule) {
      rules.push(rule)
    },
    /** The stored object, as the API has it now. */
    get(plural, name) {
      return store.get(plural).get(`${namespace}/${name}`)
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
