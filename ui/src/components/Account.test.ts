import { createElement, type ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { UiSession } from '../k8s/session'
import { SessionContext } from '../lib/hooks'
import { Account } from './Account'
import { FormError } from './briefs/PromptForm'

const OIDC: UiSession = { actions: true, mode: 'oidc', user: null, login: '/ui/login', logout: '/ui/logout' }
const SIGNED_IN: UiSession = { ...OIDC, user: { name: 'ada@example.com', groups: ['factory-admins'] } }

/** `node` as HTML under `session`. */
function render(session: UiSession | undefined, node: ReactNode): string {
  return renderToStaticMarkup(createElement(SessionContext.Provider, { value: session }, node))
}

const account = createElement(Account, { onSignOut: async () => {} })
const SIGN_IN = '<a class="account-signin" href="/ui/login?return=%2F">Sign in</a>'

describe('the account corner', () => {
  it('links to sign-in when OIDC is on and nobody is signed in', () => {
    expect(render(OIDC, account)).toBe(SIGN_IN)
  })

  it('names the person it acts as, with a sign-out', () => {
    const html = render(SIGNED_IN, account)
    expect(html).toContain('Acting as <strong>ada@example.com</strong>')
    expect(html).toContain('title="Groups: factory-admins"')
    expect(html).toContain('>Sign out</button>')
    expect(html).not.toContain('Sign in<')
  })

  it('shows nothing without OIDC, before the answer comes, and in demo mode', () => {
    expect(render({ actions: false, mode: 'off', user: null }, account)).toBe('')
    expect(render({ actions: true, mode: 'service-account', user: null }, account)).toBe('')
    expect(render(undefined, account)).toBe('')
  })
})

describe('a refused write', () => {
  const unauthorized = { message: 'Sign in to act: the UI makes writes only as a person signed in', signIn: true }

  it('links to sign-in when the refusal was a 401', () => {
    const html = render(OIDC, createElement(FormError, { error: unauthorized }))
    expect(html).toContain('Sign in to act')
    expect(html).toContain(SIGN_IN)
  })

  it('links to sign-in on a 401 even when the page thought a session was live', () => {
    expect(render(SIGNED_IN, createElement(FormError, { error: unauthorized }))).toContain(SIGN_IN)
  })

  it('has no link for any other refusal, or when the server signs nobody in', () => {
    const forbidden = { message: 'briefs is forbidden', signIn: false }
    expect(render(OIDC, createElement(FormError, { error: forbidden }))).not.toContain('Sign in<')
    expect(render(undefined, createElement(FormError, { error: unauthorized }))).not.toContain('Sign in<')
  })
})
