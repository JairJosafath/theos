import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { DEMO_NAMESPACE, demoStory } from '../demo/fixture'
import type { BriefWriter } from '../k8s/briefs'
import type { Brief } from '../k8s/types'
import { Briefs } from './Briefs'

const writer: BriefWriter = { create: async () => {}, addPrompt: async () => {}, setPaused: async () => {} }

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

  it("shows how many issues each app's crew works on at once", () => {
    expect(html).toContain(
      '<span class="brief-app-parallel" title="How many issues its crew works on at once">3 at a time</span>',
    )
    expect(html).toContain('>1 at a time</span>')
    // catalog-api has no crew yet, so no parallel.
    expect(html.match(/brief-app-parallel/g)).toHaveLength(2)
  })

  it('shows an app with releases off with its message', () => {
    expect(html).toContain('Releases off')
    expect(html).toContain('Releases are off: ask for them in a prompt, or name this app in spec.releases.')
  })
})

describe('the Brief card with failed issues', () => {
  const now = Date.parse('2026-10-07T12:00:00Z')
  const failedBrief: Brief = {
    metadata: { name: 'lens', creationTimestamp: '2026-10-07T10:00:00Z' },
    spec: { idea: 'A CEL lens', autopilot: 'Off' },
    status: {
      phase: 'Planned',
      issues: [{ repository: 'lens/cel-lens-cli', number: 1, url: 'http://g/cli/1', title: 'CLI' }],
      failed: [
        {
          container: 'cel-lens-cli',
          number: 1,
          url: 'http://g/cli/1',
          kind: 'exhausted',
          reason: 'Tasks cel-lens-cli-1, -2 and -3 failed.',
          tries: 3,
        },
      ],
      failedWaiting: 1,
    },
  }
  const html = renderToStaticMarkup(
    createElement(Briefs, { briefs: [failedBrief], namespace: DEMO_NAMESPACE, canCreate: false, writer, now }),
  )

  it('lists each failed issue with its link and reason, and says to answer in a prompt', () => {
    expect(html).toContain('Failed issues (1)')
    expect(html).toContain('Tasks cel-lens-cli-1, -2 and -3 failed.')
    expect(html).toContain('it will be filed again')
    expect(html).toContain('Say in a prompt whether to try again')
  })

  it('marks the failed issue in the issue list', () => {
    expect(html).toContain('brief-issue-failed')
  })
})

// #630: the why of a Needs you item is shown whole, however long, and wraps.
describe('the Brief card with an item that needs you', () => {
  const now = Date.parse('2026-10-08T17:00:00Z')
  const why =
    'Its only change, #136, already shipped in v0.2.0, and the v0.3.0 milestone held nothing else when the ' +
    'last try stopped; closing it keeps the release history in order until new work merges into the milestone.'
  const brief: Brief = {
    metadata: { name: 'cel-lens', creationTimestamp: '2026-10-08T10:00:00Z' },
    spec: { idea: 'A CEL lens', autopilot: 'Ask' },
    status: {
      phase: 'Planned',
      needsYou: [
        {
          kind: 'Issue',
          name: 'cel-lens-engine#158',
          question: 'Issue cel-lens-engine#158 "Prepare v0.3.0": It needs you.',
          recommend: 'Close it.',
          why,
          since: '2026-10-08T16:47:00Z',
        },
      ],
      needsYouWaiting: 1,
    },
  }
  const html = renderToStaticMarkup(
    createElement(Briefs, { briefs: [brief], namespace: DEMO_NAMESPACE, canCreate: false, writer, now }),
  )

  it('shows the whole why after what it would do', () => {
    expect(html).toContain(`It would: Close it. — ${why}`)
  })

  it('neither clamps nor keeps the item on one line', () => {
    const item = html.slice(html.indexOf('brief-needs-you'), html.indexOf(why))
    expect(item).not.toMatch(/clamp|nowrap|ellipsis/)
  })
})

describe('the Brief card paused or not', () => {
  const now = Date.parse('2026-10-07T12:00:00Z')
  const render = (briefs: Brief[]) =>
    renderToStaticMarkup(createElement(Briefs, { briefs, namespace: DEMO_NAMESPACE, canCreate: false, writer, now }))
  const story = demoStory(now).briefs
  const paused = render(story.filter((b) => b.metadata.name === 'gift-cards'))
  const running = render(story.filter((b) => b.metadata.name === 'returns'))

  it("offers Resume on the demo's paused Brief and shows what still runs", () => {
    expect(paused).toContain('aria-label="Resume brief gift-cards"')
    expect(paused).toContain('Paused; 1 Task still running: gift-cards-api-3')
    expect(paused).toContain('role="status"')
  })

  it('offers Pause on a Brief that is not paused, with no paused note', () => {
    expect(running).toContain('aria-label="Pause brief returns"')
    expect(running).not.toContain('brief-paused')
  })
})

describe('the Describe an app form', () => {
  const html = renderToStaticMarkup(
    createElement(Briefs, { briefs: [], namespace: DEMO_NAMESPACE, canCreate: true, writer, now: 0 }),
  )

  it("offers parallel 1 to 5, the factory's default first", () => {
    expect(html).toContain('Parallel')
    const options = [...html.matchAll(/<option value="(\d)"[^>]*>([^<]*)<\/option>/g)].map((m) => [m[1], m[2]])
    expect(options).toEqual([
      ['0', 'The factory’s default (3 at a time)'],
      ['1', '1 at a time'],
      ['2', '2 at a time'],
      ['3', '3 at a time'],
      ['4', '4 at a time'],
      ['5', '5 at a time'],
    ])
  })
})
