import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { DEMO_NAMESPACE } from '../demo/fixture'
import type { Problem } from '../k8s/sources'
import type { Crew } from '../k8s/types'
import { crewSummaries } from '../model/crews'
import { CrewsPanel } from './factory/Crews'
import { PanelState, type PanelStateProps } from './PanelState'

const forbidden: Problem = {
  what: 'crews',
  code: 403,
  message: 'crews.theos.factory.io is forbidden',
  resource: 'crews',
}

const text = (html: string) => html.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')

function state(props: Partial<PanelStateProps>) {
  return renderToStaticMarkup(
    createElement(
      PanelState,
      { loaded: true, loading: 'Loading the Crews…', emptyText: 'No Crews yet.', ...props },
      createElement('ul', null, 'the Crews'),
    ),
  )
}

describe('PanelState', () => {
  it('says it is loading, as a status, until its lists load', () => {
    const html = state({ loaded: false })
    expect(html).toContain('role="status"')
    expect(text(html)).toContain('Loading the Crews…')
    expect(html).not.toContain('the Crews</ul>')
  })

  it('says the panel is empty once loaded with nothing', () => {
    const t = text(state({ empty: true }))
    expect(t).toContain('No Crews yet.')
    expect(t).not.toContain('Loading')
  })

  it('says a failed read with its fix instead of loading forever, or calling the panel empty', () => {
    for (const props of [{ loaded: false }, { empty: true }]) {
      const html = state({ ...props, problems: [forbidden] })
      expect(html).toContain('role="alert"')
      expect(text(html)).toContain('Could not read crews. The UI is not allowed to read these.')
      expect(text(html)).toContain('(403: crews.theos.factory.io is forbidden)')
      expect(text(html)).not.toMatch(/Loading|No Crews yet/)
    }
  })

  it('keeps what was read before a read failed, under the failure', () => {
    const html = state({ problems: [forbidden] })
    expect(text(html)).toContain('Could not read crews.')
    expect(html.indexOf('Could not read')).toBeLessThan(html.indexOf('the Crews</ul>'))
  })

  it('leaves an empty panel to its contents when it has no empty text of its own', () => {
    expect(state({ empty: true, emptyText: undefined })).toBe('<ul>the Crews</ul>')
  })

  it('shows the contents alone when everything is read', () => {
    const html = state({})
    expect(html).toBe('<ul>the Crews</ul>')
  })
})

describe('the Crews panel', () => {
  const crew: Crew = {
    metadata: { name: 'orders-api', namespace: DEMO_NAMESPACE },
    spec: { containerRef: { name: 'orders-api' }, implementer: { name: 'dev' }, reviewer: { name: 'rev' } },
  }
  const render = (crews: Crew[], loaded: boolean, problems?: Problem[]) =>
    text(
      renderToStaticMarkup(
        createElement(CrewsPanel, {
          crews: crewSummaries(crews, [], []),
          loaded,
          problems,
          onSelectTask: () => {},
          pageHref: () => undefined,
        }),
      ),
    )

  it('has a loading, an empty and an error state of its own', () => {
    expect(render([], false)).toContain('Loading the Crews…')
    expect(render([], true)).toContain('No Crews yet: Tasks are only started by hand.')
    const failed = render([], true, [forbidden])
    expect(failed).toContain('Could not read crews.')
    expect(failed).not.toContain('No Crews yet')
  })

  it('lists its Crews under a failed re-watch', () => {
    const t = render([crew], true, [forbidden])
    expect(t).toContain('Could not read crews.')
    expect(t).toContain('orders-api')
  })
})
