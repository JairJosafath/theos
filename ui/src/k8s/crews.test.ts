import { describe, expect, it } from 'vitest'
import { LiveCrewWriter, RERUN_RELEASE, releasePatch, releaseSettings, settledEdits } from './crews'
import type { Crew } from './types'

const base: Crew = {
  metadata: { name: 'orders-api', namespace: 'webshop' },
  spec: { containerRef: { name: 'orders-api' }, implementer: { name: 'i' }, reviewer: { name: 'r' } },
}
const publishing: Crew = {
  ...base,
  spec: {
    ...base.spec,
    tagReleases: true,
    publishReleases: { github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'gh' } } },
  },
}

describe('a Crew’s release settings as a patch', () => {
  it('sends nothing when nothing changed', () => {
    expect(releasePatch(base, releaseSettings(base))).toEqual([])
    expect(releasePatch(publishing, releaseSettings(publishing))).toEqual([])
  })

  it('sets only the switches that changed', () => {
    const next = { ...releaseSettings(base), prepareReleases: true }
    expect(releasePatch(base, next)).toEqual([{ op: 'add', path: '/spec/prepareReleases', value: true }])
  })

  it('names the token Secret and leaves an empty tag prefix unset', () => {
    const next = {
      tagReleases: true,
      prepareReleases: false,
      publish: { repository: 'webshop-co/orders-api', tokenSecret: 'orders-api-github', tagPrefix: '' },
    }
    expect(releasePatch(base, next)).toEqual([
      { op: 'add', path: '/spec/tagReleases', value: true },
      {
        op: 'add',
        path: '/spec/publishReleases',
        value: { github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'orders-api-github' } } },
      },
    ])
  })

  it('removes publishing turned off', () => {
    const next = { ...releaseSettings(publishing), tagReleases: false, publish: undefined }
    expect(releasePatch(publishing, next)).toEqual([
      { op: 'add', path: '/spec/tagReleases', value: false },
      { op: 'remove', path: '/spec/publishReleases' },
    ])
  })
})

describe('a form’s edits over a Crew that changes', () => {
  it('never writes back a field the person did not touch', () => {
    // The person ticks Prepare; meanwhile someone else turns tagging on.
    const edits = { prepareReleases: true }
    const changed: Crew = { ...base, spec: { ...base.spec, tagReleases: true } }
    expect(releasePatch(changed, { ...releaseSettings(changed), ...edits })).toEqual([
      { op: 'add', path: '/spec/prepareReleases', value: true },
    ])
  })

  it('drops the edits the Crew now has, so those fields follow it again', () => {
    const saved: Crew = { ...base, spec: { ...base.spec, prepareReleases: true } }
    expect(settledEdits(saved, { prepareReleases: true, tagReleases: true })).toEqual({ tagReleases: true })
    expect(settledEdits(publishing, { publish: releaseSettings(publishing).publish })).toEqual({})
    expect(settledEdits(base, { publish: undefined })).toEqual({})
    const kept = { tagReleases: false }
    expect(settledEdits(publishing, kept)).toBe(kept)
  })
})

describe('the Crew writer', () => {
  function writer(status = 200, body = '{}') {
    const sent: { url: string; init: RequestInit }[] = []
    const fetchImpl = (async (url: string, init: RequestInit) => {
      sent.push({ url, init })
      return new Response(body, { status })
    }) as unknown as typeof fetch
    return { sent, w: new LiveCrewWriter('k8s', fetchImpl) }
  }
  const url = 'k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/crews/orders-api'

  it('pauses, sets parallel and the release settings with JSON patches', async () => {
    const { sent, w } = writer()
    await w.setPaused('webshop', 'orders-api', true)
    await w.setParallel('webshop', 'orders-api', 3)
    await w.setReleases('webshop', 'orders-api', [{ op: 'remove', path: '/spec/publishReleases' }])
    await w.setReleases('webshop', 'orders-api', [])
    expect(sent.map((s) => [s.url, s.init.method, (s.init.headers as Record<string, string>)['Content-Type']])).toEqual(
      [
        [url, 'PATCH', 'application/json-patch+json'],
        [url, 'PATCH', 'application/json-patch+json'],
        [url, 'PATCH', 'application/json-patch+json'],
      ],
    )
    expect(JSON.parse(sent[0].init.body as string)).toEqual([{ op: 'add', path: '/spec/paused', value: true }])
    expect(JSON.parse(sent[1].init.body as string)).toEqual([{ op: 'add', path: '/spec/parallel', value: 3 }])
  })

  it('asks for a re-run with a merge patch of the annotation', async () => {
    const { sent, w } = writer()
    await w.rerunRelease('webshop', 'orders-api', 'v1')
    expect((sent[0].init.headers as Record<string, string>)['Content-Type']).toBe('application/merge-patch+json')
    expect(JSON.parse(sent[0].init.body as string)).toEqual({ metadata: { annotations: { [RERUN_RELEASE]: 'v1' } } })
  })

  it('rejects with the API server’s message', async () => {
    const message = 'spec.parallel: Invalid value: 9: should be less than or equal to 5'
    const { w } = writer(422, JSON.stringify({ kind: 'Status', code: 422, message }))
    await expect(w.setParallel('webshop', 'orders-api', 9)).rejects.toThrow(message)
  })
})
