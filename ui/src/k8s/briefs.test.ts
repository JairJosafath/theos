import { describe, expect, it, vi } from 'vitest'
import { isBriefName } from '../demo/limits'
import {
  briefManifest,
  LiveBriefWriter,
  nameFromIdea,
  pausedPatch,
  pauseRunsPatch,
  promptPatch,
  settingsPatch,
  sourceAnswerPatch,
} from './briefs'

function respond(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

describe('briefManifest and promptPatch', () => {
  it('makes a Brief with only its name and idea', () => {
    expect(briefManifest('webshop', 'plant-care', 'Remind me to water plants')).toEqual({
      apiVersion: 'theos.factory.io/v1beta1',
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

  it('sets spec.paused', () => {
    expect(pausedPatch(true)).toEqual([{ op: 'add', path: '/spec/paused', value: true }])
    expect(pausedPatch(false)).toEqual([{ op: 'add', path: '/spec/paused', value: false }])
  })

  it('resumes a Brief whose runs are paused with pauseRuns unset too, as the CRD needs (#1995)', () => {
    expect(pausedPatch(false, true)).toEqual([
      { op: 'add', path: '/spec/paused', value: false },
      { op: 'add', path: '/spec/pauseRuns', value: false },
    ])
    expect(pausedPatch(true, true)).toEqual([{ op: 'add', path: '/spec/paused', value: true }])
  })

  it('pauses runs with paused, and resumes them leaving paused as it is (#1995)', () => {
    expect(pauseRunsPatch(true)).toEqual([
      { op: 'add', path: '/spec/paused', value: true },
      { op: 'add', path: '/spec/pauseRuns', value: true },
    ])
    expect(pauseRunsPatch(false)).toEqual([{ op: 'add', path: '/spec/pauseRuns', value: false }])
  })

  it('sets only the settings given, each as given', () => {
    const release = { container: 'cli', github: { repository: 'me/cli', tokenSecretRef: { name: 'gh' } } }
    expect(settingsPatch({ autopilot: 'On' })).toEqual([{ op: 'add', path: '/spec/autopilot', value: 'On' }])
    expect(settingsPatch({ parallel: 'six' })).toEqual([{ op: 'add', path: '/spec/parallel', value: 'six' }])
    expect(settingsPatch({ releases: [release] })).toEqual([{ op: 'add', path: '/spec/releases', value: [release] }])
    expect(settingsPatch({ releases: [] })).toEqual([{ op: 'add', path: '/spec/releases', value: [] }])
  })

  it('tests the resourceVersion the releases were worked out from before setting them', () => {
    expect(settingsPatch({ releases: [], resourceVersion: '42' })).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '42' },
      { op: 'add', path: '/spec/releases', value: [] },
    ])
    // Only releases replace a list; the other settings are single values.
    expect(settingsPatch({ autopilot: 'On', resourceVersion: '42' })).toEqual([
      { op: 'add', path: '/spec/autopilot', value: 'On' },
    ])
  })
})

describe('sourceAnswerPatch (#163)', () => {
  const entry = {
    name: 'product',
    linear: { team: 'ENG', label: 'factory' },
    credentialsSecretRef: { name: 'product-linear' },
    mode: 'Ask' as const,
    accepted: [409],
    declined: [415],
  }

  it('tests the whole entry at its index, then adds the list that changes, and never touches spec.releases', () => {
    const patch = sourceAnswerPatch({ index: 1, entry, accepted: [409, 412], declined: [415] })
    expect(patch).toEqual([
      { op: 'test', path: '/spec/requestSources/1', value: entry },
      { op: 'add', path: '/spec/requestSources/1/accepted', value: [409, 412] },
    ])
    expect(JSON.stringify(patch)).not.toContain('/spec/releases')
  })

  it('removes a list that ends up empty, only when the entry had it', () => {
    expect(sourceAnswerPatch({ index: 0, entry, accepted: [409, 415] })).toEqual([
      { op: 'test', path: '/spec/requestSources/0', value: entry },
      { op: 'add', path: '/spec/requestSources/0/accepted', value: [409, 415] },
      { op: 'remove', path: '/spec/requestSources/0/declined' },
    ])
    const bare = { name: 'support', credentialsSecretRef: { name: 'acme-jira' } }
    expect(sourceAnswerPatch({ index: 1, entry: bare, declined: [7] })).toEqual([
      { op: 'test', path: '/spec/requestSources/1', value: bare },
      { op: 'add', path: '/spec/requestSources/1/declined', value: [7] },
    ])
  })

  it('is sent by the writer as a JSON patch of the Brief', async () => {
    const fetchImpl = vi.fn(async () => respond(200, {}))
    await new LiveBriefWriter('k8s', fetchImpl).answerSourceRequest('webshop', 'feedback', {
      index: 0,
      entry,
      accepted: [409, 412],
      declined: [415],
    })
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/briefs/feedback')
    expect(init.method).toBe('PATCH')
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json-patch+json')
    expect(JSON.parse(init.body as string)).toEqual([
      { op: 'test', path: '/spec/requestSources/0', value: entry },
      { op: 'add', path: '/spec/requestSources/0/accepted', value: [409, 412] },
    ])
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

describe('LiveBriefWriter', () => {
  it('POSTs the Brief to its namespace', async () => {
    const fetchImpl = vi.fn(async () => respond(201, {}))
    await new LiveBriefWriter('k8s', fetchImpl).create('webshop', 'plant-care', 'Water the plants')
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/briefs')
    expect(init.method).toBe('POST')
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json')
    expect(JSON.parse(init.body as string)).toEqual({
      apiVersion: 'theos.factory.io/v1beta1',
      kind: 'Brief',
      metadata: { name: 'plant-care', namespace: 'webshop' },
      spec: { idea: 'Water the plants' },
    })
  })

  it('PATCHes one prompt onto the Brief', async () => {
    const fetchImpl = vi.fn(async () => respond(200, {}))
    await new LiveBriefWriter('k8s', fetchImpl).addPrompt('webshop', 'plant-care', 'Add a weekly summary')
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/briefs/plant-care')
    expect(init.method).toBe('PATCH')
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json-patch+json')
    expect(init.body).toBe('[{"op":"add","path":"/spec/prompts/-","value":"Add a weekly summary"}]')
  })

  it.each([true, false])('PATCHes spec.paused to %s', async (paused) => {
    const fetchImpl = vi.fn(async () => respond(200, {}))
    await new LiveBriefWriter('k8s', fetchImpl).setPaused('webshop', 'plant-care', paused)
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/briefs/plant-care')
    expect(init.method).toBe('PATCH')
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json-patch+json')
    expect(init.body).toBe(`[{"op":"add","path":"/spec/paused","value":${paused}}]`)
  })

  it.each([true, false])('PATCHes spec.pauseRuns to %s (#1995)', async (pauseRuns) => {
    const fetchImpl = vi.fn(async () => respond(200, {}))
    await new LiveBriefWriter('k8s', fetchImpl).setPauseRuns('webshop', 'plant-care', pauseRuns)
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/briefs/plant-care')
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(init.body as string)).toEqual(pauseRunsPatch(pauseRuns))
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
