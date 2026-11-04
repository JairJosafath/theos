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

/** A Ready Container shipping to deploy, with what its status says of each entry. */
const deploying = (
  deploy: NonNullable<Container['spec']['deploy']>,
  deploys: NonNullable<NonNullable<Container['status']>['deploys']>,
  deployed: Condition,
): Container => ({
  ...container({ phase: 'Ready', newestRelease: 'v1.3.0', deploys, conditions: [deployed] }),
  spec: { ...container(undefined).spec, deploy },
})
const deployed = (target: string): NonNullable<NonNullable<Container['status']>['deploys']>[number] => ({
  target,
  version: 'v1.3.0',
  wantedVersion: 'v1.3.0',
  phase: 'Deployed',
  health: 'Healthy',
  reason: 'Deployed',
  message: `${target} runs v1.3.0`,
  since: T,
})
const approvalMessage =
  "v1.3.0 waits for a person's approval to ship to prod: set approved to v1.3.0 in its spec.deploy entry"

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
        command: 'kubectl -n shop annotate container api theos.factory.io/retry=$(date +%s) --overwrite',
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
          take: 'kubectl -n shop annotate container api theos.factory.io/standard-take=v3',
          keep: 'kubectl -n shop annotate container api theos.factory.io/standard-keep=v3',
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
        command: 'kubectl -n shop annotate crew api theos.factory.io/rerun-release=$(date +%s) --overwrite',
        place: 'Crew',
      },
    },
    {
      name: 'a Crew whose Go module waits on a refused GitHub release (#1001)',
      input: input({
        crews: [
          crew({
            lastRelease: {
              tag: 'v0.15.1',
              commit: 'abc',
              time: T,
              run: 'Succeeded',
              publish: { state: 'Failed', message: 'Unauthorized' },
              goModule: { state: 'Waiting', path: 'github.com/acme/cel-lens/engine', version: 'v0.15.1' },
            },
            conditions: [
              cond('Released', 'True', 'Released', 'Released v0.15.1'),
              cond('Published', 'False', 'Unauthorized', 'GitHub refused the token'),
              cond(
                'ModulePublished',
                'False',
                'WaitsForRelease',
                'Waits for the tag engine/v0.15.1 on acme/cel-lens: Unauthorized, GitHub refused the token',
              ),
            ],
          }),
        ],
      }),
      want: {
        kind: 'Crew',
        reason: 'WaitsForRelease',
        message: 'Waits for the tag engine/v0.15.1 on acme/cel-lens: Unauthorized, GitHub refused the token',
        place: 'Crew',
      },
    },
    // A deploy waiting on a person (#1163, docs/plans/done/deploy.md decision 6).
    {
      name: 'a Container whose prod deploy waits for approval',
      input: input({
        containers: [
          deploying(
            [{ target: 'staging' }, { target: 'prod', after: 'staging' }],
            [
              deployed('staging'),
              {
                target: 'prod',
                wantedVersion: 'v1.3.0',
                phase: 'Waiting',
                reason: 'WaitingForApproval',
                message: approvalMessage,
                since: T,
              },
            ],
            cond('Deployed', 'False', 'WaitingForApproval', approvalMessage),
          ),
        ],
      }),
      want: {
        kind: 'Container',
        reason: 'WaitingForApproval',
        message: approvalMessage,
        command:
          'kubectl -n shop patch container api --type=json -p \'[{"op":"test","path":"/spec/deploy/1/target","value":"prod"},{"op":"test","path":"/spec/deploy/1","value":{"target":"prod","after":"staging"}},{"op":"add","path":"/spec/deploy/1/approved","value":"v1.3.0"}]\'',
        place: 'Container',
      },
    },
    {
      name: 'a Container whose deploy failed',
      input: input({
        containers: [
          deploying(
            [{ target: 'oss' }],
            [
              {
                target: 'oss',
                wantedVersion: 'v1.3.0',
                phase: 'Failed',
                reason: 'TagTaken',
                message: 'Shipping v1.3.0 to oss failed: a release v1.3.0 the factory didn’t make is there',
                url: 'https://github.com/acme/api/releases/tag/v1.3.0',
                since: T,
              },
            ],
            cond('Deployed', 'False', 'DeployFailed', 'Shipping v1.3.0 to oss failed'),
          ),
        ],
      }),
      want: {
        kind: 'Container',
        reason: 'TagTaken',
        message: 'Shipping v1.3.0 to oss failed: a release v1.3.0 the factory didn’t make is there',
        links: ['https://github.com/acme/api/releases/tag/v1.3.0'],
        place: 'Container',
      },
    },
    {
      name: 'a Container whose Deployed waits for approval with no entry saying which',
      input: input({
        containers: [
          deploying([{ target: 'prod' }], [], cond('Deployed', 'False', 'WaitingForApproval', approvalMessage)),
        ],
      }),
      want: { kind: 'Container', reason: 'WaitingForApproval', message: approvalMessage, place: 'Container' },
    },
    {
      name: 'a Container running degraded at a target',
      input: input({
        containers: [
          deploying(
            [{ target: 'prod' }],
            [{ ...deployed('prod'), health: 'Degraded', message: 'prod runs v1.3.0' }],
            cond('Deployed', 'True', 'Deployed', 'Every target runs its version'),
          ),
        ],
      }),
      want: { kind: 'Container', reason: 'Degraded', message: 'prod runs v1.3.0', place: 'Container' },
    },
    ...(['InvalidModule', 'NotFetchable'] as const).map((reason) => ({
      name: `a Crew whose Go module is ${reason}`,
      input: input({
        crews: [
          crew({
            lastRelease: {
              tag: 'v0.2.0',
              commit: 'abc',
              time: T,
              publish: {
                state: 'Published' as const,
                source: { commit: 'abc', tag: 'engine/v0.2.0', url: 'https://github.com/acme/lens/tree/engine/v0.2.0' },
              },
            },
            conditions: [
              cond('Published', 'True', 'Published', 'Published v0.2.0'),
              cond('ModulePublished', 'False', reason, 'The Go proxy says: not found'),
            ],
          }),
        ],
      }),
      want: {
        kind: 'Crew',
        reason,
        message: 'The Go proxy says: not found',
        links: ['https://github.com/acme/lens/tree/engine/v0.2.0'],
        place: 'Crew',
      },
    })),
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
        crews: [
          crew({ conditions: [cond('Released', 'False', 'WaitsForEarlierRelease', 'v2 waits for v1')] }),
          // The source tag is on its way: GitHub took the release, or the proxy is down for now.
          crew({
            conditions: [
              cond('Published', 'True', 'Published', 'Published v0.2.0'),
              cond('ModulePublished', 'False', 'WaitsForRelease', 'Waits for engine/v0.2.0'),
            ],
          }),
          crew({ conditions: [cond('ModulePublished', 'False', 'ProxyUnavailable', 'proxy.golang.org: timeout')] }),
        ],
      }),
    )
    expect(items).toEqual([])
  })

  it('lists no deploy the factory still works on or holds for a reason of its own (#1163)', () => {
    for (const reason of ['WaitingForPrevious', 'Deploying', 'NoRelease', 'TargetNotReady', 'Incompatible']) {
      const c = deploying(
        [{ target: 'prod' }],
        [{ target: 'prod', wantedVersion: 'v1.3.0', phase: 'Waiting', reason, message: 'held', since: T }],
        cond('Deployed', 'False', reason, 'held'),
      )
      expect(needs(input({ containers: [c] })), reason).toEqual([])
    }
  })

  it('lists an approval waiting while Deployed names another target’s reason first (#1163)', () => {
    // Deployed gives the first reason in its order: staging unreachable hides prod's approval there.
    const c = deploying(
      [{ target: 'staging' }, { target: 'prod', version: 'v1.3.0', approved: 'v1.2.0' }],
      [
        { target: 'staging', phase: 'Waiting', reason: 'Unreachable', message: 'unreachable', since: T },
        { target: 'prod', wantedVersion: 'v1.3.0', phase: 'Waiting', reason: 'WaitingForApproval', since: T },
        { target: 'oss', wantedVersion: 'v1.3.0', phase: 'Failed', reason: 'ArtifactMissing', since: T },
      ],
      cond('Deployed', 'False', 'TargetNotReady', 'staging unreachable'),
    )
    const items = needs(input({ containers: [c] }))
    expect(items.map((n) => n.title)).toEqual([
      'waits for your approval of v1.3.0 at prod',
      'failed to deploy v1.3.0 to oss',
    ])
    // The approval it replaces is tested, so a copied line can't take back a newer one.
    expect(items[0].command).toContain('{"op":"test","path":"/spec/deploy/1/approved","value":"v1.2.0"}')
  })

  it('lists each target waiting for approval, and a degraded one besides (#1163)', () => {
    const waiting = (target: string) => ({
      target,
      wantedVersion: 'v1.3.0',
      phase: 'Waiting' as const,
      reason: 'WaitingForApproval',
      message: 'approve it',
      since: T,
    })
    const c = deploying(
      [{ target: 'eu' }, { target: 'us' }, { target: 'staging' }],
      [waiting('eu'), waiting('us'), { ...deployed('staging'), health: 'Degraded' }],
      cond('Deployed', 'False', 'WaitingForApproval', 'eu waits'),
    )
    expect(
      needs(input({ containers: [c] })).map((n) => [n.title, n.command?.match(/deploy\/(\d)\/approved/)?.[1]]),
    ).toEqual([
      ['waits for your approval of v1.3.0 at eu', '0'],
      ['waits for your approval of v1.3.0 at us', '1'],
      ['runs v1.3.0 degraded at staging', undefined],
    ])
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
