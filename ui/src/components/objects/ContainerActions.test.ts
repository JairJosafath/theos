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
const behind = (update?: NonNullable<NonNullable<Container['status']>['standard']>['update']): Container => {
  const c = container('Ready')
  c.status!.standard = {
    name: 'go-standard',
    ref: 'v1.3.0',
    behind: [
      { path: 'Makefile', ref: 'v1.1.0', change: '--- Makefile (v1.1.0)\n+++ Makefile (v1.3.0)\n-lint:\n+lint: fmt\n' },
      { path: '.golangci.yml', ref: 'v1.2.0' },
    ],
    update,
  }
  return c
}
const crew: Crew = {
  metadata: { name: 'orders-api', namespace: 'webshop' },
  spec: { containerRef: { name: 'orders-api' }, implementer: { name: 'claude' }, reviewer: { name: 'codex' } },
}
const writer: ContainerWriter = {
  retry: async () => undefined,
  answerStandard: async () => undefined,
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

  it('offers to take or keep the Standard’s files left out, naming each with its ref', () => {
    const html = render(serviceAccount, behind())
    expect(html).toContain('Standard files left out')
    expect(html).toMatch(/Makefile<\/span> <span class="muted">at v1\.1\.0/)
    expect(html).toMatch(/\.golangci\.yml<\/span> <span class="muted">at v1\.2\.0/)
    expect(html).toContain('Take the Standard’s version')
    expect(html).toContain('Keep ours')
    expect(html).toContain('lists the lines it drops')
    expect(render(serviceAccount, container('Ready'))).not.toContain('Standard files left out')
  })

  it('shows each file’s change collapsed beside the take and keep buttons', () => {
    const html = render(serviceAccount, behind())
    expect(html).toContain('<details class="left-out-change"><summary>What the Standard changed')
    expect(html).toContain('+lint: fmt')
    expect(html.match(/<details/g)).toHaveLength(1)
    expect(html.indexOf('+lint: fmt')).toBeLessThan(html.indexOf('Keep ours'))
  })

  it('offers no answer while a take runs or stopped, and one after another update stopped', () => {
    const take = { ref: 'v1.3.0', take: true, pullRequest: 9 }
    expect(render(serviceAccount, behind(take))).not.toContain('Keep ours')
    expect(render(serviceAccount, behind({ ...take, stopped: 'UpdateRefused' }))).not.toContain('Keep ours')
    expect(render(serviceAccount, behind({ ref: 'v1.4.0' }))).not.toContain('Keep ours')
    expect(render(serviceAccount, behind({ ref: 'v1.4.0', stopped: 'UpdateRefused' }))).toContain('Keep ours')
  })

  it('says to sign in, in place of the buttons, in OIDC mode with nobody signed in', () => {
    const html = render(signedOut, container('Failed'))
    expect(html).toContain('Sign in to retry its scaffold or start a Task on it.')
    expect(html).toContain('href="/ui/login?return=')
    expect(html).not.toContain('<button')
    const signedIn = render({ ...signedOut, user: { name: 'oidc:ada@example.com', groups: [] } }, container('Failed'))
    expect(signedIn).toContain('Retry the scaffold…')
    expect(render(signedOut, behind())).toContain(
      'Sign in to take or keep its Standard’s files left out or start a Task on it.',
    )
  })
})
