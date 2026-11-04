// The Kubernetes list-then-watch protocol, with no React in it.
//
// A watch response is a stream of newline-delimited JSON events. ResourceWatcher
// lists a resource, then watches from the list's resourceVersion, applies each
// event to an in-memory map, and keeps going: it re-watches when the server ends
// the stream, relists when the resourceVersion has expired (410 Gone), and backs
// off on errors.

import type { K8sList, K8sObject, K8sStatus } from './types'

export type WatchEventType = 'ADDED' | 'MODIFIED' | 'DELETED' | 'BOOKMARK' | 'ERROR'

export type WatchEvent<T extends K8sObject> =
  { type: 'ADDED' | 'MODIFIED' | 'DELETED' | 'BOOKMARK'; object: T } | { type: 'ERROR'; object: K8sStatus }

const EVENT_TYPES = new Set<string>(['ADDED', 'MODIFIED', 'DELETED', 'BOOKMARK', 'ERROR'])

/**
 * Splits streamed text into complete lines. A chunk may end mid-line, so the
 * remainder is kept until the next chunk (or flush) completes it.
 */
export class LineSplitter {
  private rest = ''

  push(text: string): string[] {
    this.rest += text
    const lines = this.rest.split('\n')
    this.rest = lines.pop() ?? ''
    return lines.filter((line) => line.trim() !== '')
  }

  /** Returns what is left once the stream has ended. */
  flush(): string[] {
    const last = this.rest
    this.rest = ''
    return last.trim() === '' ? [] : [last]
  }
}

/** Parses one line of a watch stream. Throws on anything that isn't a watch event. */
export function parseWatchEvent<T extends K8sObject>(line: string): WatchEvent<T> {
  let value: unknown
  try {
    value = JSON.parse(line)
  } catch {
    throw new Error(`Watch stream sent a line that is not JSON: ${line.slice(0, 80)}`)
  }
  if (typeof value !== 'object' || value === null) {
    throw new Error('Watch stream sent a value that is not an event')
  }
  const { type, object } = value as { type?: unknown; object?: unknown }
  if (typeof type !== 'string' || !EVENT_TYPES.has(type) || typeof object !== 'object' || object === null) {
    throw new Error(`Watch stream sent an unknown event: ${line.slice(0, 80)}`)
  }
  return { type, object } as WatchEvent<T>
}

/** Reads a watch response body as a sequence of events. */
export async function* readWatchEvents<T extends K8sObject>(
  body: ReadableStream<Uint8Array>,
): AsyncGenerator<WatchEvent<T>> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  const lines = new LineSplitter()
  try {
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      for (const line of lines.push(decoder.decode(value, { stream: true }))) {
        yield parseWatchEvent<T>(line)
      }
    }
    for (const line of [...lines.push(decoder.decode()), ...lines.flush()]) {
      yield parseWatchEvent<T>(line)
    }
  } finally {
    // Cancelling also tells fetch to drop the connection if we stopped early.
    await reader.cancel().catch(() => undefined)
  }
}

/** Stable key of an object within one list: namespace and name. */
export function objectKey(obj: K8sObject): string {
  return `${obj.metadata.namespace ?? ''}/${obj.metadata.name}`
}

/**
 * Applies an ADDED, MODIFIED or DELETED event to a map of objects, in place.
 * Returns true when the map changed. BOOKMARK and ERROR events change nothing.
 */
export function applyWatchEvent<T extends K8sObject>(items: Map<string, T>, event: WatchEvent<T>): boolean {
  switch (event.type) {
    case 'ADDED':
    case 'MODIFIED':
      items.set(objectKey(event.object), event.object)
      return true
    case 'DELETED':
      return items.delete(objectKey(event.object))
    default:
      return false
  }
}

/** Exponential backoff with jitter: about 1 s, 2 s, 4 s ... up to maxMs. */
export function backoffDelay(attempt: number, initialMs = 1000, maxMs = 30000, random = Math.random): number {
  const base = Math.min(maxMs, initialMs * 2 ** Math.max(0, attempt - 1))
  // Up to 20% jitter, so many browsers don't reconnect in lockstep.
  return Math.round(base * (0.8 + 0.2 * random()))
}

