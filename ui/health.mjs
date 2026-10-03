// The factory's health, for GET /factory/health: its components' pods, the
// node, live CPU and memory from metrics-server, and the agent sandboxes.
//
// The server reads pods, nodes and metrics itself and sends the browser only a
// summary (names, states, restarts, usage), never pod specs: those carry
// environment variables and arguments. Answers are cached for a few seconds, so
// several open pages cost the API server no more than one.
//
// No runtime dependencies: only Node's standard library.

/**
 * The factory's components: where their pods run and how to find them. Each
 * `selector` is a list of label=value pairs. Optional components may be absent
 * without the factory being unhealthy. Override with FACTORY_COMPONENTS (JSON).
 */
export const DEFAULT_COMPONENTS = [
  {
    key: 'operator',
    name: 'Operator',
    role: 'Scaffolds Containers and runs Tasks',
    namespace: 'c4-factory-system',
    selector: 'control-plane=controller-manager',
  },
  {
    key: 'gitea',
    name: 'Gitea',
    role: 'Git host: templates and Container repositories',
    namespace: 'gitea',
    selector: 'app.kubernetes.io/name=gitea',
  },
  {
    key: 'runner',
    name: 'CI runner',
    role: 'Runs make check on pull requests',
    namespace: 'gitea',
    selector: 'app.kubernetes.io/name=gitea-runner',
  },
  {
    key: 'runner-android',
    name: 'Release runner',
    role: 'Builds signed APKs',
    namespace: 'gitea',
    selector: 'app.kubernetes.io/name=gitea-runner-android',
    optional: true,
  },
  {
    key: 'agent-sandbox',
    name: 'Agent Sandbox',
    role: 'Runs the agents’ sandboxes',
    namespace: 'agent-sandbox-system',
    selector: 'app=agent-sandbox-controller',
  },
  {
    key: 'ui',
    name: 'UI',
    role: 'This page',
    namespace: 'c4-factory-ui',
    selector: 'app.kubernetes.io/name=c4-factory-ui',
    optional: true,
  },
  {
    key: 'dns',
    name: 'Cluster DNS',
    role: 'Name lookups inside the cluster',
    namespace: 'kube-system',
    selector: 'k8s-app=kube-dns',
  },
  {
    key: 'metrics-server',
    name: 'metrics-server',
    role: 'CPU and memory figures',
    namespace: 'kube-system',
    selector: 'k8s-app=metrics-server',
    optional: true,
  },
]

/** The label on every agent sandbox pod, and the one naming its Task (internal/controller/task_sandbox.go). */
export const SANDBOX_SELECTOR = 'c4.factory.io/agent-sandbox=true'
const TASK_LABEL = 'c4.factory.io/task'

/** The operator's --max-agent-sandboxes when its arguments don't say (cmd/main.go). */
export const DEFAULT_MAX_SANDBOXES = 1

/** A container that last restarted within this window counts as restarting. */
const RECENT_RESTART_MS = 15 * 60_000

const CACHE_MS = 5_000

const SUFFIXES = {
  n: 1e-9,
  u: 1e-6,
  m: 1e-3,
  k: 1e3,
  K: 1e3,
  M: 1e6,
  G: 1e9,
  T: 1e12,
  P: 1e15,
  E: 1e18,
  Ki: 2 ** 10,
  Mi: 2 ** 20,
  Gi: 2 ** 30,
  Ti: 2 ** 40,
  Pi: 2 ** 50,
  Ei: 2 ** 60,
}

/** Parses a Kubernetes quantity ("250m", "1Gi", "1084Mi", "163452n", "2") into a number, or undefined. */
export function parseQuantity(text) {
  if (typeof text === 'number') return text
  if (typeof text !== 'string') return undefined
  const match = /^([+-]?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)(n|u|m|k|K|M|G|T|P|E|Ki|Mi|Gi|Ti|Pi|Ei)?$/.exec(text.trim())
  if (!match) return undefined
  return Number(match[1]) * (match[2] ? SUFFIXES[match[2]] : 1)
}

/** Parses "a=b,c=d" into pairs. */
function parseSelector(selector) {
  return selector
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => part.split('='))
}

export function matchesSelector(labels = {}, selector) {
  return parseSelector(selector).every(([key, value]) => labels[key] === value)
}

