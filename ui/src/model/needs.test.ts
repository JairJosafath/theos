import { describe, expect, it } from 'vitest'
import type { Brief, Condition, Container, Crew, Project, StandardFile, Task } from '../k8s/types'
import { byProject, needs, type NeedsInput } from './needs'

const T = '2026-06-01T10:00:00Z'
const cond = (type: string, status: Condition['status'], reason: string, message: string): Condition => ({
  type,
  status,
  reason,
  message,
  lastTransitionTime: T,
})

const brief = (conditions: Condition[], ns = 'shop'): Brief => ({
  metadata: { name: 'idea', namespace: ns },
  spec: { idea: 'A shop' },
  status: { conditions },
})

const container = (status: Container['status']): Container => ({
  metadata: { name: 'api', namespace: 'shop' },
  spec: {
    softwareSystemRef: { name: 'store' },
    type: 'service',
    gitProviderRef: { name: 'gitea' },
    frameworkRef: { name: 'go' },
  },
  status,
})

const task = (name: string, status: Task['status'], issue = 7, created = T): Task => ({
  metadata: { name, namespace: 'shop', creationTimestamp: created },
  spec: { containerRef: { name: 'api' }, agentRef: { name: 'claude' }, issue },
  status,
})

const crew = (status: Crew['status']): Crew => ({
  metadata: { name: 'api', namespace: 'shop' },
  spec: { containerRef: { name: 'api' }, implementer: { name: 'claude' }, reviewer: { name: 'claude' } },
  status,
})

const input = (part: Partial<NeedsInput>): NeedsInput => ({
  projects: [],
  briefs: [],
  containers: [],
  crews: [],
  tasks: [],
  ...part,
})

const repo = { repositoryURL: 'https://git.example/shop/api' }
const makefileBehind: StandardFile = {
  path: 'Makefile',
  ref: 'v1',
  pullRequestURL: 'https://git.example/shop/api/pulls/3',
  change: '--- Makefile (v1)\n+++ Makefile (v3)\n@@ -1 +1 @@\n-lint:\n+lint: fmt\n',
}

