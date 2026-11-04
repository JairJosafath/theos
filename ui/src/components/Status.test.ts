import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { WarningsBanner } from './Status'

const text = (html: string) => html.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')

describe('WarningsBanner', () => {
  const deprecated = 'theos.factory.io/v1alpha1 is deprecated since v0.30.0; use theos.factory.io/v1beta1.'

  it('shows what the API server warned, as a status and each once (#1618)', () => {
    const html = renderToStaticMarkup(createElement(WarningsBanner, { warnings: [deprecated, deprecated, 'other'] }))
    expect(html).toContain('class="banner banner-warning"')
    expect(html).toContain('role="status"')
    expect(html).not.toContain('role="alert"')
    expect(text(html).split(deprecated).length).toBe(2)
    expect(text(html)).toContain('other')
  })

  it('draws nothing when there is no warning', () => {
    expect(renderToStaticMarkup(createElement(WarningsBanner, { warnings: [] }))).toBe('')
    expect(renderToStaticMarkup(createElement(WarningsBanner, { warnings: [''] }))).toBe('')
  })
})
