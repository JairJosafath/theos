// A tiny in-memory Kubernetes API for theos.factory.io kinds, with lists and
// watches, for the end-to-end test and the screenshots of every view. It
// answers GETs of collections, and the factory health's Pods and Nodes as
// empty lists; a GET of one object; a POST that creates an object
// (generateName included), a merge patch of one and a JSON patch of add,
// remove, replace and test ops, refusing what a CRD rule of REFUSE would, or
// what refuseNextWrite() asks, with the API server's message; everything
// else is 404. A kind it is told to refuse answers 403, as RBAC would.

import http from 'node:http'
// The UI's own list of kinds, so the fake API serves every one.
import { RESOURCES } from '../src/k8s/types.ts'

/**
 * The CRD rules the fake API keeps, as the API server words their refusal:
 * plural -> object -> message, or undefined when it passes.
 */
const REFUSE = {
  tasks: (obj) =>
    typeof obj.spec?.goal === 'string' && obj.spec.goal.length < 1
      ? `Task.theos.factory.io "${obj.metadata.name}" is invalid: spec.goal: Invalid value: "": spec.goal in body should be at least 1 chars long`
      : undefined,
  crews: (obj) => {
    if (obj.spec?.parallel > 5) {
      return `Crew.theos.factory.io "${obj.metadata.name}" is invalid: spec.parallel: Invalid value: ${obj.spec.parallel}: spec.parallel in body should be less than or equal to 5`
    }
    if (obj.spec?.publishReleases && !obj.spec.tagReleases) {
      return `Crew.theos.factory.io "${obj.metadata.name}" is invalid: spec: Invalid value: "object": publishReleases needs tagReleases: only a release the Crew tagged is published`
    }
    return undefined
  },
  briefs: (obj) => {
    const i = (obj.spec?.releases ?? []).findIndex(
      (r) => r.requests && r.requests.mode !== 'Off' && !r.requests.authors?.length && !r.requests.teams?.length,
    )
    return i < 0
      ? undefined
      : `Brief.theos.factory.io "${obj.metadata.name}" is invalid: spec.releases[${i}].requests: Invalid value: "object": requests need authors or teams unless mode is Off`
  },
  standards: (obj) =>
    typeof obj.spec?.template?.ref === 'string' && obj.spec.template.ref.length < 1
      ? `Standard.theos.factory.io "${obj.metadata.name}" is invalid: spec.template.ref: Invalid value: "": spec.template.ref in body should be at least 1 chars long`
      : undefined,
}

function status(res, code, reason, message) {
  res.writeHead(code, { 'content-type': 'application/json' })
  res.end(JSON.stringify({ kind: 'Status', status: 'Failure', code, reason, message }))
}

function readJson(req) {
  return new Promise((resolve) => {
    let text = ''
    req.on('data', (chunk) => (text += chunk))
    req.on('end', () => {
      try {
        resolve(JSON.parse(text))
      } catch {
        resolve(undefined)
      }
    })
  })
}

/** A JSON merge patch (RFC 7396) of `patch` onto `target`. */
function mergePatch(target, patch) {
  if (patch === null || typeof patch !== 'object' || Array.isArray(patch)) return patch
  const out = target !== null && typeof target === 'object' && !Array.isArray(target) ? { ...target } : {}
  for (const [k, v] of Object.entries(patch)) {
    if (v === null) delete out[k]
    else out[k] = mergePatch(out[k], v)
  }
  return out
}

/**
 * A JSON patch (RFC 6902) of add, remove, replace and test ops onto `target`, on
 * object members, and an add at `-` appending to a list; undefined for any
 * other op or path, or when a test fails.
 */
function jsonPatch(target, ops) {
  if (!Array.isArray(ops)) return undefined
  const out = structuredClone(target)
  for (const op of ops) {
    if (
      !['add', 'remove', 'replace', 'test'].includes(op?.op) ||
      typeof op.path !== 'string' ||
      !op.path.startsWith('/')
    )
      return undefined
    const keys = op.path.slice(1).split('/')
    const last = keys.pop()
    let at = out
    for (const [i, k] of keys.entries()) {
      // A list only as the target of an append.
      const list = Array.isArray(at[k]) && i === keys.length - 1 && last === '-'
      if (at[k] === null || typeof at[k] !== 'object' || (Array.isArray(at[k]) && !list)) return undefined
      at = at[k]
    }
    if (op.op === 'test') {
      if (at[last] !== op.value) return undefined
    } else if (op.op === 'remove') {
      if (Array.isArray(at) || !(last in at)) return undefined
      delete at[last]
    } else if (last === '-' && Array.isArray(at)) at.push(op.value)
    else at[last] = op.value
  }
  return out
}

