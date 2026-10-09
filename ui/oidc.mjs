// Sign-in with OIDC for the UI's actions behind an Ingress (design.md, UI
// actions): an authorization code with PKCE (S256), the ID token checked here
// with node:crypto, and a session in this process's memory behind an opaque,
// HttpOnly, Secure, SameSite=Lax cookie. Every write is then made with
// Impersonate-User and Impersonate-Group, names the server picks from the
// token, never from the browser.
//
//   GET  /ui/login?return=/path   starts a sign-in at the issuer
//   GET  /ui/callback?code&state  the issuer's answer: checks it, signs in, returns to the path
//   POST /ui/logout               signs out
//
// No runtime dependencies: only Node's standard library.

import { createHash, createPublicKey, randomBytes, verify } from 'node:crypto'

/**
 * The session cookie, and the one that ties a sign-in's answer to the browser
 * that started it. __Host- keeps a sibling subdomain from planting either.
 */
export const SESSION_COOKIE = '__Host-c4ui_session'
const LOGIN_COOKIE = '__Host-c4ui_login'
const CALLBACK_PATH = '/ui/callback'

/** How long a started sign-in waits for the issuer's answer. */
const LOGIN_TTL_MS = 10 * 60_000
/** Most sign-ins in flight (the oldest go first) and sessions held, so nobody can fill the memory. */
const MAX_LOGINS = 1000
const MAX_SESSIONS = 10_000
/** Clock skew allowed on the ID token's exp, iat and nbf. */
const SKEW_S = 60

const ALGORITHMS = {
  RS256: { kty: 'RSA', options: {} },
  ES256: { kty: 'EC', crv: 'P-256', options: { dsaEncoding: 'ieee-p1363' } },
}

/**
 * The OIDC settings from the environment (the chart's ui.auth.oidc), or
 * undefined without OIDC_ISSUER_URL.
 * @param {Record<string, string | undefined>} env
 */
export function oidcFromEnv(env = process.env) {
  const issuer = (env.OIDC_ISSUER_URL ?? '').trim()
  if (!issuer) return undefined
  const words = (value, fallback) => (value ?? fallback).split(/[\s,]+/).filter(Boolean)
  return {
    issuer,
    clientID: (env.OIDC_CLIENT_ID ?? '').trim(),
    clientSecret: env.OIDC_CLIENT_SECRET ?? '',
    redirectURL: (env.OIDC_REDIRECT_URL ?? '').trim(),
    scopes: words(env.OIDC_SCOPES, 'openid email profile'),
    usernameClaim: (env.OIDC_USERNAME_CLAIM ?? '').trim() || 'email',
    usernamePrefix: env.OIDC_USERNAME_PREFIX ?? 'oidc:',
    groupsClaim: (env.OIDC_GROUPS_CLAIM ?? '').trim() || 'groups',
    groupsPrefix: env.OIDC_GROUPS_PREFIX ?? 'oidc:',
  }
}

const b64url = (buffer) => Buffer.from(buffer).toString('base64url')
const random = () => randomBytes(32).toString('base64url')
const isObject = (value) => typeof value === 'object' && value !== null && !Array.isArray(value)

