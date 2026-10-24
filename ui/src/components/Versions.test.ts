import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { Versions } from '../model/versions'
import { VersionLine } from './Versions'

const render = (versions: Versions) => renderToStaticMarkup(createElement(VersionLine, { versions }))

describe('the version line', () => {
  it('shows every version and no warning when the UI and the operator agree', () => {
    const html = render({ ui: '1.4.0', operator: '1.4.0', chart: '1.4.0', api: 'c4.factory.io/v1beta1' })
    expect(html).toContain('UI 1.4.0 · operator 1.4.0 · chart 1.4.0 · API c4.factory.io/v1beta1')
    expect(html).not.toContain('role="alert"')
  })

  it('warns when they differ', () => {
    const html = render({ ui: '1.4.0', operator: '1.5.0', chart: '1.5.0', api: 'c4.factory.io/v1beta1' })
    expect(html).toContain('role="alert"')
    expect(html).toContain('This UI is 1.4.0 and the operator 1.5.0')
  })

  it('says unknown, and warns of nothing, before a Project reports the operator', () => {
    const html = render({ ui: 'dev', api: 'c4.factory.io/v1beta1' })
    expect(html).toContain('operator unknown · chart unknown')
    expect(html).not.toContain('role="alert"')
  })
})
