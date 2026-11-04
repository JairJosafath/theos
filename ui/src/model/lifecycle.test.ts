import { describe, expect, it } from 'vitest'
import type { Brief, Condition, Container, Crew, Relationship, Task } from '../k8s/types'
import {
  appliesConnector,
  containerNextStep,
  describeStandardUpdate,
  crewNextStep,
  failedCondition,
  hasPage,
  hasPhases,
  lifecycleOf,
  relationshipPhase,
  rerunReleaseCommand,
  resumeCrewCommand,
  retryCommand,
  standardKeepCommand,
  taskNextStep,
  type LifecycleKind,
} from './lifecycle'

/** The states of the path's steps, then of the side exits, as phase:state. */
function states(kind: LifecycleKind, phase: string | undefined) {
  const v = lifecycleOf(kind, phase)
  return {
    path: v.path.map((s) => `${s.phase}:${s.state}`).join(' '),
    exits: v.exits.map((s) => `${s.phase}:${s.state}`).join(' '),
    current: v.current?.phase,
  }
}

describe('lifecycleOf', () => {
  it.each([
    [
      'Brief',
      undefined,
      'Pending:next Planning:next Applying:next Planned:next',
      'NeedsInput:off Failed:off',
      undefined,
    ],
    [
      'Brief',
      'Pending',
      'Pending:current Planning:next Applying:next Planned:next',
      'NeedsInput:off Failed:off',
      'Pending',
    ],
    [
      'Brief',
      'Planning',
      'Pending:done Planning:current Applying:next Planned:next',
      'NeedsInput:off Failed:off',
      'Planning',
    ],
    [
      'Brief',
      'Applying',
      'Pending:done Planning:done Applying:current Planned:next',
      'NeedsInput:off Failed:off',
      'Applying',
    ],
    [
      'Brief',
      'Planned',
      'Pending:done Planning:done Applying:done Planned:current',
      'NeedsInput:off Failed:off',
      'Planned',
    ],
    [
      'Brief',
      'NeedsInput',
      'Pending:off Planning:off Applying:off Planned:off',
      'NeedsInput:current Failed:off',
      'NeedsInput',
    ],
    ['Brief', 'Failed', 'Pending:off Planning:off Applying:off Planned:off', 'NeedsInput:off Failed:current', 'Failed'],
    ['Container', undefined, 'Pending:next Scaffolding:next Ready:next', 'Failed:off', undefined],
    ['Container', 'Pending', 'Pending:current Scaffolding:next Ready:next', 'Failed:off', 'Pending'],
    ['Container', 'Scaffolding', 'Pending:done Scaffolding:current Ready:next', 'Failed:off', 'Scaffolding'],
    ['Container', 'Ready', 'Pending:done Scaffolding:done Ready:current', 'Failed:off', 'Ready'],
    ['Container', 'Failed', 'Pending:off Scaffolding:off Ready:off', 'Failed:current', 'Failed'],
  ] as const)('%s in %s', (kind, phase, path, exits, current) => {
    expect(states(kind, phase)).toEqual({ path, exits, current })
  })

  it.each([
    ['Brief', undefined, /has not reported on this Brief/],
    ['Brief', 'Pending', /first round starts by itself/],
    ['Brief', 'Planning', /wait for the round/],
    ['Brief', 'Applying', /wait for the round/],
    ['Brief', 'Planned', /Add a prompt to steer the next round/],
    ['Brief', 'NeedsInput', /Answer the planner's question in a prompt/],
    ['Brief', 'Failed', /Read why the round failed/],
    ['Container', undefined, /has not reported on this Container/],
    ['Container', 'Pending', /names something missing/],
    ['Container', 'Scaffolding', /creating the repository/],
    ['Container', 'Ready', /Standard update pull request/],
    ['Container', 'Failed', /retry the scaffold/],
  ] as const)('says what a person does for a %s in %s', (kind, phase, person) => {
    expect(lifecycleOf(kind, phase).person).toMatch(person)
  })

  it('labels each step as the badges do', () => {
    const v = lifecycleOf('Brief', 'NeedsInput')
    expect(v.current).toMatchObject({ label: 'Needs your input', tone: 'waiting' })
    expect(lifecycleOf('Container', 'Failed').current).toMatchObject({ label: 'Failed', tone: 'error' })
  })

  it('treats a phase it does not know as no phase', () => {
    expect(states('Container', 'Exploded')).toEqual(states('Container', undefined))
  })
})

function container(status: Container['status'], namespace = 'shop'): Container {
  return {
    metadata: { name: 'api', namespace },
    spec: {
      softwareSystemRef: { name: 'core' },
      type: 'service',
      gitProviderRef: { name: 'gitea' },
      frameworkRef: { name: 'go-chi' },
    },
    status,
  }
}

const cond = (type: string, status: Condition['status'], reason?: string, message?: string): Condition => ({
  type,
  status,
  reason,
  message,
})

describe('failedCondition', () => {
  it('is the first scaffold condition that is False', () => {
    const c = container({
      phase: 'Failed',
      conditions: [
        cond('ReferencesResolved', 'True', 'Resolved'),
        cond('ChoicesValid', 'True', 'Valid'),
        cond('RepositoryCreated', 'True', 'Created'),
        cond('Scaffolded', 'False', 'ForeignCommits', 'the repository has 3 commits'),
      ],
    })
    expect(failedCondition(c)).toMatchObject({ type: 'Scaffolded', reason: 'ForeignCommits' })
  })

  it('is nothing while the Container has not failed', () => {
    expect(failedCondition(container({ phase: 'Pending', conditions: [cond('Scaffolded', 'False', 'x')] }))).toBe(
      undefined,
    )
  })
})

describe('retryCommand', () => {
  it('annotates the Container in its namespace, overwriting an earlier value', () => {
    expect(retryCommand(container({ phase: 'Failed' }))).toBe(
      'kubectl -n shop annotate containers.theos.factory.io api theos.factory.io/retry=$(date +%s) --overwrite',
    )
  })
})

describe('containerNextStep', () => {
  it.each([
    ['ForeignCommits', 'Scaffolded', /Empty it, then retry/],
    ['LayerConflict', 'Scaffolded', /Two templates write the same file/],
    ['NothingToAdopt', 'Scaffolded', /Push its history first, then retry/],
    ['IdentityMismatch', 'Scaffolded', /name this project and container, then retry/],
    ['StandardFilesDiffer', 'Scaffolded', /what the Standard renders, then retry/],
    ['UnpinnedRef', 'Scaffolded', /Pin the template’s ref/],
    ['UnsupportedCI', 'Scaffolded', /Pick a template that supports it, or one without CI files, then retry/],
    ['UnsupportedType', 'ChoicesValid', /support this Container’s type/],
    ['UnsupportedFramework', 'ChoicesValid', /support this Container’s type/],
    ['NotWired', 'ChoicesValid', /wired only by the Frameworks the condition names\. Pick one of them/],
    ['DeploysItself', 'ChoicesValid', /remove the Compute, and any Deployment but gitea-release/],
    ['GitError', 'RepositoryCreated', /Fix what the failed condition names/],
  ])('on Failed with %s, says what to fix and gives the retry line', (reason, type, text) => {
    const c = container({
      phase: 'Failed',
      repositoryURL: 'https://git.example/shop/api',
      conditions: [cond(type, 'False', reason)],
    })
    const step = containerNextStep(c)!
    expect(step.text).toMatch(text)
    expect(step.command).toBe(retryCommand(c))
  })

  it('links the repository to empty on ForeignCommits', () => {
    const c = container({
      phase: 'Failed',
      repositoryURL: 'https://git.example/shop/api',
      conditions: [cond('Scaffolded', 'False', 'ForeignCommits')],
    })
    expect(containerNextStep(c)?.link).toEqual({ href: 'https://git.example/shop/api', label: 'Open the repository' })
  })

  const update = {
    ref: 'v1.3.0',
    pullRequest: 7,
    pullRequestURL: 'https://git.example/shop/api/pulls/7',
  }

  it('points at the update pull request when its checks failed', () => {
    const c = container({
      phase: 'Ready',
      standard: { name: 'go-standard', ref: 'v1.2.0', update },
      conditions: [cond('StandardUpdated', 'False', 'ChecksFailed')],
    })
    expect(containerNextStep(c)).toEqual({
      text: expect.stringMatching(/update to v1\.3\.0 waits: its checks failed/),
      link: { href: update.pullRequestURL, label: 'Open pull request #7' },
    })
  })

  it('points at the update pull request when the factory refused it', () => {
    const c = container({
      phase: 'Ready',
      standard: { name: 'go-standard', ref: 'v1.2.0', update: { ...update, stopped: 'UpdateRefused' } },
      conditions: [cond('StandardUpdated', 'False', 'UpdateRefused')],
    })
    expect(containerNextStep(c)?.text).toMatch(/stopped the Standard update to v1\.3\.0/)
  })

  it('reads a take whose checks failed, or that the factory stopped, as a take', () => {
    const take = { ...update, ref: 'v1.2.0', take: true, paths: ['Makefile'] }
    const failed = container({
      phase: 'Ready',
      standard: { name: 'go-standard', ref: 'v1.2.0', update: take },
      conditions: [cond('StandardUpdated', 'False', 'ChecksFailed')],
    })
    expect(containerNextStep(failed)).toEqual({
      text: expect.stringMatching(
        /^Taking go-standard v1\.2\.0’s version waits: its checks failed\..*leave the files behind/,
      ),
      link: { href: update.pullRequestURL, label: 'Open pull request #7' },
    })
    const stopped = container({
      phase: 'Ready',
      standard: { name: 'go-standard', ref: 'v1.2.0', update: { ...take, stopped: 'UpdateRefused' } },
      conditions: [cond('StandardUpdated', 'False', 'UpdateRefused')],
    })
    expect(containerNextStep(stopped)?.text).toMatch(/stopped taking go-standard v1\.2\.0’s version/)
  })

  it('names the files the updates left out, and the two answers', () => {
    const c = container({
      phase: 'Ready',
      repositoryURL: 'https://git.example/shop/api',
      standard: {
        name: 'go-standard',
        ref: 'v1.3.0',
        leftOut: ['Makefile', '.golangci.yml'],
        behind: [
          { path: 'Makefile', ref: 'v1.1.0', pullRequestURL: 'https://git.example/shop/api/pulls/4' },
          { path: '.golangci.yml', ref: 'v1.2.0', pullRequestURL: 'https://git.example/shop/api/pulls/6' },
        ],
      },
      conditions: [cond('StandardUpdated', 'False', 'FilesBehind')],
    })
    const next = containerNextStep(c)
    expect(next?.text).toMatch(/left 2 files out .*: Makefile \(at v1\.1\.0\), \.golangci\.yml \(at v1\.2\.0\)\./)
    expect(next?.text).toMatch(/theos\.factory\.io\/standard-keep=v1\.3\.0/)
    expect(next?.text).not.toMatch(/by hand/)
    expect(next?.command).toBe('kubectl -n shop annotate container api theos.factory.io/standard-take=v1.3.0')
    expect(standardKeepCommand(c)).toBe('kubectl -n shop annotate container api theos.factory.io/standard-keep=v1.3.0')
    expect(next?.link).toEqual({ href: 'https://git.example/shop/api/pulls/6', label: 'Open the update pull request' })
  })

  it('is nothing while an update runs, or none is in flight', () => {
    expect(
      containerNextStep(
        container({
          phase: 'Ready',
          standard: { name: 'go-standard', ref: 'v1.2.0', update },
          conditions: [cond('StandardUpdated', 'False', 'Updating')],
        }),
      ),
    ).toBe(undefined)
    expect(containerNextStep(container({ phase: 'Ready' }))).toBe(undefined)
    expect(containerNextStep(container({ phase: 'Scaffolding' }))).toBe(undefined)
  })
})

describe('the Crew and Task lifecycles', () => {
  it.each([
    ['Crew', undefined, 'Waiting:next Idle:next Working:next', 'Paused:off', undefined],
    ['Crew', 'Waiting', 'Waiting:current Idle:next Working:next', 'Paused:off', 'Waiting'],
    ['Crew', 'Idle', 'Waiting:done Idle:current Working:next', 'Paused:off', 'Idle'],
    ['Crew', 'Working', 'Waiting:done Idle:done Working:current', 'Paused:off', 'Working'],
    ['Crew', 'Paused', 'Waiting:off Idle:off Working:off', 'Paused:current', 'Paused'],
    [
      'Task',
      undefined,
      'Pending:next Implementing:next Checking:next Reviewing:next Merged:next',
      'Blocked:off Failed:off',
      undefined,
    ],
    [
      'Task',
      'Pending',
      'Pending:current Implementing:next Checking:next Reviewing:next Merged:next',
      'Blocked:off Failed:off',
      'Pending',
    ],
    [
      'Task',
      'Implementing',
      'Pending:done Implementing:current Checking:next Reviewing:next Merged:next',
      'Blocked:off Failed:off',
      'Implementing',
    ],
    [
      'Task',
      'Checking',
      'Pending:done Implementing:done Checking:current Reviewing:next Merged:next',
      'Blocked:off Failed:off',
      'Checking',
    ],
    [
      'Task',
      'Reviewing',
      'Pending:done Implementing:done Checking:done Reviewing:current Merged:next',
      'Blocked:off Failed:off',
      'Reviewing',
    ],
    [
      'Task',
      'Merged',
      'Pending:done Implementing:done Checking:done Reviewing:done Merged:current',
      'Blocked:off Failed:off',
      'Merged',
    ],
    [
      'Task',
      'Blocked',
      'Pending:off Implementing:off Checking:off Reviewing:off Merged:off',
      'Blocked:current Failed:off',
      'Blocked',
    ],
    [
      'Task',
      'Failed',
      'Pending:off Implementing:off Checking:off Reviewing:off Merged:off',
      'Blocked:off Failed:current',
      'Failed',
    ],
  ] as const)('%s in %s', (kind, phase, path, exits, current) => {
    expect(states(kind, phase)).toEqual({ path, exits, current })
  })

  it.each([
    ['Crew', undefined, /has not reported on this Crew/],
    ['Crew', 'Waiting', /Ready condition names/],
    ['Crew', 'Idle', /status\/ready/],
    ['Crew', 'Working', /its Tasks run/],
    ['Crew', 'Paused', /Resume it/],
    ['Task', undefined, /has not reported on this Task/],
    ['Task', 'Pending', /free agent slot/],
    ['Task', 'Implementing', /writes the change/],
    ['Task', 'Checking', /CI runs/],
    ['Task', 'Reviewing', /reviews the pull request/],
    ['Task', 'Merged', /follow-up issues/],
    ['Task', 'Blocked', /merge the Standard change/],
    ['Task', 'Failed', /Read why it failed/],
  ] as const)('says what a person does for a %s in %s', (kind, phase, person) => {
    expect(lifecycleOf(kind, phase).person).toMatch(person)
  })

  it('labels each step as the badges do', () => {
    expect(lifecycleOf('Crew', 'Paused').current).toMatchObject({ label: 'Paused', tone: 'waiting' })
    expect(lifecycleOf('Task', 'Pending').current).toMatchObject({ label: 'Queued', tone: 'waiting' })
    expect(lifecycleOf('Task', 'Failed').current).toMatchObject({ label: 'Failed', tone: 'error' })
  })
})

function crew(status: Crew['status'], spec: Partial<Crew['spec']> = {}): Crew {
  return {
    metadata: { name: 'api', namespace: 'shop' },
    spec: { containerRef: { name: 'api' }, implementer: { name: 'dev' }, reviewer: { name: 'rev' }, ...spec },
    status,
  }
}

function brief(name: string, paused: boolean, created: { kind: string; name: string }[]): Brief {
  return { metadata: { name, namespace: 'shop' }, spec: { idea: 'a shop', paused }, status: { created } } as Brief
}

const release = (over: Partial<NonNullable<NonNullable<Crew['status']>['lastRelease']>>) => ({
  tag: 'v1.2.0',
  commit: 'abc1234',
  time: '2026-06-01T10:00:00Z',
  ...over,
})

describe('crewNextStep', () => {
  it('re-runs a failed release run, with the annotation and a link to the run', () => {
    const c = crew({ phase: 'Idle', lastRelease: release({ run: 'Failed', runURL: 'http://gitea/runs/7' }) })
    expect(crewNextStep(c, [])).toEqual({
      text: expect.stringMatching(/^Release v1\.2\.0’s CI runs failed/),
      command: 'kubectl -n shop annotate crews.theos.factory.io api theos.factory.io/rerun-release=$(date +%s) --overwrite',
      link: { href: 'http://gitea/runs/7', label: 'Open the failed run' },
    })
    expect(rerunReleaseCommand(c)).toBe(crewNextStep(c, [])?.command)
  })

  it('says why publishing failed and which Secret holds the token', () => {
    const c = crew(
      { phase: 'Idle', lastRelease: release({ run: 'Succeeded', publish: { state: 'Failed', message: 'bad token' } }) },
      { publishReleases: { github: { repository: 'acme/api', tokenSecretRef: { name: 'gh' } } } },
    )
    expect(crewNextStep(c, [])?.text).toMatch(/^Publishing v1\.2\.0 to acme\/api failed: bad token\. .*Secret gh/)
  })

  it('says a Crew changed under its Brief publishes nothing until the two agree', () => {
    const brief = { github: { repository: 'acme/api', tokenSecretRef: { name: 'gh' } } }
    const c = crew(
      {
        phase: 'Idle',
        conditions: [{ type: 'ReleaseTarget', status: 'False', reason: 'DiffersFromBrief', message: '' }],
      },
      { publishReleases: { github: { repository: 'acme/api-v2', tokenSecretRef: { name: 'gh' } } } },
    )
    c.metadata.labels = { 'theos.factory.io/brief': 'shop' }
    c.metadata.annotations = { 'theos.factory.io/brief-publish-releases': JSON.stringify(brief) }
    expect(crewNextStep(c, [])).toEqual({
      text: expect.stringMatching(
        /differs from Brief shop’s: the Crew says acme\/api-v2, the Brief acme\/api\. Nothing is published/,
      ),
      page: { kind: 'Brief', name: 'shop', label: 'Open Brief shop' },
    })
    c.status!.conditions = [{ type: 'ReleaseTarget', status: 'True', reason: 'SetByBrief', message: '' }]
    expect(crewNextStep(c, [])).toBeUndefined()
  })

  it('says when Go can’t take the module', () => {
    const c = crew({
      phase: 'Idle',
      lastRelease: release({ run: 'Succeeded', goModule: { state: 'Invalid', message: 'lacks /v2' } }),
    })
    expect(crewNextStep(c, [])?.text).toMatch(/Go can’t take the module at v1\.2\.0: lacks \/v2/)
  })

  it('says when Go can’t fetch the module, naming path and version', () => {
    const c = crew({
      phase: 'Idle',
      lastRelease: release({
        run: 'Succeeded',
        goModule: { state: 'NotFetchable', path: 'github.com/acme/lens', version: 'v1.2.0', message: 'not found' },
      }),
    })
    expect(crewNextStep(c, [])?.text).toMatch(/^Go can’t fetch github\.com\/acme\/lens@v1\.2\.0: not found\./)
  })

  it('resumes a Crew its paused Brief made through the Brief’s page', () => {
    const briefs = [brief('other', true, []), brief('shop', true, [{ kind: 'Crew', name: 'api' }])]
    expect(crewNextStep(crew({ phase: 'Paused' }, { paused: true }), briefs)).toEqual({
      text: expect.stringMatching(/^Its Brief shop is paused/),
      page: { kind: 'Brief', name: 'shop', label: 'Open Brief shop' },
    })
  })

  it('resumes a Crew paused by itself with a patch', () => {
    const c = crew({ phase: 'Paused' }, { paused: true })
    const briefs = [brief('shop', false, [{ kind: 'Crew', name: 'api' }])]
    expect(crewNextStep(c, briefs)).toEqual({
      text: 'Resume it when it should pick up issues again:',
      command: `kubectl -n shop patch crews.theos.factory.io api --type merge -p '{"spec":{"paused":false}}'`,
    })
    expect(resumeCrewCommand(c)).toBe(crewNextStep(c, briefs)?.command)
  })

  it('names what keeps it waiting, linking to a Container that isn’t Ready', () => {
    const c = crew({
      phase: 'Waiting',
      conditions: [cond('Ready', 'False', 'ContainerNotReady', 'Waiting for Container api to be Ready')],
    })
    expect(crewNextStep(c, [])).toEqual({
      text: 'It can’t pick up issues: Waiting for Container api to be Ready.',
      page: { kind: 'Container', name: 'api', label: 'Open Container api' },
    })
  })

  it('asks nothing while it works, idles or its release goes well', () => {
    expect(crewNextStep(crew({ phase: 'Working' }), [])).toBeUndefined()
    expect(crewNextStep(crew({ phase: 'Idle', lastRelease: release({ run: 'Running' }) }), [])).toBeUndefined()
    expect(crewNextStep(crew(undefined), [])).toBeUndefined()
  })
})

function task(status: Task['status'], spec: Partial<Task['spec']> = {}): Task {
  return { metadata: { name: 'api-7', namespace: 'shop' }, spec: { agentRef: { name: 'dev' }, ...spec }, status }
}

describe('taskNextStep', () => {
  it('points a Blocked Task at the Standard change it waits for', () => {
    const t = task({
      phase: 'Blocked',
      standardChange: {
        issue: 4,
        issueURL: 'http://gitea/std/issues/4',
        paths: ['Makefile', '.github/ci.yml'],
        count: 1,
      },
    })
    expect(taskNextStep(t)).toEqual({
      text: expect.stringMatching(/^It waits for the Standard change it filed, #4, for Makefile, \.github\/ci\.yml\./),
      link: { href: 'http://gitea/std/issues/4', label: 'Open the Standard change #4' },
    })
  })

  it('sends a person to the issue a Failed Task handed back', () => {
    const t = task({ phase: 'Failed', issueURL: 'http://gitea/api/issues/7' }, { issue: 7 })
    expect(taskNextStep(t)).toEqual({
      text: expect.stringMatching(/^Its issue #7 went back to you, labelled status\/needs-human/),
      link: { href: 'http://gitea/api/issues/7', label: 'Open issue #7' },
    })
  })

  it('asks nothing inside the loop, or of a Failed Task with a goal and no issue', () => {
    for (const phase of ['Pending', 'Implementing', 'Checking', 'Reviewing', 'Merged'] as const)
      expect(taskNextStep(task({ phase }))).toBeUndefined()
    expect(taskNextStep(task({ phase: 'Failed' }, { goal: 'Add a health endpoint' }))).toBeUndefined()
  })
})

describe('the read-only kinds', () => {
  it.each([
    [
      'Relationship',
      'WaitingForScaffold',
      'WaitingForScaffold:current AppliedAtScaffold:next',
      'AddedAfterScaffold:off',
    ],
    [
      'Relationship',
      'AppliedAtScaffold',
      'WaitingForScaffold:done AppliedAtScaffold:current',
      'AddedAfterScaffold:off',
    ],
    [
      'Relationship',
      'AddedAfterScaffold',
      'WaitingForScaffold:off AppliedAtScaffold:off',
      'AddedAfterScaffold:current',
    ],
    ['Session', 'Active', 'Active:current', 'Stale:off'],
    ['Session', 'Stale', 'Active:off', 'Stale:current'],
    ['Session', undefined, 'Active:next', 'Stale:off'],
  ] as const)('%s at %s', (kind, phase, path, exits) => {
    const s = states(kind, phase)
    expect([s.path, s.exits]).toEqual([path, exits])
  })

  it('says what a person does on an AddedAfterScaffold Relationship', () => {
    expect(lifecycleOf('Relationship', 'AddedAfterScaffold').person).toMatch(/by hand.*scaffolds a Container once/)
  })

  it.each(['Project', 'SoftwareSystem', 'Standard', 'Compute', 'Deployment', 'Bundle', 'GitProvider'] as const)(
    '%s has no phases, and one thing a person does',
    (kind) => {
      expect(hasPhases(kind)).toBe(false)
      expect(hasPage(kind)).toBe(true)
      const v = lifecycleOf(kind, undefined)
      expect([v.path, v.exits, v.current]).toEqual([[], [], undefined])
      expect(v.person).not.toMatch(/has not reported/)
    },
  )

  it('reads a Relationship’s phase from its Applied reason', () => {
    const r: Relationship = {
      metadata: { name: 'api-to-db' },
      spec: { source: { kind: 'Container', name: 'api' }, target: { kind: 'DataStore', name: 'db' } },
      status: { conditions: [{ type: 'Applied', status: 'False', reason: 'AddedAfterScaffold' }] },
    }
    expect(relationshipPhase(r)).toBe('AddedAfterScaffold')
    expect(appliesConnector(r)).toBe(true)
    const uses: Relationship = {
      metadata: { name: 'customer-uses-shop' },
      spec: { source: { kind: 'Person', name: 'customer' }, target: { kind: 'SoftwareSystem', name: 'shop' } },
    }
    expect(relationshipPhase(uses)).toBeUndefined()
    expect(appliesConnector(uses)).toBe(false)
  })
})

describe('describeStandardUpdate', () => {
  const at = (update: NonNullable<NonNullable<Container['status']>['standard']>['update']) =>
    container({ phase: 'Ready', standard: { name: 'go-standard', ref: 'v1.2.0', update } })

  it('reads an update as one from ref to ref', () => {
    expect(describeStandardUpdate(at({ ref: 'v1.3.0', pullRequestURL: 'https://git.example/pulls/7' }))).toEqual({
      title: 'Standard update to v1.3.0',
      text: 'The factory merges its pull request once its checks pass.',
    })
    expect(describeStandardUpdate(at({ ref: 'v1.3.0', stopped: 'UpdateClosed' }))?.text).toBe(
      'Stopped (UpdateClosed); it waits until the Standard’s ref changes again.',
    )
  })

  it('reads a take as a take of the Standard’s version of its paths', () => {
    const take = { ref: 'v1.2.0', take: true, paths: ['Makefile', '.golangci.yml'] }
    expect(describeStandardUpdate(at({ ...take, pullRequestURL: 'https://git.example/pulls/9' }))).toEqual({
      title: 'Taking go-standard v1.2.0’s version',
      text: 'Its pull request takes go-standard v1.2.0’s version of Makefile, .golangci.yml; the factory merges it once CI passes.',
    })
    expect(describeStandardUpdate(at(take))?.text).toMatch(/^The factory is preparing the pull request that takes/)
    expect(describeStandardUpdate(at({ ...take, stopped: 'UpdateRefused' }))?.text).toMatch(/files stay behind/)
  })

  it('is nothing without an update', () => {
    expect(describeStandardUpdate(container({ phase: 'Ready' }))).toBe(undefined)
  })
})
