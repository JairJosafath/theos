// Tests for health.mjs: quantities, pod and component states, the summary the
// browser gets, and the cached handler.
// Run with: node --test test/health.test.mjs

import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import { describe, it } from 'node:test'
import {
  componentState,
  addSample,
  componentsFromEnv,
  createHealthHandler,
  DEFAULT_COMPONENTS,
  maxSandboxesFromArgs,
  parseQuantity,
  sampleOf,
  summarizeHealth,
  summarizePod,
} from '../health.mjs'

const NOW = Date.parse('2026-10-01T12:00:00Z')

// The value of a Secret env var in every pod; no summary may show it.
const secretValue = randomBytes(18).toString('base64url')

function pod(
  namespace,
  name,
  labels,
  { ready = true, restarts = 0, lastRestartAt, waiting, phase = 'Running', args, memoryLimit } = {},
) {
  return {
    metadata: { namespace, name, labels },
    spec: {
      containers: [
        {
          name: 'main',
          args,
          resources: memoryLimit ? { limits: { memory: memoryLimit } } : {},
          env: [{ name: 'SECRET', value: secretValue }],
        },
      ],
    },
    status: {
      phase,
      startTime: '2026-10-01T10:00:00Z',
      containerStatuses: [
        {
          name: 'main',
          ready,
          restartCount: restarts,
          state: waiting ? { waiting: { reason: waiting } } : { running: {} },
          lastState: lastRestartAt ? { terminated: { finishedAt: lastRestartAt, reason: 'Error', exitCode: 1 } } : {},
        },
      ],
    },
  }
}

const node = {
  metadata: { name: 'theos-control-plane' },
  status: {
    allocatable: { cpu: '12', memory: '15000000Ki' },
    conditions: [
      { type: 'Ready', status: 'True' },
      { type: 'MemoryPressure', status: 'False' },
    ],
  },
}

describe('parseQuantity', () => {
  for (const [text, want] of [
    ['250m', 0.25],
    ['2', 2],
    ['163452812n', 0.163452812],
    ['1Ki', 1024],
    ['1084Mi', 1084 * 2 ** 20],
    ['1G', 1e9],
    ['1.5Gi', 1.5 * 2 ** 30],
    ['1e3', 1000],
  ]) {
    it(`reads ${text}`, () =>
      assert.ok(Math.abs(parseQuantity(text) - want) < 1e-9, `${parseQuantity(text)} != ${want}`))
  }
  it('rejects nonsense', () => {
    assert.equal(parseQuantity('lots'), undefined)
    assert.equal(parseQuantity(undefined), undefined)
  })
})

describe('maxSandboxesFromArgs', () => {
  it('reads the flag in either form', () => {
    assert.equal(maxSandboxesFromArgs(['--leader-elect', '--max-agent-sandboxes=2']), 2)
    assert.equal(maxSandboxesFromArgs(['--max-agent-sandboxes', '3']), 3)
    assert.equal(maxSandboxesFromArgs(['-max-agent-sandboxes=4']), 4)
  })
  it('says nothing when the flag is missing', () => assert.equal(maxSandboxesFromArgs(['--leader-elect']), undefined))
})

describe('summarizePod', () => {
  it('counts restarts and says when the last one was recent', () => {
    const summary = summarizePod(
      pod('gitea', 'runner-1', {}, { restarts: 3, lastRestartAt: '2026-10-01T11:55:00Z' }),
      undefined,
      NOW,
    )
    assert.equal(summary.restarts, 3)
    assert.equal(summary.restartedRecently, true)
    assert.deepEqual(summary.lastRestart, { at: '2026-10-01T11:55:00Z', reason: 'Error', exitCode: 1 })
  })
  it('forgets restarts older than 15 minutes', () => {
    const summary = summarizePod(
      pod('gitea', 'r', {}, { restarts: 1, lastRestartAt: '2026-10-01T11:00:00Z' }),
      undefined,
      NOW,
    )
    assert.equal(summary.restartedRecently, false)
  })
  it('adds usage from metrics and the memory limit', () => {
    const summary = summarizePod(
      pod('gitea', 'gitea-1', {}, { memoryLimit: '256Mi' }),
      { containers: [{ usage: { cpu: '3123456n', memory: '93Mi' } }] },
      NOW,
    )
    assert.equal(summary.cpuMilli, 3)
    assert.equal(summary.memoryBytes, 93 * 2 ** 20)
    assert.equal(summary.memoryLimitBytes, 256 * 2 ** 20)
  })
  it('names why a container waits', () => {
    const summary = summarizePod(pod('x', 'y', {}, { ready: false, waiting: 'CrashLoopBackOff' }), undefined, NOW)
    assert.equal(summary.waiting, 'CrashLoopBackOff')
    assert.equal(summary.ready, false)
  })
  it('never passes on the spec', () => {
    const summary = summarizePod(pod('x', 'y', {}), undefined, NOW)
    assert.ok(!JSON.stringify(summary).includes(secretValue))
  })
})

