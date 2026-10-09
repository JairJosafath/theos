import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { FixedNow } from '../lib/hooks'
import { absoluteTime } from '../model/format'
import { When } from './bits'

const NOW = Date.parse('2026-06-01T12:00:00Z')
const AT = '2026-06-01T11:48:00Z'

const render = (props: Parameters<typeof When>[0]) =>
  renderToStaticMarkup(createElement(FixedNow.Provider, { value: NOW }, createElement(When, props)))

describe('When', () => {
  it('shows how long ago, with the date and time on hover and to a screen reader', () => {
    const html = render({ at: AT })
    expect(html).toContain(`dateTime="${AT}"`)
    expect(html).toContain(`title="${absoluteTime(AT)}"`)
    expect(html).toMatch(/>12 min ago<span class="visually-hidden"> \(.+\)<\/span><\/time>$/)
  })

  it('shows both with full, and takes milliseconds', () => {
    const html = render({ at: Date.parse(AT), full: true })
    expect(html).toContain('dateTime="2026-06-01T11:48:00.000Z"')
    expect(html).toContain(`12 min ago (${absoluteTime(AT)})`)
  })

  it('says never for no time, without a time element', () => {
    expect(render({})).toBe('never')
  })
})