/** Reads --max-agent-sandboxes from a container's arguments. */
export function maxSandboxesFromArgs(args = []) {
  for (let i = 0; i < args.length; i++) {
    const arg = args[i]
    const inline = /^--?max-agent-sandboxes=(\d+)$/.exec(arg)
    if (inline) return Number(inline[1])
    if (/^--?max-agent-sandboxes$/.test(arg) && /^\d+$/.test(args[i + 1] ?? '')) return Number(args[i + 1])
  }
  return undefined
}

function sum(values) {
  const known = values.filter((v) => v !== undefined)
  return known.length ? known.reduce((a, b) => a + b, 0) : undefined
}

/** CPU in millicores and memory in bytes of one pod, from its PodMetrics. */
function podUsage(metrics) {
  if (!metrics) return {}
  const containers = metrics.containers ?? []
  const cpu = sum(containers.map((c) => parseQuantity(c.usage?.cpu)))
  const memory = sum(containers.map((c) => parseQuantity(c.usage?.memory)))
  return { cpuMilli: cpu === undefined ? undefined : Math.round(cpu * 1000), memoryBytes: memory }
}

/** The pod's memory limit, when every container has one. */
function memoryLimit(pod) {
  const limits = (pod.spec?.containers ?? []).map((c) => parseQuantity(c.resources?.limits?.memory))
  return limits.length && limits.every((l) => l !== undefined) ? sum(limits) : undefined
}

/** One pod, as the browser sees it. */
export function summarizePod(pod, metrics, now = Date.now()) {
  const statuses = pod.status?.containerStatuses ?? []
  const restarts = statuses.reduce((n, s) => n + (s.restartCount ?? 0), 0)
  let lastRestart
  for (const s of statuses) {
    const t = s.lastState?.terminated
    if (t?.finishedAt && (!lastRestart || t.finishedAt > lastRestart.at)) {
      lastRestart = { at: t.finishedAt, reason: t.reason ?? '', exitCode: t.exitCode ?? 0 }
    }
  }
  const waiting = statuses.map((s) => s.state?.waiting?.reason).find(Boolean)
  const ready = statuses.length > 0 && statuses.every((s) => s.ready)
  const deleting = Boolean(pod.metadata?.deletionTimestamp)
  return {
    name: pod.metadata?.name ?? '',
    namespace: pod.metadata?.namespace ?? '',
    phase: deleting ? 'Terminating' : (pod.status?.phase ?? 'Unknown'),
    ready,
    restarts,
    restartedRecently: Boolean(lastRestart && now - Date.parse(lastRestart.at) < RECENT_RESTART_MS),
    ...(lastRestart ? { lastRestart } : {}),
    ...(waiting ? { waiting } : {}),
    ...(pod.status?.startTime ? { startedAt: pod.status.startTime } : {}),
    ...podUsage(metrics),
    ...(memoryLimit(pod) !== undefined ? { memoryLimitBytes: memoryLimit(pod) } : {}),
  }
}

/**
 * ok: every pod ready. degraded: some ready, or all ready but one restarted in
 * the last 15 minutes. down: none ready. absent: no pods (fine when optional).
 */
export function componentState(pods, optional) {
  if (!pods.length) return optional ? 'absent' : 'down'
  const ready = pods.filter((p) => p.ready).length
  if (ready === 0) return 'down'
  if (ready < pods.length || pods.some((p) => p.restartedRecently)) return 'degraded'
  return 'ok'
}

/** The worst of the required components' states. */
function overallState(components) {
  const order = ['ok', 'degraded', 'down']
  let worst = 'ok'
  for (const c of components) {
    const state = c.state === 'absent' ? (c.optional ? 'ok' : 'down') : c.state
    if (order.indexOf(state) > order.indexOf(worst)) worst = state
  }
  return worst
}

/**
 * Turns raw API lists into the health summary.
 * @param {{ components?: typeof DEFAULT_COMPONENTS, pods?: any[] | null, podMetrics?: any[] | null,
 *   nodes?: any[] | null, nodeMetrics?: any[] | null, now?: number, errors?: string[] }} input
 *   pods and nodes are null when they could not be read (state unknown); podMetrics
 *   and nodeMetrics are null when metrics-server is not installed.
 */
