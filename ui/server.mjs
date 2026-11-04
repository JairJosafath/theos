#!/usr/bin/env node
// Serves the built UI (dist/) and a read-only gateway to the Kubernetes API.
//
//   GET /healthz          200 "ok"
//   GET /k8s/<api path>   the Kubernetes API, as-is: lists, gets, single watches
//   GET /k8s-watch?...    several watches over one response (see watchMany)
//   GET /factory/health   the factory's components, node, agent sandboxes and last hour, summarized (health.mjs)
//   GET /downloads/theos-session-linux-<amd64|arm64>
//                         the Session client, from downloads/ (the image builds it; #1151)
//   GET /downloads/SHA256SUMS
//                         its checksums, which the theos-session feature verifies (#1175)
//   GET /...              files from dist/
//
//   GET /ui/session       whether actions are on, who makes them, who is signed in, and the writes
//   POST, PATCH /k8s/...  the writes of the action table (see ACTIONS)
//   GET /ui/login, GET /ui/callback, POST /ui/logout
//                         sign-in with OIDC, with UI_ACTIONS=oidc (oidc.mjs)
//
// Read-only but for the action table's writes: GET reaches the API only under
// /apis/theos.factory.io/. With actions off (UI_ACTIONS unset) the only writes
// are creating a Brief in a namespace that holds a Project, and a JSON patch
// on a Brief that appends a prompt at /spec/prompts/- (to a full list, the
// server retires the planned prompts past the newest ten in the same patch)
// or adds a boolean at /spec/paused, /spec/pauseRuns or both. With UI_ACTIONS=service-account (over port-forward) the table
// also takes design.md's UI actions: a Brief's and a Crew's settings, a
// release re-run, a Container's scaffold retry, its answer to the Standard's
// files left out (take or keep), a Task from a goal and a Standard's ref.
// With UI_ACTIONS=oidc every write needs a person signed in, and is made as them: Impersonate-User and Impersonate-Group, names the
// server takes from their ID token. Reads stay the UI's own.
// Every other method on /k8s gets 405, every other write 403. The browser's
// own headers (cookies, Authorization, impersonation) never go upstream.
//
// Upstream, in order of preference:
//   K8S_API=http://127.0.0.1:8001   a `kubectl proxy`, or any API URL (no auth added)
//   in a Pod                        https://kubernetes.default.svc with the
//                                   ServiceAccount token and CA
//   otherwise                       http://127.0.0.1:8001
//
// No runtime dependencies: only Node's standard library.

import { existsSync, readFileSync } from 'node:fs'
import { readFile, stat } from 'node:fs/promises'
import http from 'node:http'
import https from 'node:https'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { componentsFromEnv, createHealthHandler } from './health.mjs'
import { createOIDC, oidcFromEnv } from './oidc.mjs'

const SERVICE_ACCOUNT_DIR = '/var/run/secrets/kubernetes.io/serviceaccount'

/** Only theos.factory.io API group is reachable: the UI needs nothing else. */
const ALLOWED_PREFIX = '/apis/theos.factory.io/'

/** The JSON patch paths the UI may write: append a prompt, and pause or resume a Brief or its running Tasks. */
const PROMPT_PATH = '/spec/prompts/-'
const PAUSED_PATH = '/spec/paused'
const PAUSE_RUNS_PATH = '/spec/pauseRuns'

/** Most bytes of a write's body; an idea is at most 4000 characters. */
const MAX_WRITE_BYTES = 64 * 1024

/** Who the API server records as writing the UI's changes. */
const FIELD_MANAGER = 'theos-ui'

/** Most watches one /k8s-watch request may carry. */
const MAX_WATCHES = 32

/**
 * The files under /downloads/: the Session client the image builds for each
 * architecture (ui/Dockerfile), which a scaffolded devcontainer's theos-session
 * feature fetches from the chart's default sessionBinaryURL (#1151), and the
 * SHA256SUMS over both, which the feature checks the download against (#1175).
 * Any other name is 404.
 */
export const DOWNLOADS = ['theos-session-linux-amd64', 'theos-session-linux-arm64', 'SHA256SUMS']

/** Request headers passed upstream. Everything else, auth and cookies included, is dropped. */
const FORWARDED_REQUEST_HEADERS = ['accept', 'user-agent']

/**
 * Response headers passed back to the browser. `warning` carries the API
 * server's deprecation notice for a request at a deprecated version
 * (theos.factory.io/v1alpha1, #1618), which the project page shows.
 */
const FORWARDED_RESPONSE_HEADERS = ['content-type', 'cache-control', 'date', 'audit-id', 'warning']

const CONTENT_TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.txt': 'text/plain; charset=utf-8',
  '.map': 'application/json; charset=utf-8',
}

const SECURITY_HEADERS = {
  'x-content-type-options': 'nosniff',
  'referrer-policy': 'no-referrer',
  'x-frame-options': 'DENY',
  'content-security-policy':
    "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'none'",
}

/**
 * Works out where the Kubernetes API is and how to authenticate to it.
 * @param {Record<string, string | undefined>} env
 */
export function resolveUpstream(env = process.env, saDir = SERVICE_ACCOUNT_DIR) {
  if (env.K8S_API) {
    return { url: new URL(env.K8S_API), mode: 'K8S_API', token: () => undefined, ca: undefined }
  }
  const tokenFile = path.join(saDir, 'token')
  if (existsSync(tokenFile)) {
    const host = env.KUBERNETES_SERVICE_HOST
    const port = env.KUBERNETES_SERVICE_PORT || '443'
    const base = host ? `https://${host.includes(':') ? `[${host}]` : host}:${port}` : 'https://kubernetes.default.svc'
    // The kubelet rotates the token file, so re-read it now and then.
    let cached = { value: '', readAt: 0 }
    const token = () => {
      if (Date.now() - cached.readAt > 60_000) {
        cached = { value: readFileSync(tokenFile, 'utf8').trim(), readAt: Date.now() }
      }
      return cached.value
    }
    const caFile = path.join(saDir, 'ca.crt')
    return { url: new URL(base), mode: 'in-cluster', token, ca: existsSync(caFile) ? readFileSync(caFile) : undefined }
  }
  return { url: new URL('http://127.0.0.1:8001'), mode: 'default kubectl proxy', token: () => undefined, ca: undefined }
}

