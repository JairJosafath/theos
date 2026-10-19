import { describe, expect, it, vi } from 'vitest'
import { LiveContainerWriter, checksPatch, retryPatch, standardAnswerPatch, taskManifest } from './containers'
import type { Crew } from './types'

function respond(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

const crew: Crew = {
  metadata: { name: 'orders-api', namespace: 'webshop' },
  spec: { containerRef: { name: 'orders-api' }, implementer: { name: 'claude' }, reviewer: { name: 'codex' } },
}
const at = new Date('2026-10-09T03:00:00Z')

describe('retryPatch and taskManifest', () => {
  it('sets only the retry annotation, to the time', () => {
    expect(retryPatch(at)).toEqual({ metadata: { annotations: { 'c4.factory.io/retry': '2026-10-09T03:00:00.000Z' } } })
  })

  it('sets only the take or the keep annotation, to the Standard’s ref', () => {
    expect(standardAnswerPatch('take', 'v1.4.0')).toEqual({
      metadata: { annotations: { 'c4.factory.io/standard-take': 'v1.4.0' } },
    })
    expect(standardAnswerPatch('keep', 'v1.4.0')).toEqual({
      metadata: { annotations: { 'c4.factory.io/standard-keep': 'v1.4.0' } },
    })
  })

  it('makes an Implement Task for the Container with its Crew’s Agents, named by the API server', () => {
    expect(taskManifest('webshop', 'orders-api', crew, 'Add pagination')).toEqual({
      apiVersion: 'c4.factory.io/v1alpha1',
      kind: 'Task',
      metadata: { generateName: 'orders-api-', namespace: 'webshop' },
      spec: {
        kind: 'Implement',
        containerRef: { name: 'orders-api' },
        agentRef: { name: 'claude' },
        reviewerRef: { name: 'codex' },
        goal: 'Add pagination',
      },
    })
  })

  it('sends an empty goal as it is, for the CRD to judge', () => {
    expect(taskManifest('webshop', 'orders-api', crew, '').spec.goal).toBe('')
  })
})

describe('LiveContainerWriter', () => {
  it('retries with a merge patch on the Container', async () => {
    const fetchImpl = vi.fn(async () => respond(200, {}))
    await new LiveContainerWriter('k8s', fetchImpl, () => at).retry('webshop', 'orders-api')
    expect(fetchImpl).toHaveBeenCalledWith('k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers/orders-api', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json', Accept: 'application/json' },
      body: JSON.stringify(retryPatch(at)),
    })
  })

  it('answers the Standard’s files left out with a merge patch on the Container', async () => {
    const fetchImpl = vi.fn(async () => respond(200, {}))
    await new LiveContainerWriter('k8s', fetchImpl).answerStandard('webshop', 'orders-api', 'keep', 'v1.4.0')
    expect(fetchImpl).toHaveBeenCalledWith('k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers/orders-api', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json', Accept: 'application/json' },
      body: JSON.stringify(standardAnswerPatch('keep', 'v1.4.0')),
    })
  })

  it('sets the checks with a JSON patch of spec.checks alone', async () => {
    const fetchImpl = vi.fn(async () => respond(200, {}))
    await new LiveContainerWriter('k8s', fetchImpl).setChecks('webshop', 'orders-api', 'BeforeRelease')
    expect(fetchImpl).toHaveBeenCalledWith('k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers/orders-api', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json-patch+json', Accept: 'application/json' },
      body: JSON.stringify([{ op: 'add', path: '/spec/checks', value: 'BeforeRelease' }]),
    })
    expect(checksPatch('EveryChange')).toEqual([{ op: 'add', path: '/spec/checks', value: 'EveryChange' }])
  })

  it('approves a deploy with a JSON patch of one entry’s approved, after a test of its target (#1163)', async () => {
    const fetchImpl = vi.fn(async () => respond(200, {}))
    await new LiveContainerWriter('k8s', fetchImpl).approveDeploy(
      'webshop',
      'orders-api',
      1,
      'prod',
      'v1.3.0',
      'v1.2.0',
    )
    expect(fetchImpl).toHaveBeenCalledWith('k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/containers/orders-api', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json-patch+json', Accept: 'application/json' },
      body: JSON.stringify([
        { op: 'test', path: '/spec/deploy/1/target', value: 'prod' },
        { op: 'test', path: '/spec/deploy/1/approved', value: 'v1.2.0' },
        { op: 'add', path: '/spec/deploy/1/approved', value: 'v1.3.0' },
      ]),
    })
  })

  it('creates the Task and resolves to the name the API server gave it', async () => {
    const fetchImpl = vi.fn(async () => respond(201, { metadata: { name: 'orders-api-x7k2p' } }))
    const name = await new LiveContainerWriter('k8s', fetchImpl).startTask('webshop', 'orders-api', crew, 'Add paging')
    expect(name).toBe('orders-api-x7k2p')
    expect(fetchImpl).toHaveBeenCalledWith('k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(taskManifest('webshop', 'orders-api', crew, 'Add paging')),
    })
  })

  it('rejects with the API server’s message when it refuses', async () => {
    const message =
      'Task.c4.factory.io "" is invalid: spec.goal: Invalid value: "": spec.goal in body should be at least 1 chars long'
    const fetchImpl = vi.fn(async () => respond(422, { kind: 'Status', code: 422, message }))
    await expect(
      new LiveContainerWriter('k8s', fetchImpl).startTask('webshop', 'orders-api', crew, ''),
    ).rejects.toThrow(message)
    await expect(new LiveContainerWriter('k8s', fetchImpl).retry('webshop', 'orders-api')).rejects.toThrow(message)
    await expect(
      new LiveContainerWriter('k8s', fetchImpl).answerStandard('webshop', 'orders-api', 'take', 'v1'),
    ).rejects.toThrow(message)
    await expect(
      new LiveContainerWriter('k8s', fetchImpl).setChecks('webshop', 'orders-api', 'BeforeRelease'),
    ).rejects.toThrow(message)
  })
})