export function summarizeHealth({
  components = DEFAULT_COMPONENTS,
  pods = [],
  podMetrics = null,
  nodes = [],
  nodeMetrics = null,
  now = Date.now(),
  errors = [],
}) {
  const metricsByPod = new Map((podMetrics ?? []).map((m) => [`${m.metadata?.namespace}/${m.metadata?.name}`, m]))
  const metricsFor = (pod) => metricsByPod.get(`${pod.metadata?.namespace}/${pod.metadata?.name}`)
  const podsKnown = pods !== null
  const nodesKnown = nodes !== null
  pods ??= []
  nodes ??= []
  const live = (pod) => pod.status?.phase !== 'Succeeded' && pod.status?.phase !== 'Failed'

  let maxSandboxes
  const summarized = components.map((component) => {
    const matching = pods.filter(
      (p) => p.metadata?.namespace === component.namespace && matchesSelector(p.metadata?.labels, component.selector),
    )
    if (component.key === 'operator') {
      for (const pod of matching) {
        for (const c of pod.spec?.containers ?? []) maxSandboxes ??= maxSandboxesFromArgs(c.args)
      }
    }
    const podSummaries = matching.filter(live).map((p) => summarizePod(p, metricsFor(p), now))
    return {
      key: component.key,
      name: component.name,
      role: component.role ?? '',
      namespace: component.namespace,
      optional: Boolean(component.optional),
      state: podsKnown ? componentState(podSummaries, component.optional) : 'unknown',
      pods: podSummaries,
    }
  })

  const sandboxes = pods
    .filter((p) => matchesSelector(p.metadata?.labels, SANDBOX_SELECTOR))
    .map((p) => ({ ...summarizePod(p, metricsFor(p), now), task: p.metadata?.labels?.[TASK_LABEL] ?? '' }))

  const usageByNode = new Map((nodeMetrics ?? []).map((m) => [m.metadata?.name, m.usage ?? {}]))
  const nodeSummaries = nodes.map((node) => {
    const usage = usageByNode.get(node.metadata?.name)
    const cpu = parseQuantity(usage?.cpu)
    const conditions = node.status?.conditions ?? []
    return {
      name: node.metadata?.name ?? '',
      ready: conditions.some((c) => c.type === 'Ready' && c.status === 'True'),
      pressure: conditions
        .filter((c) => c.type !== 'Ready' && c.status === 'True')
        .map((c) => c.type)
        .sort(),
      cpuAllocatableMilli: Math.round((parseQuantity(node.status?.allocatable?.cpu) ?? 0) * 1000),
      memoryAllocatableBytes: parseQuantity(node.status?.allocatable?.memory) ?? 0,
      ...(cpu !== undefined ? { cpuMilli: Math.round(cpu * 1000) } : {}),
      ...(usage?.memory !== undefined ? { memoryBytes: parseQuantity(usage.memory) } : {}),
    }
  })

  return {
    generatedAt: new Date(now).toISOString(),
    state:
      !podsKnown || !nodesKnown
        ? 'unknown'
        : nodeSummaries.length && nodeSummaries.every((n) => n.ready)
          ? overallState(summarized)
          : 'down',
    metrics: podMetrics !== null,
    nodes: nodeSummaries,
    components: summarized,
    agents: {
      maxSandboxes: maxSandboxes ?? DEFAULT_MAX_SANDBOXES,
      sandboxes,
    },
    errors,
  }
}

/** Reads FACTORY_COMPONENTS, falling back to the defaults on anything malformed. */
export function componentsFromEnv(env = process.env, log = () => {}) {
  if (!env.FACTORY_COMPONENTS) return DEFAULT_COMPONENTS
  try {
    const parsed = JSON.parse(env.FACTORY_COMPONENTS)
    const valid =
      Array.isArray(parsed) &&
      parsed.every(
        (c) =>
          c &&
          typeof c.key === 'string' &&
          typeof c.name === 'string' &&
          typeof c.namespace === 'string' &&
          typeof c.selector === 'string',
      )
    if (valid) return parsed
  } catch {
    // fall through
  }
  log('FACTORY_COMPONENTS is not a JSON list of {key, name, namespace, selector}; using the defaults')
  return DEFAULT_COMPONENTS
}

