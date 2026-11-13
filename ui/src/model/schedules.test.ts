import { describe, expect, it } from 'vitest'
import type { Container, Crew, CrewSchedule, Standard } from '../k8s/types'
import { proposalUse, scheduleDifferences, scheduleProposals, writtenZone } from './schedules'

const DEPS: CrewSchedule = {
  name: 'dependencies',
  cron: '0 6 * * 1',
  timeZone: 'UTC',
  goal: 'Update the dependencies.',
}
const FLAKES: CrewSchedule = { name: 'flakes', cron: '0 2 * * *', timeZone: 'UTC', goal: 'Find the flaky tests.' }
const README: CrewSchedule = { name: 'Zreadme', cron: '0 9 1 * *', timeZone: 'UTC', goal: 'Check the README.' }

function crew(schedules?: CrewSchedule[], namespace = 'webshop'): Crew {
  return {
    metadata: { name: 'orders-api', namespace },
    spec: { containerRef: { name: 'orders-api' }, implementer: { name: 'i' }, reviewer: { name: 'r' }, schedules },
  }
}

function container(spec: Partial<Container['spec']>, status?: Container['status'], namespace = 'webshop'): Container {
  return {
    metadata: { name: 'orders-api', namespace },
    spec: { softwareSystemRef: { name: 'shop' }, type: 'service', gitProviderRef: { name: 'gitea' }, ...spec },
    status,
  }
}

function standard(name: string, schedules?: CrewSchedule[], namespace = 'webshop'): Standard {
  return {
    metadata: { name, namespace },
    spec: {
      supportedTypes: ['service'],
      supportedFrameworks: ['fastapi'],
      template: { url: 'https://example.com/t.git', ref: 'v1' },
      schedules,
    },
  }
}

const PY = container({ standardRef: { name: 'python-standard' } })