/** An HTTP error from the Kubernetes API, with its status code. */
export class ApiError extends Error {
  readonly code: number

  constructor(code: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.code = code
  }
}

export type WatchPhase = 'connecting' | 'live' | 'reconnecting'

export interface WatchState<T> {
  items: T[]
  phase: WatchPhase
  /** Set when the last attempt failed; the watcher keeps retrying. */
  error?: ApiError | Error
  /** Set once the first list has succeeded. */
  loaded: boolean
  /**
   * What the API server warned about the last list, from the response's
   * Warning header: a request at a deprecated version, such as
   * theos.factory.io/v1alpha1 (#1618). Unset when it warned about nothing.
   */
  warning?: string
}

/**
 * The texts of a Warning header's entries (`299 - "text"`, comma-separated
 * when several), without their codes and agents; the API server sends one
 * entry per warning, text in quotes.
 */
export function parseWarnings(header: string | null): string[] {
  const texts: string[] = []
  if (!header) return texts
  for (const m of header.matchAll(/\d{3}\s+\S+\s+"((?:[^"\\]|\\.)*)"/g)) {
    const text = m[1].replace(/\\(.)/g, '$1').trim()
    if (text) texts.push(text)
  }
  return texts
}

/** Opens a watch from a resourceVersion; throws ApiError for error statuses (410 Gone ...). */
export type OpenWatch = (
  path: string,
  resourceVersion: string,
  timeoutSeconds: number,
  signal: AbortSignal,
) => AsyncIterable<WatchEvent<K8sObject>>

export interface WatcherOptions {
  /** Base URL of the API proxy, such as "k8s" (relative to the page) or "/k8s". */
  baseUrl: string
  /** API path of the resource, such as /apis/theos.factory.io/v1beta1/namespaces/webshop/containers. */
  path: string
  fetch?: typeof fetch
  /**
   * How to open a watch. By default, one fetch per watch; a WatchMux shares one
   * connection between many watches.
   */
  openWatch?: OpenWatch
  /** Seconds the server keeps one watch open before it ends it cleanly. */
  timeoutSeconds?: number
  backoff?: { initialMs: number; maxMs: number }
  sleep?: (ms: number, signal: AbortSignal) => Promise<void>
}

function abortableSleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(done, ms)
    signal.addEventListener('abort', done, { once: true })
    function done() {
      clearTimeout(timer)
      signal.removeEventListener('abort', done)
      resolve()
    }
  })
}

export async function errorFromResponse(res: Response): Promise<ApiError> {
  let message = `${res.status} ${res.statusText}`.trim()
  try {
    const text = await res.text()
    const status = JSON.parse(text) as Partial<K8sStatus>
    if (status.message) message = status.message
  } catch {
    // Not a Kubernetes Status; keep the HTTP status line.
  }
  return new ApiError(res.status, message)
}

/**
 * Keeps an up-to-date copy of one resource list through list and watch.
 * Subscribers get a new WatchState after every change.
 */
export class ResourceWatcher<T extends K8sObject> {
  private readonly opts: Required<Omit<WatcherOptions, 'fetch' | 'openWatch'>> & {
    fetch: typeof fetch
    openWatch?: OpenWatch
  }
  private readonly items = new Map<string, T>()
  private readonly listeners = new Set<(state: WatchState<T>) => void>()
  private state: WatchState<T> = { items: [], phase: 'connecting', loaded: false }
  private abort?: AbortController

  constructor(options: WatcherOptions) {
    this.opts = {
      timeoutSeconds: 300,
      backoff: { initialMs: 1000, maxMs: 30000 },
      sleep: abortableSleep,
      ...options,
      // Bind, so a stubbed or native fetch is never called with the wrong `this`.
      fetch: (options.fetch ?? globalThis.fetch).bind(globalThis),
    }
  }

  getState(): WatchState<T> {
    return this.state
  }

