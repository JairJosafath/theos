import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import type { CrewWriter } from '../../k8s/crews'
import type { UiSession } from '../../k8s/session'
import type { Crew } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { CrewPage } from './CrewPage'

const crew = (run?: 'Failed' | 'Succeeded'): Crew => ({
  metadata: { name: 'orders-api', namespace: 'webshop' },
  spec: {
    containerRef: { name: 'orders-api' },
    implementer: { name: 'i' },
    reviewer: { name: 'r' },
    tagReleases: true,
  },
  status: run ? { lastRelease: { tag: 'v0.2.0', commit: 'a04e6f1', time: '2026-06-01T09:00:00Z', run } } : {},
})
const writer: CrewWriter = {
  setPaused: async () => {},
  setParallel: async () => {},
  setReleases: async () => {},
  rerunRelease: async () => {},
}

function render(session: UiSession | undefined, c: Crew): string {
  return renderToStaticMarkup(
    createElement(
      SessionContext.Provider,
      { value: session },
      createElement(CrewPage, { crew: c, agents: [], tasks: [], containers: [], briefs: [], writer }),
    ),
  )
}

/** What each of the Crew's controls shows. */
const CONTROLS = ['Pause crew', 'Resume crew', 'Issues at once', 'Save release settings', 'Re-run']
const ACTING: UiSession = { actions: true, mode: 'service-account', user: null }
const SIGNED_OUT: UiSession = { actions: true, mode: 'oidc', user: null, login: '/ui/login', logout: '/ui/logout' }

// The page's links read the URL they are on.
beforeAll(() => vi.stubGlobal('window', { location: { pathname: '/', search: '', hash: '' } }))

describe('the Crew page’s actions', () => {
  it('offers pause, parallel and the release settings when the UI server acts', () => {
    const html = render(ACTING, crew('Succeeded'))
    expect(html).toContain('Pause crew</button>')
    expect(html).toContain('Issues at once')
    expect(html).toContain('Save release settings')
  })

  it('offers the re-run only on a failed release run', () => {
    expect(render(ACTING, crew('Failed'))).toContain('Re-run v0.2.0’s failed runs')
    expect(render(ACTING, crew('Succeeded'))).not.toContain('Re-run')
    expect(render(ACTING, crew())).not.toContain('Re-run')
  })

  it('says to sign in instead, with OIDC and nobody signed in', () => {
    const html = render(SIGNED_OUT, crew('Failed'))
    expect(html).toContain('Sign in to pause this Crew')
    expect(html).toContain('href="/ui/login?return=%2F"')
    for (const control of CONTROLS) expect(html).not.toContain(control)
  })

  it('offers nothing with actions off, and in demo mode', () => {
    for (const session of [{ actions: false, mode: 'off', user: null } as UiSession, undefined]) {
      const html = render(session, crew('Failed'))
      for (const control of CONTROLS) expect(html).not.toContain(control)
      expect(html).not.toContain('Sign in')
    }
  })
})