/** A fake API whose objects default to `namespace` when they name none. */
export function fakeApi(namespace) {
  let rv = 1
  const store = new Map() // plural -> Map(namespace/name -> object)
  const watchers = new Map() // plural -> Set(res)
  const refused = new Set() // plurals answered with 403
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
  /** Every write it took or refused, in order: method, path, body. */
  const writes = []
  let suffix = 0
  // The message the next write is refused with, set by refuseNextWrite().
  let refusal
  const write = async (req, res, plural, ns, name) => {
    const body = await readJson(req)
    const contentType = req.headers['content-type']
    writes.push({ method: req.method, path: new URL(req.url, 'http://x').pathname, contentType, body })
    if (refusal) {
      const message = refusal
      refusal = undefined
      return status(res, 422, 'Invalid', message)
    }
    let obj
    if (req.method === 'POST') {
      if (!body?.metadata) return status(res, 400, 'BadRequest', 'the body is not an object')
      const generated = body.metadata.generateName
        ? `${body.metadata.generateName}${String(++suffix).padStart(5, 'x')}`
        : ''
      obj = { ...body, metadata: { ...body.metadata, namespace: ns, name: body.metadata.name ?? generated } }
      delete obj.metadata.generateName
    } else {
      const type = req.headers['content-type']
      if (type !== 'application/merge-patch+json' && type !== 'application/json-patch+json')
        return status(res, 415, 'UnsupportedMediaType', 'the fake API takes only merge and JSON patches')
      const old = store.get(plural).get(`${ns}/${name}`)
      if (!old) return status(res, 404, 'NotFound', `${plural} "${name}" not found`)
      obj = type === 'application/merge-patch+json' ? mergePatch(old, body) : jsonPatch(old, body)
      if (!obj) return status(res, 422, 'UnprocessableEntity', 'the fake API takes only add, remove and replace ops')
    }
    const refused = REFUSE[plural]?.(obj)
    if (refused) return status(res, 422, 'Invalid', refused)
    const created = req.method === 'POST'
    if (created && store.get(plural).has(`${ns}/${obj.metadata.name}`))
      return status(res, 409, 'AlreadyExists', `${plural} "${obj.metadata.name}" already exists`)
    send(plural, created ? 'ADDED' : 'MODIFIED', put(plural, obj))
    res.writeHead(created ? 201 : 200, { 'content-type': 'application/json' })
    res.end(JSON.stringify(obj))
  }
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://x')
    const one = url.pathname.match(/^\/apis\/theos\.factory\.io\/v1beta1\/namespaces\/([^/]+)\/([a-z]+)(?:\/([^/]+))?$/)
    if (one && store.has(one[2]) && ((req.method === 'POST' && !one[3]) || (req.method === 'PATCH' && one[3]))) {
      void write(req, res, one[2], one[1], one[3] && decodeURIComponent(one[3]))
      return
    }
    if (one && store.has(one[2]) && req.method === 'GET' && one[3]) {
      const obj = store.get(one[2]).get(`${one[1]}/${decodeURIComponent(one[3])}`)
      if (!obj) return status(res, 404, 'NotFound', `${one[2]} "${one[3]}" not found`)
      res.writeHead(200, { 'content-type': 'application/json' })
      return res.end(JSON.stringify(obj))
    }
    if (
      req.method === 'GET' &&
      (/^\/api\/v1\/namespaces\/[^/]+\/pods$/.test(url.pathname) || url.pathname === '/api/v1/nodes')
    ) {
      res.writeHead(200, { 'content-type': 'application/json' })
      return res.end(JSON.stringify({ metadata: { resourceVersion: String(rv) }, items: [] }))
    }
    const m = url.pathname.match(/^\/apis\/theos\.factory\.io\/v1beta1(?:\/namespaces\/([^/]+))?\/([a-z]+)$/)
    if (req.method !== 'GET' || !m || !store.has(m[2])) {
      res.writeHead(404, { 'content-type': 'application/json' })
      return res.end(JSON.stringify({ kind: 'Status', code: 404, message: 'not found' }))
    }
    const [, ns, plural] = m
    if (refused.has(plural)) {
      res.writeHead(403, { 'content-type': 'application/json' })
      return res.end(
        JSON.stringify({
          kind: 'Status',
          code: 403,
          reason: 'Forbidden',
          message: `${plural}.theos.factory.io is forbidden: the UI's role cannot list it`,
        }),
      )
    }
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
    writes,
    /** The stored object of a kind, as the API has it now. */
    get(plural, name) {
      return store.get(plural).get(`${namespace}/${name}`)
    },
    /** Refuses the next write with 422 and `message`, as the API server does when the CRD refuses a value. */
    refuseNextWrite(message) {
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
    /** Answers every read of a kind with 403 from now on, and ends its open watches. */
    refuse(plural) {
      refused.add(plural)
      for (const w of [...watchers.get(plural)]) {
        watchers.get(plural).delete(w)
        w.res.end()
      }
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
