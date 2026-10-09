// Who the UI acts as, from GET ui/session (see server.mjs), and signing out.
// With OIDC on, every write needs a person signed in at /ui/login.

import { ApiError, errorFromResponse } from './watch'

/** How the UI server acts: off (Briefs only), as its service account, or as the person signed in. */
export type ActionsMode = 'off' | 'service-account' | 'oidc'

/** What GET ui/session answers. Never a token. */
export interface UiSession {
  actions: boolean
  mode: ActionsMode
  /** The person signed in, with OIDC; null when nobody is. */
  user: { name: string; groups: string[] } | null
  /** Where to sign in and out; set only with OIDC. */
  login?: string
  logout?: string
}

/** The UI server's session answer; rejects with the server's message when it refuses. */
export async function fetchSession(fetchImpl: typeof fetch = fetch.bind(globalThis)): Promise<UiSession> {
  const res = await fetchImpl('ui/session', { headers: { Accept: 'application/json' }, cache: 'no-store' })
  if (!res.ok) throw await errorFromResponse(res)
  return (await res.json()) as UiSession
}

/** Ends the session at its logout route; rejects with the server's message when it refuses. */
export async function signOut(logout: string, fetchImpl: typeof fetch = fetch.bind(globalThis)): Promise<void> {
  const res = await fetchImpl(logout, { method: 'POST', headers: { Accept: 'application/json' } })
  if (!res.ok) throw await errorFromResponse(res)
}

/** The sign-in link that comes back to `here`, a path on this site, once the person is signed in. */
export function signInHref(login: string, here: string): string {
  return `${login}?${new URLSearchParams({ return: here })}`
}

/** Whether a write was refused because nobody is signed in (the UI server's 401 "Sign in to act"). */
export function needsSignIn(err: unknown): boolean {
  return err instanceof ApiError && err.code === 401
}

/**
 * Whether the page can make a write: `act`; `signIn` with OIDC and nobody
 * signed in; `off` for a setting while actions are off. A prompt is one of the
 * writes the UI always makes, so it needs only a person signed in with OIDC.
 * Without a session answer (demo mode, or the server didn't say) the page
 * offers prompts only; the server decides either way.
 */
export function canWrite(session: UiSession | undefined, write: 'prompt' | 'setting'): 'act' | 'signIn' | 'off' {
  if (session?.mode === 'oidc' && !session.user) return 'signIn'
  if (write === 'prompt') return 'act'
  return session?.actions ? 'act' : 'off'
}
