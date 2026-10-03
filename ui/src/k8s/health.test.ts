import { afterEach, describe, expect, it, vi } from 'vitest'
import { HealthSource, type FactoryHealth } from './health'

const HEALTH = { state: 'ok', components: [], nodes: [], errors: [] } as unknown as FactoryHealth

function respond(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

describe('HealthSource', () => {
  afterEach(() => vi.useRealTimers())

  it('polls while started and stops when stopped', async () => {
    vi.useFakeTimers()
    const fetchImpl = vi.fn(async () => respond(200, HEALTH))
    const source = new HealthSource('factory/health', { intervalMs: 1000, fetchImpl })
    const seen: unknown[] = []
    source.subscribe(() => seen.push(source.getSnapshot().data))
    source.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(source.getSnapshot()).toMatchObject({ data: HEALTH, connection: { phase: 'live', loaded: true } })
    await vi.advanceTimersByTimeAsync(2500)
    expect(fetchImpl).toHaveBeenCalledTimes(3)
    source.stop()
    await vi.advanceTimersByTimeAsync(5000)
    expect(fetchImpl).toHaveBeenCalledTimes(3)
    expect(seen.length).toBe(3)
  })

  it('keeps the last health and reports the problem when a read fails', async () => {
    const answers = [respond(200, HEALTH), respond(502, { message: 'Cannot reach the Kubernetes API' })]
    const source = new HealthSource('factory/health', { intervalMs: 60_000, fetchImpl: async () => answers.shift()! })
    source.start()
    await vi.waitFor(() => expect(source.getSnapshot().data).toEqual(HEALTH))
    await source.poll()
    expect(source.getSnapshot()).toMatchObject({
      data: HEALTH,
      connection: { phase: 'reconnecting', problems: [{ code: 502, message: 'Cannot reach the Kubernetes API' }] },
    })
    source.stop()
  })
})
