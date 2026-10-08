import { describe, expect, it, vi } from 'vitest'
import { briefManifest, isBriefName, LiveBriefWriter, nameFromIdea, promptPatch } from './briefs'

function respond(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

describe('briefManifest and promptPatch', () => {
  it('makes a Brief with only its name and idea', () => {
    expect(briefManifest('webshop', 'plant-care', 'Remind me to water plants')).toEqual({
      apiVersion: 'c4.factory.io/v1alpha1',
      kind: 'Brief',
      metadata: { name: 'plant-care', namespace: 'webshop' },
      spec: { idea: 'Remind me to water plants' },
    })
  })

  it('sets the autopilot mode only when one is chosen', () => {
    expect(briefManifest('webshop', 'plant-care', 'Water plants', { autopilot: 'Ask' }).spec).toEqual({
      idea: 'Water plants',
      autopilot: 'Ask',
    })
    expect(briefManifest('webshop', 'plant-care', 'Water plants', {}).spec).toEqual({ idea: 'Water plants' })
  })

  it('sets spec.parallel only when one is chosen', () => {
    expect(briefManifest('webshop', 'plant-care', 'Water plants', { parallel: 2 }).spec).toEqual({
      idea: 'Water plants',
      parallel: 2,
    })
    expect(briefManifest('webshop', 'plant-care', 'Water plants', { parallel: undefined }).spec).toEqual({
      idea: 'Water plants',
    })
  })

  it('appends one prompt', () => {
    expect(promptPatch('Use Stripe')).toEqual([{ op: 'add', path: '/spec/prompts/-', value: 'Use Stripe' }])
  })
})

describe('nameFromIdea', () => {
  it.each([
    ['An app where customers order coffee ahead', [], 'customers-order-coffee'],
    ['Plant watering reminders for my balcony', [], 'plant-watering-reminders'],
    ['  Café Crème: loyalty cards!  ', [], 'cafe-creme-loyalty'],
    ['', [], 'app'],
    ['!!! ???', [], 'app'],
    ['I want an app', ['app'], 'app-2'],
    [
      'Plant watering reminders',
      ['plant-watering-reminders', 'plant-watering-reminders-2'],
      'plant-watering-reminders-3',
    ],
  ])('names %j', (idea, taken, want) => {
    expect(nameFromIdea(idea, taken)).toBe(want)
  })

  it('keeps names short and valid', () => {
    const name = nameFromIdea('Supercalifragilisticexpialidocious extraordinarily-long words everywhere', [])
    expect(name.length).toBeLessThanOrEqual(40)
    expect(isBriefName(name)).toBe(true)
  })
})

describe('isBriefName', () => {
  it.each([
    ['shop', true],
    ['web-shop-2', true],
    ['a'.repeat(63), true],
    ['a'.repeat(64), false],
    ['Shop', false],
    ['-shop', false],
    ['shop-', false],
    ['web_shop', false],
    ['', false],
  ])('%j is %s', (name, want) => {
    expect(isBriefName(name)).toBe(want)
  })
})

describe('LiveBriefWriter', () => {
  it('POSTs the Brief to its namespace', async () => {
    const fetchImpl = vi.fn(async () => respond(201, {}))
    await new LiveBriefWriter('k8s', fetchImpl).create('webshop', 'plant-care', 'Water the plants')
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/briefs')
    expect(init.method).toBe('POST')
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json')
    expect(JSON.parse(init.body as string)).toEqual({
      apiVersion: 'c4.factory.io/v1alpha1',
      kind: 'Brief',
      metadata: { name: 'plant-care', namespace: 'webshop' },
      spec: { idea: 'Water the plants' },
    })
  })

  it('PATCHes one prompt onto the Brief', async () => {
    const fetchImpl = vi.fn(async () => respond(200, {}))
    await new LiveBriefWriter('k8s', fetchImpl).addPrompt('webshop', 'plant-care', 'Add a weekly summary')
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('k8s/apis/c4.factory.io/v1alpha1/namespaces/webshop/briefs/plant-care')
    expect(init.method).toBe('PATCH')
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json-patch+json')
    expect(init.body).toBe('[{"op":"add","path":"/spec/prompts/-","value":"Add a weekly summary"}]')
  })

  it("rejects with the API's message", async () => {
    const status = {
      kind: 'Status',
      status: 'Failure',
      reason: 'AlreadyExists',
      code: 409,
      message: 'briefs "x" already exists',
    }
    const writer = new LiveBriefWriter(
      'k8s',
      vi.fn(async () => respond(409, status)),
    )
    await expect(writer.create('webshop', 'x', 'idea')).rejects.toThrow('briefs "x" already exists')
    await expect(writer.create('webshop', 'x', 'idea')).rejects.toMatchObject({ code: 409 })
  })

  it('falls back to the HTTP status when the answer is not a Status', async () => {
    const writer = new LiveBriefWriter(
      'k8s',
      vi.fn(async () => new Response('nope', { status: 502, statusText: 'Bad Gateway' })),
    )
    await expect(writer.addPrompt('webshop', 'x', 'p')).rejects.toThrow('502 Bad Gateway')
  })
})