// #72, step 6: which proposals a Crew is offered, which names it uses, and what differs.
describe('scheduleProposals', () => {
  const cases: {
    name: string
    crew: Crew
    containers: Container[]
    standards: Standard[]
    standard?: string
    offered: string[]
    inUse: [string, string[]][]
  }[] = [
    {
      name: 'a Crew with no schedules is offered every proposal',
      crew: crew(),
      containers: [PY],
      standards: [standard('python-standard', [DEPS, FLAKES])],
      standard: 'python-standard',
      offered: ['dependencies', 'flakes'],
      inUse: [],
    },
    {
      name: 'a proposal whose name the Crew uses is in use, not offered, and the same one differs in nothing',
      crew: crew([{ ...DEPS }]),
      containers: [PY],
      standards: [standard('python-standard', [DEPS, FLAKES])],
      standard: 'python-standard',
      offered: ['flakes'],
      inUse: [['dependencies', []]],
    },
    {
      name: 'a schedule of the name with another cron, zone and goal says each',
      crew: crew([{ name: 'dependencies', cron: '0 7 * * 2', timeZone: 'Europe/Amsterdam', goal: 'Update them all.' }]),
      containers: [PY],
      standards: [standard('python-standard', [DEPS])],
      standard: 'python-standard',
      offered: [],
      inUse: [['dependencies', ['cron', 'timeZone', 'goal']]],
    },
    {
      name: 'a zone given on take is the one difference of a proposal taken as it is written',
      crew: crew([{ ...DEPS, timeZone: 'Europe/Amsterdam' }]),
      containers: [PY],
      standards: [standard('python-standard', [DEPS])],
      standard: 'python-standard',
      offered: [],
      inUse: [['dependencies', ['timeZone']]],
    },
    {
      name: 'a schedule the Crew has under a name no proposal has is neither',
      crew: crew([{ name: 'docs', cron: '0 9 1 * *', goal: 'Check the docs.' }]),
      containers: [PY],
      standards: [standard('python-standard', [DEPS])],
      standard: 'python-standard',
      offered: ['dependencies'],
      inUse: [],
    },
    {
      name: 'both lists are by name in code-point order, whatever the Standard’s order',
      crew: crew([{ ...README }, { ...FLAKES }]),
      containers: [PY],
      standards: [
        standard('python-standard', [
          { ...DEPS, name: 'zeta' },
          README,
          FLAKES,
          { ...DEPS, name: 'Alpha' },
          { ...DEPS, name: 'alpha' },
        ]),
      ],
      standard: 'python-standard',
      offered: ['Alpha', 'alpha', 'zeta'],
      inUse: [
        ['Zreadme', []],
        ['flakes', []],
      ],
    },
    {
      name: 'a bundled Container’s Standard is the one its Bundle expanded to',
      crew: crew(),
      containers: [
        container(
          { bundleRef: { name: 'py' } },
          { bundle: { name: 'py', framework: 'fastapi', standard: 'python-standard' } },
        ),
      ],
      standards: [standard('python-standard', [DEPS])],
      standard: 'python-standard',
      offered: ['dependencies'],
      inUse: [],
    },
    {
      name: 'a Standard with no proposals offers none',
      crew: crew([{ ...DEPS }]),
      containers: [PY],
      standards: [standard('python-standard')],
      standard: 'python-standard',
      offered: [],
      inUse: [],
    },
    {
      name: 'another Standard’s proposals are not this Crew’s',
      crew: crew(),
      containers: [PY],
      standards: [standard('go-standard', [DEPS]), standard('python-standard', [FLAKES])],
      standard: 'python-standard',
      offered: ['flakes'],
      inUse: [],
    },
    {
      name: 'a Container with no Standard offers none',
      crew: crew(),
      containers: [container({ adopt: true })],
      standards: [standard('python-standard', [DEPS])],
      standard: undefined,
      offered: [],
      inUse: [],
    },
    {
      name: 'a Container that isn’t found offers none',
      crew: crew(),
      containers: [],
      standards: [standard('python-standard', [DEPS])],
      standard: undefined,
      offered: [],
      inUse: [],
    },
    {
      name: 'a Standard named but not found offers none, and is still named',
      crew: crew(),
      containers: [PY],
      standards: [],
      standard: 'python-standard',
      offered: [],
      inUse: [],
    },
    {
      name: 'a Container and a Standard of the same names in another namespace are not this Crew’s',
      crew: crew(),
      containers: [container({ standardRef: { name: 'python-standard' } }, undefined, 'other')],
      standards: [standard('python-standard', [DEPS], 'other')],
      standard: undefined,
      offered: [],
      inUse: [],
    },
    {
      name: 'a Standard of the name only in another namespace is not this Container’s: named, and offers none',
      crew: crew(),
      containers: [PY],
      standards: [standard('python-standard', [DEPS], 'other')],
      standard: 'python-standard',
      offered: [],
      inUse: [],
    },
  ]
  for (const c of cases) {
    it(c.name, () => {
      const got = scheduleProposals(c.crew, c.containers, c.standards)
      expect(got.standard).toBe(c.standard)
      expect(got.offered.map((p) => p.name)).toEqual(c.offered)
      expect(got.inUse.map((u) => [u.proposal.name, u.differs.map((d) => d.field)])).toEqual(c.inUse)
    })
  }

  it('gives the Crew’s schedule beside the proposal it shares a name with', () => {
    const mine = { ...DEPS, cron: '0 7 * * 2' }
    const [use] = scheduleProposals(crew([mine]), [PY], [standard('python-standard', [DEPS])]).inUse
    expect(use.schedule).toEqual(mine)
    expect(use.proposal).toEqual(DEPS)
  })
})

