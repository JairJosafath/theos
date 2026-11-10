import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import type { CrewWriter } from '../../k8s/crews'
import type { UiSession } from '../../k8s/session'
import type { Agent, Crew } from '../../k8s/types'
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
  setPauseRuns: async () => {},
  setParallel: async () => {},
  setReleases: async () => {},
  rerunRelease: async () => {},
}

function render(session: UiSession | undefined, c: Crew, agents: Agent[] = []): string {
  return renderToStaticMarkup(
    createElement(
      SessionContext.Provider,
      { value: session },
      createElement(CrewPage, { crew: c, agents, tasks: [], containers: [], briefs: [], writer }),
    ),
  )
}

/** What each of the Crew's controls shows. */
const CONTROLS = [
  'Pause runs',
  'Resume runs',
  'Pause crew',
  'Resume crew',
  'Issues at once',
  'Save release settings',
  'Re-run',
]
const ACTING: UiSession = { actions: true, mode: 'service-account', user: null }
const SIGNED_OUT: UiSession = { actions: true, mode: 'oidc', user: null, login: '/ui/login', logout: '/ui/logout' }

// The page's links read the URL they are on.
beforeAll(() => vi.stubGlobal('window', { location: { pathname: '/', search: '', hash: '' } }))

describe('the Crew page’s actions', () => {
  it('offers pause, parallel and the release settings when the UI server acts', () => {
    const html = render(ACTING, crew('Succeeded'))
    expect(html).toContain('Pause crew</button>')
    expect(html).toContain('Pause runs</button>')
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

describe('the Crew page’s published release', () => {
  const published = (state: 'Published' | 'Failed', reason?: string): Crew => ({
    ...crew('Succeeded'),
    spec: {
      ...crew().spec,
      publishReleases: {
        github: { repository: 'me/cel-lens', tokenSecretRef: { name: 'gh' } },
        source: { path: 'engine' },
      },
    },
    status: {
      lastRelease: {
        tag: 'v0.6.1',
        commit: 'a04e6f1',
        time: '2026-06-01T09:00:00Z',
        run: 'Succeeded',
        publish: {
          state,
          message: reason ? 'what the operator says' : undefined,
          source:
            state === 'Published'
              ? {
                  commit: '9b1c2d3e4f',
                  tag: 'engine/v0.6.1',
                  url: 'https://github.com/me/cel-lens/tree/engine/v0.6.1/engine',
                }
              : undefined,
        },
      },
      conditions: reason ? [{ type: 'Published', status: 'False', reason, message: 'what the operator says' }] : [],
    },
  })

  it('says publishReleases is deprecated and links the migration (#1603)', () => {
    const html = render(undefined, published('Published'))
    expect(html).toContain('href="https://github.com/JairJosafath/theos/blob/main/docs/design.md#publishing-a-release"')
    expect(html.replace(/<[^>]+>/g, '')).toContain('publishReleases is deprecated: a github DeployTarget')
    expect(render(undefined, crew('Succeeded'))).not.toContain('is deprecated')
  })

  it('links the published source at its tag', () => {
    const html = render(undefined, published('Published'))
    expect(html).toContain('<dt>Source</dt>')
    expect(html).toContain('href="https://github.com/me/cel-lens/tree/engine/v0.6.1/engine"')
    expect(html).toContain('engine/v0.6.1')
    expect(html).toContain('at 9b1c2d3')
  })

  it('shows no source before it is out', () => {
    expect(render(undefined, published('Failed', 'GitHubError'))).not.toContain('<dt>Source</dt>')
  })

  it.each([
    ['SourceTagTaken', 'Failed: the source’s tag is taken by a commit with other files'],
    ['FolderOverlaps', 'Failed: its source folder overlaps another Crew’s'],
    ['SourceTooLarge', 'Failed: a file of the source is too large for GitHub'],
    ['BranchProtected', 'Failed: the repository’s default branch is protected'],
    ['SecretFound', 'Failed: the source holds a secret'],
  ])('says why it failed with %s, in words, beside the message', (reason, words) => {
    const html = render(undefined, published('Failed', reason))
    expect(html).toContain(words)
    expect(html).toContain('what the operator says')
  })
})

// #1226: the page lists spec.routes in order, with the Crew's own Agent where
// a route leaves a role blank.
describe('the Crew page’s routes by label', () => {
  const agent = (name: string, displayName?: string): Agent => ({
    metadata: { name, namespace: 'webshop' },
    spec: { displayName, image: 'agent:1', harness: 'claude-code' },
  })
  const agents = [agent('i', 'Implementer'), agent('r', 'Reviewer'), agent('cheap', 'Cheap model')]
  const routed: Crew = {
    ...crew(),
    spec: {
      ...crew().spec,
      routes: [
        { label: 'kind/docs', implementer: { name: 'cheap' } },
        { label: 'area/security', implementer: { name: 'cheap' }, reviewer: { name: 'strict' } },
      ],
    },
  }

  it('lists each route in order: the label, who implements and who reviews', () => {
    const html = render(undefined, routed, agents)
    expect(html).toContain('Routes by label')
    const docs = html.indexOf('kind/docs')
    const security = html.indexOf('area/security')
    expect(docs).toBeGreaterThan(-1)
    expect(security).toBeGreaterThan(docs)
    // The second route names both roles; its reviewer isn't an Agent the page knows.
    expect(html.slice(security)).toContain('Cheap model')
    expect(html.slice(security)).toContain('title="Agent strict was not found">strict')
    expect(html.slice(security)).not.toContain('the Crew’s')
  })

  it('shows the Crew’s own Agent for a role the route leaves blank', () => {
    const html = render(undefined, routed, agents)
    const docs = html.slice(html.indexOf('kind/docs'), html.indexOf('area/security'))
    expect(docs).toContain('Cheap model')
    expect(docs).toContain('Reviewer</span>')
    expect(docs).toContain('(the Crew’s)')
    expect(docs).not.toContain('Implementer</span>')
  })

  it('shows nothing new for a Crew with no routes', () => {
    for (const c of [crew(), { ...crew(), spec: { ...crew().spec, routes: [] } }]) {
      const html = render(undefined, c, agents)
      expect(html).not.toContain('Routes by label')
      expect(html).not.toContain('crew-routes')
    }
  })
})

// #63, #72: a Crew's schedules are on its page, with when each is next due,
// the issue it filed last, and why one can't be used.
describe('the Crew page’s schedules', () => {
  const scheduled = (): Crew => {
    const c = crew()
    c.spec.schedules = [
      { name: 'dependencies', cron: '0 6 * * 1', timeZone: 'Europe/Amsterdam', goal: 'Update the dependencies.' },
      { name: 'flakes', cron: '0 2 * * *', goal: 'Find the flaky tests.' },
    ]
    c.status = {
      schedules: [
        {
          name: 'dependencies',
          since: '2026-11-09T05:00:00Z',
          lastFiled: '2026-11-09T05:00:00Z',
          lastIssue: 12,
          next: '2026-11-16T05:00:00Z',
        },
        { name: 'flakes', since: '2026-11-09T00:00:00Z' },
      ],
    }
    return c
  }

  it('shows no schedules panel for a Crew without schedules', () => {
    expect(render(undefined, crew())).not.toContain('Work on a timer')
  })

  it('lists each schedule with its cron, zone, goal, next time and last issue', () => {
    const html = render(undefined, scheduled())
    expect(html).toContain('Work on a timer')
    expect(html).toContain('<span class="mono">dependencies</span>')
    expect(html).toContain('<span class="mono">0 6 * * 1</span>')
    expect(html).toContain('(Europe/Amsterdam)')
    expect(html).toContain('Update the dependencies.')
    expect(html).toContain('<time dateTime="2026-11-16T05:00:00Z">')
    expect(html).toContain('last filed issue #12')
    expect(html).toContain('(UTC)')
    expect(html).toContain('No next time · nothing filed yet')
  })

  it('says why a schedule can’t be used, from the Scheduled condition', () => {
    const c = scheduled()
    c.status!.conditions = [
      {
        type: 'Scheduled',
        status: 'False',
        reason: 'InvalidSchedule',
        message: 'schedule flakes: time zone "Mars/Olympus" is not one the operator can load',
      },
    ]
    const html = render(undefined, c)
    expect(html).toContain('role="alert"')
    expect(html).toContain('schedule flakes: time zone &quot;Mars/Olympus&quot; is not one the operator can load')
  })

  it('says a paused Crew files none', () => {
    const c = scheduled()
    c.spec.paused = true
    expect(render(undefined, c)).toContain('a paused Crew files none')
  })
})
