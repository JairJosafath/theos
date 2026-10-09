import { describe, expect, it } from 'vitest'
import type { Condition, Container, Relationship } from '../k8s/types'
import {
  appliesConnector,
  containerNextStep,
  failedCondition,
  hasPage,
  hasPhases,
  lifecycleOf,
  relationshipPhase,
  retryCommand,
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

  it.each(['Project', 'SoftwareSystem', 'Standard', 'Compute', 'Deployment', 'GitProvider'] as const)(
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