describe('componentState', () => {
  const ready = { ready: true, restartedRecently: false }
  it('is ok when every pod is ready', () => assert.equal(componentState([ready, ready]), 'ok'))
  it('is degraded when one pod is not ready', () => assert.equal(componentState([ready, { ready: false }]), 'degraded'))
  it('is degraded when a ready pod just restarted', () =>
    assert.equal(componentState([{ ready: true, restartedRecently: true }]), 'degraded'))
  it('is down when no pod is ready', () => assert.equal(componentState([{ ready: false }]), 'down'))
  it('is down without pods, unless optional', () => {
    assert.equal(componentState([], false), 'down')
    assert.equal(componentState([], true), 'absent')
  })
})

describe('summarizeHealth', () => {
  const operator = pod(
    'theos-system',
    'manager-1',
    { 'control-plane': 'controller-manager' },
    {
      args: ['--leader-elect', '--max-agent-sandboxes=2'],
    },
  )
  const gitea = pod('gitea', 'gitea-1', { 'app.kubernetes.io/name': 'gitea' })
  const sandbox = pod('apps', 'image-shrink-v1-r1-implement', {
    'theos.factory.io/agent-sandbox': 'true',
    'theos.factory.io/task': 'image-shrink-v1',
  })
  const finishedSeed = pod(
    'gitea',
    'seed-1',
    { 'app.kubernetes.io/name': 'gitea' },
    { phase: 'Succeeded', ready: false },
  )
  const components = DEFAULT_COMPONENTS.filter((c) => ['operator', 'gitea', 'ui'].includes(c.key))

  it('finds each component by namespace and labels', () => {
    const health = summarizeHealth({
      components,
      pods: [operator, gitea, sandbox, finishedSeed],
      nodes: [node],
      now: NOW,
    })
    const byKey = Object.fromEntries(health.components.map((c) => [c.key, c]))
    assert.deepEqual(
      byKey.operator.pods.map((p) => p.name),
      ['manager-1'],
    )
    assert.deepEqual(
      byKey.gitea.pods.map((p) => p.name),
      ['gitea-1'],
      'finished pods are left out',
    )
    assert.equal(byKey.ui.state, 'absent')
    assert.equal(health.state, 'ok')
  })

  it('reads the agent slots from the operator and lists the sandboxes', () => {
    const health = summarizeHealth({ components, pods: [operator, sandbox], nodes: [node], now: NOW })
    assert.equal(health.agents.maxSandboxes, 2)
    assert.deepEqual(
      health.agents.sandboxes.map((s) => [s.namespace, s.task]),
      [['apps', 'image-shrink-v1']],
    )
  })

  it("falls back to the operator's default slot count", () => {
    const plain = pod('theos-system', 'm', { 'control-plane': 'controller-manager' }, { args: ['--leader-elect'] })
    assert.equal(summarizeHealth({ components, pods: [plain], nodes: [node] }).agents.maxSandboxes, 1)
  })

  it('is down when a required component is', () => {
    const health = summarizeHealth({ components, pods: [gitea], nodes: [node], now: NOW })
    assert.equal(health.components.find((c) => c.key === 'operator').state, 'down')
    assert.equal(health.state, 'down')
  })

  it('reports the node with its usage, when metrics are there', () => {
    const health = summarizeHealth({
      components,
      pods: [],
      nodes: [node],
      nodeMetrics: [{ metadata: { name: 'theos-control-plane' }, usage: { cpu: '163m', memory: '1084Mi' } }],
      podMetrics: [],
      now: NOW,
    })
    assert.equal(health.metrics, true)
    assert.deepEqual(health.nodes[0], {
      name: 'theos-control-plane',
      ready: true,
      pressure: [],
      cpuAllocatableMilli: 12000,
      memoryAllocatableBytes: 15000000 * 1024,
      cpuMilli: 163,
      memoryBytes: 1084 * 2 ** 20,
    })
  })

  it('says when metrics-server is missing', () => {
    assert.equal(summarizeHealth({ components, pods: [], nodes: [node] }).metrics, false)
  })

  it('is unknown, not down, when pods could not be read', () => {
    const health = summarizeHealth({ components, pods: null, nodes: [node], errors: ['/api/v1/pods: 403'] })
    assert.equal(health.state, 'unknown')
    assert.ok(health.components.every((c) => c.state === 'unknown'))
    assert.deepEqual(health.errors, ['/api/v1/pods: 403'])
  })

  it('carries no pod spec to the browser', () => {
    const health = summarizeHealth({ components, pods: [operator, gitea, sandbox], nodes: [node], now: NOW })
    assert.ok(!JSON.stringify(health).includes(secretValue))
  })
})

