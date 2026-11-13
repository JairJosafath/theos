import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import type { CrewWriter } from '../../k8s/crews'
import type { UiSession } from '../../k8s/session'
import type { Agent, Container, Crew, CrewSchedule, Standard } from '../../k8s/types'
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
  runSchedule: async () => {},
  addSchedule: async () => {},
}

function render(
  session: UiSession | undefined,
  c: Crew,
  agents: Agent[] = [],
  containers: Container[] = [],
  standards: Standard[] = [],
): string {
  return renderToStaticMarkup(
    createElement(
      SessionContext.Provider,
      { value: session },
      createElement(CrewPage, { crew: c, agents, tasks: [], containers, standards, briefs: [], writer }),
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

// #62: a Crew's approval rules are on its page, each with what it is for and
// what it asks.
describe('the Crew page’s approval rules', () => {
  it('shows no approval panel for a Crew without rules', () => {
    expect(render(undefined, crew())).not.toContain('Who approves a change')
  })

  it('lists each rule in order, with the files and changes it is for and what it asks', () => {
    const c = crew()
    c.spec.approval = [
      { paths: ['migrations/', 'internal/auth/'], require: 'Person' },
      { labels: ['risk/high', 'kind/security'], require: 'Person' },
      { changedLinesOver: 400, require: 'Person' },
      { paths: ['*.md'], labels: ['kind/docs'], require: 'None' },
      { require: 'Reviewer' },
    ]
    const html = render(undefined, c)
    expect(html).toContain('Who approves a change')
    expect(html).toContain('the strictest of what its files need')
    const rules = [...html.matchAll(/<span class="mono">([^<]*)<\/span><span class="muted"> · <\/span>([^<]*)</g)]
      .map((m) => [m[1], m[2]])
      .filter(([, asks]) => /waits|merges/.test(asks))
    expect(rules).toEqual([
      ['migrations/, internal/auth/ in every change', 'waits for a person'],
      ['changes for issues labelled risk/high or kind/security', 'waits for a person'],
      ['changes of more than 400 lines', 'waits for a person'],
      ['*.md in changes for issues labelled kind/docs', 'merges once CI passes, with no review'],
      ['every change', 'merges on the reviewer’s approval'],
    ])
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

  // #72: a schedule runs now when a person asks, from a button per schedule.
  it('offers to run each schedule now, only where the page can write', () => {
    const html = render(ACTING, scheduled())
    expect(html.match(/>Run now<\/button>/g)).toHaveLength(2)
    expect(html).toContain('Run now files a schedule’s issue at once, under the same two rules.')
    expect(render(undefined, scheduled())).not.toContain('>Run now</button>')
    expect(render(SIGNED_OUT, scheduled())).not.toContain('>Run now</button>')
  })

  it('shows a request the operator has not answered yet, and why it waits', () => {
    const c = scheduled()
    c.metadata.annotations = { 'theos.factory.io/run-schedule': 'dependencies' }
    c.status!.conditions = [
      {
        type: 'Scheduled',
        status: 'True',
        reason: 'Scheduled',
        message:
          '2 schedules, the next due at 2026-11-16T05:00:00Z; schedule dependencies is asked to run now, and waits for its open issue to close',
      },
    ]
    const html = render(ACTING, c)
    expect(html).toContain('disabled="">Asked to run now</button>')
    expect(html.match(/>Run now<\/button>/g)).toHaveLength(1)
    expect(html).toContain('role="status"')
    expect(html).toContain('schedule dependencies is asked to run now, and waits for its open issue to close')
  })
})

// #72, step 6: the Crew's page offers the schedules its Container's Standard
// proposes, and for a name the Crew uses says where its schedule differs.
describe('the Crew page’s Standard proposals', () => {
  const DEPS: CrewSchedule = { name: 'dependencies', cron: '0 6 * * 1', timeZone: 'UTC', goal: 'Update the deps.' }
  const FLAKES: CrewSchedule = { name: 'flakes', cron: '0 2 * * *', timeZone: 'UTC', goal: 'Find the flaky tests.' }
  const container: Container = {
    metadata: { name: 'orders-api', namespace: 'webshop' },
    spec: {
      softwareSystemRef: { name: 'shop' },
      type: 'service',
      gitProviderRef: { name: 'gitea' },
      standardRef: { name: 'python-standard' },
    },
  }
  const standard = (schedules?: CrewSchedule[]): Standard => ({
    metadata: { name: 'python-standard', namespace: 'webshop' },
    spec: {
      supportedTypes: ['service'],
      supportedFrameworks: ['fastapi'],
      template: { url: 'https://example.com/t.git', ref: 'v1' },
      schedules,
    },
  })
  const withSchedules = (schedules: CrewSchedule[]): Crew => {
    const c = crew()
    c.spec.schedules = schedules
    return c
  }
  const page = (session: UiSession | undefined, c: Crew, proposed?: CrewSchedule[]) =>
    render(session, c, [], [container], [standard(proposed)])
  /** The proposals panel alone: from its title to the end of its section. */
  const panel = (html: string) => {
    const from = html.indexOf('<section class="panel" aria-labelledby="crew-proposals">')
    return from < 0 ? '' : html.slice(from, html.indexOf('</section>', from))
  }

  it('shows no proposals panel when the Standard proposes nothing, or the Container has no Standard', () => {
    expect(page(ACTING, crew())).not.toContain('Schedules its Standard proposes')
    expect(render(ACTING, crew(), [], [], [standard([DEPS])])).not.toContain('Schedules its Standard proposes')
  })

  it('lists the proposals whose name the Crew doesn’t use, with their cron and goal and the Standard they are from', () => {
    const html = panel(page(undefined, crew(), [FLAKES, DEPS]))
    expect(html).toContain('Schedules its Standard proposes')
    expect(html).toContain('>Standard python-standard</a>')
    expect(html).toContain('Not in use by this Crew')
    expect(html).toContain('<span class="mono">0 6 * * 1</span><div>Update the deps.</div>')
    expect(html).toContain('<span class="mono">0 2 * * *</span><div>Find the flaky tests.</div>')
    expect(html.indexOf('>dependencies<')).toBeLessThan(html.indexOf('>flakes<'))
    expect(html).not.toContain('Names this Crew uses')
  })

  it('shows no time zone as the proposal’s own: the zone is asked for on take', () => {
    expect(panel(page(undefined, crew(), [DEPS]))).not.toContain('UTC')
    const acting = panel(page(ACTING, crew(), [DEPS]))
    expect(acting).toContain('Time zone for dependencies')
    expect(acting).toContain('you say which time zone its cron is read in')
    // The field starts empty: no zone is filled in for the person.
    expect(acting).toMatch(/<input[^>]*class="input mono crew-zone"[^>]*value=""/)
  })

  it('shows the zone a Standard wrote into a proposal on the offered row, and still asks for the Crew’s', () => {
    const NY = { ...DEPS, timeZone: 'America/New_York' }
    for (const session of [ACTING, undefined]) {
      const html = panel(page(session, crew(), [NY, FLAKES]))
      expect(html).toContain('Written for the time zone <span class="mono">America/New_York</span>.')
      // Only on the row of the proposal that names one.
      expect(html.match(/Written for the time zone/g)).toHaveLength(1)
    }
    expect(panel(page(ACTING, crew(), [NY]))).toMatch(/<input[^>]*class="input mono crew-zone"[^>]*value=""/)
  })

  it('offers to take a proposal only where the page can write, and says how otherwise', () => {
    const acting = panel(page(ACTING, crew(), [DEPS, FLAKES]))
    expect(acting).toContain('>Take dependencies</button>')
    expect(acting).toContain('>Take flakes</button>')
    // Nothing is taken before a zone is given.
    expect(acting.match(/<button type="submit"[^>]*disabled=""/g)).toHaveLength(2)
    for (const session of [undefined, SIGNED_OUT, { actions: false, mode: 'off', user: null } as UiSession]) {
      const html = panel(page(session, crew(), [DEPS, FLAKES]))
      expect(html).not.toContain('>Take ')
      expect(html).not.toContain('<input')
      expect(html).toContain('kubectl edit crew orders-api -n webshop')
    }
  })

  it('offers no proposal whose name the Crew uses, and says the Crew’s schedule has the proposal’s fields when it does', () => {
    const html = panel(page(ACTING, withSchedules([{ ...DEPS }]), [DEPS, FLAKES]))
    expect(html).toContain('Names this Crew uses')
    expect(html).not.toContain('>Take dependencies</button>')
    expect(html).toContain('>Take flakes</button>')
    // UTC is not called the proposal's: one that names no zone reads UTC too.
    expect(html).toContain(
      'the Crew’s schedule of this name has the proposal’s cron and goal, and is in UTC, ' +
        'which a proposal that names no time zone also reads as',
    )
    expect(html).not.toContain('the proposal’s cron, time zone and goal')
    expect(html).not.toContain('differs from the proposal')
  })

  it('says the Crew’s schedule has the proposal’s time zone only for a zone the Standard wrote', () => {
    const NY = { ...DEPS, timeZone: 'America/New_York' }
    const html = panel(page(ACTING, withSchedules([{ ...NY }]), [NY]))
    expect(html).toContain('the Crew’s schedule of this name has the proposal’s cron, time zone and goal')
    const differing = panel(page(ACTING, withSchedules([{ ...NY, timeZone: 'UTC' }]), [NY]))
    expect(differing).toContain(
      '<div><span class="muted">The proposal’s: </span><span class="mono">America/New_York</span></div>',
    )
  })

  it('shows where the Crew’s schedule differs from the proposal of its name: cron, zone and goal', () => {
    const mine = { name: 'dependencies', cron: '0 7 * * 2', timeZone: 'Europe/Amsterdam', goal: 'Update them all.' }
    const html = panel(page(undefined, withSchedules([mine]), [DEPS]))
    expect(html).toContain('the Crew’s schedule of this name differs from the proposal:')
    expect(html).toContain(
      '<dt>Cron</dt><dd><div><span class="muted">The Crew’s: </span><span class="mono">0 7 * * 2</span></div>' +
        '<div><span class="muted">The proposal’s: </span><span class="mono">0 6 * * 1</span></div></dd>',
    )
    expect(html).toContain(
      '<dt>Time zone</dt><dd><div><span class="muted">The Crew’s: </span><span class="mono">Europe/Amsterdam</span></div>' +
        '<div><span class="muted">The proposal: </span><span class="mono">UTC</span>' +
        '<span class="muted">, which one that names no time zone also reads as</span></div></dd>',
    )
    expect(html).toContain(
      '<dt>Goal</dt><dd><div><span class="muted">The Crew’s: </span><span>Update them all.</span></div>' +
        '<div><span class="muted">The proposal’s: </span><span>Update the deps.</span></div></dd>',
    )
    expect(html).not.toContain('The proposal’s: </span><span class="mono">UTC')
    expect(html).not.toContain('Not in use by this Crew')
  })

  it('names only the fields that differ', () => {
    const html = panel(page(undefined, withSchedules([{ ...DEPS, timeZone: 'Asia/Tokyo' }]), [DEPS]))
    expect(html).toContain('<dt>Time zone</dt>')
    expect(html).not.toContain('<dt>Cron</dt>')
    expect(html).not.toContain('<dt>Goal</dt>')
  })

  it('claims nothing about where a schedule came from', () => {
    const mine = { ...DEPS, cron: '0 7 * * 2' }
    for (const c of [withSchedules([mine]), withSchedules([{ ...DEPS }])]) {
      for (const session of [ACTING, undefined]) {
        expect(panel(page(session, c, [DEPS, FLAKES]))).not.toMatch(
          /taken from|was taken|changed since|came from|copied from/i,
        )
      }
    }
  })

  it('leaves the Crew’s own schedules as they were listed', () => {
    const html = page(ACTING, withSchedules([{ ...DEPS, timeZone: 'Europe/Amsterdam' }]), [DEPS])
    expect(html).toContain('Work on a timer')
    expect(html).toContain('(Europe/Amsterdam)')
    expect(html.indexOf('Work on a timer')).toBeLessThan(html.indexOf('Schedules its Standard proposes'))
  })
})
