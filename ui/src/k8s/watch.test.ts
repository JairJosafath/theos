import { describe, expect, it } from 'vitest'
import type { K8sObject } from './types'
import {
  applyWatchEvent,
  backoffDelay,
  LineSplitter,
  objectKey,
  parseWatchEvent,
  readWatchEvents,
  ResourceWatcher,
  type WatchEvent,
  type WatchState,
} from './watch'

const obj = (name: string, rv: string): K8sObject => ({ metadata: { name, namespace: 'webshop', resourceVersion: rv } })
const line = (type: string, o: unknown) => `${JSON.stringify({ type, object: o })}\n`

/** A ReadableStream that yields the given chunks, then optionally stays open. */
function streamOf(chunks: string[], keepOpen = false): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder()
  return new ReadableStream({
    start(controller) {
      for (const c of chunks) controller.enqueue(encoder.encode(c))
      if (!keepOpen) controller.close()
    },
  })
}

describe('LineSplitter', () => {
  it('joins lines split across chunks and skips blank lines', () => {
    const s = new LineSplitter()
    expect(s.push('{"a":')).toEqual([])
    expect(s.push('1}\n\n{"b"')).toEqual(['{"a":1}'])
    expect(s.push(':2}\n{"c":3}')).toEqual(['{"b":2}'])
    expect(s.flush()).toEqual(['{"c":3}'])
    expect(s.flush()).toEqual([])
  })
})

describe('parseWatchEvent', () => {
  it('parses every event type', () => {
    for (const type of ['ADDED', 'MODIFIED', 'DELETED', 'BOOKMARK']) {
      const e = parseWatchEvent(line(type, obj('a', '1')).trim())
      expect(e.type).toBe(type)
      expect((e.object as K8sObject).metadata).toMatchObject({ name: 'a' })
    }
    const err = parseWatchEvent(line('ERROR', { kind: 'Status', code: 410, reason: 'Expired' }).trim())
    expect(err).toEqual({ type: 'ERROR', object: { kind: 'Status', code: 410, reason: 'Expired' } })
  })

  it('rejects lines that are not watch events', () => {
    expect(() => parseWatchEvent('not json')).toThrow(/not JSON/)
    expect(() => parseWatchEvent('42')).toThrow(/not an event/)
    expect(() => parseWatchEvent('{"type":"WHATEVER","object":{}}')).toThrow(/unknown event/)
    expect(() => parseWatchEvent('{"type":"ADDED"}')).toThrow(/unknown event/)
  })
})

describe('readWatchEvents', () => {
  it('reads events, including a multi-byte character split across chunks', async () => {
    const text = line('ADDED', { metadata: { name: 'café' } }) + line('DELETED', obj('b', '3'))
    const bytes = new TextEncoder().encode(text)
    const cut = text.indexOf('é') + 1 // inside the two bytes of "é"
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        c.enqueue(bytes.slice(0, cut))
        c.enqueue(bytes.slice(cut))
        c.close()
      },
    })
    const events: WatchEvent<K8sObject>[] = []
    for await (const e of readWatchEvents(body)) events.push(e)
    expect(events.map((e) => e.type)).toEqual(['ADDED', 'DELETED'])
    expect((events[0].object as K8sObject).metadata.name).toBe('café')
  })
})

describe('applyWatchEvent', () => {
  it('adds, replaces and deletes by namespace and name', () => {
    const items = new Map<string, K8sObject>()
    expect(applyWatchEvent(items, { type: 'ADDED', object: obj('a', '1') })).toBe(true)
    expect(applyWatchEvent(items, { type: 'MODIFIED', object: obj('a', '2') })).toBe(true)
    expect(items.get(objectKey(obj('a', '0')))?.metadata.resourceVersion).toBe('2')
    expect(applyWatchEvent(items, { type: 'BOOKMARK', object: obj('', '9') })).toBe(false)
    expect(applyWatchEvent(items, { type: 'DELETED', object: obj('a', '3') })).toBe(true)
    expect(applyWatchEvent(items, { type: 'DELETED', object: obj('a', '3') })).toBe(false)
    expect(items.size).toBe(0)
  })
})

