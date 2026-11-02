import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { demoStory } from '../../demo/fixture'
import type { BriefWriter } from '../../k8s/briefs'
import type { UiSession } from '../../k8s/session'
import type { Brief } from '../../k8s/types'
import { FixedNow, SessionContext } from '../../lib/hooks'
import { BriefPage } from './BriefPage'

const writer: BriefWriter = {
  create: async () => {},
  addPrompt: async () => {},
  setPaused: async () => {},
  setPauseRuns: async () => {},
  setSettings: async () => {},
}
const NOW = Date.parse('2026-10-07T12:00:00Z')
const webshop = demoStory(NOW).briefs.find((b) => b.metadata.name === 'webshop')!

const ACTING: UiSession = { actions: true, mode: 'service-account', user: null }
const OFF: UiSession = { actions: false, mode: 'off', user: null }
const SIGNED_OUT: UiSession = { actions: true, mode: 'oidc', user: null, login: '/ui/login', logout: '/ui/logout' }

// The page's links read the address; the sign-in link comes back to it.
vi.stubGlobal('window', { location: { pathname: '/', search: '?kind=Brief&name=webshop' } })

function render(session: UiSession | undefined, brief: Brief = webshop): string {
  return renderToStaticMarkup(
    createElement(
      SessionContext.Provider,
      { value: session },
      createElement(FixedNow.Provider, { value: NOW }, createElement(BriefPage, { brief, agents: [], writer })),
    ),
  )
}

/** The buttons of the page by their accessible name. */
function buttons(html: string): string[] {
  return [...html.matchAll(/<button[^>]*aria-label="([^"]+)"/g)].map((m) => m[1])
}

describe('the Brief page with actions on', () => {
  const html = render(ACTING)

  it('answers the Needs you issue with retry, close or wait, the recommended one marked', () => {
    expect(buttons(html)).toEqual(
      expect.arrayContaining(['Retry storefront #3', 'Close storefront #3', 'Wait storefront #3, as it would']),
    )
  })

  it('takes or skips each reviewer suggestion', () => {
    expect(buttons(html)).toEqual(
      expect.arrayContaining([
        'Take orders-api #17',
        'Skip orders-api #17',
        'Take storefront #4',
        'Skip storefront #4',
      ]),
    )
  })

  it('offers each autopilot mode, the Brief’s own pressed, parallel and releases', () => {
    expect(html).toContain('aria-label="Settings of brief webshop"')
    expect(html).toMatch(/aria-pressed="true"[^>]*>Ask</)
    expect(html).not.toMatch(/aria-pressed="false"[^>]*disabled=""[^>]*>Off</)
    expect(html).toContain('Set parallel')
    expect(html).toContain('Publish its releases')
    expect(html).not.toContain('ui.actions.enabled')
  })

  it('says an item is answered while its prompt waits for the next round', () => {
    const answered = render(ACTING, {
      ...webshop,
      spec: {
        ...webshop.spec,
        prompts: [
          ...webshop.spec.prompts!,
          'Skip suggestion #4 in storefront: Escape the product names on the list page.',
        ],
      },
    })
    expect(answered).toContain('Answered: Skip, for the next round')
    expect(buttons(answered)).not.toContain('Take storefront #4')
    expect(buttons(answered)).toContain('Take orders-api #17')
  })
})

describe('the Brief page with actions off', () => {
  const html = render(OFF)

  it('still answers in a prompt, a write the UI always makes', () => {
    expect(buttons(html)).toContain('Retry storefront #3')
  })

  it('shows the settings and says where to change them', () => {
    expect(html).toContain('ui.actions.enabled')
    expect(html).not.toContain('Set parallel')
    expect(html).not.toContain('Publish its releases')
    expect(html).toMatch(/aria-pressed="false"[^>]*disabled=""[^>]*>Off</)
  })
})

describe('the Brief page in OIDC mode, signed out', () => {
  const html = render(SIGNED_OUT)

  it('says to sign in instead of offering an answer or a setting', () => {
    expect(buttons(html).filter((b) => /^(Retry|Close|Wait|Take|Skip) /.test(b))).toEqual([])
    expect(html).toContain('to answer here.')
    expect(html).toContain('to change these settings.')
    expect(html).toContain('href="/ui/login?return=')
    expect(html).not.toContain('Set parallel')
  })
})
