import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { DEMO_NAMESPACE, demoStory } from '../demo/fixture'
import type { BriefWriter } from '../k8s/briefs'
import type { Brief } from '../k8s/types'
import { FixedNow } from '../lib/hooks'
import { Briefs } from './Briefs'

const writer: BriefWriter = {
  create: async () => {},
  addPrompt: async () => {},
  setPaused: async () => {},
  setSettings: async () => {},
}

const pageHref = (name: string) => `?kind=Brief&name=${name}`

/** The Brief cards of `briefs` as HTML, with the clock stopped at `now`. */
function render(briefs: Brief[], now: number): string {
  return renderToStaticMarkup(
    createElement(
      FixedNow.Provider,
      { value: now },
      createElement(Briefs, { briefs, namespace: DEMO_NAMESPACE, canCreate: false, writer, pageHref }),
    ),
  )
}

describe('the Brief card', () => {
  const now = Date.parse('2026-10-07T12:00:00Z')
  const briefs = demoStory(now).briefs.filter((b) => b.metadata.name === 'webshop')
  const html = render(briefs, now)

  it('lists each app of the demo Brief under Releases', () => {
    expect(html).toContain('Releases <span class="section-hint">3</span>')
  })

  it('links a released app to its Gitea release, and says where it failed to publish', () => {
    expect(html).toContain('href="http://localhost:3000/webshop/orders-api/releases/tag/v0.2.0"')
    // GitHub refused the token, so there is no published release to link.
    expect(html).not.toContain('href="https://github.com/webshop-co/orders-api/releases/tag/v0.2.0"')
    expect(html).toContain('to webshop-co/orders-api')
    expect(html).toContain('Releases on')
    expect(html).toContain(
      'Release v0.2.0 could not be published to webshop-co/orders-api; the Crew says why and tries again.',
    )
    expect(html).toContain('3 h ago')
  })

  it('shows the Go module nobody can fetch yet, with the Crew’s message (#1001)', () => {
    expect(html).toContain('class="brief-app-module"')
    expect(html).toContain('Waiting')
    expect(html).toContain('github.com/webshop-co/orders-api')
    expect(html).toContain('has no tag v0.2.0 yet: Published is Unauthorized')
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
  const html = render([failedBrief], now)

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
  const html = render([brief], now)

  it('shows the whole why after what it would do', () => {
    expect(html).toContain(`It would: Close it. — ${why}`)
  })

  it('neither clamps nor keeps the item on one line', () => {
    const item = html.slice(html.indexOf('brief-needs-you'), html.indexOf(why))
    expect(item).not.toMatch(/clamp|nowrap|ellipsis/)
  })
})

// #892: a Container whose Standard files an update left out links to the
// Container's page, where it is settled, not to a prompt.
describe('the Brief card with a Container whose Standard files are behind', () => {
  const now = Date.parse('2026-10-12T17:00:00Z')
  const question =
    'Container `cel-lens-core` is at go-standard v1.2.0, but its updates left out .gitea/workflows/check.yaml ' +
    '(at v1.1.6): the repository changed them in lines the Standard changed too.'
  const brief: Brief = {
    metadata: { name: 'cel-lens', namespace: DEMO_NAMESPACE, creationTimestamp: '2026-10-12T10:00:00Z' },
    spec: { idea: 'A CEL lens', autopilot: 'Off' },
    status: {
      phase: 'Planned',
      needsYou: [
        {
          kind: 'Container',
          name: 'cel-lens-core',
          question,
          recommend: "Take the Standard's version: annotate the Container `c4.factory.io/standard-take=v1.2.0`.",
          why: "The Standard's fix (go-standard v1.2.0) reaches the app.",
          since: '2026-10-12T16:00:00Z',
        },
      ],
      needsYouWaiting: 1,
    },
  }
  // An object's page link reads the URL it is on.
  vi.stubGlobal('window', { location: { pathname: '/', search: '', hash: '' } })
  const html = render([brief], now)
  vi.unstubAllGlobals()

  it('shows the question and what the Brief would do', () => {
    expect(html).toContain(question)
    expect(html).toContain('It would: Take the Standard&#x27;s version')
  })

  it("links the item to the Container's page", () => {
    expect(html).toMatch(new RegExp(`href="[^"]*kind=Container&amp;name=cel-lens-core"[^>]*>open the Container</a>`))
  })

  it('says a Container is settled on its page, not in a prompt', () => {
    expect(html).toContain('A Container is settled on its page, which says what to do.')
    expect(html).not.toContain('Answer in a prompt')
  })
})

describe('the Brief card paused or not', () => {
  const now = Date.parse('2026-10-07T12:00:00Z')
  const story = demoStory(now).briefs
  const paused = render(
    story.filter((b) => b.metadata.name === 'gift-cards'),
    now,
  )
  const running = render(
    story.filter((b) => b.metadata.name === 'returns'),
    now,
  )

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
    createElement(Briefs, { briefs: [], namespace: DEMO_NAMESPACE, canCreate: true, writer, pageHref }),
  )

  it('copies none of the CRD’s limits or defaults', () => {
    expect(html).toContain('Parallel')
    expect(html).toMatch(
      /<input id="[^"]*-parallel" class="input" inputMode="numeric" [^>]*placeholder="the factory’s default"/,
    )
    expect(html).toContain('<option value="" selected="">The factory’s default</option>')
    expect(html).not.toMatch(/maxLength|maxlength|min=|max=|pattern=/)
  })
})