describe('backoffDelay', () => {
  it('doubles up to the cap, with at most 20% jitter', () => {
    const none = () => 1
    expect([1, 2, 3, 4].map((a) => backoffDelay(a, 1000, 30000, none))).toEqual([1000, 2000, 4000, 8000])
    expect(backoffDelay(20, 1000, 30000, none)).toBe(30000)
    expect(backoffDelay(1, 1000, 30000, () => 0)).toBe(800)
  })
})

/**
 * A fake API: answers requests in order from a script. Each entry checks the URL
 * and returns a Response. Requests beyond the script hang until aborted.
 */
function fakeFetch(script: ((url: URL) => Response)[]) {
  const urls: URL[] = []
  const fn = (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input), 'http://ui.test/')
    urls.push(url)
    const step = script.shift()
    if (step) return Promise.resolve(step(url))
    return new Promise((_, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
    })
  }
  return { fetch: fn as typeof fetch, urls }
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
const list = (rv: string, items: K8sObject[]) => json({ metadata: { resourceVersion: rv }, items })
const watchStream = (chunks: string[], keepOpen = false) => new Response(streamOf(chunks, keepOpen), { status: 200 })

/** Resolves once the watcher reaches a state the predicate accepts. */
function until<T extends K8sObject>(w: ResourceWatcher<T>, ok: (s: WatchState<T>) => boolean): Promise<WatchState<T>> {
  return new Promise((resolve) => {
    if (ok(w.getState())) return resolve(w.getState())
    const off = w.subscribe((s) => {
      if (ok(s)) {
        off()
        resolve(s)
      }
    })
  })
}

/** Polls until a condition holds, for requests the watcher makes after a state change. */
async function waitFor(ok: () => boolean): Promise<void> {
  for (let i = 0; i < 100 && !ok(); i++) await new Promise((r) => setTimeout(r, 5))
  expect(ok()).toBe(true)
}

const names = (s: WatchState<K8sObject>) => s.items.map((i) => i.metadata.name).sort()
const noSleep = () => Promise.resolve()

