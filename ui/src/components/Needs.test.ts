import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import type { ContainerWriter } from '../k8s/containers'
import type { UiSession } from '../k8s/session'
import { SessionContext } from '../lib/hooks'
import type { Need } from '../model/needs'
import { NeedsArea } from './Needs'

const writer: ContainerWriter = {
  retry: async () => undefined,
  answerStandard: async () => undefined,
  setChecks: async () => undefined,
  startTask: async () => 'api-x7k2p',
}
const take = 'kubectl -n shop annotate container api c4.factory.io/standard-take=v3'
const keep = 'kubectl -n shop annotate container api c4.factory.io/standard-keep=v3'
const filesBehind: Need = {
  key: 'Container/shop/api/StandardFilesBehind',
  kind: 'Container',
  namespace: 'shop',
  name: 'api',
  title: 'has Standard files left out of an update',
  reason: 'FilesBehind',
  message: 'go-standard is at v3, but its updates left out Makefile',
  place: { label: 'Open it', to: { page: { ns: 'shop', kind: 'Container', name: 'api' } } },
  links: [],
  standardAnswer: {
    ref: 'v3',
    take,
    keep,
    files: [
      { path: 'Makefile', ref: 'v1', change: '--- Makefile (v1)\n+++ Makefile (v3)\n@@ -1 +1 @@\n-lint:\n+lint: fmt\n' },
      { path: 'check.yaml', ref: 'v2' },
    ],
  },
}
beforeAll(() => vi.stubGlobal('window', { location: { pathname: '/', search: '', hash: '' } }))

const serviceAccount: UiSession = { actions: true, mode: 'service-account', user: null }
const signedOut: UiSession = { actions: true, mode: 'oidc', user: null, login: '/ui/login', logout: '/ui/logout' }

function render(session: UiSession | undefined, item: Need = filesBehind): string {
  return renderToStaticMarkup(
    createElement(
      SessionContext.Provider,
      { value: session },
      createElement(NeedsArea, { items: [item], projects: [], loaded: true, problems: [], setUrl: () => {}, writer }),
    ),
  )
}

describe('NeedsArea', () => {
  it('offers the take and keep buttons of Standard files left out, with actions on', () => {
    const html = render(serviceAccount)
    expect(html).toContain('Take the Standard’s version')
    expect(html).toContain('Keep ours')
    expect(html).not.toContain(take)
    const signedIn = render({ ...signedOut, user: { name: 'oidc:ada@example.com', groups: [] } })
    expect(signedIn).toContain('Keep ours')
  })

  it('shows the take and keep commands without actions', () => {
    for (const session of [{ actions: false, mode: 'off', user: null } as UiSession, undefined]) {
      const html = render(session)
      expect(html).toContain(take)
      expect(html).toContain(keep)
      expect(html).not.toContain('Keep ours')
    }
  })

  it('says to sign in, with the commands, in OIDC mode with nobody signed in', () => {
    const html = render(signedOut)
    expect(html).toContain('Sign in to take or keep them.')
    expect(html).toContain('href="/ui/login?return=')
    expect(html).toContain(keep)
    expect(html).not.toContain('Keep ours')
  })

  it('shows each file’s change collapsed beside the answers, with actions on or off', () => {
    for (const session of [serviceAccount, { actions: false, mode: 'off', user: null } as UiSession, signedOut]) {
      const html = render(session)
      expect(html).toContain('<details class="left-out-change"><summary>What the Standard changed')
      expect(html).toContain('+lint: fmt')
      expect(html).toContain('check.yaml')
      expect(html.match(/<details/g)).toHaveLength(1)
    }
  })

  it('offers no answers on an item without them', () => {
    const html = render(serviceAccount, { ...filesBehind, standardAnswer: undefined })
    expect(html).not.toContain('Keep ours')
    expect(html).not.toContain(keep)
  })
})
