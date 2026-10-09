import { describe, expect, it, vi } from 'vitest'
import { LiveBriefWriter } from './briefs'
import { fetchSession, needsSignIn, signInHref, signOut } from './session'
import { ApiError } from './watch'

/** The UI server's answer to a write with OIDC on and nobody signed in (server.mjs). */
const UNAUTHORIZED = {
  kind: 'Status',
  apiVersion: 'v1',
  status: 'Failure',
  reason: 'Unauthorized',
  message: 'Sign in to act: the UI makes writes only as a person signed in',
  code: 401,
}

describe('the UI session', () => {
  it('asks ui/session, relative to the page', async () => {
    const answer = { actions: true, mode: 'oidc', user: null, writes: [], login: '/ui/login', logout: '/ui/logout' }
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify(answer)))
    expect(await fetchSession(fetchImpl)).toEqual(answer)
    expect(fetchImpl).toHaveBeenCalledWith('ui/session', expect.objectContaining({ cache: 'no-store' }))
  })

  it('signs out with a POST to the logout route', async () => {
    const fetchImpl = vi.fn(async () => new Response('{"user":null}'))
    await signOut('/ui/logout', fetchImpl)
    expect(fetchImpl).toHaveBeenCalledWith('/ui/logout', expect.objectContaining({ method: 'POST' }))
  })

  it("rejects with the server's message when sign-out is refused", async () => {
    const refused = { kind: 'Status', message: 'Sign-out comes only from the UI itself', code: 403 }
    const fetchImpl = async () => new Response(JSON.stringify(refused), { status: 403 })
    await expect(signOut('/ui/logout', fetchImpl)).rejects.toThrow('Sign-out comes only from the UI itself')
  })

  it('signs in and comes back to the page it left', () => {
    expect(signInHref('/ui/login', '/?project=shop&view=containers')).toBe(
      '/ui/login?return=%2F%3Fproject%3Dshop%26view%3Dcontainers',
    )
  })

  it('takes a 401 as "sign in", and no other refusal', async () => {
    const fetchImpl = async () => new Response(JSON.stringify(UNAUTHORIZED), { status: 401 })
    const err = await new LiveBriefWriter('k8s', fetchImpl).addPrompt('shop', 'webshop', 'more tests').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.message).toBe(UNAUTHORIZED.message)
    expect(needsSignIn(err)).toBe(true)
    expect(needsSignIn(new ApiError(403, 'forbidden'))).toBe(false)
    expect(needsSignIn(new Error('network'))).toBe(false)
  })
})