describe('ResourceWatcher', () => {
  const path = '/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers'

  it('lists, then applies watch events from the list resourceVersion', async () => {
    const api = fakeFetch([
      () => list('10', [obj('a', '5'), obj('b', '6')]),
      () =>
        watchStream(
          [line('MODIFIED', obj('a', '11')), line('ADDED', obj('c', '12')), line('DELETED', obj('b', '13'))],
          true,
        ),
    ])
    const w = new ResourceWatcher<K8sObject>({ baseUrl: 'k8s', path, fetch: api.fetch, sleep: noSleep })
    w.start()
    const state = await until(w, (s) => names(s).join() === 'a,c')
    w.stop()

    expect(state.phase).toBe('live')
    expect(state.loaded).toBe(true)
    expect(api.urls[0].pathname).toBe(`/k8s${path}`)
    expect(api.urls[0].search).toBe('')
    expect(api.urls[1].searchParams.get('watch')).toBe('1')
    expect(api.urls[1].searchParams.get('resourceVersion')).toBe('10')
    expect(api.urls[1].searchParams.get('allowWatchBookmarks')).toBe('true')
    expect(state.items.find((i) => i.metadata.name === 'a')?.metadata.resourceVersion).toBe('11')
  })

  it('re-watches from the last resourceVersion (bookmarks included) when the server ends the stream', async () => {
    const api = fakeFetch([
      () => list('10', []),
      () =>
        watchStream([
          line('ADDED', obj('a', '11')),
          line('BOOKMARK', { metadata: { name: '', resourceVersion: '15' } }),
        ]),
      () => watchStream([line('ADDED', obj('b', '16'))], true),
    ])
    const w = new ResourceWatcher<K8sObject>({ baseUrl: 'k8s', path, fetch: api.fetch, sleep: noSleep })
    w.start()
    await until(w, (s) => names(s).join() === 'a,b')
    w.stop()
    expect(api.urls[2].searchParams.get('resourceVersion')).toBe('15')
    // No relist: the second watch continued where the first ended.
    expect(api.urls.filter((u) => !u.searchParams.has('watch'))).toHaveLength(1)
  })

  it('relists when the watch answers 410 Gone, dropping objects deleted meanwhile', async () => {
    const api = fakeFetch([
      () => list('10', [obj('a', '5'), obj('b', '6')]),
      () => json({ kind: 'Status', code: 410, message: 'too old resource version' }, 410),
      () => list('20', [obj('b', '6')]),
      () => watchStream([], true),
    ])
    const w = new ResourceWatcher<K8sObject>({ baseUrl: 'k8s', path, fetch: api.fetch, sleep: noSleep })
    w.start()
    const state = await until(w, (s) => names(s).join() === 'b')
    w.stop()
    expect(state.phase).toBe('live')
    expect(api.urls.filter((u) => !u.searchParams.has('watch'))).toHaveLength(2)
  })

  it('relists after an ERROR event with code 410 inside the stream', async () => {
    const api = fakeFetch([
      () => list('10', [obj('a', '5')]),
      () => watchStream([line('ERROR', { kind: 'Status', code: 410, reason: 'Expired', message: 'too old' })]),
      () => list('30', [obj('z', '29')]),
      () => watchStream([], true),
    ])
    const w = new ResourceWatcher<K8sObject>({ baseUrl: 'k8s', path, fetch: api.fetch, sleep: noSleep })
    w.start()
    await until(w, (s) => names(s).join() === 'z')
    await waitFor(() => api.urls.length === 4)
    w.stop()
    expect(api.urls[3].searchParams.get('resourceVersion')).toBe('30')
  })

  it('reports errors in plain terms and backs off before retrying', async () => {
    const sleeps: number[] = []
    const api = fakeFetch([
      () => json({ kind: 'Status', code: 403, message: 'containers is forbidden' }, 403),
      () => new Response('bad gateway', { status: 502, statusText: 'Bad Gateway' }),
      () => list('10', [obj('a', '1')]),
      () => watchStream([], true),
    ])
    const states: WatchState<K8sObject>[] = []
    const w = new ResourceWatcher<K8sObject>({
      baseUrl: 'k8s',
      path,
      fetch: api.fetch,
      backoff: { initialMs: 100, maxMs: 1000 },
      sleep: (ms) => {
        sleeps.push(ms)
        return Promise.resolve()
      },
    })
    w.subscribe((s) => states.push(s))
    w.start()
    await until(w, (s) => s.loaded)
    w.stop()

    expect(states[0]).toMatchObject({ phase: 'connecting', loaded: false })
    expect(states[0].error?.message).toBe('containers is forbidden')
    expect((states[0].error as { code?: number }).code).toBe(403)
    expect(states[1].error?.message).toBe('502 Bad Gateway')
    expect(sleeps).toHaveLength(2)
    expect(sleeps[1]).toBeGreaterThan(sleeps[0])
    expect(w.getState().error).toBeUndefined()
  })

  it('shows "reconnecting" when a live watch fails, and keeps the items it had', async () => {
    let failWatch!: () => void
    const api = fakeFetch([
      () => list('10', [obj('a', '1')]),
      () =>
        new Response(
          new ReadableStream({
            start(c) {
              failWatch = () => c.error(new TypeError('network error'))
            },
          }),
        ),
    ])
    const w = new ResourceWatcher<K8sObject>({
      baseUrl: 'k8s',
      path,
      fetch: api.fetch,
      sleep: () => new Promise(() => {}),
    })
    w.start()
    await until(w, (s) => s.loaded)
    await new Promise((r) => setTimeout(r, 0))
    failWatch()
    const state = await until(w, (s) => s.phase === 'reconnecting')
    w.stop()
    expect(names(state)).toEqual(['a'])
    expect(state.error?.message).toMatch(/network error/)
  })
})