function send(res, status, body, headers = {}) {
  res.writeHead(status, { 'content-type': 'text/plain; charset=utf-8', ...SECURITY_HEADERS, ...headers })
  res.end(body)
}

/** A Kubernetes-style Status body, so the UI can show the reason. */
function sendStatus(res, code, reason, message, headers = {}) {
  const body = JSON.stringify({ kind: 'Status', apiVersion: 'v1', status: 'Failure', reason, message, code })
  send(res, code, `${body}\n`, { 'content-type': 'application/json', ...headers })
}

/**
 * True for an API path the UI may read: inside theos.factory.io, plain characters
 * only (resource paths never need escapes, so no encoded slashes or dots), and
 * no "." or ".." segments.
 */
export function isAllowedApiPath(apiPath) {
  return (
    apiPath.startsWith(ALLOWED_PREFIX) &&
    /^[A-Za-z0-9._\-/]+$/.test(apiPath) &&
    !apiPath.split('/').some((segment) => segment === '.' || segment === '..')
  )
}

/**
 * One request to the API. `as`, a person signed in ({ user, groups }), makes
 * it a write as them: the UI's own credentials, impersonating.
 */
function upstreamRequest(upstream, apiPath, search, onResponse, { method = 'GET', contentType, as } = {}) {
  const target = new URL(upstream.url)
  target.pathname = `${upstream.url.pathname.replace(/\/$/, '')}${apiPath}`
  target.search = search
  const headers = { accept: 'application/json' }
  if (contentType) headers['content-type'] = contentType
  if (as) {
    headers['impersonate-user'] = as.user
    if (as.groups.length) headers['impersonate-group'] = as.groups
  }
  const token = upstream.token()
  if (token) headers.authorization = `Bearer ${token}`
  const client = target.protocol === 'https:' ? https : http
  return client.request(target, { method, headers, ca: upstream.ca }, onResponse)
}

/** Most bytes getJson reads; a cluster-wide pod list on a small cluster is far less. */
const MAX_JSON_BYTES = 32 * 1024 * 1024

/**
 * Reads one API path for the server itself (not for the browser, so not limited
 * to theos.factory.io). Resolves to { status, body }.
 */
function getJson(upstream, apiPath) {
  return requestJson(upstream, apiPath)
}

/**
 * One request of the server's own to the API, with an optional JSON body.
 * Resolves to { status, body }.
 */
function requestJson(upstream, apiPath, { method = 'GET', search = '', contentType, body, as } = {}) {
  return new Promise((resolve, reject) => {
    const options = { method, contentType: body === undefined ? undefined : contentType, as }
    const req = upstreamRequest(
      upstream,
      apiPath,
      search,
      (upstreamRes) => {
        const chunks = []
        let size = 0
        upstreamRes.on('data', (chunk) => {
          size += chunk.length
          if (size > MAX_JSON_BYTES)
            return req.destroy(new Error(`${apiPath} answered more than ${MAX_JSON_BYTES} bytes`))
          chunks.push(chunk)
        })
        upstreamRes.on('end', () =>
          resolve({ status: upstreamRes.statusCode ?? 502, body: parseJson(Buffer.concat(chunks).toString('utf8')) }),
        )
        upstreamRes.on('error', reject)
      },
      options,
    )
    req.setTimeout(10_000, () => req.destroy(new Error(`${apiPath} took longer than 10 s`)))
    req.on('error', reject)
    req.end(body === undefined ? undefined : JSON.stringify(body))
  })
}

/** Reads a request's body, at most `limit` bytes; resolves to undefined past it. */
function readBody(req, limit) {
  return new Promise((resolve, reject) => {
    const chunks = []
    let size = 0
    let tooBig = false
    req.on('data', (chunk) => {
      size += chunk.length
      if (size > limit) tooBig = true
      else chunks.push(chunk)
    })
    req.on('end', () => resolve(tooBig ? undefined : Buffer.concat(chunks).toString('utf8')))
    req.on('error', reject)
  })
}

/** The media type of a request, without parameters such as charset. */
function mediaType(req) {
  return (req.headers['content-type'] ?? '').split(';')[0].trim().toLowerCase()
}

/** Host names the server always answers: a browser on this machine. */
const LOCAL_HOSTS = ['localhost', '127.0.0.1', '[::1]']

/**
 * The host names the server answers: localhost and the comma-separated names in
 * UI_ALLOWED_HOSTS (an Ingress's host, for example). Ports don't matter.
 */
export function allowedHostsFromEnv(env = process.env) {
  const extra = (env.UI_ALLOWED_HOSTS ?? '')
    .split(',')
    .map((name) => name.trim().toLowerCase())
    .filter(Boolean)
  return new Set([...LOCAL_HOSTS, ...extra])
}

/**
 * True when the request's Host is one the server answers. A page on another
 * site that rebinds its name to this address (DNS rebinding) sends its own name
 * in Host, and in a matching Origin, so only the Host tells it apart.
 */
function isServedHost(req, allowed) {
  const host = req.headers.host
  if (!host) return false
  try {
    return allowed.has(new URL(`http://${host}`).hostname.toLowerCase())
  } catch {
    return false
  }
}

