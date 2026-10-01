// The factory view's demo: Tasks in every stage across two projects, one that
// ran before timelines, a component that just restarted, and an agent slot in
// use with another Task waiting for it. Times are relative to `now`.

import type { FactoryHealth, PodHealth } from '../k8s/health'
import type { FactoryData } from '../k8s/sources'
import type { Agent, Project, Task, TaskPhase, TaskTransition } from '../k8s/types'
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
  const toolbox: Project = {
    metadata: { name: 'toolbox', namespace: 'toolbox', creationTimestamp: at(600) },
    spec: { displayName: 'Pocket Tools', description: 'Offline tool apps, built by agents' },
  }

  const agents: Agent[] = [
    {
      metadata: { name: 'claude', namespace: 'toolbox' },
      spec: { displayName: 'Claude Code (Flutter)', image: 'c4factory/agent-flutter:dev', maxRounds: 4 },
    },
    {
      metadata: { name: 'stand-in', namespace: 'toolbox' },
      spec: { displayName: 'Stand-in agent', image: 'c4factory/agent-flutter:dev', maxRounds: 2 },
    },
    {
      metadata: { name: 'claude', namespace: 'webshop' },
      spec: { displayName: 'Claude Code (Python)', image: 'c4factory/agent-python:dev', maxRounds: 3 },
    },
  ]

  const tasks: Task[] = [
    task('toolbox', 'stand-in-loop', 'image-shrink', 'stand-in', 'Prove the loop with the stand-in agent', 560, {
      phase: 'Merged',
      round: 2,
      completedAt: at(557),
      review: { verdict: 'approve', summary: 'Looks fine.' },
      ...pr('toolbox', 'image-shrink', 1),
    }),
    task(
      'toolbox',
      'image-shrink-v1',
      'image-shrink',
      'claude',
      'Build version 1 of Image Shrink: shrink photos for forms and messages.\n- Pick one or more photos\n- Three modes: by size, by dimensions, by quality',
      190,
      {
        phase: 'Merged',
        round: 3,
        completedAt: at(118),
        checks: { state: 'success' },
        review: { verdict: 'approve', summary: 'Complete v1 with tests for every mode.' },
        summary:
          'I fixed both review points: the scaling parameters are pinned by a test, and reads run off the UI isolate.',
        ...pr('toolbox', 'image-shrink', 3),
        conditions: [
          { type: 'Progressing', status: 'False', reason: 'Merged', message: 'Approved; merging' },
          { type: 'Succeeded', status: 'True', reason: 'Merged', message: 'Approved; merging' },
        ],
        history: [
          step(190, 'Implementing', 1, 'Running', 'Starting round 1 in sandbox image-shrink-v1-r1-implement'),
          step(189, 'Implementing', 1, 'Waiting', 'The agent is working on round 1'),
          step(166, 'Checking', 1, 'Checking', 'The agent pushed 1c2d3e4; waiting for checks'),
          step(158, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
          step(149, 'Implementing', 2, 'NextRound', 'Round 1 was not accepted; starting round 2'),
          step(137, 'Checking', 2, 'Checking', 'The agent pushed 5f6a7b8; waiting for checks'),
          step(130, 'Reviewing', 2, 'Running', 'Checks passed; starting the review'),
          step(124, 'Implementing', 3, 'NextRound', 'Round 2 was not accepted; starting round 3'),
          step(121, 'Checking', 3, 'Checking', 'The agent pushed 8476b13; waiting for checks'),
          step(120, 'Reviewing', 3, 'Running', 'Checks passed; starting the review'),
          step(118, 'Merged', 3, 'Merged', 'Approved; merging'),
        ],
      },
    ),
    task('toolbox', 'qr-scanner-v1', 'qr-scanner', 'claude', 'Build version 1 of QR Scanner', 170, {
      phase: 'Failed',
      round: 4,
      completedAt: at(40),
      checks: { state: 'success' },
      review: { verdict: 'changes', summary: 'Exclude the scan history from Auto Backup; two style points.' },
      feedback: 'The reviewer asked for changes:\nExclude the scan history from Auto Backup: it holds Wi-Fi passwords.',
      ...pr('toolbox', 'qr-scanner', 4),
      conditions: [
        { type: 'Progressing', status: 'False', reason: 'RoundsExhausted', message: 'Failed after 4 rounds' },
        {
          type: 'Succeeded',
          status: 'False',
          reason: 'RoundsExhausted',
          message: 'Failed after 4 rounds: the reviewer asked for an Auto Backup exclusion',
        },
      ],
      history: [
        step(170, 'Implementing', 1, 'WaitingForSlot', 'Waiting for a free agent slot'),
        step(118, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(96, 'Checking', 1, 'Checking', 'Waiting for checks'),
        step(88, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(81, 'Implementing', 2, 'NextRound', 'Round 1 was not accepted; starting round 2'),
        step(72, 'Checking', 2, 'Checking', 'Waiting for checks'),
        step(66, 'Reviewing', 2, 'Running', 'Checks passed; starting the review'),
        step(61, 'Implementing', 3, 'NextRound', 'Round 2 was not accepted; starting round 3'),
        step(55, 'Checking', 3, 'Checking', 'Waiting for checks'),
        step(50, 'Reviewing', 3, 'Running', 'Checks passed; starting the review'),
        step(47, 'Implementing', 4, 'NextRound', 'Round 3 was not accepted; starting round 4'),
        step(45, 'Checking', 4, 'Checking', 'Waiting for checks'),
        step(43, 'Reviewing', 4, 'Running', 'Checks passed; starting the review'),
        step(40, 'Failed', 4, 'RoundsExhausted', 'Failed after 4 rounds'),
      ],
    }),
    task('toolbox', 'audio-cutter-v1', 'audio-cutter', 'claude', 'Build version 1 of Audio Cutter', 75, {
      phase: 'Reviewing',
      round: 2,
      sandbox: 'audio-cutter-v1-r2-review-a1',
      checks: { state: 'success' },
      review: { verdict: 'changes', summary: 'Fade-out clips the last 20 ms.' },
      summary: 'Fixed the fade-out and added a waveform golden test.',
      ...pr('toolbox', 'audio-cutter', 5),
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
    task('toolbox', 'bg-eraser-v1', 'bg-eraser', 'claude', 'Build version 1 of Background Eraser', 12, {
      phase: 'Implementing',
      round: 1,
      slotWaitSince: at(12),
      conditions: [progressing('WaitingForSlot', 'Waiting for a free agent slot')],
      history: [step(12, 'Implementing', 1, 'WaitingForSlot', 'Waiting for a free agent slot')],
    }),
    task('toolbox', 'photo-editor-v1', 'photo-editor', 'claude', 'Build version 1 of Photo Editor', 8, {
      phase: 'Pending',
      conditions: [progressing('Waiting', 'Waiting for Container photo-editor to be Ready')],
      history: [step(8, 'Pending', 0, 'Waiting', 'Waiting for Container photo-editor to be Ready')],
    }),
    task('webshop', 'orders-validation', 'orders-api', 'claude', 'Validate orders before saving them', 300, {
      phase: 'Merged',
      round: 1,
      completedAt: at(262),
      checks: { state: 'success' },
      review: { verdict: 'approve', summary: 'Validation and tests are complete.' },
      ...pr('webshop', 'orders-api', 2),
      history: [
        step(300, 'Implementing', 1, 'Running', 'Starting round 1'),
        step(279, 'Checking', 1, 'Checking', 'Waiting for checks'),
        step(270, 'Reviewing', 1, 'Running', 'Checks passed; starting the review'),
        step(262, 'Merged', 1, 'Merged', 'Approved; merging'),
      ],
    }),
  ]

  return {
    projects: [story.project!, toolbox],
    tasks,
    agents,
    containers: story.containers,
    sessions: story.sessions,
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
        name: 'c4factory-control-plane',
        ready: true,
        pressure: [],
        cpuAllocatableMilli: 12000,
        memoryAllocatableBytes: 14.7 * 2 ** 30,
        cpuMilli: 1370,
        memoryBytes: 4.2 * 2 ** 30,
      },
    ],
    components: [
      {
        key: 'operator',
        name: 'Operator',
        role: 'Scaffolds Containers and runs Tasks',
        namespace: 'c4-factory-system',
        optional: false,
        state: 'ok',
        pods: [
          pod('c4-factory-system', 'c4-factory-controller-manager-7d9c', 6, 41, {
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
        namespace: 'c4-factory-ui',
        optional: true,
        state: 'ok',
        pods: [pod('c4-factory-ui', 'c4-factory-ui-6c9f', 3, 46)],
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
      sandboxes: [{ ...pod('toolbox', 'audio-cutter-v1-r2-review-a1', 1200, 2150), task: 'audio-cutter-v1' }],
    },
    errors: [],
    history: demoHistory(now),
  }
}

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
    samples.push({
      at: new Date(now - minutesAgo * MIN).toISOString(),
      apiMs: stall ? 10_000 : busy ? 300 + Math.round(wave * 2600) : 9 + Math.round(wave * 12),
      ...(stall && i % 2 ? { apiFailed: true } : {}),
      cpuMilli: Math.round(160 + ci + agent),
      memoryBytes: Math.round((1.9 + (busy ? 6.5 : 0) + (review ? 2.2 : 0) + wave * 0.2) * 2 ** 30),
      ciCpuMilli: Math.round(ci),
      agentCpuMilli: Math.round(agent),
    })
  }
  return samples
}