describe('componentsFromEnv', () => {
  it('uses the defaults without the variable', () => assert.equal(componentsFromEnv({}), DEFAULT_COMPONENTS))
  it('reads a valid list', () => {
    const list = [{ key: 'gitea', name: 'Gitea', namespace: 'git', selector: 'app=gitea' }]
    assert.deepEqual(componentsFromEnv({ FACTORY_COMPONENTS: JSON.stringify(list) }), list)
  })
  it('falls back on a malformed list', () => {
    const logs = []
    assert.equal(
      componentsFromEnv({ FACTORY_COMPONENTS: '[{"key":1}]' }, (m) => logs.push(m)),
      DEFAULT_COMPONENTS,
    )
    assert.equal(logs.length, 1)
  })
})

describe('createHealthHandler', () => {
  const projects = '/apis/theos.factory.io/v1beta1/projects'
  const namespaces = [...new Set(DEFAULT_COMPONENTS.map((c) => c.namespace))]
  const answers = {
    '/api/v1/nodes': { status: 200, body: { items: [node] } },
    '/apis/metrics.k8s.io/v1beta1/nodes': { status: 404, body: { message: 'not found' } },
    [projects]: { status: 200, body: { items: [] } },
  }
  const answer = (path) =>
    answers[path] ??
    (path.startsWith('/apis/metrics.k8s.io/')
      ? { status: 404, body: { message: 'not found' } }
      : { status: 200, body: { items: [] } })

  it('reads the API once per cache period, even for callers at the same time', async () => {
    const calls = []
    let clock = NOW
    const health = createHealthHandler({
      getJson: async (path) => {
        calls.push(path)
        return answer(path)
      },
      now: () => clock,
    })
    const reads = 3 + 2 * namespaces.length
    const [a, b] = await Promise.all([health(), health()])
    assert.equal(a, b)
    assert.equal(calls.length, reads)
    await health()
    assert.equal(calls.length, reads, 'cached')
    clock += 6_000
    await health()
    assert.equal(calls.length, 2 * reads, 'read again after the cache period')
  })

  it("reads pods only in the components' and the Projects' namespaces", async () => {
    const calls = []
    const health = await createHealthHandler({
      components: DEFAULT_COMPONENTS.filter((c) => c.key === 'operator'),
      getJson: async (path) => {
        calls.push(path)
        if (path === projects) {
          return { status: 200, body: { items: [{ metadata: { namespace: 'apps', name: 'apps' } }] } }
        }
        if (path === '/api/v1/namespaces/apps/pods') {
          return { status: 200, body: { items: [pod('apps', 'sb', { 'theos.factory.io/agent-sandbox': 'true' })] } }
        }
        return answer(path)
      },
    })()
    assert.deepEqual(calls.filter((p) => p.includes('pods')).sort(), [
      '/api/v1/namespaces/apps/pods',
      '/api/v1/namespaces/theos-system/pods',
      '/apis/metrics.k8s.io/v1beta1/namespaces/apps/pods',
      '/apis/metrics.k8s.io/v1beta1/namespaces/theos-system/pods',
    ])
    assert.deepEqual(
      health.agents.sandboxes.map((s) => s.name),
      ['sb'],
    )
  })

  it('treats missing metrics as no metrics, not as an error', async () => {
    const health = await createHealthHandler({ getJson: async (path) => answer(path) })()
    assert.equal(health.metrics, false)
    assert.deepEqual(health.errors, [])
  })

  it('reports a failed read, and only its namespace as unknown', async () => {
    const refused = '/api/v1/namespaces/gitea/pods'
    const health = await createHealthHandler({
      getJson: async (path) => (path === refused ? { status: 403, body: { message: 'forbidden' } } : answer(path)),
    })()
    assert.equal(health.state, 'unknown')
    assert.deepEqual(health.errors, [`${refused}: 403 forbidden`])
    for (const c of health.components) assert.equal(c.state === 'unknown', c.namespace === 'gitea', c.key)
  })

  it('lists a failed read once, however many times the health was loaded', async () => {
    let clock = NOW
    const health = createHealthHandler({
      components: DEFAULT_COMPONENTS.filter((c) => c.key === 'operator'),
      getJson: async (path) =>
        path.startsWith('/api/v1/') ? { status: 404, body: { message: 'not found' } } : answer(path),
      now: () => clock,
    })
    for (let i = 0; i < 3; i++) {
      await Promise.all([health(), health()])
      clock += 6_000
    }
    assert.deepEqual((await health()).errors, [
      '/api/v1/nodes: 404 not found',
      '/api/v1/namespaces/theos-system/pods: 404 not found',
    ])
  })
})