/** True when a browser sent the request from another site: its Origin names another host. */
function isCrossSite(req) {
  const origin = req.headers.origin
  if (!origin) return false
  try {
    return new URL(origin).host !== req.headers.host
  } catch {
    return true
  }
}

const isObject = (value) => typeof value === 'object' && value !== null && !Array.isArray(value)

/** A namespaced theos.factory.io resource path: the collection, or one object with `one`. */
const resourcePath = (plural, one = false) =>
  new RegExp(
    `^/apis/c4\\.factory\\.io/v1beta1/namespaces/([a-z0-9-]{1,63})/${plural}${one ? '/([a-z0-9.-]{1,253})' : ''}$`,
  )

const JSON_MEDIA = 'application/json'
const JSON_PATCH = 'application/json-patch+json'
const MERGE_PATCH = 'application/merge-patch+json'

/** True when value has the JSON type `type`: boolean, integer, string, array or object. */
function hasType(value, type) {
  if (type === 'integer') return Number.isInteger(value)
  if (type === 'array') return Array.isArray(value)
  if (type === 'object') return isObject(value)
  return typeof value === type
}

/**
 * A JSON patch of `add` operations, each on one of `fields` (path to JSON
 * type), no path twice. `add` sets a field whether or not it is there; a path
 * in `removable` may instead be a `remove`, which unsets it. Returns the
 * operations, or undefined for any other patch.
 */
function addsFields(fields, removable = []) {
  return (body) => {
    if (!Array.isArray(body) || body.length === 0 || body.length > Object.keys(fields).length) return undefined
    const paths = new Set()
    for (const op of body) {
      if (!isObject(op) || typeof op.path !== 'string' || !Object.hasOwn(fields, op.path) || paths.has(op.path)) {
        return undefined
      }
      const keys = Object.keys(op).sort().join()
      const adds = op.op === 'add' && keys === 'op,path,value' && hasType(op.value, fields[op.path])
      const removes = op.op === 'remove' && keys === 'op,path' && removable.includes(op.path)
      if (!adds && !removes) return undefined
      paths.add(op.path)
    }
    return body
  }
}

/** A merge patch setting exactly one annotation, `key`, to a string. Returns the patch, or undefined. */
function setsAnnotation(key) {
  return (body) => {
    const metadata = isObject(body) && Object.keys(body).join() === 'metadata' ? body.metadata : undefined
    const annotations =
      isObject(metadata) && Object.keys(metadata).join() === 'annotations' ? metadata.annotations : undefined
    const ok = isObject(annotations) && Object.keys(annotations).join() === key && typeof annotations[key] === 'string'
    return ok ? { metadata: { annotations: { [key]: annotations[key] } } } : undefined
  }
}

/**
 * A new object of `kind` in the path's namespace, its spec only of `specFields`
 * and with `fixed` values where given. Only its name (or generateName) and spec
 * go upstream: no labels, owners or finalizers. Returns it, or undefined.
 */
function createsObject(kind, specFields, fixed = {}) {
  return (body, namespace) => {
    if (!isObject(body) || body.apiVersion !== 'theos.factory.io/v1beta1' || body.kind !== kind) return undefined
    const meta = isObject(body.metadata) ? body.metadata : {}
    if (meta.namespace !== undefined && meta.namespace !== namespace) return undefined
    if (specFields && !(isObject(body.spec) && Object.keys(body.spec).every((f) => specFields.includes(f)))) {
      return undefined
    }
    if (Object.entries(fixed).some(([f, value]) => body.spec?.[f] !== value)) return undefined
    const metadata = { namespace }
    if (typeof meta.name === 'string') metadata.name = meta.name
    if (typeof meta.generateName === 'string') metadata.generateName = meta.generateName
    return { apiVersion: body.apiVersion, kind, metadata, spec: body.spec }
  }
}

/**
 * A JSON patch approving a version at one spec.deploy entry: a `test` that the
 * entry at index N names a target (a string), then a `test` of the approval
 * the page showed, either the entry's approved as it was (a string) or, where
 * it had none, the whole entry as it was (its target, after and version, all
 * strings, and no approved), then its only write, an `add` of a string at
 * /spec/deploy/N/approved, the same N throughout. The tests keep a list
 * reordered since the page read it from approving another target, and a stale
 * page from taking back a newer approval, a first one too (#1461): JSON Patch
 * can't test that a member is missing, so that test is of the whole entry.
 * Returns the patch, or undefined.
 */
function approvesDeploy(body) {
  if (!Array.isArray(body) || body.length !== 3) return undefined
  const op = (o, name, field, valid = (v) => typeof v === 'string') => {
    if (!isObject(o) || o.op !== name || Object.keys(o).sort().join() !== 'op,path,value') return undefined
    if (!valid(o.value)) return undefined
    const m = typeof o.path === 'string' ? /^\/spec\/deploy\/(0|[1-9][0-9]?)(?:\/([a-z]+))?$/.exec(o.path) : null
    return m && m[2] === field ? m[1] : undefined
  }
  const entry = (v) =>
    isObject(v) &&
    typeof v.target === 'string' &&
    Object.entries(v).every(([k, f]) => ['target', 'after', 'version'].includes(k) && typeof f === 'string')
  const index = op(body[0], 'test', 'target')
  if (index === undefined || op(body[2], 'add', 'approved') !== index) return undefined
  return op(body[1], 'test', 'approved') === index || op(body[1], 'test', undefined, entry) === index ? body : undefined
}

