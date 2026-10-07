import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { DEMO_NAMESPACE, demoStory } from '../demo/fixture'
import type { BriefWriter } from '../k8s/briefs'
import { Briefs } from './Briefs'

const writer: BriefWriter = { create: async () => {}, addPrompt: async () => {} }

describe('the Brief card', () => {
  const now = Date.parse('2026-10-07T12:00:00Z')
  const briefs = demoStory(now).briefs.filter((b) => b.metadata.name === 'webshop')
  const html = renderToStaticMarkup(
    createElement(Briefs, { briefs, namespace: DEMO_NAMESPACE, canCreate: false, writer, now }),
  )

  it('lists each app of the demo Brief under Releases', () => {
    expect(html).toContain('Releases <span class="section-hint">3</span>')
  })

  it('links a released app to its Gitea release and its published GitHub release', () => {
    expect(html).toContain('href="http://localhost:3000/webshop/orders-api/releases/tag/v0.2.0"')
    expect(html).toContain('href="https://github.com/webshop-co/orders-api/releases/tag/v0.2.0"')
    expect(html).toContain('Releases on')
    expect(html).toContain('Released v0.2.0 and published it to webshop-co/orders-api.')
    expect(html).toContain('3 h ago')
  })

  it('shows an app with releases off with its message', () => {
    expect(html).toContain('Releases off')
    expect(html).toContain('Releases are off: ask for them in a prompt, or name this app in spec.releases.')
  })
})
