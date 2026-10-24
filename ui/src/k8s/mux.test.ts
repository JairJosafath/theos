import { describe, expect, it } from 'vitest'
import { WatchMux } from './mux'
import type { K8sObject } from './types'
import { ApiError, ResourceWatcher, type WatchEvent } from './watch'

const obj = (name: string, rv: string): K8sObject => ({ metadata: { name, resourceVersion: rv } })

/** A /k8s-watch response the test writes to line by line. */
function controllableResponse() {
  const encoder = new TextEncoder()
  let controller!: ReadableStreamDefaultController<Uint8Array>
  const body = new ReadableStream<Uint8Array>({ start: (c) => void (controller = c) })
  return {
    response: new Response(body, { status: 200 }),
    send: (value: unknown) => controller.enqueue(encoder.encode(`${JSON.stringify(value)}\n`)),
    end: () => controller.close(),
    fail: (err: Error) => controller.error(err),
  }
}

async function collect<T>(it: AsyncIterable<T>): Promise<T[]> {
  const out: T[] = []
  for await (const x of it) out.push(x)
  return out
}

describe('WatchMux', () => {
  it('sends watches asked for together as one request and splits the answer', async () => {
    const urls: URL[] = []
    const stream = controllableResponse()
    const mux = new WatchMux({
      url: 'k8s-watch',
      batchMs: 5,
      fetch: ((input: RequestInfo | URL) => {
        urls.push(new URL(String(input), 'http://ui.test/'))
        return Promise.resolve(stream.response)
      }) as typeof fetch,
    })
    const signal = new AbortController().signal
    const a = collect(mux.watch('/apis/c4.factory.io/v1beta1/namespaces/w/containers', '10', 300, signal))
    const b = collect(mux.watch('/apis/c4.factory.io/v1beta1/namespaces/w/sessions', '20', 300, signal))
    await new Promise((r) => setTimeout(r, 20))

    expect(urls).toHaveLength(1)
    expect(urls[0].pathname).toBe('/k8s-watch')
    expect(urls[0].searchParams.getAll('w')).toEqual([
      '/apis/c4.factory.io/v1beta1/namespaces/w/containers',
      '/apis/c4.factory.io/v1beta1/namespaces/w/sessions',
    ])
    expect(urls[0].searchParams.getAll('rv')).toEqual(['10', '20'])
    expect(urls[0].searchParams.get('timeoutSeconds')).toBe('300')

    stream.send({ i: 1, event: { type: 'ADDED', object: obj('s1', '21') } })
    stream.send({ i: 0, event: { type: 'MODIFIED', object: obj('c1', '11') } })
    stream.send({ i: 0, end: true, code: 200 })
    stream.send({
      i: 1,
      end: true,
      code: 410,
      status: { kind: 'Status', code: 410, message: 'too old resource version' },
    })
    stream.end()

    expect((await a).map((e) => e.type)).toEqual(['MODIFIED'])
    await expect(b).rejects.toEqual(new ApiError(410, 'too old resource version'))
  })

  it('fails every watch in a request when the connection breaks', async () => {
    const stream = controllableResponse()
    const mux = new WatchMux({
      url: 'k8s-watch',
      batchMs: 1,
      fetch: (() => Promise.resolve(stream.response)) as typeof fetch,
    })
    const signal = new AbortController().signal
    const a = collect(mux.watch('/apis/c4.factory.io/v1beta1/a', '1', 300, signal))
    const b = collect(mux.watch('/apis/c4.factory.io/v1beta1/b', '1', 300, signal))
    await new Promise((r) => setTimeout(r, 10))
    stream.fail(new TypeError('network error'))
    await expect(a).rejects.toThrow('network error')
    await expect(b).rejects.toThrow('network error')
  })

  it('turns an HTTP error on the request into an ApiError for each watch', async () => {
    const mux = new WatchMux({
      url: 'k8s-watch',
      batchMs: 1,
      fetch: (() =>
        Promise.resolve(new Response('{"kind":"Status","message":"nope"}', { status: 403 }))) as typeof fetch,
    })
    await expect(
      collect(mux.watch('/apis/c4.factory.io/v1beta1/a', '1', 300, new AbortController().signal)),
    ).rejects.toEqual(new ApiError(403, 'nope'))
  })

  it('aborts the request once every watch in it has stopped', async () => {
    let aborted = false
    const mux = new WatchMux({
      url: 'k8s-watch',
      batchMs: 1,
      fetch: ((_: unknown, init?: RequestInit) => {
        init?.signal?.addEventListener('abort', () => (aborted = true))
        return Promise.resolve(controllableResponse().response)
      }) as typeof fetch,
    })
    const one = new AbortController()
    const two = new AbortController()
    const a = collect(mux.watch('/apis/c4.factory.io/v1beta1/a', '1', 300, one.signal)).catch(() => 'stopped')
    const b = collect(mux.watch('/apis/c4.factory.io/v1beta1/b', '1', 300, two.signal)).catch(() => 'stopped')
    await new Promise((r) => setTimeout(r, 10))
    one.abort()
    await a
    expect(aborted).toBe(false)
    two.abort()
    await b
    expect(aborted).toBe(true)
  })

  it('drives ResourceWatchers: ten kinds, one connection', async () => {
    const lists = (url: string) =>
      Promise.resolve(
        new Response(JSON.stringify({ metadata: { resourceVersion: '5' }, items: [obj(url.split('/').pop()!, '5')] })),
      )
    const muxRequests: URL[] = []
    const stream = controllableResponse()
    const fetchFn = ((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.startsWith('k8s-watch')) {
        muxRequests.push(new URL(url, 'http://ui.test/'))
        return Promise.resolve(stream.response)
      }
      return lists(url)
    }) as typeof fetch
    const mux = new WatchMux({ url: 'k8s-watch', batchMs: 20, fetch: fetchFn })
    const watchers = Array.from({ length: 10 }, (_, i) => {
      const w = new ResourceWatcher<K8sObject>({
        baseUrl: 'k8s',
        path: `/apis/c4.factory.io/v1beta1/namespaces/w/kind${i}`,
        fetch: fetchFn,
        openWatch: (path, rv, timeout, signal) => mux.watch(path, rv, timeout, signal),
      })
      w.start()
      return w
    })
    await new Promise((r) => setTimeout(r, 60))
    expect(muxRequests).toHaveLength(1)
    expect(muxRequests[0].searchParams.getAll('w')).toHaveLength(10)

    const event: WatchEvent<K8sObject> = { type: 'ADDED', object: obj('new', '6') }
    stream.send({ i: 3, event })
    await new Promise((r) => setTimeout(r, 10))
    expect(
      watchers[3]
        .getState()
        .items.map((o) => o.metadata.name)
        .sort(),
    ).toEqual(['kind3', 'new'])
    expect(watchers[4].getState().items).toHaveLength(1)
    watchers.forEach((w) => w.stop())
  })
})