/**
 * The UI's writes, checked before anything reaches the API (design.md, UI
 * actions). Each entry is one request: a method, a path, a content type and a
 * check of the body, which returns what goes upstream or undefined. The check
 * is of the body's shape, not its values: limits and enums are the CRD's, and
 * the API server's answer, a refusal included, goes back to the browser as-is.
 * In the cluster RBAC refuses the rest too.
 *
 * With actions off (`actions: false`) only the entries marked `always`, the
 * Brief writes the UI has always made, and the Brief's settings narrowed to
 * spec.paused and spec.pauseRuns.
 */
const BRIEF_SETTINGS = {
  '/spec/paused': 'boolean',
  '/spec/pauseRuns': 'boolean',
  '/spec/autopilot': 'string',
  '/spec/parallel': 'integer',
  '/spec/releases': 'array',
}
const RELEASES_PATH = '/spec/releases'
const PUBLISH_PATH = '/spec/publishReleases'
const RV_PATH = '/metadata/resourceVersion'

/**
 * `check` over a JSON patch that may start with a test of the object's
 * resourceVersion, which an operation on one of `guarded` needs. Those set a
 * whole list or object, so without it a write worked out from a stale object
 * would silently drop what someone else changed meanwhile.
 */
function testsVersion(check, guarded) {
  return (body) => {
    if (!Array.isArray(body)) return undefined
    const [first, ...rest] = body
    const tested =
      isObject(first) &&
      Object.keys(first).sort().join() === 'op,path,value' &&
      first.op === 'test' &&
      first.path === RV_PATH &&
      typeof first.value === 'string' &&
      first.value !== ''
    const ops = check(tested ? rest : body)
    if (!ops) return undefined
    if (!tested && ops.some((op) => guarded.includes(op.path))) return undefined
    return body
  }
}

/**
 * Forwards a patch checked by testsVersion. With a test of its
 * resourceVersion, an object of `kind` that has changed since is refused as a
 * Conflict saying so; the test goes upstream too, so a change landing between
 * the read and the write is refused by the API server.
 */
function writeTested(kind) {
  return async (args) => {
    const { res, upstream, apiPath, namespace, name, body } = args
    if (body[0].op === 'test') {
      const current = await requestJson(upstream, apiPath)
      if (current.status !== 200) return send(res, current.status, `${JSON.stringify(current.body)}\n`, JSON_TYPE)
      const now = current.body?.metadata?.resourceVersion
      if (now !== body[0].value) {
        return sendStatus(
          res,
          409,
          'Conflict',
          `${kind} ${namespace}/${name} changed since this page showed it, so nothing was saved: ` +
            'check its settings as they are now and make the change again',
        )
      }
    }
    return forwardPatch(args)
  }
}

const ACTIONS = [
  {
    what: 'create a Brief',
    always: true,
    method: 'POST',
    path: resourcePath('briefs'),
    type: JSON_MEDIA,
    check: createsObject('Brief'),
    write: createBrief,
  },
  {
    what: `add a prompt (a string) to a Brief at ${PROMPT_PATH}`,
    always: true,
    method: 'PATCH',
    path: resourcePath('briefs', true),
    type: JSON_PATCH,
    check: (body) =>
      Array.isArray(body) && body.length === 1 && addsFields({ [PROMPT_PATH]: 'string' })(body) ? body[0] : undefined,
    write: addPrompt,
  },
  {
    what: `pause or resume a Brief or its running Tasks (a boolean at ${PAUSED_PATH}, ${PAUSE_RUNS_PATH} or both)`,
    always: true,
    offOnly: true,
    method: 'PATCH',
    path: resourcePath('briefs', true),
    type: JSON_PATCH,
    check: addsFields({ [PAUSED_PATH]: 'boolean', [PAUSE_RUNS_PATH]: 'boolean' }),
  },
  {
    what: `set a Brief's ${Object.keys(BRIEF_SETTINGS).join(', ')} (${RELEASES_PATH} after a test of ${RV_PATH})`,
    method: 'PATCH',
    path: resourcePath('briefs', true),
    type: JSON_PATCH,
    check: testsVersion(addsFields(BRIEF_SETTINGS), [RELEASES_PATH]),
    write: writeTested('Brief'),
  },
  {
    what: `set a Crew's /spec/paused, /spec/pauseRuns, /spec/parallel, /spec/tagReleases, /spec/prepareReleases, ${PUBLISH_PATH} (or remove it; ${PUBLISH_PATH} after a test of ${RV_PATH})`,
    method: 'PATCH',
    path: resourcePath('crews', true),
    type: JSON_PATCH,
    check: testsVersion(
      addsFields(
        {
          '/spec/paused': 'boolean',
          '/spec/pauseRuns': 'boolean',
          '/spec/parallel': 'integer',
          '/spec/tagReleases': 'boolean',
          '/spec/prepareReleases': 'boolean',
          [PUBLISH_PATH]: 'object',
        },
        [PUBLISH_PATH],
      ),
      [PUBLISH_PATH],
    ),
    write: writeTested('Crew'),
  },
  {
    what: 'set annotation theos.factory.io/rerun-release on a Crew',
    method: 'PATCH',
    path: resourcePath('crews', true),
    type: MERGE_PATCH,
    check: setsAnnotation('theos.factory.io/rerun-release'),
  },
  {
    what: 'set annotation theos.factory.io/retry on a Container',
    method: 'PATCH',
    path: resourcePath('containers', true),
    type: MERGE_PATCH,
    check: setsAnnotation('theos.factory.io/retry'),
  },
  {
    what: 'set annotation theos.factory.io/standard-take on a Container',
    method: 'PATCH',
    path: resourcePath('containers', true),
    type: MERGE_PATCH,
    check: setsAnnotation('theos.factory.io/standard-take'),
  },
  {
    what: 'set annotation theos.factory.io/standard-keep on a Container',
    method: 'PATCH',
    path: resourcePath('containers', true),
    type: MERGE_PATCH,
    check: setsAnnotation('theos.factory.io/standard-keep'),
  },
  {
    what: "set a Container's /spec/checks",
    method: 'PATCH',
    path: resourcePath('containers', true),
    type: JSON_PATCH,
    check: addsFields({ '/spec/checks': 'string' }),
  },
  {
    what: "approve a version at one of a Container's /spec/deploy entries (a test of /spec/deploy/N/target, optionally a test of /spec/deploy/N/approved, then an add at /spec/deploy/N/approved)",
    method: 'PATCH',
    path: resourcePath('containers', true),
    type: JSON_PATCH,
    check: approvesDeploy,
  },
  {
    what: 'create a Task of kind Implement with containerRef, agentRef, reviewerRef and goal',
    method: 'POST',
    path: resourcePath('tasks'),
    type: JSON_MEDIA,
    check: createsObject('Task', ['kind', 'containerRef', 'agentRef', 'reviewerRef', 'goal'], { kind: 'Implement' }),
  },
  {
    what: "set a Standard's /spec/template/ref",
    method: 'PATCH',
    path: resourcePath('standards', true),
    type: JSON_PATCH,
    check: addsFields({ '/spec/template/ref': 'string' }),
  },
]