/** How long the history reaches back, and the least time between two samples. */
export const HISTORY_MS = 60 * 60_000
export const SAMPLE_GAP_MS = 14_000

/**
 * One point of the factory's recent history: how long the API server took to
 * answer (a stalling API server is what restarts controllers on a small
 * cluster), the node's CPU and memory, and what CI and the agents used.
 */
export function sampleOf(health, apiMs, apiFailed) {
  const node = health.nodes[0]
  const runner = health.components.find((c) => c.key === 'runner')
  const sum = (pods, field) => {
    const values = pods.map((p) => p[field]).filter((v) => v !== undefined)
    return values.length ? values.reduce((a, b) => a + b, 0) : undefined
  }
  return {
    at: health.generatedAt,
    apiMs: Math.round(apiMs),
    ...(apiFailed ? { apiFailed: true } : {}),
    ...(node?.cpuMilli !== undefined ? { cpuMilli: node.cpuMilli } : {}),
    ...(node?.memoryBytes !== undefined ? { memoryBytes: node.memoryBytes } : {}),
    ...(runner && sum(runner.pods, 'cpuMilli') !== undefined ? { ciCpuMilli: sum(runner.pods, 'cpuMilli') } : {}),
    ...(sum(health.agents.sandboxes, 'cpuMilli') !== undefined
      ? { agentCpuMilli: sum(health.agents.sandboxes, 'cpuMilli') }
      : {}),
  }
}

/** Adds a sample unless the last one is too recent, and drops those older than HISTORY_MS. */
export function addSample(history, sample, { keepMs = HISTORY_MS, gapMs = SAMPLE_GAP_MS } = {}) {
  const at = Date.parse(sample.at)
  const last = history[history.length - 1]
  if (last && at - Date.parse(last.at) < gapMs) return history
  return [...history.filter((s) => at - Date.parse(s.at) <= keepMs), sample]
}

/**
 * Answers GET /factory/health. getJson(path) reads one API path and resolves to
 * { status, body }. With sampleEveryMs, it also reads the health in the
 * background, so the history covers the time nobody was looking.
 */
export function createHealthHandler({
  getJson,
  components = DEFAULT_COMPONENTS,
  cacheMs = CACHE_MS,
  now = Date.now,
  sampleEveryMs = 0,
}) {
  let cached = { at: -Infinity, value: undefined }
  let inFlight
  let history = []

  async function load() {
    const errors = []
    let apiMs = 0
    let apiFailed = false
    const list = async (path, { optional = false, timed = false } = {}) => {
      const started = performance.now()
      try {
        const { status, body } = await getJson(path)
        if (timed) apiMs = performance.now() - started
        if (status === 200) return body?.items ?? []
        // metrics.k8s.io answers 404 or 503 when metrics-server is missing or starting.
        if (!optional) errors.push(`${path}: ${status} ${body?.message ?? ''}`.trim())
      } catch (err) {
        if (timed) {
          apiMs = performance.now() - started
          apiFailed = true
        }
        if (!optional) errors.push(`${path}: ${err.message}`)
      }
      return null
    }
    const [pods, nodes, podMetrics, nodeMetrics] = await Promise.all([
      list('/api/v1/pods'),
      // The node list is small and served by the API server itself: its time is the API's latency.
      list('/api/v1/nodes', { timed: true }),
      list('/apis/metrics.k8s.io/v1beta1/pods', { optional: true }),
      list('/apis/metrics.k8s.io/v1beta1/nodes', { optional: true }),
    ])
    const health = summarizeHealth({ components, pods, nodes, podMetrics, nodeMetrics, now: now(), errors })
    history = addSample(history, sampleOf(health, apiMs, apiFailed))
    return { ...health, history }
  }

  async function health() {
    if (now() - cached.at < cacheMs) return cached.value
    inFlight ??= load().finally(() => {
      inFlight = undefined
    })
    const value = await inFlight
    cached = { at: now(), value }
    return value
  }

  if (sampleEveryMs > 0) {
    // unref: the sampler never keeps the process alive on its own.
    setInterval(() => health().catch(() => {}), sampleEveryMs).unref()
  }
  return health
}