  subscribe(listener: (state: WatchState<T>) => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  start(): void {
    if (this.abort) return
    this.abort = new AbortController()
    void this.run(this.abort.signal)
  }

  stop(): void {
    this.abort?.abort()
    this.abort = undefined
  }

  private update(patch: Partial<WatchState<T>>, itemsChanged = false): void {
    this.state = {
      ...this.state,
      ...patch,
      items: itemsChanged ? [...this.items.values()] : this.state.items,
    }
    for (const listener of this.listeners) listener(this.state)
  }

  private url(query: Record<string, string>): string {
    const params = new URLSearchParams(query).toString()
    return `${this.opts.baseUrl}${this.opts.path}${params ? `?${params}` : ''}`
  }

  private async list(signal: AbortSignal): Promise<string> {
    const res = await this.opts.fetch(this.url({}), { signal, headers: { Accept: 'application/json' } })
    if (!res.ok) throw await errorFromResponse(res)
    const list = (await res.json()) as K8sList<T>
    // A relist replaces everything, which also drops objects deleted while we
    // weren't watching.
    this.items.clear()
    for (const item of list.items ?? []) this.items.set(objectKey(item), item)
    const warning = parseWarnings(res.headers.get('warning')).join(' ') || undefined
    this.update({ phase: 'live', error: undefined, loaded: true, warning }, true)
    return list.metadata?.resourceVersion ?? ''
  }

  /** One watch over its own fetch: the default when there is no WatchMux. */
  private async *fetchWatch(resourceVersion: string, signal: AbortSignal): AsyncGenerator<WatchEvent<K8sObject>> {
    const res = await this.opts.fetch(
      this.url({
        watch: '1',
        resourceVersion,
        allowWatchBookmarks: 'true',
        timeoutSeconds: String(this.opts.timeoutSeconds),
      }),
      { signal, headers: { Accept: 'application/json' } },
    )
    if (!res.ok || !res.body) throw await errorFromResponse(res)
    yield* readWatchEvents<K8sObject>(res.body)
  }

  private async run(signal: AbortSignal): Promise<void> {
    // resourceVersion to watch from; undefined means "list first".
    let resourceVersion: string | undefined
    let failures = 0
    const pause = () =>
      this.opts.sleep(backoffDelay(failures, this.opts.backoff.initialMs, this.opts.backoff.maxMs), signal)

    while (!signal.aborted) {
      try {
        if (resourceVersion === undefined) {
          resourceVersion = await this.list(signal)
        }

        const events = this.opts.openWatch
          ? this.opts.openWatch(this.opts.path, resourceVersion, this.opts.timeoutSeconds, signal)
          : this.fetchWatch(resourceVersion, signal)
        const openedAt = Date.now()
        let gone = false
        try {
          for await (const event of events as AsyncIterable<WatchEvent<T>>) {
            if (event.type === 'ERROR') {
              if (event.object.code === 410) {
                gone = true
                break
              }
              throw new ApiError(event.object.code ?? 500, event.object.message ?? 'Watch failed')
            }
            if (event.object.metadata?.resourceVersion) resourceVersion = event.object.metadata.resourceVersion
            if (applyWatchEvent(this.items, event)) this.update({}, true)
          }
        } catch (err) {
          if (!(err instanceof ApiError && err.code === 410)) throw err
          gone = true
        }

        if (gone) {
          // The resourceVersion is too old to watch from: start over with a list.
          resourceVersion = undefined
          failures++
          if (failures > 1) await pause()
          continue
        }
        // The server ended the watch, usually at timeoutSeconds: watch again from
        // where we are. A watch that ends at once counts as a failure, so a
        // misbehaving proxy can't make us spin.
        if (Date.now() - openedAt < 1000) {
          failures++
          await pause()
        } else {
          failures = 0
        }
      } catch (err) {
        if (signal.aborted) return
        failures++
        const error = err instanceof Error ? err : new Error(String(err))
        this.update({ phase: this.state.loaded ? 'reconnecting' : 'connecting', error })
        // List again once we are back: whatever changed meanwhile is then in,
        // and a successful list is what tells us the connection is good.
        resourceVersion = undefined
        await pause()
      }
    }
  }
}