/** The table's entries in force: all with actions on, else the `always` ones. */
export function actionTable(actions) {
  return ACTIONS.filter((entry) => (actions ? !entry.offOnly : entry.always))
}

/**
 * How the UI acts, from UI_ACTIONS: off (unset or empty), service-account (over
 * port-forward, as the UI's ServiceAccount) or oidc (as the person signed in).
 */
export function actionsFromEnv(env = process.env) {
  const mode = (env.UI_ACTIONS ?? '').trim()
  if (mode === '' || mode === 'off') return false
  if (mode === 'service-account' || mode === 'oidc') return mode
  throw new Error(`UI_ACTIONS is ${mode}: expected off, service-account or oidc`)
}

/** True when some entry of `table` takes a write to apiPath, whatever the method. */
function isWritePath(table, apiPath) {
  return table.some((entry) => entry.path.test(apiPath))
}

/**
 * One write from the browser: finds the table's entries for its method and
 * path, checks the content type and body, and makes it as the UI (over
 * port-forward) or as the person signed in (OIDC), refusing it with no one.
 */
async function actionWrite(req, res, apiPath, upstream, log, table, mode, oidc) {
  const writes = table.map((entry) => `${entry.method} ${entry.what}`).join('; ')
  const entries = table.filter((entry) => entry.method === req.method && entry.path.test(apiPath))
  if (!entries.length) {
    return sendStatus(res, 403, 'Forbidden', `The theos UI writes only these: ${writes}`)
  }
  if (isCrossSite(req)) return sendStatus(res, 403, 'Forbidden', 'Writes come only from the UI itself')
  const as = mode === 'oidc' ? oidc?.who(req) : undefined
  if (mode === 'oidc' && !as) {
    return sendStatus(res, 401, 'Unauthorized', 'Sign in to act: the UI makes writes only as a person signed in')
  }
  const types = [...new Set(entries.map((entry) => entry.type))]
  const typed = entries.filter((entry) => entry.type === mediaType(req))
  if (!typed.length) {
    return sendStatus(res, 415, 'UnsupportedMediaType', `Expected Content-Type ${types.join(' or ')}`)
  }
  const text = await readBody(req, MAX_WRITE_BYTES)
  if (text === undefined) return sendStatus(res, 413, 'RequestEntityTooLarge', `At most ${MAX_WRITE_BYTES} bytes`)
  const body = parseJson(text)
  for (const entry of typed) {
    const [, namespace, name] = entry.path.exec(apiPath)
    const checked = entry.check(body, namespace)
    if (checked === undefined) continue
    const write = entry.write ?? forwardPatch
    return write({ res, upstream, log, apiPath, namespace, name, entry, body: checked, as })
  }
  const here = typed.map((entry) => entry.what).join('; or ')
  return sendStatus(res, 403, 'Forbidden', `The theos UI writes here only: ${here}`)
}

/** Creates a Brief, once the namespace is known to hold a Project. */
async function createBrief({ res, upstream, log, apiPath, namespace, body, as }) {
  const projects = await requestJson(upstream, `/apis/theos.factory.io/v1beta1/namespaces/${namespace}/projects`)
  if (projects.status !== 200) return send(res, projects.status, `${JSON.stringify(projects.body)}\n`, JSON_TYPE)
  if (!projects.body?.items?.length) {
    return sendStatus(res, 403, 'Forbidden', `Namespace ${namespace} holds no Project, so the UI makes no Brief there`)
  }
  log(`Creating a Brief in ${namespace}${by(as)}`)
  return forwardWrite(res, upstream, 'POST', apiPath, JSON_MEDIA, body, as)
}

/** Most prompts a Brief lists at once (spec.prompts' CEL rule). */
const MAX_PROMPTS = 50

/** How many of the prompts rounds planned a full list keeps (model.KeptPlannedPrompts). */
const KEPT_PLANNED_PROMPTS = 10

/**
 * Appends a prompt. A JSON patch can't append to a list that isn't there, and
 * a Brief without prompts has none: then the patch adds the list, guarded by
 * the resourceVersion just read so it never replaces a list made meanwhile.
 * To a full list, the append retires the planned prompts past the newest ten
 * in the same write (retiringAppend); the operator never writes the spec.
 */
