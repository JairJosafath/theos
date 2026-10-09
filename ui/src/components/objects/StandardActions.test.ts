import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { UiSession } from '../../k8s/session'
import type { StandardWriter } from '../../k8s/standards'
import type { Container, ContainerPhase, Standard } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { StandardActions, standardUpdates } from './StandardActions'

const standard: Standard = {
  metadata: { name: 'python-standard', namespace: 'webshop' },
  spec: {
    template: { url: 'https://gitea.example/templates/python-standard', ref: 'v1.2.0' },
    supportedTypes: ['service'],
    supportedFrameworks: ['python-fastapi'],
  },
}
const container = (name: string, phase: ContainerPhase | undefined, standardRef = 'python-standard'): Container => ({
  metadata: { name, namespace: 'webshop' },
  spec: {
    softwareSystemRef: { name: 'webshop-core' },
    type: 'service',
    gitProviderRef: { name: 'gitea' },
    frameworkRef: { name: 'python-fastapi' },
    standardRef: { name: standardRef },
  },
  status: phase ? { phase } : undefined,
})
const containers = [
  container('orders-api', 'Ready'),
  container('payments', 'Ready'),
  container('search', 'Failed'),
  container('inventory', undefined),
  container('storefront', 'Ready', 'node-standard'),
]
const writer: StandardWriter = { bumpRef: async () => undefined }
const serviceAccount: UiSession = { actions: true, mode: 'service-account', user: null }
const signedOut: UiSession = { actions: true, mode: 'oidc', user: null, login: '/ui/login', logout: '/ui/logout' }

function render(session: UiSession | undefined, list: Container[] = containers): string {
  return renderToStaticMarkup(
    createElement(
      SessionContext.Provider,
      { value: session },
      createElement(StandardActions, { standard, containers: list, writer }),
    ),
  )
}

describe('standardUpdates', () => {
  it('counts the Ready Containers on the Standard, and names the others on it', () => {
    expect(standardUpdates('python-standard', containers)).toEqual({
      ready: ['orders-api', 'payments'],
      notReady: ['search', 'inventory'],
    })
    expect(standardUpdates('none', containers)).toEqual({ ready: [], notReady: [] })
  })
})

describe('StandardActions', () => {
  it('shows nothing with actions off, or before the session is known', () => {
    expect(render({ actions: false, mode: 'off', user: null })).toBe('')
    expect(render(undefined)).toBe('')
  })

  it('offers the ref, filled with the pinned one, and a dialog counting the update pull requests', () => {
    const html = render(serviceAccount)
    expect(html).toContain('Bump the ref…')
    expect(html).toMatch(/<input[^>]*value="v1.2.0"/)
    expect(html).toContain('<dialog')
    expect(html).toContain('<strong>2 Containers</strong> on python-standard will get an update pull request')
    expect(html).toContain('orders-api, payments')
    expect(html).toContain('Not Ready, so no pull request: <span class="mono">search, inventory</span>')
  })

  it('says when no Container gets a pull request, and words one Container as one', () => {
    expect(render(serviceAccount, [])).toContain('No Container on python-standard is Ready')
    expect(render(serviceAccount, [container('orders-api', 'Ready')])).toContain('<strong>1 Container</strong>')
  })

  it('never refuses a ref itself: the button stays enabled', () => {
    expect(render(serviceAccount)).not.toMatch(/<button[^>]*disabled/)
  })

  it('says to sign in, in place of the button, in OIDC mode with nobody signed in', () => {
    const html = render(signedOut)
    expect(html).toContain('Sign in to bump its ref.')
    expect(html).toContain('href="/ui/login?return=')
    expect(html).not.toContain('<button')
    const signedIn = render({ ...signedOut, user: { name: 'oidc:ada@example.com', groups: [] } })
    expect(signedIn).toContain('Bump the ref…')
  })
})
