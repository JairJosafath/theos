#!/usr/bin/env node
// Serves the built UI (dist/) and a read-only gateway to the Kubernetes API.
//
//   GET /healthz          200 "ok"
//   GET /k8s/<api path>   the Kubernetes API, as-is: lists, gets, single watches
//   GET /k8s-watch?...    several watches over one response (see watchMany)
//   GET /factory/health   the factory's components, node and agent sandboxes, summarized (health.mjs)
//   GET /...              files from dist/
//
// Read-only by construction: only GET reaches the API, only under
// /apis/c4.factory.io/, and every other method on /k8s gets 405. The browser's
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

const SERVICE_ACCOUNT_DIR = '/var/run/secrets/kubernetes.io/serviceaccount'

/** Only the c4.factory.io API group is reachable: the UI needs nothing else. */
const ALLOWED_PREFIX = '/apis/c4.factory.io/'

/** Most watches one /k8s-watch request may carry. */
const MAX_WATCHES = 32

/** Request headers passed upstream. Everything else, auth and cookies included, is dropped. */
const FORWARDED_REQUEST_HEADERS = ['accept', 'user-agent']

/** Response headers passed back to the browser. */
const FORWARDED_RESPONSE_HEADERS = ['content-type', 'cache-control', 'date', 'audit-id']

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
 * True for an API path the UI may read: inside c4.factory.io, plain characters
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

function upstreamRequest(upstream, apiPath, search, onResponse) {
  const target = new URL(upstream.url)
  target.pathname = `${upstream.url.pathname.replace(/\/$/, '')}${apiPath}`
  target.search = search
  const headers = { accept: 'application/json' }
  const token = upstream.token()
  if (token) headers.authorization = `Bearer ${token}`
  const client = target.protocol === 'https:' ? https : http
  return client.request(target, { method: 'GET', headers, ca: upstream.ca }, onResponse)
}

/** Most bytes getJson reads; a cluster-wide pod list on a small cluster is far less. */
const MAX_JSON_BYTES = 32 * 1024 * 1024

/**
 * Reads one API path for the server itself (not for the browser, so not limited
 * to c4.factory.io). Resolves to { status, body }.
 */
function getJson(upstream, apiPath) {
  return new Promise((resolve, reject) => {
    const req = upstreamRequest(upstream, apiPath, '', (upstreamRes) => {
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
    })
    req.setTimeout(10_000, () => req.destroy(new Error(`${apiPath} took longer than 10 s`)))
    req.on('error', reject)
    req.end()
  })
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
} = {}) {
  const health = createHealthHandler({ getJson: (apiPath) => getJson(upstream, apiPath), components })
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
        sendStatus(res, 405, 'MethodNotAllowed', 'The C4 Factory UI is read-only: only GET is allowed', {
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
    const isProxy = url.pathname === '/k8s' || url.pathname.startsWith('/k8s/')
    const isWatch = url.pathname === '/k8s-watch'
    if (!isProxy && !isWatch) return false

    // Read-only by construction: nothing but GET ever reaches the API.
    if (req.method !== 'GET') {
      sendStatus(res, 405, 'MethodNotAllowed', 'The C4 Factory UI is read-only: only GET is allowed', { allow: 'GET' })
      return true
    }
    if (isWatch) {
      watchMany(res, url, upstream, log)
      return true
    }
    // URL() has already resolved any "..".
    const apiPath = url.pathname.slice('/k8s'.length)
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
 * Creates the server without starting it.
 * @param {{ distDir?: string, upstream?: ReturnType<typeof resolveUpstream>, log?: (msg: string) => void }} options
 */
export function createServer(options = {}) {
  const root = path.resolve(options.distDir ?? path.join(path.dirname(fileURLToPath(import.meta.url)), 'dist'))
  const api = createApiHandler(options)

  return http.createServer((req, res) => {
    if ((req.url ?? '/').split('?')[0] === '/healthz') return send(res, 200, 'ok\n')
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
  const server = createServer({ upstream })
  server.listen(port, () => {
    console.log(`C4 Factory UI on http://localhost:${port} (Kubernetes API: ${upstream.url.origin}, ${upstream.mode})`)
  })
  const shutdown = () => {
    server.close(() => process.exit(0))
    // Open watches keep connections alive; don't wait for them.
    server.closeAllConnections?.()
  }
  process.on('SIGTERM', shutdown)
  process.on('SIGINT', shutdown)
}