async function addPrompt({ res, upstream, log, apiPath, namespace, name, body: op, as }) {
  const current = await requestJson(upstream, apiPath)
  if (current.status !== 200) return send(res, current.status, `${JSON.stringify(current.body)}\n`, JSON_TYPE)
  const prompts = current.body?.spec?.prompts
  const ops = !Array.isArray(prompts)
    ? [
        { op: 'test', path: '/metadata/resourceVersion', value: current.body?.metadata?.resourceVersion },
        { op: 'add', path: '/spec/prompts', value: [op.value] },
      ]
    : (retiringAppend(current.body, op.value) ?? [op])
  log(`Adding a prompt to Brief ${namespace}/${name}${by(as)}`)
  return forwardWrite(res, upstream, 'PATCH', apiPath, JSON_PATCH, ops, as)
}

/**
 * The patch that appends prompt to a Brief whose list is full, dropping the
 * oldest planned prompts past the newest ten and counting them in
 * spec.promptsRetired (model.PromptsToRetire), or undefined when the list
 * has room or none can go. It is guarded by the prompts just read, not the
 * resourceVersion the operator's status writes move, so a prompt appended
 * meanwhile is never lost.
 */
function retiringAppend(brief, prompt) {
  const prompts = brief?.spec?.prompts ?? []
  if (prompts.length < MAX_PROMPTS) return undefined
  const retired = brief.spec.promptsRetired ?? 0
  const planned = brief.status?.promptsPlanned ?? 0
  const n = Math.max(Math.min(planned - retired - KEPT_PLANNED_PROMPTS, prompts.length), 0)
  if (n === 0) return undefined
  return [
    { op: 'test', path: '/spec/prompts', value: prompts },
    { op: 'replace', path: '/spec/prompts', value: [...prompts.slice(n), prompt] },
    { op: 'add', path: '/spec/promptsRetired', value: retired + n },
  ]
}

/** " as <user>" for a log line about a write made as a person, else "". */
const by = (as) => (as ? ` as ${as.user}` : '')

/**
 * What GET /ui/session answers: whether actions are on, who makes them, who is
 * signed in (with OIDC: the names the writes impersonate, and where to sign
 * in and out), and the writes the table takes. Never a token.
 */
function session(actions, table, person) {
  const answer = {
    actions: Boolean(actions),
    mode: actions || 'off',
    user: person ? { name: person.user, groups: person.groups } : null,
    writes: table.map((entry) => ({ method: entry.method, type: entry.type, what: entry.what })),
  }
  if (actions === 'oidc') Object.assign(answer, { login: '/ui/login', logout: '/ui/logout' })
  return answer
}

/** Any other entry: sends the checked body as the entry's write. */
function forwardPatch({ res, upstream, log, apiPath, namespace, name, entry, body, as }) {
  log(`UI action: ${entry.what}${name ? `: ${namespace}/${name}` : ` in ${namespace}`}${by(as)}`)
  return forwardWrite(res, upstream, entry.method, apiPath, entry.type, body, as)
}

const JSON_TYPE = { 'content-type': 'application/json', 'cache-control': 'no-store' }

/** Sends one write upstream and the API's answer, a refusal included, back to the browser. */
async function forwardWrite(res, upstream, method, apiPath, contentType, body, as) {
  const search = `?${new URLSearchParams({ fieldManager: FIELD_MANAGER })}`
  const answer = await requestJson(upstream, apiPath, { method, search, contentType, body, as })
  send(
    res,
    answer.status,
    `${JSON.stringify(answer.body ?? {})}
`,
    JSON_TYPE,
  )
}

/** Proxies one GET request to the Kubernetes API and streams the answer back. */
function proxy(req, res, apiPath, search, upstream, log) {
  const upstreamReq = upstreamRequest(upstream, apiPath, search, (upstreamRes) => {
    const out = { ...SECURITY_HEADERS, 'cache-control': 'no-store', 'x-accel-buffering': 'no' }
    for (const name of FORWARDED_RESPONSE_HEADERS) {
      if (upstreamRes.headers[name]) out[name] = upstreamRes.headers[name]
    }
    res.writeHead(upstreamRes.statusCode ?? 502, out)
    // Send the headers now: a watch may not produce a byte for minutes.
    res.flushHeaders()
    upstreamRes.pipe(res)
    upstreamRes.on('error', () => res.destroy())
  })
  for (const name of FORWARDED_REQUEST_HEADERS) {
    if (req.headers[name]) upstreamReq.setHeader(name, req.headers[name])
  }
  upstreamReq.on('error', (err) => {
    log(`Upstream request failed: ${err.message}`)
    if (!res.headersSent) sendStatus(res, 502, 'BadGateway', `Cannot reach the Kubernetes API: ${err.message}`)
    else res.destroy()
  })
  // A browser that goes away (tab closed, watch restarted) ends the upstream request too.
  res.on('close', () => upstreamReq.destroy())
  upstreamReq.end()
}

/**
 * Several watches over one response. Browsers open at most six HTTP/1.1
 * connections per host, and a page watches about ten kinds, so one long-lived
 * request per watch would leave some of them queued forever.
 *
 *   GET /k8s-watch?w=<api path>&rv=<resourceVersion>[&w=...&rv=...][&timeoutSeconds=300]
 *
 * The answer is newline-delimited JSON. Each line is either an event of watch i,
 *   {"i":0,"event":{"type":"MODIFIED","object":{...}}}
 * or the end of watch i, with the upstream status code (200 when the API ended
 * the watch normally, 410 when the resourceVersion is too old, ...):
 *   {"i":0,"end":true,"code":410,"status":{"kind":"Status",...}}
 * The response ends when every watch has ended.
 */
