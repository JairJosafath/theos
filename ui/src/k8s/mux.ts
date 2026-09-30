// Many watches over few connections. Browsers allow six HTTP/1.1 connections
// per host, and a page watches about ten kinds, so each ResourceWatcher asks the
// WatchMux instead of fetching its own watch. Watches asked for within a few
// milliseconds of each other share one GET /k8s-watch request (see server.mjs),
// and the answer is split back into one event stream per watch.

import type { K8sObject, K8sStatus } from './types'
import { ApiError, LineSplitter, type WatchEvent } from './watch'

/** One line of a /k8s-watch response. */
type MuxLine =
  { i: number; event: WatchEvent<K8sObject> } | { i: number; end: true; code: number; status?: Partial<K8sStatus> }

/** An async queue of events for one watch, ending normally or with an error. */
class Channel<T> implements AsyncIterableIterator<T> {
  private readonly items: T[] = []
  private waiting?: { resolve: (r: IteratorResult<T>) => void; reject: (e: unknown) => void }
  private done = false
  private error?: unknown
  /** Called when the reader stops early (break or return). */
  onReturn?: () => void

  push(item: T): void {
    if (this.done) return
    if (this.waiting) {
      this.waiting.resolve({ value: item, done: false })
      this.waiting = undefined
    } else this.items.push(item)
  }

  close(error?: unknown): void {
    if (this.done) return
    this.done = true
    this.error = error
    if (this.waiting && !this.items.length) {
      if (error) this.waiting.reject(error)
      else this.waiting.resolve({ value: undefined, done: true })
      this.waiting = undefined
    }
  }

  get closed(): boolean {
    return this.done
  }

  next(): Promise<IteratorResult<T>> {
    if (this.items.length) return Promise.resolve({ value: this.items.shift()!, done: false })
    if (this.done) return this.error ? Promise.reject(this.error) : Promise.resolve({ value: undefined, done: true })
    return new Promise((resolve, reject) => (this.waiting = { resolve, reject }))
  }

  return(): Promise<IteratorResult<T>> {
    this.close()
    this.onReturn?.()
    return Promise.resolve({ value: undefined, done: true })
  }

  [Symbol.asyncIterator](): AsyncIterableIterator<T> {
    return this
  }
}

interface Pending {
  path: string
  resourceVersion: string
  channel: Channel<WatchEvent<K8sObject>>
  signal: AbortSignal
}

export interface WatchMuxOptions {
  /** URL of the multiplexing endpoint, such as "k8s-watch" (relative to the page). */
  url: string
  fetch?: typeof fetch
  /**
   * How long to wait for more watches before sending a request. Each kind starts
   * watching when its list returns, so this should cover the spread of list
   * times; events in the meantime are not lost, the watch starts from the list's
   * resourceVersion.
   */
  batchMs?: number
}

export class WatchMux {
  private readonly url: string
  private readonly fetch: typeof fetch
  private readonly batchMs: number
  private pending: Pending[] = []
  private timer?: ReturnType<typeof setTimeout>
  /** Longest timeout asked for in the batch being collected. */
  private timeoutSeconds = 0

  constructor(options: WatchMuxOptions) {
    this.url = options.url
    this.fetch = (options.fetch ?? globalThis.fetch).bind(globalThis)
    this.batchMs = options.batchMs ?? 250
  }

  /**
   * Watches one resource from a resourceVersion. The events end when the API
   * ends the watch; an error status (410 Gone, 403 ...) is thrown as ApiError.
   */
  watch<T extends K8sObject>(
    path: string,
    resourceVersion: string,
    timeoutSeconds: number,
    signal: AbortSignal,
  ): AsyncIterable<WatchEvent<T>> {
    const channel = new Channel<WatchEvent<K8sObject>>()
    const abort = () => channel.close(new DOMException('Watch stopped', 'AbortError'))
    if (signal.aborted) abort()
    else signal.addEventListener('abort', abort, { once: true })
    this.pending.push({ path, resourceVersion, channel, signal })
    this.timeoutSeconds = Math.max(this.timeoutSeconds, timeoutSeconds)
    this.timer ??= setTimeout(() => this.flush(), this.batchMs)
    return channel as unknown as AsyncIterable<WatchEvent<T>>
  }

  private flush(): void {
    this.timer = undefined
    const batch = this.pending.filter((p) => !p.channel.closed)
    const timeoutSeconds = this.timeoutSeconds
    this.pending = []
    this.timeoutSeconds = 0
    if (batch.length) void this.run(batch, timeoutSeconds)
  }

  private async run(batch: Pending[], timeoutSeconds: number): Promise<void> {
    const controller = new AbortController()
    // Stop the request once nobody in it is listening any more.
    const release = () => {
      if (batch.every((p) => p.channel.closed)) controller.abort()
    }
    for (const p of batch) {
      p.signal.addEventListener('abort', release, { once: true })
      p.channel.onReturn = release
    }

    const query = new URLSearchParams()
    for (const p of batch) {
      query.append('w', p.path)
      query.append('rv', p.resourceVersion)
    }
    if (timeoutSeconds > 0) query.set('timeoutSeconds', String(timeoutSeconds))

    const fail = (error: unknown) => {
      for (const p of batch) p.channel.close(error)
    }
    try {
      const res = await this.fetch(`${this.url}?${query}`, {
        signal: controller.signal,
        headers: { Accept: 'application/x-ndjson' },
      })
      if (!res.ok || !res.body) {
        let message = `${res.status} ${res.statusText}`.trim()
        try {
          message = ((await res.json()) as Partial<K8sStatus>).message ?? message
        } catch {
          // Not a Status body.
        }
        return fail(new ApiError(res.status, message))
      }
      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      const lines = new LineSplitter()
      const dispatch = (line: string) => {
        const msg = JSON.parse(line) as MuxLine
        const target = batch[msg.i]
        if (!target) return
        if ('event' in msg) target.channel.push(msg.event)
        else if (msg.code === 200) target.channel.close()
        else target.channel.close(new ApiError(msg.code, msg.status?.message ?? `Watch failed with ${msg.code}`))
      }
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        lines.push(decoder.decode(value, { stream: true })).forEach(dispatch)
      }
      lines.push(decoder.decode()).forEach(dispatch)
      lines.flush().forEach(dispatch)
      // Anything the server did not end explicitly ended with the response.
      for (const p of batch) p.channel.close()
    } catch (err) {
      fail(err)
    } finally {
      for (const p of batch) p.signal.removeEventListener('abort', release)
    }
  }
}