describe('needs', () => {
  const cases: {
    name: string
    input: NeedsInput
    want: {
      kind: string
      reason: string
      message: string
      links?: string[]
      command?: string
      standardAnswer?: { ref: string; take: string; keep: string; files: StandardFile[] }
      place: string
    }
  }[] = [
    {
      name: 'a Brief waiting for input',
      input: input({ briefs: [brief([cond('NeedsInput', 'True', 'PlannerNeedsInput', 'Which payment provider?')])] }),
      want: { kind: 'Brief', reason: 'PlannerNeedsInput', message: 'Which payment provider?', place: 'Brief' },
    },
    {
      name: 'a Brief with something only the person can decide',
      input: input({ briefs: [brief([cond('NeedsYou', 'True', 'Waiting', 'Retry api#7? and 2 more')])] }),
      want: { kind: 'Brief', reason: 'Waiting', message: 'Retry api#7? and 2 more', place: 'Brief' },
    },
    {
      name: 'a Failed Container',
      input: input({
        containers: [
          container({
            phase: 'Failed',
            ...repo,
            conditions: [
              cond('ReferencesResolved', 'True', 'Resolved', ''),
              cond('Scaffolded', 'False', 'Scaffolding', 'Creating the repository and rendering the templates'),
              cond(
                'RepositoryCreated',
                'False',
                'ForeignCommits',
                'The repository has commits the factory did not make',
              ),
            ],
          }),
        ],
      }),
      want: {
        kind: 'Container',
        reason: 'ForeignCommits',
        message: 'The repository has commits the factory did not make',
        links: [repo.repositoryURL],
        command: 'kubectl -n shop annotate container api c4.factory.io/retry=$(date +%s) --overwrite',
        place: 'Container',
      },
    },
    {
      name: 'a Container whose Standard update waits on a person',
      input: input({
        containers: [
          container({
            phase: 'Ready',
            standard: {
              name: 'go-standard',
              ref: 'v1',
              update: { ref: 'v2', pullRequest: 3, pullRequestURL: 'https://git.example/shop/api/pulls/3' },
            },
            conditions: [cond('StandardUpdated', 'False', 'ChecksFailed', 'CI failed on the update to v2')],
          }),
        ],
      }),
      want: {
        kind: 'Container',
        reason: 'ChecksFailed',
        message: 'CI failed on the update to v2',
        links: ['https://git.example/shop/api/pulls/3'],
        place: 'Container',
      },
    },
    {
      name: 'a Container with Standard files left out of an update',
      input: input({
        containers: [
          container({
            phase: 'Ready',
            standard: {
              name: 'go-standard',
              ref: 'v3',
              leftOut: ['Makefile', 'check.yaml'],
              behind: [
                makefileBehind,
                { path: 'check.yaml', ref: 'v2', pullRequestURL: 'https://git.example/shop/api/pulls/3' },
              ],
            },
            conditions: [
              cond('StandardUpdated', 'False', 'FilesBehind', 'go-standard is at v3, but its updates left out'),
            ],
          }),
        ],
      }),
      want: {
        kind: 'Container',
        reason: 'FilesBehind',
        message: 'go-standard is at v3, but its updates left out',
        links: ['https://git.example/shop/api/pulls/3'],
        standardAnswer: {
          ref: 'v3',
          take: 'kubectl -n shop annotate container api c4.factory.io/standard-take=v3',
          keep: 'kubectl -n shop annotate container api c4.factory.io/standard-keep=v3',
          files: [
            makefileBehind,
            { path: 'check.yaml', ref: 'v2', pullRequestURL: 'https://git.example/shop/api/pulls/3' },
          ],
        },
        place: 'Container',
      },
    },
    {
      name: 'a Blocked Task',
      input: input({
        tasks: [
          task('api-7', {
            phase: 'Blocked',
            standardChange: {
              issue: 12,
              issueURL: 'https://git.example/shop/api/issues/12',
              paths: ['.golangci.yml'],
              count: 1,
            },
            conditions: [
              cond('Progressing', 'False', 'WaitingForStandardChange', 'Waits for #12 to change .golangci.yml'),
            ],
          }),
        ],
      }),
      want: {
        kind: 'Task',
        reason: 'WaitingForStandardChange',
        message: 'Waits for #12 to change .golangci.yml',
        links: ['https://git.example/shop/api/issues/12'],
        place: 'Task',
      },
    },
    {
      name: 'a Failed Task',
      input: input({
        tasks: [
          task('api-7', {
            phase: 'Failed',
            completedAt: T,
            issueURL: 'https://git.example/shop/api/issues/7',
            pullRequest: 9,
            pullRequestURL: 'https://git.example/shop/api/pulls/9',
            conditions: [cond('Succeeded', 'False', 'ReviewRejected', 'The reviewer rejected it 3 times')],
          }),
        ],
      }),
      want: {
        kind: 'Task',
        reason: 'ReviewRejected',
        message: 'The reviewer rejected it 3 times',
        links: ['https://git.example/shop/api/issues/7', 'https://git.example/shop/api/pulls/9'],
        place: 'Task',
      },
    },
    {
      name: 'a Crew whose release is left to a person',
      input: input({
        containers: [container({ phase: 'Ready', ...repo })],
        crews: [
          crew({
            conditions: [
              cond('Ready', 'True', 'Ready', ''),
              cond(
                'Released',
                'False',
                'NoMergedIssue',
                'v1.2.0 is done but none of its issues was merged by a Task; a person tags it',
              ),
            ],
          }),
        ],
      }),
      want: {
        kind: 'Crew',
        reason: 'NoMergedIssue',
        message: 'v1.2.0 is done but none of its issues was merged by a Task; a person tags it',
        links: ['https://git.example/shop/api/milestones'],
        place: 'Crew',
      },
    },
    {
      name: 'a Crew whose last release run failed',
      input: input({
        crews: [
          crew({
            lastRelease: {
              tag: 'v1.1.0',
              commit: 'abcdef0123456789',
              time: T,
              run: 'Failed',
              runURL: 'https://git.example/run/4',
            },
            conditions: [cond('Released', 'True', 'Released', 'Released v1.1.0')],
          }),
        ],
      }),
      want: {
        kind: 'Crew',
        reason: 'ReleaseRunFailed',
        message: 'status.lastRelease.run is Failed for v1.1.0 at abcdef012345',
        links: ['https://git.example/run/4'],
        command: 'kubectl -n shop annotate crew api c4.factory.io/rerun-release=$(date +%s) --overwrite',
        place: 'Crew',
      },
    },
  ]

  it.each(cases)('lists $name, with why and where', ({ input, want }) => {
    const items = needs(input)
    expect(items).toHaveLength(1)
    const [item] = items
    expect(item.kind).toBe(want.kind)
    expect(item.namespace).toBe('shop')
    expect(item.reason).toBe(want.reason)
    expect(item.message).toBe(want.message)
    expect(item.links.map((l) => l.url)).toEqual(want.links ?? [])
    expect(item.command).toBe(want.command)
    expect(item.standardAnswer).toEqual(want.standardAnswer)
    // An object page where the kind has one, else an area.
    const to = item.place.to
    expect('page' in to ? to.page.kind : to.area).toBe(want.place)
    if ('page' in to) expect(to.page).toEqual({ ns: 'shop', kind: want.kind, name: item.name })
  })

  it('gives every item somewhere to go: a place, and a link or a command where one exists', () => {
    for (const { input } of cases)
      for (const item of needs(input)) {
        expect(item.place.label).not.toBe('')
        expect(item.title).not.toBe('')
      }
  })

  it('lists nothing for healthy objects', () => {
    expect(
      needs(
        input({
          briefs: [
            brief([
              cond('Planned', 'True', 'Applied', 'Round 2 applied'),
              cond('NeedsInput', 'False', 'Planned', ''),
              cond('NeedsYou', 'False', 'Nothing', ''),
            ]),
          ],
          containers: [
            container({
              phase: 'Ready',
              ...repo,
              conditions: [
                cond('Scaffolded', 'True', 'Created', ''),
                cond('StandardUpdated', 'True', 'UpToDate', 'At v2'),
              ],
            }),
          ],
          tasks: [task('api-7', { phase: 'Merged' }), task('api-8', { phase: 'Implementing' }, 8)],
          crews: [
            crew({
              lastRelease: { tag: 'v1.1.0', commit: 'abc', time: T, run: 'Succeeded' },
              conditions: [cond('Released', 'True', 'Released', 'Released v1.1.0')],
            }),
          ],
        }),
      ),
    ).toEqual([])
  })

  it('lists no release or Standard update the factory still works on', () => {
    const items = needs(
      input({
        containers: [
          container({ phase: 'Ready', conditions: [cond('StandardUpdated', 'False', 'Updating', 'Waiting for CI')] }),
        ],
        crews: [crew({ conditions: [cond('Released', 'False', 'WaitsForEarlierRelease', 'v2 waits for v1')] })],
      }),
    )
    expect(items).toEqual([])
  })

  it('drops a Failed Task once a newer Task works its issue', () => {
    const failed = task('api-7', { phase: 'Failed', completedAt: T })
    const retry = task('api-7-2', { phase: 'Implementing' }, 7, '2026-06-01T11:00:00Z')
    expect(needs(input({ tasks: [failed, retry] }))).toEqual([])
  })

  it('groups items by project, with its display name', () => {
    const items = needs(
      input({
        briefs: [
          brief([cond('NeedsInput', 'True', 'PlannerNeedsInput', 'Which?')], 'zoo'),
          brief([cond('NeedsInput', 'True', 'PlannerNeedsInput', 'Which?')], 'shop'),
        ],
      }),
    )
    const projects: Project[] = [{ metadata: { name: 'shop', namespace: 'shop' }, spec: { displayName: 'The shop' } }]
    expect(byProject(items, projects).map((g) => [g.namespace, g.title, g.items.length])).toEqual([
      ['shop', 'The shop', 1],
      ['zoo', 'zoo', 1],
    ])
  })
})
