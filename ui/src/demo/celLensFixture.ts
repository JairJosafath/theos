// CEL Lens, the issue-driven story: two Containers (a command-line tool and an
// editor extension), a Crew on each with a Sonnet implementer and an Opus
// reviewer, and Tasks made from issues: merged ones with follow-ups, a failed
// one tried again, one waiting for its agent after a usage limit, and one in CI
// on a Crew that has been paused. Times are relative to `now`, and the project
// is younger than the webshop, so demo mode still opens on the webshop.

import type { StoryData } from '../k8s/sources'
import type { Agent, CatalogEntry, Condition, Container, Crew, Task, TaskPhase, TaskTransition } from '../k8s/types'

export const CEL_LENS_NAMESPACE = 'cel-lens'

const API_VERSION = 'c4.factory.io/v1alpha1'
const GITEA = 'http://gitea.gitea.svc.cluster.local:3000'
/** Where the operator's issue and pull request links point: Gitea's own address, port-forwarded. */
const GITEA_WEB = 'http://localhost:3000'
const MIN = 60_000

function meta(name: string, created: string, labels?: Record<string, string>) {
  return {
    name,
    namespace: CEL_LENS_NAMESPACE,
    uid: `demo-${CEL_LENS_NAMESPACE}-${name}`,
    resourceVersion: '1',
    creationTimestamp: created,
    ...(labels ? { labels } : {}),
  }
}

function cond(type: string, status: Condition['status'], reason: string, message: string, at: string): Condition {
  return { type, status, reason, message, lastTransitionTime: at }
}