// #144: what a Standard's page says of its proposals, and which Crews use each name.
describe('proposalUse', () => {
  /** A Container of this name and its Crew of the same name, with these schedules. */
  const pair = (
    name: string,
    spec: Partial<Container['spec']>,
    schedules?: CrewSchedule[],
    namespace = 'webshop',
  ): [Container, Crew] => {
    const c = container(spec, undefined, namespace)
    const k = crew(schedules, namespace)
    return [
      { ...c, metadata: { ...c.metadata, name } },
      { ...k, metadata: { ...k.metadata, name }, spec: { ...k.spec, containerRef: { name } } },
    ]
  }
  const PYTHON = { standardRef: { name: 'python-standard' } }
  const cases: {
    name: string
    proposed?: CrewSchedule[]
    pairs: [Container, Crew][]
    want: [string, [string, string[]][]][]
  }[] = [
    { name: 'a Standard that proposes none has none', pairs: [pair('orders-api', PYTHON, [DEPS])], want: [] },
    {
      name: 'a proposal no Crew has a schedule of is listed with no Crew',
      proposed: [DEPS],
      pairs: [pair('orders-api', PYTHON)],
      want: [['dependencies', []]],
    },
    {
      name: 'the Crews with a schedule of the name, by name, each with what differs',
      proposed: [DEPS],
      pairs: [
        pair('storefront', PYTHON, [{ ...DEPS }]),
        pair('orders-api', PYTHON, [{ ...DEPS, timeZone: 'Europe/Amsterdam' }, FLAKES]),
        pair('catalog-api', PYTHON, [FLAKES]),
      ],
      want: [
        [
          'dependencies',
          [
            ['orders-api', ['timeZone']],
            ['storefront', []],
          ],
        ],
      ],
    },
    {
      name: 'the proposals are by name in code-point order, whatever the Standard’s order',
      proposed: [FLAKES, README, DEPS],
      pairs: [pair('orders-api', PYTHON, [FLAKES])],
      want: [
        ['Zreadme', []],
        ['dependencies', []],
        ['flakes', [['orders-api', []]]],
      ],
    },
    {
      name: 'a Crew of another Standard’s Container is not listed, whatever its schedules are named',
      proposed: [DEPS],
      pairs: [
        pair('orders-api', { standardRef: { name: 'go-standard' } }, [DEPS]),
        pair('adopted', { adopt: true }, [DEPS]),
      ],
      want: [['dependencies', []]],
    },
    {
      name: 'a Crew of the same names in another namespace is not listed',
      proposed: [DEPS],
      pairs: [pair('orders-api', PYTHON, [DEPS], 'other')],
      want: [['dependencies', []]],
    },
  ]
  for (const c of cases) {
    it(c.name, () => {
      const got = proposalUse(
        standard('python-standard', c.proposed),
        c.pairs.map(([container]) => container),
        c.pairs.map(([, crew]) => crew),
      )
      expect(got.map((u) => [u.proposal.name, u.users.map((x) => [x.crew, x.differs.map((d) => d.field)])])).toEqual(
        c.want,
      )
    })
  }

  it('counts the Crew of a bundled Container, by the Standard its Bundle expanded to', () => {
    const [c, k] = pair('inventory-api', { bundleRef: { name: 'py' } }, [DEPS])
    c.status = { bundle: { name: 'py', framework: 'fastapi', standard: 'python-standard' } }
    expect(proposalUse(standard('python-standard', [DEPS]), [c], [k])[0].users).toEqual([
      { crew: 'inventory-api', differs: [] },
    ])
  })
})

describe('writtenZone', () => {
  it('is the zone a proposal names, and none for UTC, which the API server writes into one that names none', () => {
    expect(writtenZone({ ...DEPS, timeZone: 'America/New_York' })).toBe('America/New_York')
    expect(writtenZone({ ...DEPS, timeZone: 'UTC' })).toBeUndefined()
    expect(writtenZone({ ...DEPS, timeZone: undefined })).toBeUndefined()
  })
})

describe('scheduleDifferences', () => {
  const cases: { name: string; schedule: Partial<CrewSchedule>; proposal?: Partial<CrewSchedule>; want: unknown[] }[] =
    [
      { name: 'nothing for the same schedule', schedule: {}, want: [] },
      {
        name: 'the cron as each is written',
        schedule: { cron: '0 7 * * 1' },
        want: [{ field: 'cron', crew: '0 7 * * 1', proposal: '0 6 * * 1' }],
      },
      {
        name: 'the zone, the Crew’s first',
        schedule: { timeZone: 'Europe/Amsterdam' },
        want: [{ field: 'timeZone', crew: 'Europe/Amsterdam', proposal: 'UTC' }],
      },
      { name: 'no zone is UTC, on the Crew’s side', schedule: { timeZone: undefined }, want: [] },
      { name: 'no zone is UTC, on the proposal’s side', schedule: {}, proposal: { timeZone: undefined }, want: [] },
      {
        name: 'the goal',
        schedule: { goal: 'Update them all.' },
        want: [{ field: 'goal', crew: 'Update them all.', proposal: 'Update the dependencies.' }],
      },
      { name: 'not a goal’s white space at its ends', schedule: { goal: '  Update the dependencies.\n' }, want: [] },
      {
        name: 'a goal’s white space inside it',
        schedule: { goal: 'Update  the dependencies.' },
        want: [{ field: 'goal', crew: 'Update  the dependencies.', proposal: 'Update the dependencies.' }],
      },
      {
        name: 'cron, zone and goal in that order',
        schedule: { goal: 'x', timeZone: 'Asia/Tokyo', cron: '1 1 1 1 1' },
        want: [
          { field: 'cron', crew: '1 1 1 1 1', proposal: '0 6 * * 1' },
          { field: 'timeZone', crew: 'Asia/Tokyo', proposal: 'UTC' },
          { field: 'goal', crew: 'x', proposal: 'Update the dependencies.' },
        ],
      },
    ]
  for (const c of cases) {
    it(c.name, () => {
      expect(scheduleDifferences({ ...DEPS, ...c.schedule }, { ...DEPS, ...c.proposal })).toEqual(c.want)
    })
  }
})
