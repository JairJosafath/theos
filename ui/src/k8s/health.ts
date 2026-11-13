// The factory's health, from GET /factory/health (server.mjs and health.mjs).
// Polled while someone looks at the Work or Factory area; the server caches it for a
// few seconds, so several open pages cost the cluster no more than one.

import type { Snapshot, Source } from './sources'

export type ComponentState = 'ok' | 'degraded' | 'down' | 'absent' | 'unknown'

export interface PodHealth {
  name: string
  namespace: string
  phase: string
  ready: boolean
  restarts: number
  restartedRecently: boolean
  lastRestart?: { at: string; reason: string; exitCode: number }
  waiting?: string
  startedAt?: string
  cpuMilli?: number
  memoryBytes?: number
  memoryLimitBytes?: number
}

export interface ComponentHealth {
  key: string
  name: string
  role: string
  namespace: string
  optional: boolean
  state: ComponentState
  pods: PodHealth[]
}

export interface NodeHealth {
  name: string
  ready: boolean
  pressure: string[]
  cpuAllocatableMilli: number
  memoryAllocatableBytes: number
  cpuMilli?: number
  memoryBytes?: number
}

/** The fullest node of a sample: its name and its usage over its own allocatable, 1 being full. */
export interface FullestNode {
  node: string
  share: number
}

/** One point of the last hour, sampled by the UI server every 15 s. */
export interface HealthSample {
  at: string
  /** How long the API server took to list the nodes. */
  apiMs: number
  /** The API server did not answer at all (timeout or error). */
  apiFailed?: boolean
  /** Every node's CPU, added up; absent when no node has figures and something to allocate. */
  cpuMilli?: number
  /**
   * What the nodes added up in cpuMilli could allocate together when the sample
   * was taken (#141): the sample's own scale, whatever nodes came or went
   * since. Absent with cpuMilli, and from older servers.
   */
  cpuAllocatableMilli?: number
  /** The node that used most of its own CPU, and that share. */
  cpuFullest?: FullestNode
  /** Every node's memory, added up. */
  memoryBytes?: number
  /** As cpuAllocatableMilli, for the nodes added up in memoryBytes. */
  memoryAllocatableBytes?: number
  /** The node that used most of its own memory, and that share. */
  memoryFullest?: FullestNode
  ciCpuMilli?: number
  agentCpuMilli?: number
}

export interface FactoryHealth {
  generatedAt: string
  state: 'ok' | 'degraded' | 'down' | 'unknown'
  /** False when metrics-server is not installed: no CPU or memory figures. */
  metrics: boolean
  nodes: NodeHealth[]
  components: ComponentHealth[]
  agents: { maxSandboxes: number; sandboxes: (PodHealth & { task: string })[] }
  errors: string[]
  /** The last hour, oldest first; absent from older servers. */
  history?: HealthSample[]
}

export const HEALTH_POLL_MS = 10_000

/** Polls /factory/health while subscribed and started. */
export class HealthSource implements Source<FactoryHealth | undefined> {
  private readonly listeners = new Set<() => void>()
  private snapshot: Snapshot<FactoryHealth | undefined> = {
    data: undefined,
    connection: { phase: 'connecting', loaded: false, problems: [] },
  }
  private timer?: ReturnType<typeof setTimeout>
  private controller?: AbortController
  private readonly url: string
  private readonly intervalMs: number
  private readonly fetchImpl: typeof fetch

  constructor(url = 'factory/health', { intervalMs = HEALTH_POLL_MS, fetchImpl = fetch.bind(globalThis) } = {}) {
    this.url = url
    this.intervalMs = intervalMs
    this.fetchImpl = fetchImpl
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  getSnapshot(): Snapshot<FactoryHealth | undefined> {
    return this.snapshot
  }

  start(): void {
    if (this.controller) return
    this.controller = new AbortController()
    void this.poll()
  }

  stop(): void {
    this.controller?.abort()
    this.controller = undefined
    clearTimeout(this.timer)
    this.timer = undefined
  }

  /** Reads once, then schedules the next read. Exposed for tests. */
  async poll(): Promise<void> {
    const controller = this.controller
    try {
      const res = await this.fetchImpl(this.url, {
        signal: controller?.signal,
        headers: { accept: 'application/json' },
      })
      if (!res.ok) {
        const body = (await res.json().catch(() => undefined)) as { message?: string } | undefined
        this.set(this.snapshot.data, {
          what: 'the factory’s health',
          message: body?.message ?? res.statusText,
          code: res.status,
        })
      } else {
        this.set((await res.json()) as FactoryHealth)
      }
    } catch (err) {
      if (controller?.signal.aborted) return
      this.set(this.snapshot.data, { what: 'the factory’s health', message: (err as Error).message })
    }
    if (controller && !controller.signal.aborted) {
      this.timer = setTimeout(() => void this.poll(), this.intervalMs)
    }
  }

  private set(data: FactoryHealth | undefined, problem?: { what: string; message: string; code?: number }): void {
    this.snapshot = {
      data,
      connection: {
        phase: problem ? (data ? 'reconnecting' : 'connecting') : 'live',
        loaded: data !== undefined,
        problems: problem ? [problem] : [],
        updatedAt: problem ? this.snapshot.connection.updatedAt : Date.now(),
      },
    }
    for (const listener of this.listeners) listener()
  }
}