export function demoCelLens(now: number): StoryData {
  const at = (minutesAgo: number) => new Date(now - minutesAgo * MIN).toISOString()
  const created = at(150)

  const container = (
    name: string,
    displayName: string,
    description: string,
    type: Container['spec']['type'],
    framework: string,
    standard: string,
  ): Container => ({
    apiVersion: API_VERSION,
    kind: 'Container',
    metadata: meta(name, created),
    spec: {
      softwareSystemRef: { name: 'cel-lens' },
      displayName,
      description,
      type,
      gitProviderRef: { name: 'gitea' },
      frameworkRef: { name: framework },
      standardRef: { name: standard },
      deploymentRef: { name: 'gitea-release' },
    },
    status: {
      phase: 'Ready',
      repositoryURL: `${GITEA}/cel-lens/${name}`,
      scaffoldedFrom: [`${framework}@v1.0.0`, `${standard}@v1.0.0`, 'gitea-release@v1.0.0'],
      activeSessions: 0,
      conditions: [
        cond('ReferencesResolved', 'True', 'Resolved', 'All references exist', at(149)),
        cond('ChoicesValid', 'True', 'Valid', `Every catalog entry supports ${type}`, at(149)),
        cond('RepositoryCreated', 'True', 'Created', `Created ${GITEA}/cel-lens/${name}`, at(149)),
        cond('Scaffolded', 'True', 'Scaffolded', 'Pushed the initial commit', at(148)),
      ],
    },
  })

  const containers = [
    container(
      'cel-language-server',
      'CEL language server',
      'Finds CEL in Kubernetes YAML and checks, completes and explains it: over LSP for editors, and as a CLI for CI.',
      'cli',
      'go-cli',
      'go-standard',
    ),
    container(
      'vscode-extension',
      'CEL Lens for VS Code',
      'Starts the CEL language server for YAML files, highlights CEL inside YAML, and shows the server’s state.',
      'extension',
      'vscode-extension',
      'ts-standard',
    ),
  ]

  const agent = (name: string, spec: Agent['spec']): Agent => ({
    apiVersion: API_VERSION,
    kind: 'Agent',
    metadata: meta(name, created),
    spec,
  })
  const agents: Agent[] = [
    agent('claude-implementer', {
      displayName: 'Claude Code, Sonnet (implementer)',
      description: 'Implements one issue at a time, test first, until make check passes.',
      image: 'c4factory/agent-go:dev',
      harness: 'claude-code',
      model: 'claude-sonnet-5-5',
      maxRounds: 3,
      timeoutMinutes: 90,
    }),
    agent('claude-reviewer', {
      displayName: 'Claude Code, Opus (reviewer)',
      description: 'Reviews each round against the issue’s acceptance criteria and the Standard’s checklist.',
      image: 'c4factory/agent-go:dev',
      harness: 'claude-code',
      model: 'claude-opus-5-5',
      maxRounds: 3,
      timeoutMinutes: 45,
    }),
    agent('stand-in', {
      displayName: 'Stand-in agent',
      description: 'Adds a line to STAND-IN.md; asks for changes once, then approves.',
      image: 'c4factory/agent-go:dev',
      harness: 'stand-in',
      maxRounds: 2,
      timeoutMinutes: 15,
    }),
  ]

  const step = (minutesAgo: number, phase: TaskPhase, round: number, reason: string, message = ''): TaskTransition => ({
    at: at(minutesAgo),
    phase,
    round,
    reason,
    message,
  })
  const issueURL = (repo: string, n: number) => `${GITEA_WEB}/cel-lens/${repo}/issues/${n}`
  const pr = (repo: string, n: number) => ({
    pullRequest: n,
    pullRequestURL: `${GITEA_WEB}/cel-lens/${repo}/pulls/${n}`,
  })
  const merged = (minutesAgo: number) => ({
    phase: 'Merged' as const,
    completedAt: at(minutesAgo),
    checks: { state: 'success' },
    review: { verdict: 'approve', summary: 'Every acceptance criterion is met.' },
    conditions: [
      cond('Progressing', 'False', 'Merged', 'Approved; merging', at(minutesAgo)),
      cond('Succeeded', 'True', 'Merged', 'Approved; merging', at(minutesAgo)),
    ],
  })
  /** A Task a Crew started from an issue: named <container>-<issue>, with the attempt from the second one on. */
  const task = (
    repo: string,
    issue: number,
    title: string,
    createdMinutesAgo: number,
    status: Task['status'],
    attempt = 1,
  ): Task => {
    const name = `${repo}-${issue}${attempt > 1 ? `-${attempt}` : ''}`
    return {
      apiVersion: API_VERSION,
      kind: 'Task',
      metadata: meta(name, at(createdMinutesAgo), { 'c4.factory.io/crew': repo, 'c4.factory.io/issue': `${issue}` }),
      spec: {
        containerRef: { name: repo },
        agentRef: { name: 'claude-implementer' },
        reviewerRef: { name: 'claude-reviewer' },
        issue,
      },
      status: { branch: `task/${name}`, issueTitle: title, issueURL: issueURL(repo, issue), ...status },
    }
  }

  const server = 'cel-language-server'
  const extension = 'vscode-extension'
  const back = now + 38 * MIN
  const unavailable = `The agent is unavailable (usage limit reached); round 2 waits for it; trying again at ${new Date(back).toISOString().replace(/\.\d+Z$/, 'Z')}`

  const tasks: Task[] = [
    task(server, 1, 'Find the CEL expressions in Kubernetes YAML', 140, {
      ...merged(118),
      round: 1,
      summary: 'A table of CEL fields per kind, and a walker that finds them with their YAML node.',
      ...pr(server, 1),
      history: [
        step(140, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(139, 'Implementing', 1, 'Waiting', 'The agent is working on round 1'),
        step(126, 'Checking', 1, 'Checking', 'The agent pushed 3e1f0a2; waiting for checks'),
        step(123, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(118, 'Merged', 1, 'Merged', 'Approved; merging'),
      ],
    }),
    task(server, 2, 'Map positions inside YAML scalars', 117, {
      ...merged(85),
      round: 2,
      summary: 'Offsets map through block, folded and quoted scalars; escapes are covered by table tests.',
      followUps: [issueURL(server, 15), issueURL(server, 16)],
      ...pr(server, 2),
      history: [
        step(117, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(104, 'Checking', 1, 'Checking', 'The agent pushed 7b2c9d4; waiting for checks'),
        step(101, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(97, 'Implementing', 2, 'NextRound', 'Round 1 was not accepted (folded scalars); starting round 2'),
        step(91, 'Checking', 2, 'Checking', 'The agent pushed a04e6f1; waiting for checks'),
        step(89, 'Reviewing', 2, 'Running', 'Checks passed; starting the review'),
        step(85, 'Merged', 2, 'Merged', 'Approved; merging'),
      ],
    }),
    task(server, 3, 'Embed Kubernetes’ OpenAPI schemas and resolve kinds offline', 84, {
      phase: 'Failed',
      round: 3,
      completedAt: at(52),
      checks: { state: 'failure' },
      review: { verdict: 'changes', summary: 'The embedded schemas add 40 MB; embed only the groups policies use.' },
      feedback: 'CI failed: the binary is over the 60 MB limit in make check.',
      ...pr(server, 3),
      conditions: [
        cond('Progressing', 'False', 'RoundsExhausted', 'Failed after 3 rounds', at(52)),
        cond(
          'Succeeded',
          'False',
          'RoundsExhausted',
          'Failed after 3 rounds: the binary is over its size limit',
          at(52),
        ),
      ],
      history: [
        step(84, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(74, 'Checking', 1, 'Checking', 'Waiting for checks'),
        step(71, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(68, 'Implementing', 2, 'NextRound', 'Round 1 was not accepted; starting round 2'),
        step(61, 'Checking', 2, 'Checking', 'Waiting for checks'),
        step(58, 'Implementing', 3, 'NextRound', 'Round 2 was not accepted (CI failed); starting round 3'),
        step(54, 'Checking', 3, 'Checking', 'Waiting for checks'),
        step(52, 'Failed', 3, 'RoundsExhausted', 'Failed after 3 rounds'),
      ],
    }),
    task(
      server,
      3,
      'Embed Kubernetes’ OpenAPI schemas and resolve kinds offline',
      45,
      {
        ...merged(30),
        round: 1,
        summary: 'Embeds the schemas of the groups policies match, compressed: 6 MB.',
        followUps: [issueURL(server, 17)],
        ...pr(server, 4),
        history: [
          step(45, 'Implementing', 1, 'Running', 'Starting round 1'),
          step(36, 'Checking', 1, 'Checking', 'The agent pushed 0c9d3be; waiting for checks'),
          step(33, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
          step(30, 'Merged', 1, 'Merged', 'Approved; merging'),
        ],
      },
      2,
    ),
    task(server, 4, 'Check ValidatingAdmissionPolicy expressions like the API server', 29, {
      phase: 'Implementing',
      round: 2,
      checks: { state: 'success' },
      review: { verdict: 'changes', summary: 'messageExpression is not checked; criterion 3 is not met.' },
      feedback: 'The reviewer asked for changes:\nmessageExpression is not checked; criterion 3 is not met.',
      agentBackAt: new Date(back).toISOString(),
      ...pr(server, 5),
      conditions: [cond('Progressing', 'True', 'AgentUnavailable', unavailable, at(6))],
      history: [
        step(29, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(17, 'Checking', 1, 'Checking', 'The agent pushed 5d8e2a7; waiting for checks'),
        step(14, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(10, 'Implementing', 2, 'NextRound', 'Round 1 was not accepted (messageExpression); starting round 2'),
        step(6, 'Implementing', 2, 'AgentUnavailable', unavailable),
      ],
    }),
    task(server, 5, 'Report diagnostics with the position of the failing expression', 12, {
      phase: 'Reviewing',
      round: 1,
      checks: { state: 'success' },
      commit: '2b7c4d9',
      summary: 'Publishes a diagnostic per failing CEL expression, positioned on its YAML node.',
      ...pr(server, 6),
      conditions: [
        cond('Progressing', 'True', 'Reviewing', 'Checks passed; the reviewer is reading the change', at(2)),
      ],
      history: [
        step(12, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(5, 'Checking', 1, 'Checking', 'The agent pushed 2b7c4d9; waiting for checks'),
        step(2, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
      ],
    }),
    task(extension, 1, 'Start the language server for YAML files', 120, {
      ...merged(95),
      round: 1,
      summary: 'Starts cel-language-server serve --stdio for YAML documents, with a test for a missing binary.',
      ...pr(extension, 1),
      history: [
        step(120, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(104, 'Checking', 1, 'Checking', 'Waiting for checks'),
        step(100, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(95, 'Merged', 1, 'Merged', 'Approved; merging'),
      ],
    }),
    task(extension, 2, 'Highlight CEL inside YAML', 25, {
      phase: 'Checking',
      round: 1,
      checks: { state: 'pending' },
      commit: '9f41c2e',
      summary: 'An injection grammar for the CEL fields of known kinds, with grammar snapshot tests.',
      ...pr(extension, 2),
      conditions: [cond('Progressing', 'True', 'Checking', 'The agent pushed 9f41c2e; waiting for checks', at(8))],
      history: [
        step(25, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(24, 'Implementing', 1, 'Waiting', 'The agent is working on round 1'),
        step(8, 'Checking', 1, 'Checking', 'The agent pushed 9f41c2e; waiting for checks'),
      ],
    }),
  ]

  const crew = (
    name: string,
    displayName: string,
    description: string,
    paused: boolean,
    status: Crew['status'],
    parallel = 1,
  ): Crew => ({
    apiVersion: API_VERSION,
    kind: 'Crew',
    metadata: meta(name, created),
    spec: {
      containerRef: { name },
      displayName,
      description,
      implementer: { name: 'claude-implementer' },
      reviewer: { name: 'claude-reviewer' },
      ...(paused ? { paused } : {}),
      ...(parallel > 1 ? { parallel } : {}),
    },
    status: { lastPolled: at(0.5), ...status },
  })
  const crews: Crew[] = [
    crew(
      'cel-language-server',
      'Language server crew',
      'Builds the server issue by issue.',
      false,
      {
        phase: 'Working',
        activeTasks: ['cel-language-server-4', 'cel-language-server-5'],
        ready: [],
        waiting: [6, 7, 8, 9, 10, 11],
        conditions: [
          cond(
            'Ready',
            'True',
            'Working',
            'Tasks cel-language-server-4 and cel-language-server-5 are working on issues #4 and #5',
            at(29),
          ),
        ],
      },
      2,
    ),
    // Paused by a person who wants to see the highlighting first: the Task in CI runs to its end.
    crew('vscode-extension', 'VS Code extension crew', 'Builds the extension issue by issue.', true, {
      phase: 'Working',
      activeTasks: ['vscode-extension-2'],
      ready: [3],
      waiting: [4, 5],
      conditions: [cond('Ready', 'True', 'Working', 'Task vscode-extension-2 is working on issue #2', at(25))],
    }),
  ]

  const catalog = (kind: string, name: string, displayName: string, types: string[]): CatalogEntry => ({
    apiVersion: API_VERSION,
    kind,
    metadata: meta(name, created),
    spec: {
      displayName,
      supportedTypes: types as CatalogEntry['spec']['supportedTypes'],
      template: { url: `${GITEA}/templates/${name}`, ref: 'v1.0.0' },
    },
  })

  return {
    namespace: CEL_LENS_NAMESPACE,
    project: {
      apiVersion: API_VERSION,
      kind: 'Project',
      metadata: meta('cel-lens', created),
      spec: {
        displayName: 'CEL Lens',
        description: 'Rich language support for CEL inside Kubernetes YAML, built by agents from a backlog of issues.',
      },
      status: {
        softwareSystems: 1,
        containers: 2,
        readyContainers: 2,
        activeSessions: 0,
        summary: '2 of 2 containers Ready, 0 active sessions',
      },
    },
    people: [
      {
        kind: 'Person',
        metadata: meta('policy-author', created),
        spec: {
          displayName: 'Policy author',
          description: 'A platform engineer who writes admission policies and CRD validation rules in YAML.',
        },
      },
    ],
    systems: [
      {
        kind: 'SoftwareSystem',
        metadata: meta('cel-lens', created),
        spec: {
          displayName: 'CEL Lens',
          description: 'Rich language support for CEL inside Kubernetes YAML.',
          needs: [
            'Finds CEL in Kubernetes YAML by itself; known kinds need no markers',
            'Reports the errors the API server would report, at the right line and column',
            'Works offline, without a cluster',
          ],
          kpis: ['CEL errors caught before kubectl apply', 'Time to a correct expression'],
        },
        status: { containers: 2, readyContainers: 2, dataStores: 0, summary: '2 of 2 containers Ready' },
      },
    ],
    containers,
    dataStores: [],
    relationships: [
      {
        kind: 'Relationship',
        metadata: meta('policy-author-uses-cel-lens', created),
        spec: {
          source: { kind: 'Person', name: 'policy-author' },
          target: { kind: 'SoftwareSystem', name: 'cel-lens' },
          description: 'Writes and checks CEL in Kubernetes YAML with',
        },
      },
      {
        kind: 'Relationship',
        metadata: meta('vscode-extension-to-cel-language-server', created),
        spec: {
          source: { kind: 'Container', name: 'vscode-extension' },
          target: { kind: 'Container', name: 'cel-language-server' },
          description: 'Starts cel-language-server serve --stdio and speaks LSP 3.17 with it',
        },
        status: { conditions: [cond('ReferencesResolved', 'True', 'Resolved', 'Both ends exist', created)] },
      },
    ],
    sessions: [],
    frameworks: [
      catalog('Framework', 'go-cli', 'Go command-line program', ['cli']),
      catalog('Framework', 'vscode-extension', 'VS Code extension (TypeScript)', ['extension']),
    ],
    connectors: [],
    crews,
    agents,
    tasks,
  }
}