function parseJson(text) {
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

/** The request's cookies by name. */
function cookies(req) {
  const out = {}
  for (const part of (req.headers.cookie ?? '').split(';')) {
    const at = part.indexOf('=')
    if (at > 0) out[part.slice(0, at).trim()] = part.slice(at + 1).trim()
  }
  return out
}

function cookie(name, value, maxAge) {
  return `${name}=${value}; Path=/; Max-Age=${maxAge}; HttpOnly; Secure; SameSite=Lax`
}

/** Text from the request, safe for one log line. */
const quoted = (text) => JSON.stringify(String(text).slice(0, 200))

/** A path on this site to return to after sign-in: never another site. */
function localPath(value) {
  return typeof value === 'string' && /^\/(?![/\\])[^\s]*$/.test(value) ? value : '/'
}

/**
 * Checks an ID token against the issuer's keys and the sign-in it answers.
 * Returns its claims, or throws an Error saying what is wrong.
 * @param {string} token
 * @param {{ issuer: string, clientID: string, nonce: string, keys: (kid?: string) => Promise<object[]>, now?: number }} want
 */
export async function verifyIDToken(token, { issuer, clientID, nonce, keys, now = Date.now() / 1000 }) {
  const parts = typeof token === 'string' ? token.split('.') : []
  if (parts.length !== 3) throw new Error('the ID token is not a signed JWT')
  const header = parseJson(Buffer.from(parts[0], 'base64url').toString('utf8'))
  const claims = parseJson(Buffer.from(parts[1], 'base64url').toString('utf8'))
  if (!isObject(header) || !isObject(claims)) throw new Error('the ID token is not a signed JWT')
  const alg = ALGORITHMS[header.alg]
  if (!alg) throw new Error(`the ID token is signed with ${header.alg}: expected RS256 or ES256`)
  const candidates = (await keys(header.kid)).filter(
    (jwk) =>
      jwk.kty === alg.kty &&
      (alg.crv === undefined || jwk.crv === alg.crv) &&
      (header.kid === undefined || jwk.kid === header.kid) &&
      (jwk.use === undefined || jwk.use === 'sig') &&
      (jwk.alg === undefined || jwk.alg === header.alg),
  )
  const data = Buffer.from(`${parts[0]}.${parts[1]}`)
  const signature = Buffer.from(parts[2], 'base64url')
  const signed = candidates.some((jwk) => {
    try {
      const key = createPublicKey({ key: jwk, format: 'jwk' })
      return verify('sha256', data, { key, ...alg.options }, signature)
    } catch {
      return false
    }
  })
  if (!signed) throw new Error("the ID token's signature does not match the issuer's keys")
  if (claims.iss !== issuer) throw new Error(`the ID token's issuer is ${claims.iss}, not ${issuer}`)
  const audience = Array.isArray(claims.aud) ? claims.aud : [claims.aud]
  if (!audience.includes(clientID)) throw new Error(`the ID token is not for client ${clientID}`)
  if ((audience.length > 1 || claims.azp !== undefined) && claims.azp !== clientID)
    throw new Error(`the ID token was not issued to ${clientID}`)
  if (typeof claims.exp !== 'number' || claims.exp + SKEW_S < now) throw new Error('the ID token has expired')
  if (typeof claims.nbf === 'number' && claims.nbf - SKEW_S > now) throw new Error('the ID token is not valid yet')
  if (typeof claims.iat === 'number' && claims.iat - SKEW_S > now)
    throw new Error('the ID token was issued in the future')
  if (typeof nonce !== 'string' || claims.nonce !== nonce) throw new Error("the ID token's nonce is not this sign-in's")
  return claims
}

/**
 * The names a signed-in person acts as: the prefixed username claim (an email
 * only when verified) and the prefixed groups. Never a system: name.
 */
export function identity(claims, config) {
  const name = claims[config.usernameClaim]
  if (typeof name !== 'string' || name === '') {
    throw new Error(`the ID token has no ${config.usernameClaim} claim`)
  }
  if (config.usernameClaim === 'email' && claims.email_verified !== true) {
    throw new Error(`the issuer has not verified the email ${name}`)
  }
  const user = `${config.usernamePrefix}${name}`
  if (user.startsWith('system:')) throw new Error(`the UI never acts as ${user}`)
  const raw = claims[config.groupsClaim]
  const groups = (Array.isArray(raw) ? raw : typeof raw === 'string' ? [raw] : [])
    .filter((group) => typeof group === 'string' && group !== '')
    .map((group) => `${config.groupsPrefix}${group}`)
    .filter((group) => !group.startsWith('system:'))
  return { user, groups: [...new Set(groups)] }
}

/**
 * The sign-in routes and the session store. `handle` answers /ui/login,
 * /ui/callback and /ui/logout (true when it did), `who` names the person
 * signed in on a request, or undefined.
 * @param {ReturnType<typeof oidcFromEnv>} config
 * @param {{ log?: (msg: string) => void, fetch?: typeof fetch, send: Function, sendStatus: Function, isCrossSite: Function }} deps
 */
export function createOIDC(config, { log = () => {}, fetch: get = fetch, send, sendStatus, isCrossSite }) {
  /** Sign-ins in flight by state; sessions by their cookie's id. */
  const logins = new Map()
  const sessions = new Map()
  let discovered
  let jwks = { keys: [], readAt: 0 }

  const getJson = async (url, init) => {
    const res = await get(url, { ...init, signal: AbortSignal.timeout(10_000) })
    const body = parseJson(await res.text())
    if (!res.ok || !isObject(body)) {
      const why = isObject(body) && typeof body.error === 'string' ? `: ${body.error}` : ''
      throw new Error(`${url} answered ${res.status}${why}`)
    }
    return body
  }

  /** The issuer's endpoints, read once (and again after a failure). */
  async function discovery() {
    if (!discovered) {
      discovered = getJson(`${config.issuer.replace(/\/$/, '')}/.well-known/openid-configuration`).then((doc) => {
        if (doc.issuer !== config.issuer) throw new Error(`the issuer calls itself ${doc.issuer}, not ${config.issuer}`)
        for (const field of ['authorization_endpoint', 'token_endpoint', 'jwks_uri']) {
          if (typeof doc[field] !== 'string') throw new Error(`the issuer's discovery has no ${field}`)
        }
        return doc
      })
      discovered.catch(() => (discovered = undefined))
    }
    return discovered
  }

  /** The issuer's keys; read again for a key id not seen, at most once a minute. */
  async function keys(kid) {
    const known = jwks.keys.some((jwk) => jwk.kid === kid)
    if (!jwks.readAt || (!known && Date.now() - jwks.readAt > 60_000)) {
      const body = await getJson((await discovery()).jwks_uri)
      jwks = { keys: Array.isArray(body.keys) ? body.keys.filter(isObject) : [], readAt: Date.now() }
    }
    return jwks.keys
  }

  const redirectURL = (req) => config.redirectURL || `https://${req.headers.host}${CALLBACK_PATH}`

  function prune(now = Date.now()) {
    for (const [state, login] of logins) if (login.expiresAt <= now) logins.delete(state)
    for (const [id, session] of sessions) if (session.expiresAt <= now) sessions.delete(id)
  }

  async function login(req, res, url) {
    prune()
    // Past the limit the oldest sign-in in flight goes, so no one can lock the others out.
    while (logins.size >= MAX_LOGINS) logins.delete(logins.keys().next().value)
    const doc = await discovery()
    const state = random()
    const browser = random()
    const login = {
      browser,
      nonce: random(),
      verifier: random(),
      redirectURL: redirectURL(req),
      returnTo: localPath(url.searchParams.get('return')),
      expiresAt: Date.now() + LOGIN_TTL_MS,
    }
    logins.set(state, login)
    const target = new URL(doc.authorization_endpoint)
    target.search = new URLSearchParams({
      response_type: 'code',
      client_id: config.clientID,
      redirect_uri: login.redirectURL,
      scope: config.scopes.join(' '),
      state,
      nonce: login.nonce,
      code_challenge: b64url(createHash('sha256').update(login.verifier).digest()),
      code_challenge_method: 'S256',
    }).toString()
    send(res, 302, '', {
      location: target.href,
      'cache-control': 'no-store',
      'set-cookie': cookie(LOGIN_COOKIE, browser, LOGIN_TTL_MS / 1000),
    })
  }

  async function callback(req, res, url) {
    const refuse = (message) => {
      log(`Sign-in refused: ${message}`)
      sendStatus(res, 401, 'Unauthorized', `Sign-in failed: ${message}`, {
        'set-cookie': cookie(LOGIN_COOKIE, '', 0),
      })
    }
    const state = url.searchParams.get('state') ?? ''
    const pending = logins.get(state)
    logins.delete(state)
    if (!pending || pending.expiresAt <= Date.now()) return refuse('this sign-in is unknown or too old; sign in again')
    if (cookies(req)[LOGIN_COOKIE] !== pending.browser) return refuse('this browser did not start this sign-in')
    if (url.searchParams.has('error')) return refuse(`the issuer answered ${quoted(url.searchParams.get('error'))}`)
    const code = url.searchParams.get('code')
    if (!code) return refuse('the issuer sent no code')
    const doc = await discovery()
    let tokens
    try {
      tokens = await getJson(doc.token_endpoint, {
        method: 'POST',
        headers: {
          'content-type': 'application/x-www-form-urlencoded',
          accept: 'application/json',
          authorization: `Basic ${Buffer.from(
            `${encodeURIComponent(config.clientID)}:${encodeURIComponent(config.clientSecret)}`,
          ).toString('base64')}`,
        },
        body: new URLSearchParams({
          grant_type: 'authorization_code',
          code,
          redirect_uri: pending.redirectURL,
          code_verifier: pending.verifier,
        }).toString(),
      })
    } catch (err) {
      return refuse(`the token exchange failed: ${err.message}`)
    }
    let person
    let claims
    try {
      claims = await verifyIDToken(tokens.id_token, {
        issuer: config.issuer,
        clientID: config.clientID,
        nonce: pending.nonce,
        keys,
      })
      person = identity(claims, config)
    } catch (err) {
      return refuse(err.message)
    }
    prune()
    if (sessions.size >= MAX_SESSIONS) return sendStatus(res, 503, 'ServiceUnavailable', 'Too many people signed in')
    const id = random()
    const expiresAt = Math.min(claims.exp * 1000, Date.now() + 24 * 3600_000)
    sessions.set(id, { ...person, expiresAt })
    log(`Signed in: ${person.user}`)
    send(res, 302, '', {
      location: pending.returnTo,
      'cache-control': 'no-store',
      'set-cookie': [
        cookie(SESSION_COOKIE, id, Math.max(0, Math.floor((expiresAt - Date.now()) / 1000))),
        cookie(LOGIN_COOKIE, '', 0),
      ],
    })
  }

  function logout(req, res) {
    if (isCrossSite(req)) return sendStatus(res, 403, 'Forbidden', 'Sign-out comes only from the UI itself')
    const id = cookies(req)[SESSION_COOKIE]
    if (id && sessions.has(id)) log(`Signed out: ${sessions.get(id).user}`)
    if (id) sessions.delete(id)
    send(res, 200, '{"user":null}\n', {
      'content-type': 'application/json',
      'cache-control': 'no-store',
      'set-cookie': cookie(SESSION_COOKIE, '', 0),
    })
  }

  /** The person signed in on req, { user, groups }, or undefined. */
  function who(req) {
    const id = cookies(req)[SESSION_COOKIE]
    const session = id ? sessions.get(id) : undefined
    if (!session) return undefined
    if (session.expiresAt <= Date.now()) {
      sessions.delete(id)
      return undefined
    }
    return { user: session.user, groups: session.groups }
  }

  const ROUTES = {
    '/ui/login': ['GET', login],
    [CALLBACK_PATH]: ['GET', callback],
    '/ui/logout': ['POST', logout],
  }

  /** Answers a sign-in route and returns true, or returns false for any other path. */
  function handle(req, res, url) {
    const route = ROUTES[url.pathname]
    if (!route) return false
    const [method, answer] = route
    if (req.method !== method) {
      sendStatus(res, 405, 'MethodNotAllowed', `Only ${method} is allowed here`, { allow: method })
      return true
    }
    Promise.resolve(answer(req, res, url)).catch((err) => {
      log(`Sign-in failed: ${err.message}`)
      if (!res.headersSent) sendStatus(res, 502, 'BadGateway', `Cannot reach the sign-in issuer: ${err.message}`)
      else res.destroy()
    })
    return true
  }

  return { handle, who }
}
