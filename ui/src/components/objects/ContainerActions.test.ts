import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { ContainerWriter } from '../../k8s/containers'
import type { UiSession } from '../../k8s/session'
import type { Container, Crew } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { ContainerActions } from './ContainerActions'

const container = (phase: 'Ready' | 'Failed'): Container => ({
  metadata: { name: 'orders-api', namespace: 'webshop' },
  spec: {
    softwareSystemRef: { name: 'webshop-core' },
    type: 'service',
    gitProviderRef: { name: 'gitea' },
    frameworkRef: { name: 'python-fastapi' },
  },
  status: { phase, repositoryURL: 'https://gitea.example/webshop/orders-api' },
})
const crew: Crew = {
  metadata: { name: 'orders-api', namespace: 'webshop' },
  spec: { containerRef: { name: 'orders-api' }, implementer: { name: 'claude' }, reviewer: { name: 'codex' } },
}
const writer: ContainerWriter = {
  retry: async () => undefined,
  setChecks: async () => undefined,
  startTask: async () => 'orders-api-x7k2p',
}
const serviceAccount: UiSession = { actions: true, mode: 'service-account', user: null }
const signedOut: UiSession = { actions: true, mode: 'oidc', user: null, login: '/ui/login', logout: '/ui/logout' }

function render(session: UiSession | undefined, c: Container, withCrew: Crew | null = crew): string {
  return renderToStaticMarkup(
    createElement(
      SessionContext.Provider,
      { value: session },
      createElement(ContainerActions, { container: c, crew: withCrew ?? undefined, writer }),
    ),
  )
}

describe('ContainerActions', () => {
  it('shows nothing with actions off, or before the session is known', () => {
    expect(render({ actions: false, mode: 'off', user: null }, container('Failed'))).toBe('')
    expect(render(undefined, container('Failed'))).toBe('')
  })

  it('offers the retry only on a Failed Container, with a dialog linking the repository', () => {
    const failed = render(serviceAccount, container('Failed'))
    expect(failed).toContain('Retry the scaffold…')
    expect(failed).toContain('<dialog')
    expect(failed).toContain('Empty the repository first')
    expect(failed).toContain('href="https://gitea.example/webshop/orders-api"')
    expect(render(serviceAccount, container('Ready'))).not.toContain('Retry the scaffold')
  })

  it('offers a goal box naming the Crew’s implementer and reviewer, never refusing an empty goal itself', () => {
    const html = render(serviceAccount, container('Ready'))
    expect(html).toContain('Start the Task')
    expect(html).toMatch(/claude<\/span> implements it and <span class="mono">codex<\/span> reviews it/)
    expect(html).not.toMatch(/<button type="submit"[^>]*disabled/)
  })

  it('says a Task needs a Crew when the Container has none', () => {
    const html = render(serviceAccount, container('Ready'), null)
    expect(html).toContain('sees no Crew for orders-api')
    expect(html).not.toContain('Start the Task')
  })

  it('says to sign in, in place of the buttons, in OIDC mode with nobody signed in', () => {
    const html = render(signedOut, container('Failed'))
    expect(html).toContain('Sign in to retry its scaffold or start a Task on it.')
    expect(html).toContain('href="/ui/login?return=')
    expect(html).not.toContain('<button')
    const signedIn = render({ ...signedOut, user: { name: 'oidc:ada@example.com', groups: [] } }, container('Failed'))
    expect(signedIn).toContain('Retry the scaffold…')
  })
})
