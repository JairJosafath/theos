import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { demoStory } from '../../demo/fixture'
import type { Task } from '../../k8s/types'
import { MetricsPanel } from './MetricsPanel'

const text = (html: string) =>
  html
    .replace(/<[^>]+>/g, ' ')
    .replace(/&#x27;/g, "'")
    .replace(/\s+/g, ' ')

const NOW = Date.parse('2026-10-16T04:00:00Z')
const story = demoStory(NOW)

function panel(tasks: Task[] = story.tasks, loaded = true) {
  return renderToStaticMarkup(createElement(MetricsPanel, { tasks, now: NOW, loaded, problems: [] }))
}

const rows = (html: string, caption: string) => {
  const table = html.split('<table').find((t) => t.includes(caption)) ?? ''
  return [...table.matchAll(/<tr><th scope="row">(.*?)<\/tr>/g)].map((m) => text(m[1]).trim())
}

describe('How well it works (measure-and-improve, step 3)', () => {
  it('rows every merged Task and each configuration its implementer ran on, the unrecorded last', () => {
    expect(rows(panel(), 'by their implementer')).toEqual([
      // Merged, autonomy, round 1, a person approved (#92: none of these was held), the three times, cost.
      'All 3 67% 67% 0% 13 min 16 min 22 min $2.21',
      'claude-code · claude-sonnet-5-5 2 50% 50% 0% 13 min 16 min 27 min $2.16',
      'Not recorded 1 100% 100% 0% 9 min 12 min 15 min $2.21',
    ])
  })

  it('sets the cost against the size of the change', () => {
    expect(rows(panel(), 'by the size of its change')).toEqual([
      'S (under 100 lines) 1 $2.94',
      'M (100 to 499) 1 $1.38',
      'L (500 to 999) 1 $2.21',
      'XL (1000 or more) 0 –',
    ])
  })

  it('offers the two periods, the last 7 days pressed, and says what Autonomy counts and what is left out', () => {
    const html = panel()
    expect(html).toContain('aria-pressed="true">7 days</button>')
    expect(html).toContain('aria-pressed="false">30 days</button>')
    expect(text(html)).toContain('Autonomy counts the code, not the decisions')
    expect(text(html)).toContain('Not recorded: Tasks whose runs ended before each run recorded its Agent.')
  })

  it('says when nothing merged in the period', () => {
    expect(text(panel([]))).toContain('No Task merged a pull request in the last 7 days.')
  })
})
