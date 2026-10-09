import { describe, expect, it } from 'vitest'
import type { Brief, Condition, Container, Crew, Task } from '../k8s/types'
import {
  containerNextStep,
  crewNextStep,
  failedCondition,
  lifecycleOf,
  rerunReleaseCommand,
  resumeCrewCommand,
  retryCommand,
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
      'kubectl -n shop annotate containers.c4.factory.io api c4.factory.io/retry=$(date +%s) --overwrite',
    )
  })
})

describe('containerNextStep', () => {
  it.each([
    ['ForeignCommits', 'Scaffolded', /Empty it, then retry/],
    ['LayerConflict', 'Scaffolded', /Two templates write the same file/],
    ['UnpinnedRef', 'Scaffolded', /Pin the template’s ref/],
    ['UnsupportedType', 'ChoicesValid', /support this Container’s type/],
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

  it('names the files the last update left out', () => {
    const c = container({
      phase: 'Ready',
      repositoryURL: 'https://git.example/shop/api',
      standard: { name: 'go-standard', ref: 'v1.3.0', leftOut: ['Makefile', '.golangci.yml'] },
      conditions: [cond('StandardUpdated', 'True', 'UpToDate')],
    })
    expect(containerNextStep(c)?.text).toMatch(/left 2 files out .*: Makefile, \.golangci\.yml/)
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
      command: 'kubectl -n shop annotate crews.c4.factory.io api c4.factory.io/rerun-release=$(date +%s) --overwrite',
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

  it('says when Go can’t take the module', () => {
    const c = crew({
      phase: 'Idle',
      lastRelease: release({ run: 'Succeeded', goModule: { state: 'Invalid', message: 'lacks /v2' } }),
    })
    expect(crewNextStep(c, [])?.text).toMatch(/Go can’t take the module at v1\.2\.0: lacks \/v2/)
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
      command: `kubectl -n shop patch crews.c4.factory.io api --type merge -p '{"spec":{"paused":false}}'`,
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
