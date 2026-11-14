// The Work area's demo: the webshop's Tasks in every stage, one that ran
// before timelines, a component that just restarted, and an agent slot in use
// with another Task waiting for it; the story adds the Crews and their issue
// Tasks. Times are relative to `now`.

import type { FactoryHealth, PodHealth } from '../k8s/health'
import type { FactoryData } from '../k8s/sources'
import type { Chat, Task, TaskPhase, TaskTransition } from '../k8s/types'
import { demoStory } from './fixture'

const MIN = 60_000

export function demoFactory(now: number): FactoryData {
  const at = (minutesAgo: number) => new Date(now - minutesAgo * MIN).toISOString()
  const step = (minutesAgo: number, phase: TaskPhase, round: number, reason: string, message = ''): TaskTransition => ({
    at: at(minutesAgo),
    phase,
    round,
    reason,
    message,
  })
  const progressing = (reason: string, message: string) => ({
    type: 'Progressing',
    status: 'True' as const,
    reason,
    message,
    lastTransitionTime: at(1),
  })
  const task = (
    namespace: string,
    name: string,
    container: string,
    agent: string,
    goal: string,
    createdMinutesAgo: number,
    status: Task['status'],
  ): Task => ({
    metadata: { name, namespace, uid: `demo-${name}`, creationTimestamp: at(createdMinutesAgo) },
    spec: { containerRef: { name: container }, agentRef: { name: agent }, goal },
    status: { branch: `task/${name}`, ...status },
  })
  const pr = (namespace: string, container: string, n: number) => ({
    pullRequest: n,
    pullRequestURL: `http://localhost:3000/${namespace}/${container}/pulls/${n}`,
  })

  const story = demoStory(now)

  // Tasks people started by hand, next to the orders-api Crew's issue Tasks.
  const tasks: Task[] = [
    task('webshop', 'orders-api-health', 'orders-api', 'claude', 'Add a health endpoint', 170, {
      phase: 'Merged',
      round: 1,
      completedAt: at(166),
      review: { verdict: 'approve', summary: 'Looks fine.' },
      ...pr('webshop', 'orders-api', 1),
    }),
    task('webshop', 'orders-validation', 'orders-api', 'claude', 'Validate orders before saving them', 160, {
      phase: 'Merged',
      round: 1,
      completedAt: at(122),
      checks: { state: 'success' },
      review: { verdict: 'approve', summary: 'Validation and tests are complete.' },
      ...pr('webshop', 'orders-api', 2),
      history: [
        step(160, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(139, 'Checking', 1, 'Checking', 'Waiting for checks'),
        step(130, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(122, 'Merged', 1, 'Merged', 'Approved; merging'),
      ],
    }),
    task('webshop', 'api-reference', 'orders-api', 'claude', 'Write the API reference', 75, {
      phase: 'Reviewing',
      round: 2,
      sandbox: 'api-reference-r2-review-a1',
      checks: { state: 'success' },
      review: { verdict: 'changes', summary: 'The refund endpoint is missing.' },
      summary: 'Added the refund endpoint and an example for every error.',
      ...pr('webshop', 'orders-api', 9),
      conditions: [progressing('Waiting', 'The reviewer is reviewing')],
      history: [
        step(75, 'Implementing', 1, 'WaitingForSlot', 'Waiting for a free agent slot'),
        step(40, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(26, 'Checking', 1, 'Checking', 'Waiting for checks'),
        step(19, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(15, 'Implementing', 2, 'NextRound', 'Round 1 was not accepted; starting round 2'),
        step(9, 'Checking', 2, 'Checking', 'Waiting for checks'),
        step(4, 'Reviewing', 2, 'Running', 'Checks passed; starting the review'),
        step(4, 'Reviewing', 2, 'Waiting', 'The reviewer is reviewing'),
      ],
    }),
    task('webshop', 'cart-totals', 'orders-api', 'claude', 'Check cart totals against the catalog prices', 12, {
      phase: 'Implementing',
      round: 1,
      slotWaitSince: at(12),
      conditions: [progressing('WaitingForSlot', 'Waiting for a free agent slot')],
      history: [step(12, 'Implementing', 1, 'WaitingForSlot', 'Waiting for a free agent slot')],
    }),
    task('webshop', 'catalog-seed', 'catalog-api', 'claude', 'Seed the catalog with sample products', 8, {
      phase: 'Pending',
      conditions: [progressing('Waiting', 'Waiting for Container catalog-api to be Ready')],
      history: [step(8, 'Pending', 0, 'Waiting', 'Waiting for Container catalog-api to be Ready')],
    }),
  ]

  return {
    projects: [story.project!],
    tasks: [...tasks, ...story.tasks],
    agents: story.agents,
    containers: story.containers,
    sessions: story.sessions,
    crews: story.crews,
    briefs: story.briefs,
    chats: [demoChat(at(240)), demoSlackChat(at(180))],
  }
}

/** The webshop's Chat on the in-cluster homeserver, its six rooms made. */
function demoChat(created: string): Chat {
  const server = 'theos.internal'
  const purposes = ['updates', 'general', 'triage', 'specs', 'reviews', 'alerts'] as const
  return {
    metadata: { name: 'webshop', namespace: 'webshop', uid: 'demo-chat-webshop', creationTimestamp: created },
    spec: {
      credentialsSecretRef: { name: 'matrix-bot' },
      rooms: { general: 'lobby' },
      people: [
        { matrixID: `@ada:${server}`, user: 'ada@example.com' },
        { matrixID: `@ghost:${server}`, user: 'ghost@example.com' },
      ],
    },
    status: {
      homeserver: 'http://theos-matrix.theos-system.svc:8008',
      bot: `@theos:${server}`,
      space: { id: `!webshop-space:${server}`, alias: `#theos-webshop:${server}` },
      rooms: purposes.map((purpose) => ({
        purpose,
        id: `!webshop-${purpose}:${server}`,
        alias: `#theos-webshop-${purpose}:${server}`,
      })),
      invited: [`@ada:${server}`],
      refused: [{ id: `@ghost:${server}`, code: 'M_NOT_FOUND', rooms: [`!webshop-general:${server}`] }],
      conditions: [
        {
          type: 'Ready',
          status: 'True',
          reason: 'Ready',
          message: 'The Space and its 6 rooms are there, 1 person invited',
          lastTransitionTime: created,
        },
        {
          type: 'Invited',
          status: 'False',
          reason: 'InviteRefused',
          message: `@ghost:${server}: M_NOT_FOUND`,
          lastTransitionTime: created,
        },
      ],
    },
  }
}

/**
 * A Chat on Slack (#162), in a namespace of its own: a namespace has one Chat, so it cannot sit in webshop beside the
 * Matrix one. Six private channels, no Space, its people by Slack member ID; open it with ?project=webshop-slack.
 */
function demoSlackChat(created: string): Chat {
  const purposes = ['updates', 'general', 'triage', 'specs', 'reviews', 'alerts'] as const
  return {
    metadata: {
      name: 'webshop-slack',
      namespace: 'webshop-slack',
      uid: 'demo-chat-webshop-slack',
      creationTimestamp: created,
    },
    spec: {
      host: 'slack',
      credentialsSecretRef: { name: 'slack-bot' },
      slack: {
        people: [
          { slackID: 'U0DEMOADA01', user: 'ada@example.com', groups: ['developers'] },
          { slackID: 'U0DEMOBOB02', user: 'bob@example.com' },
        ],
      },
    },
    status: {
      homeserver: 'https://slack.com/api',
      bot: 'U0DEMOBOT00',
      rooms: purposes.map((purpose) => ({
        purpose,
        id: `C0DEMO${purpose.toUpperCase().slice(0, 4)}`,
        alias: `#theos-webshop-slack-${purpose}`,
      })),
      invited: ['U0DEMOADA01', 'U0DEMOBOB02'],
      conditions: [
        {
          type: 'Ready',
          status: 'True',
          reason: 'Ready',
          message: 'The 6 rooms are ready on https://slack.com/api, with no Space, 2 people invited',
          lastTransitionTime: created,
        },
        {
          type: 'Invited',
          status: 'True',
          reason: 'Invited',
          message: 'Every person is invited to every room',
          lastTransitionTime: created,
        },
      ],
    },
  }
}

export function demoHealth(now: number): FactoryHealth {
  const at = (minutesAgo: number) => new Date(now - minutesAgo * MIN).toISOString()
  const pod = (
    namespace: string,
    name: string,
    cpuMilli: number,
    memoryMiB: number,
    extra: Partial<PodHealth> = {},
  ) => ({
    name,
    namespace,
    phase: 'Running',
    ready: true,
    restarts: 0,
    restartedRecently: false,
    startedAt: at(600),
    cpuMilli,
    memoryBytes: memoryMiB * 2 ** 20,
    ...extra,
  })
  return {
    generatedAt: at(0.1),
    state: 'degraded',
    metrics: true,
    nodes: [
      {
        name: DEMO_NODE,
        ready: true,
        pressure: [],
        cpuAllocatableMilli: DEMO_CPU_MILLI,
        memoryAllocatableBytes: DEMO_MEMORY_BYTES,
        cpuMilli: 1370,
        memoryBytes: 4.2 * 2 ** 30,
      },
    ],
    components: [
      {
        key: 'operator',
        name: 'Operator',
        role: 'Scaffolds Containers and runs Tasks',
        namespace: 'theos-system',
        optional: false,
        state: 'ok',
        pods: [
          pod('theos-system', 'theos-controller-manager-7d9c', 6, 41, {
            restarts: 2,
            lastRestart: { at: at(190), reason: 'Error', exitCode: 1 },
          }),
        ],
      },
      {
        key: 'gitea',
        name: 'Gitea',
        role: 'Git host: templates and Container repositories',
        namespace: 'gitea',
        optional: false,
        state: 'ok',
        pods: [pod('gitea', 'gitea-7d49bb798b-6zzz2', 14, 128)],
      },
      {
        key: 'runner',
        name: 'CI runner',
        role: 'Runs make check on pull requests',
        namespace: 'gitea',
        optional: false,
        state: 'degraded',
        pods: [
          pod('gitea', 'gitea-runner-df6cb6cc-lswhf', 1810, 3900, {
            restarts: 1,
            restartedRecently: true,
            lastRestart: { at: at(4), reason: 'OOMKilled', exitCode: 137 },
          }),
        ],
      },
      {
        key: 'runner-android',
        name: 'Release runner',
        role: 'Builds signed APKs',
        namespace: 'gitea',
        optional: true,
        state: 'absent',
        pods: [],
      },
      {
        key: 'agent-sandbox',
        name: 'Agent Sandbox',
        role: 'Runs the agents’ sandboxes',
        namespace: 'agent-sandbox-system',
        optional: false,
        state: 'ok',
        pods: [pod('agent-sandbox-system', 'agent-sandbox-controller-5d58', 2, 14)],
      },
      {
        key: 'ui',
        name: 'UI',
        role: 'This page',
        namespace: 'theos-ui',
        optional: true,
        state: 'ok',
        pods: [pod('theos-ui', 'theos-ui-6c9f', 3, 46)],
      },
      {
        key: 'dns',
        name: 'Cluster DNS',
        role: 'Name lookups inside the cluster',
        namespace: 'kube-system',
        optional: false,
        state: 'ok',
        pods: [pod('kube-system', 'coredns-559f-fjgvf', 2, 15), pod('kube-system', 'coredns-559f-smhhj', 2, 14)],
      },
      {
        key: 'metrics-server',
        name: 'metrics-server',
        role: 'CPU and memory figures',
        namespace: 'kube-system',
        optional: true,
        state: 'ok',
        pods: [pod('kube-system', 'metrics-server-64b5', 4, 19)],
      },
    ],
    agents: {
      maxSandboxes: 1,
      sandboxes: [{ ...pod('webshop', 'api-reference-r2-review-a1', 1200, 2150), task: 'api-reference' }],
    },
    errors: [],
    history: demoHistory(now),
  }
}

/** The demo's one node, and what it can allocate: the scale every sample of its hour carries. */
const DEMO_NODE = 'theos-control-plane'
const DEMO_CPU_MILLI = 12000
const DEMO_MEMORY_BYTES = 14.7 * 2 ** 30

/**
 * An hour sampled every 15 s: quiet, then CI and an agent sandbox running at
 * once 40 to 30 minutes ago, when the API server stalled, then the review now.
 */
function demoHistory(now: number) {
  const samples = []
  for (let i = 240; i >= 0; i--) {
    const minutesAgo = i / 4
    const busy = minutesAgo <= 40 && minutesAgo >= 30
    const review = minutesAgo <= 4
    const wave = Math.sin(i / 3) * 0.5 + 0.5
    const ci = busy ? 4200 + wave * 2500 : minutesAgo < 9 && minutesAgo > 4 ? 1800 : 5
    const agent = busy ? 3600 + wave * 1800 : review ? 1200 : 0
    const stall = busy && minutesAgo > 33 && minutesAgo < 35
    const cpuMilli = Math.round(160 + ci + agent)
    const memoryBytes = Math.round((1.9 + (busy ? 6.5 : 0) + (review ? 2.2 : 0) + wave * 0.2) * 2 ** 30)
    samples.push({
      at: new Date(now - minutesAgo * MIN).toISOString(),
      apiMs: stall ? 10_000 : busy ? 300 + Math.round(wave * 2600) : 9 + Math.round(wave * 12),
      ...(stall && i % 2 ? { apiFailed: true } : {}),
      cpuMilli,
      cpuAllocatableMilli: DEMO_CPU_MILLI,
      cpuFullest: { node: DEMO_NODE, share: cpuMilli / DEMO_CPU_MILLI },
      memoryBytes,
      memoryAllocatableBytes: DEMO_MEMORY_BYTES,
      memoryFullest: { node: DEMO_NODE, share: memoryBytes / DEMO_MEMORY_BYTES },
      ciCpuMilli: Math.round(ci),
      agentCpuMilli: Math.round(agent),
    })
  }
  return samples
}
