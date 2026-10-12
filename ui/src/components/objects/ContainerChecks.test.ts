import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { ContainerWriter } from '../../k8s/containers'
import type { UiSession } from '../../k8s/session'
import type { ChecksMode, Condition, Container } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { ContainerChecks } from './ContainerChecks'

const notApplied: Condition = {
  type: 'ChecksApplied',
  status: 'False',
  reason: 'StandardRunsFullChecks',
  message: "The Standard hello-standard can't run the full check only before a release; CI runs it on every change",
}

function container(asked?: ChecksMode, runs: ChecksMode = 'EveryChange', conditions: Condition[] = []): Container {
  return {
    metadata: { name: 'orders-api', namespace: 'webshop' },
    spec: {
      softwareSystemRef: { name: 'webshop-core' },
      type: 'service',
      gitProviderRef: { name: 'gitea' },
      frameworkRef: { name: 'python-fastapi' },
      standardRef: { name: 'python-standard' },
      checks: asked,
    },
    status: { phase: 'Ready', standard: { name: 'python-standard', ref: 'v1.0.0', checks: runs }, conditions },
  }
}
const writer: ContainerWriter = {
  retry: async () => undefined,
  setChecks: async () => undefined,
  startTask: async () => 'orders-api-x7k2p',
}
const serviceAccount: UiSession = { actions: true, mode: 'service-account', user: null }
const signedOut: UiSession = { actions: true, mode: 'oidc', user: null, login: '/ui/login', logout: '/ui/logout' }

function render(session: UiSession | undefined, c: Container): string {
  return renderToStaticMarkup(
    createElement(
      SessionContext.Provider,
      { value: session },
      createElement(ContainerChecks, { container: c, writer }),
    ),
  )
}

describe('ContainerChecks', () => {
  it('reads an unset setting as every change, with no form when actions are off', () => {
    const html = render({ actions: false, mode: 'off', user: null }, container())
    expect(html).toContain('On every change')
    expect(html).toContain('(EveryChange)')
    expect(html).not.toContain('<select')
    expect(html).not.toContain('Sign in')
  })

  it('reads BeforeRelease, and tells a person who tags by hand to push release-check and wait for full', () => {
    const html = render(undefined, container('BeforeRelease', 'BeforeRelease'))
    expect(html).toContain('Before a release')
    expect(html).toContain('make check-light')
    expect(html).toMatch(/release-check\/vX\.Y\.Z/)
    expect(html).toMatch(/<span class="mono">full<\/span> job/)
    expect(html).not.toContain('still runs')
  })

  it('says the repository still runs the other mode until the Standard update merges', () => {
    const html = render(undefined, container('BeforeRelease', 'EveryChange'))
    expect(html).toContain('The repository still runs the full check on every change')
  })

  it('shows ChecksApplied when False, with its reason and message', () => {
    const html = render(undefined, container('BeforeRelease', 'EveryChange', [notApplied]))
    expect(html).toContain('Not applied (StandardRunsFullChecks)')
    expect(html).toContain('hello-standard can&#x27;t run the full check only before a release')
    expect(html).not.toContain('still runs')
    expect(html).not.toContain('release-check/')
    expect(render(undefined, container('BeforeRelease', 'BeforeRelease'))).not.toContain('Not applied')
  })

  it('offers the setting with actions on, the current mode selected and Save off until it changes', () => {
    const html = render(serviceAccount, container('BeforeRelease', 'BeforeRelease'))
    expect(html).toContain('<select')
    expect(html).toContain('<option value="EveryChange">On every change</option>')
    expect(html).toContain('<option value="BeforeRelease" selected="">Before a release</option>')
    expect(html).toMatch(/<button type="submit"[^>]*disabled/)
  })

  it('says to sign in, in place of the form, in OIDC mode with nobody signed in', () => {
    const html = render(signedOut, container())
    expect(html).toContain('Sign in to change when the full check runs.')
    expect(html).not.toContain('<select')
  })
})