function watchMany(res, url, upstream, log) {
  const paths = url.searchParams.getAll('w')
  const versions = url.searchParams.getAll('rv')
  const timeout = url.searchParams.get('timeoutSeconds')
  if (!paths.length || paths.length !== versions.length || paths.length > MAX_WATCHES) {
    return sendStatus(res, 400, 'BadRequest', `Expected 1 to ${MAX_WATCHES} pairs of w and rv`)
  }
  if (!paths.every(isAllowedApiPath) || (timeout !== null && !/^\d{1,5}$/.test(timeout))) {
    return sendStatus(res, 403, 'Forbidden', `The UI proxy only serves ${ALLOWED_PREFIX}`)
  }

  res.writeHead(200, {
    ...SECURITY_HEADERS,
    'content-type': 'application/x-ndjson',
    'cache-control': 'no-store',
    'x-accel-buffering': 'no',
  })
  res.flushHeaders()

  let open = paths.length
  const write = (value) => {
    if (!res.writableEnded) res.write(`${JSON.stringify(value)}\n`)
  }
  const finish = (i, code, status) => {
    write({ i, end: true, code, status })
    if (--open === 0) res.end()
  }

  const requests = paths.map((apiPath, i) => {
    const query = new URLSearchParams({ watch: '1', resourceVersion: versions[i], allowWatchBookmarks: 'true' })
    if (timeout !== null) query.set('timeoutSeconds', timeout)
    let ended = false
    const end = (code, status) => {
      if (ended) return
      ended = true
      finish(i, code, status)
    }
    const upstreamReq = upstreamRequest(upstream, apiPath, `?${query}`, (upstreamRes) => {
      upstreamRes.setEncoding('utf8')
      if (upstreamRes.statusCode !== 200) {
        // An error before the watch started, such as 410 Gone or 403: pass on its Status.
        let body = ''
        upstreamRes.on('data', (chunk) => {
          if (body.length < 65536) body += chunk
        })
        upstreamRes.on('end', () => end(upstreamRes.statusCode ?? 502, parseJson(body)))
        return
      }
      let rest = ''
      upstreamRes.on('data', (chunk) => {
        const lines = (rest + chunk).split('\n')
        rest = lines.pop() ?? ''
        for (const line of lines) {
          const event = parseJson(line)
          if (event) write({ i, event })
        }
      })
      upstreamRes.on('end', () => {
        const event = parseJson(rest)
        if (event) write({ i, event })
        end(200)
      })
      upstreamRes.on('error', (err) => end(502, { kind: 'Status', code: 502, message: err.message }))
    })
    upstreamReq.on('error', (err) => {
      log(`Upstream watch failed: ${err.message}`)
      end(502, { kind: 'Status', code: 502, message: `Cannot reach the Kubernetes API: ${err.message}` })
    })
    upstreamReq.end()
    return upstreamReq
  })
  res.on('close', () => requests.forEach((r) => r.destroy()))
}

