import { describe, expect, it, vi } from 'vitest'
import { LiveStandardWriter, refPatch } from './standards'

function respond(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

describe('refPatch', () => {
  it('sets only spec.template.ref, as typed', () => {
    expect(refPatch('v1.3.0')).toEqual([{ op: 'add', path: '/spec/template/ref', value: 'v1.3.0' }])
    expect(refPatch('')).toEqual([{ op: 'add', path: '/spec/template/ref', value: '' }])
  })
})

describe('LiveStandardWriter', () => {
  it('bumps the ref with a JSON patch on the Standard', async () => {
    const fetchImpl = vi.fn(async () => respond(200, {}))
    await new LiveStandardWriter('k8s', fetchImpl).bumpRef('webshop', 'python-standard', 'v1.3.0')
    expect(fetchImpl).toHaveBeenCalledWith(
      'k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/standards/python-standard',
      {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json-patch+json', Accept: 'application/json' },
        body: JSON.stringify(refPatch('v1.3.0')),
      },
    )
  })

  it('rejects with the API server’s message when it refuses', async () => {
    const message = 'spec.template.ref in body should be at least 1 chars long'
    const fetchImpl = vi.fn(async () => respond(422, { kind: 'Status', code: 422, reason: 'Invalid', message }))
    await expect(new LiveStandardWriter('k8s', fetchImpl).bumpRef('webshop', 'python-standard', '')).rejects.toThrow(
      message,
    )
  })
})