describe('history', () => {
  const sample = (iso, apiMs = 5) => ({ at: iso, apiMs })

  it('keeps one sample per gap and an hour of them', () => {
    let history = []
    history = addSample(history, sample('2026-10-01T12:00:00Z'))
    history = addSample(history, sample('2026-10-01T12:00:05Z'))
    assert.equal(history.length, 1, 'too soon after the last one')
    history = addSample(history, sample('2026-10-01T12:00:15Z'))
    history = addSample(history, sample('2026-10-01T13:00:10Z'))
    assert.deepEqual(
      history.map((s) => s.at),
      ['2026-10-01T12:00:15Z', '2026-10-01T13:00:10Z'],
    )
  })

  it('samples the nodes, CI and the agents', () => {
    const health = summarizeHealth({
      components: DEFAULT_COMPONENTS.filter((c) => c.key === 'runner'),
      pods: [
        pod('gitea', 'runner-1', { 'app.kubernetes.io/name': 'gitea-runner' }),
        pod('apps', 'sb', { 'theos.factory.io/agent-sandbox': 'true', 'theos.factory.io/task': 't' }),
      ],
      podMetrics: [
        { metadata: { namespace: 'gitea', name: 'runner-1' }, containers: [{ usage: { cpu: '4', memory: '1Gi' } }] },
        { metadata: { namespace: 'apps', name: 'sb' }, containers: [{ usage: { cpu: '2500m', memory: '2Gi' } }] },
      ],
      nodes: [node],
      nodeMetrics: [{ metadata: { name: 'theos-control-plane' }, usage: { cpu: '7', memory: '8Gi' } }],
      now: NOW,
    })
    assert.deepEqual(sampleOf(health, 1234.4, true), {
      at: '2026-10-01T12:00:00.000Z',
      apiMs: 1234,
      apiFailed: true,
      cpuMilli: 7000,
      cpuAllocatableMilli: 12000,
      cpuFullest: { node: 'theos-control-plane', share: 0.5833 },
      memoryBytes: 8 * 2 ** 30,
      memoryAllocatableBytes: 15000000 * 1024,
      memoryFullest: { node: 'theos-control-plane', share: 0.5592 },
      ciCpuMilli: 4000,
      agentCpuMilli: 2500,
    })
  })

  // What each sample carries besides its sums (#141): what the nodes it added
  // up could allocate at that moment, and the fullest of them by its share of
  // itself. A row is a node: its name, the CPU and memory it can allocate, and
  // the CPU and memory it uses, absent when metrics-server has no figures.
  const GiB = 2 ** 30
  const sampleOfNodes = (rows) => {
    const health = summarizeHealth({
      components: [],
      pods: [],
      podMetrics: [],
      nodes: rows.map(([name, cpu, memory]) => ({
        metadata: { name },
        status: { allocatable: { cpu, memory }, conditions: [{ type: 'Ready', status: 'True' }] },
      })),
      nodeMetrics: rows
        .filter(([, , , cpu]) => cpu !== undefined)
        .map(([name, , , cpu, memory]) => ({ metadata: { name }, usage: { cpu, memory } })),
      now: NOW,
    })
    return sampleOf(health, 5, false)
  }
  for (const [name, rows, want] of [
    [
      'one node is its own scale and its own fullest node',
      [['worker-a', '8', '16Gi', '7', '8Gi']],
      {
        cpuMilli: 7000,
        cpuAllocatableMilli: 8000,
        cpuFullest: { node: 'worker-a', share: 0.875 },
        memoryBytes: 8 * GiB,
        memoryAllocatableBytes: 16 * GiB,
        memoryFullest: { node: 'worker-a', share: 0.5 },
      },
    ],
    [
      'a node that joined adds what it can allocate, not only what it uses',
      [
        ['worker-a', '8', '16Gi', '7', '8Gi'],
        ['worker-b', '8', '16Gi', '1', '2Gi'],
      ],
      {
        cpuMilli: 8000,
        cpuAllocatableMilli: 16000,
        cpuFullest: { node: 'worker-a', share: 0.875 },
        memoryBytes: 10 * GiB,
        memoryAllocatableBytes: 32 * GiB,
        memoryFullest: { node: 'worker-a', share: 0.5 },
      },
    ],
    [
      'a full node among idle ones is the fullest by its share of itself, for CPU and for memory apart',
      [
        ['worker-a', '8', '16Gi', '400m', '2Gi'],
        ['worker-b', '4', '8Gi', '3800m', '1Gi'],
        ['worker-c', '8', '16Gi', '400m', '14Gi'],
      ],
      {
        cpuMilli: 4600,
        cpuAllocatableMilli: 20000,
        cpuFullest: { node: 'worker-b', share: 0.95 },
        memoryBytes: 17 * GiB,
        memoryAllocatableBytes: 40 * GiB,
        memoryFullest: { node: 'worker-c', share: 0.875 },
      },
    ],
    [
      'a node without figures is in neither the allocatable nor the fullest',
      [
        ['worker-a', '8', '16Gi', '2', '4Gi'],
        ['worker-b', '64', '256Gi'],
      ],
      {
        cpuMilli: 2000,
        cpuAllocatableMilli: 8000,
        cpuFullest: { node: 'worker-a', share: 0.25 },
        memoryBytes: 4 * GiB,
        memoryAllocatableBytes: 16 * GiB,
        memoryFullest: { node: 'worker-a', share: 0.25 },
      },
    ],
    [
      'on a tie the fullest is the first by name, in whatever order the nodes are listed',
      [
        ['worker-b', '8', '16Gi', '4', '8Gi'],
        ['worker-a', '4', '8Gi', '2', '4Gi'],
      ],
      {
        cpuMilli: 6000,
        cpuAllocatableMilli: 12000,
        cpuFullest: { node: 'worker-a', share: 0.5 },
        memoryBytes: 12 * GiB,
        memoryAllocatableBytes: 24 * GiB,
        memoryFullest: { node: 'worker-a', share: 0.5 },
      },
    ],
    [
      'a node that allocates nothing is in neither the sum, the allocatable nor the fullest',
      [
        ['worker-a', '0', '0', '1', '1Gi'],
        ['worker-b', '8', '16Gi', '2', '4Gi'],
      ],
      {
        cpuMilli: 2000,
        cpuAllocatableMilli: 8000,
        cpuFullest: { node: 'worker-b', share: 0.25 },
        memoryBytes: 4 * GiB,
        memoryAllocatableBytes: 16 * GiB,
        memoryFullest: { node: 'worker-b', share: 0.25 },
      },
    ],
    ['nodes that allocate nothing leave the sample without CPU or memory', [['worker-a', '0', '0', '1', '1Gi']], {}],
  ]) {
    it(`${name} (#141)`, () =>
      assert.deepEqual(sampleOfNodes(rows), { at: '2026-10-01T12:00:00.000Z', apiMs: 5, ...want }))
  }

  it('samples every node together, not the first one listed (#122)', () => {
    const worker = (name) => ({ ...node, metadata: { name } })
    const health = summarizeHealth({
      components: [],
      pods: [],
      podMetrics: [],
      nodes: [worker('worker-a'), worker('worker-b'), worker('worker-c')],
      nodeMetrics: [
        { metadata: { name: 'worker-a' }, usage: { cpu: '1', memory: '2Gi' } },
        { metadata: { name: 'worker-b' }, usage: { cpu: '7500m', memory: '11Gi' } },
        // worker-c has no figures yet: it adds nothing, and does not blank the sample.
      ],
      now: NOW,
    })
    const sample = sampleOf(health, 5, false)
    assert.equal(sample.cpuMilli, 8500)
    assert.equal(sample.memoryBytes, 13 * 2 ** 30)
  })

  it('samples no CPU or memory when no node has figures', () => {
    const health = summarizeHealth({ components: [], pods: [], nodes: [node, node], now: NOW })
    assert.deepEqual(sampleOf(health, 5, false), { at: '2026-10-01T12:00:00.000Z', apiMs: 5 })
  })

  it('comes with every answer and grows with time', async () => {
    let clock = NOW
    const health = createHealthHandler({
      getJson: async (path) =>
        path === '/api/v1/nodes' ? { status: 200, body: { items: [node] } } : { status: 200, body: { items: [] } },
      now: () => clock,
    })
    assert.equal((await health()).history.length, 1)
    clock += 20_000
    const later = await health()
    assert.equal(later.history.length, 2)
    assert.equal(typeof later.history[1].apiMs, 'number')
  })
})