function parseJson(text) {
  if (!text || !text.trim()) return undefined
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

/**
 * The API part of the server, usable on its own as middleware (the Vite dev
 * server mounts it, so development behaves exactly like production).
 * Returns a function that answers a request and returns true, or returns false
 * when the request is not for the API.
 */
export function createApiHandler({
  upstream = resolveUpstream(),
  log = (msg) => console.log(msg),
  components = componentsFromEnv(process.env, log),
  sampleEveryMs = 0,
  actions = actionsFromEnv(),
  oidc = oidcFromEnv(),
  fetch: fetchIssuer = fetch,
} = {}) {
  const table = actionTable(actions)
  // Sign-in, only with UI_ACTIONS=oidc; without its settings nobody can sign in.
  const signIn =
    actions === 'oidc' && oidc
      ? createOIDC(oidc, { log, fetch: fetchIssuer, send, sendStatus, isCrossSite })
      : undefined
  const health = createHealthHandler({ getJson: (apiPath) => getJson(upstream, apiPath), components, sampleEveryMs })
  return (req, res) => {
    let url
    try {
      url = new URL(req.url ?? '/', 'http://localhost')
    } catch {
      send(res, 400, 'Bad Request\n')
      return true
    }
    if (url.pathname === '/factory/health') {
      if (req.method !== 'GET') {
        sendStatus(res, 405, 'MethodNotAllowed', 'The theos UI is read-only: only GET is allowed', {
          allow: 'GET',
        })
        return true
      }
      health().then(
        (value) =>
          send(res, 200, `${JSON.stringify(value)}\n`, {
            'content-type': 'application/json',
            'cache-control': 'no-store',
          }),
        (err) => {
          log(`Health check failed: ${err.message}`)
          sendStatus(res, 502, 'BadGateway', `Could not read the factory's health: ${err.message}`)
        },
      )
      return true
    }
    if (actions === 'oidc' && ['/ui/login', '/ui/callback', '/ui/logout'].includes(url.pathname)) {
      if (signIn) return signIn.handle(req, res, url)
      sendStatus(res, 503, 'ServiceUnavailable', 'Sign-in is not set up: OIDC_ISSUER_URL is empty (ui.auth.oidc)')
      return true
    }
    if (url.pathname === '/ui/session') {
      if (req.method !== 'GET') {
        sendStatus(res, 405, 'MethodNotAllowed', 'Only GET is allowed here', { allow: 'GET' })
        return true
      }
      send(res, 200, `${JSON.stringify(session(actions, table, signIn?.who(req)))}\n`, JSON_TYPE)
      return true
    }
    const isProxy = url.pathname === '/k8s' || url.pathname.startsWith('/k8s/')
    const isWatch = url.pathname === '/k8s-watch'
    if (!isProxy && !isWatch) return false

    // URL() has already resolved any "..".
    const apiPath = url.pathname.slice('/k8s'.length)
    // Nothing but GET reaches the API, except the action table's writes.
    if (isProxy && (req.method === 'POST' || req.method === 'PATCH') && isWritePath(table, apiPath)) {
      actionWrite(req, res, apiPath, upstream, log, table, actions, signIn).catch((err) => {
        log(`Write failed: ${err.message}`)
        if (!res.headersSent) sendStatus(res, 502, 'BadGateway', `Cannot reach the Kubernetes API: ${err.message}`)
        else res.destroy()
      })
      return true
    }
    if (req.method !== 'GET') {
      const only = actions ? 'the writes GET /ui/session lists' : 'Briefs'
      sendStatus(
        res,
        405,
        'MethodNotAllowed',
        `Only GET is allowed here: the theos UI writes nothing but ${only}`,
        {
          allow: 'GET',
        },
      )
      return true
    }
    if (isWatch) {
      watchMany(res, url, upstream, log)
      return true
    }
    if (!isAllowedApiPath(apiPath)) {
      sendStatus(res, 403, 'Forbidden', `The UI proxy only serves ${ALLOWED_PREFIX}`)
      return true
    }
    proxy(req, res, apiPath, url.search, upstream, log)
    return true
  }
}

/** Serves a file from the dist directory, or 404. Only files inside `root`. */
async function serveStatic(res, root, pathname, method) {
  let relative
  try {
    relative = decodeURIComponent(pathname)
  } catch {
    return send(res, 400, 'Bad Request\n')
  }
  if (relative.endsWith('/')) relative += 'index.html'
  const file = path.resolve(root, `.${relative}`)
  if (file !== root && !file.startsWith(root + path.sep)) return send(res, 404, 'Not Found\n')
  try {
    const info = await stat(file)
    if (!info.isFile()) return send(res, 404, 'Not Found\n')
    const body = await readFile(file)
    const ext = path.extname(file).toLowerCase()
    // Hashed assets never change; index.html must always be fresh.
    const cache = relative.startsWith('/assets/') ? 'public, max-age=31536000, immutable' : 'no-cache'
    res.writeHead(200, {
      ...SECURITY_HEADERS,
      'content-type': CONTENT_TYPES[ext] ?? 'application/octet-stream',
      'content-length': body.length,
      'cache-control': cache,
    })
    res.end(method === 'HEAD' ? undefined : body)
  } catch {
    send(res, 404, 'Not Found\n')
  }
}

/**
 * Serves one of DOWNLOADS from `dir`, or 404 for any other name (#1151). The
 * name is matched whole, so nothing else under `dir` or outside it is served.
 */
async function serveDownload(res, dir, name, method) {
  if (!DOWNLOADS.includes(name)) return send(res, 404, 'Not Found\n')
  try {
    const body = await readFile(path.join(dir, name))
    res.writeHead(200, {
      ...SECURITY_HEADERS,
      'content-type': name === 'SHA256SUMS' ? 'text/plain; charset=utf-8' : 'application/octet-stream',
      'content-length': body.length,
      'content-disposition': `attachment; filename="${name}"`,
      'cache-control': 'no-cache',
    })
    res.end(method === 'HEAD' ? undefined : body)
  } catch {
    send(res, 404, 'Not Found\n')
  }
}

/**
 * Creates the server without starting it.
 * @param {{ distDir?: string, downloadsDir?: string, upstream?: ReturnType<typeof resolveUpstream>, log?: (msg: string) => void, allowedHosts?: Set<string> }} options
 */
export function createServer(options = {}) {
  const here = path.dirname(fileURLToPath(import.meta.url))
  const root = path.resolve(options.distDir ?? path.join(here, 'dist'))
  const downloads = path.resolve(options.downloadsDir ?? path.join(here, 'downloads'))
  const api = createApiHandler(options)
  const allowedHosts = options.allowedHosts ?? allowedHostsFromEnv()

  return http.createServer((req, res) => {
    // The kubelet probes by Pod IP; /healthz says nothing and changes nothing.
    const pathOnly = (req.url ?? '/').split('?')[0]
    if (pathOnly === '/healthz') return send(res, 200, 'ok\n')
    // The Session client is a public file, and a devcontainer's feature
    // fetches it by whatever address reaches the UI (an Ingress host, a
    // port-forward, the Service's name in the cluster), so the Host check
    // that keeps a rebinding page off the API doesn't apply to it.
    if (pathOnly.startsWith('/downloads/')) {
      if (req.method !== 'GET' && req.method !== 'HEAD') {
        return send(res, 405, 'Method Not Allowed\n', { allow: 'GET, HEAD' })
      }
      return serveDownload(res, downloads, pathOnly.slice('/downloads/'.length), req.method)
    }
    if (!isServedHost(req, allowedHosts)) {
      return send(res, 421, 'Misdirected Request: this server does not answer for that host (UI_ALLOWED_HOSTS)\n')
    }
    if (api(req, res)) return
    if (req.method !== 'GET' && req.method !== 'HEAD') {
      return send(res, 405, 'Method Not Allowed\n', { allow: 'GET, HEAD' })
    }
    let pathname
    try {
      pathname = new URL(req.url ?? '/', 'http://localhost').pathname
    } catch {
      return send(res, 400, 'Bad Request\n')
    }
    return serveStatic(res, root, pathname, req.method)
  })
}

// Run when started directly (node server.mjs), not when imported.
if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const port = Number(process.env.PORT || 8080)
  const upstream = resolveUpstream()
  // The health history is sampled in the background, every 15 s by default.
  const sampleEveryMs = Number(process.env.FACTORY_SAMPLE_SECONDS ?? 15) * 1000
  const server = createServer({ upstream, sampleEveryMs })
  server.listen(port, () => {
    console.log(`theos UI on http://localhost:${port} (Kubernetes API: ${upstream.url.origin}, ${upstream.mode})`)
  })
  const shutdown = () => {
    server.close(() => process.exit(0))
    // Open watches keep connections alive; don't wait for them.
    server.closeAllConnections?.()
  }
  process.on('SIGTERM', shutdown)
  process.on('SIGINT', shutdown)
}
