// Who the UI acts as, in the top bar: with OIDC on, a "Sign in" link when
// nobody is signed in, else the person's name and a sign-out. A write refused
// with 401 shows the same link (SignInLink, under SessionContext).

import { useContext, useState } from 'react'
import { signInHref } from '../k8s/session'
import { SessionContext } from '../lib/hooks'

/** The page's path, where a sign-in comes back to. */
function here(): string {
  return typeof window === 'undefined' ? '/' : `${window.location.pathname}${window.location.search}`
}

/** The "Sign in" link, when the UI server signs people in; nothing otherwise. */
export function SignInLink() {
  const session = useContext(SessionContext)
  if (!session?.login) return null
  return (
    <a className="account-signin" href={signInHref(session.login, here())}>
      Sign in
    </a>
  )
}

/** The top bar's account corner; empty unless the UI server signs people in. */
export function Account({ onSignOut }: { onSignOut: () => Promise<void> }) {
  const session = useContext(SessionContext)
  const [signingOut, setSigningOut] = useState(false)
  const [problem, setProblem] = useState<string>()
  if (session?.mode !== 'oidc') return null
  if (!session.user) return <SignInLink />

  async function signOut() {
    setSigningOut(true)
    setProblem(undefined)
    try {
      await onSignOut()
    } catch (err) {
      setProblem(`Sign-out failed: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setSigningOut(false)
    }
  }

  const { name, groups } = session.user
  return (
    <span className="account" title={groups.length ? `Groups: ${groups.join(', ')}` : undefined}>
      <span className="account-name">
        Acting as <strong>{name}</strong>
      </span>
      <button type="button" className="button button-secondary account-signout" onClick={signOut} disabled={signingOut}>
        {signingOut ? 'Signing out…' : 'Sign out'}
      </button>
      {problem ? (
        <span className="account-problem" role="alert">
          {problem}
        </span>
      ) : null}
    </span>
  )
}
