import { describe, expect, it } from 'vitest'
import {
  LiveCrewWriter,
  RERUN_RELEASE,
  RUN_SCHEDULE,
  addSchedulePatch,
  releasePatch,
  releaseSettings,
  settledEdits,
  sourceTagPrefix,
  takenSchedule,
} from './crews'
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
      publish: { repository: 'webshop-co/orders-api', tokenSecret: 'orders-api-github', tagPrefix: '', latest: true },
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

  it('sets latest only when it is off', () => {
    const now = releaseSettings(publishing)
    const next = { ...now, publish: { ...now.publish!, latest: false } }
    const ops = releasePatch(publishing, next)
    expect(ops).toContainEqual({
      op: 'add',
      path: '/spec/publishReleases',
      value: { github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'gh' } }, latest: false },
    })
    const off: Crew = {
      ...publishing,
      spec: { ...publishing.spec, publishReleases: { ...publishing.spec.publishReleases!, latest: false } },
    }
    expect(releaseSettings(off).publish?.latest).toBe(false)
    expect(releasePatch(off, releaseSettings(off))).toEqual([])
  })

  it('removes publishing turned off', () => {
    const next = { ...releaseSettings(publishing), tagReleases: false, publish: undefined }
    expect(releasePatch(publishing, next)).toEqual([
      { op: 'add', path: '/spec/tagReleases', value: false },
      { op: 'remove', path: '/spec/publishReleases' },
    ])
  })

  it('starts with the test of the resourceVersion, also when a switch changed too (theos#31)', () => {
    // server.mjs looks for the test first, and refused the save with the switch's op before it.
    const read: Crew = { ...publishing, metadata: { ...publishing.metadata, resourceVersion: '41' } }
    const test = { op: 'test', path: '/metadata/resourceVersion', value: '41' }
    const off = { ...releaseSettings(read), tagReleases: false, publish: undefined }
    expect(releasePatch(read, off)).toEqual([
      test,
      { op: 'add', path: '/spec/tagReleases', value: false },
      { op: 'remove', path: '/spec/publishReleases' },
    ])
    const now = releaseSettings(read)
    const token = { ...now, prepareReleases: true, publish: { ...now.publish!, tokenSecret: 'gh-2' } }
    expect(releasePatch(read, token, '40')).toEqual([
      { ...test, value: '40' },
      { op: 'add', path: '/spec/prepareReleases', value: true },
      {
        op: 'add',
        path: '/spec/publishReleases',
        value: { github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'gh-2' } } },
      },
    ])
    // A switch alone tests nothing.
    expect(releasePatch(read, { ...now, prepareReleases: true })).toEqual([
      { op: 'add', path: '/spec/prepareReleases', value: true },
    ])
  })
})

describe('a Crew’s published source as a patch', () => {
  const github = { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'gh' } }
  const withSource = (source: { path: string; tagPrefix?: string }): Crew => ({
    ...publishing,
    spec: { ...publishing.spec, publishReleases: { github, source } },
  })
  const publishOps = (crew: Crew, source: { path: string; tagPrefix?: string } | undefined) => {
    const now = releaseSettings(crew)
    return releasePatch(crew, { ...now, publish: { ...now.publish!, source } })
  }

  it('reads the folder and its tag prefix, and sends nothing unchanged', () => {
    const crew = withSource({ path: 'engine', tagPrefix: '' })
    expect(releaseSettings(crew).publish?.source).toEqual({ path: 'engine', tagPrefix: '' })
    expect(releasePatch(crew, releaseSettings(crew))).toEqual([])
  })

  it('sets the folder, leaving an unset tag prefix to its default', () => {
    expect(publishOps(publishing, { path: 'engine' })).toEqual([
      { op: 'add', path: '/spec/publishReleases', value: { github, source: { path: 'engine' } } },
    ])
  })

  it('sends a changed folder or tag prefix, an empty prefix included', () => {
    const crew = withSource({ path: 'engine' })
    expect(publishOps(crew, { path: 'cli' })).toEqual([
      { op: 'add', path: '/spec/publishReleases', value: { github, source: { path: 'cli' } } },
    ])
    expect(publishOps(crew, { path: 'engine', tagPrefix: '' })).toEqual([
      { op: 'add', path: '/spec/publishReleases', value: { github, source: { path: 'engine', tagPrefix: '' } } },
    ])
  })

  it('removes the source turned off and keeps publishing the release', () => {
    expect(publishOps(withSource({ path: 'engine', tagPrefix: 'e/' }), undefined)).toEqual([
      { op: 'add', path: '/spec/publishReleases', value: { github } },
    ])
  })

  it('tags the source path + "/" by default, nothing for the whole repository', () => {
    expect(sourceTagPrefix({ path: 'engine' })).toBe('engine/')
    expect(sourceTagPrefix({ path: 'tools/engine' })).toBe('tools/engine/')
    expect(sourceTagPrefix({ path: '.' })).toBe('')
    expect(sourceTagPrefix({ path: 'engine', tagPrefix: '' })).toBe('')
    expect(sourceTagPrefix({ path: 'engine', tagPrefix: 'core/' })).toBe('core/')
  })
})

describe('a Crew’s publishing worked out from a version', () => {
  const at = (crew: Crew, resourceVersion: string): Crew => ({
    ...crew,
    metadata: { ...crew.metadata, resourceVersion },
  })

  it('tests the version it was worked out from before setting or removing it', () => {
    const next = { ...releaseSettings(publishing), publish: undefined }
    expect(releasePatch(at(publishing, '6'), next, '5')).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '5' },
      { op: 'remove', path: '/spec/publishReleases' },
    ])
    // The Crew's own version when no other is given.
    expect(releasePatch(at(publishing, '6'), next)[0]).toEqual({
      op: 'test',
      path: '/metadata/resourceVersion',
      value: '6',
    })
  })

  it('tests nothing when only the switches change, as each is one value', () => {
    const next = { ...releaseSettings(publishing), prepareReleases: true }
    expect(releasePatch(at(publishing, '6'), next, '5')).toEqual([
      { op: 'add', path: '/spec/prepareReleases', value: true },
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
  const url = 'k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/crews/orders-api'

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

  it('pauses and resumes its running Tasks with JSON patches of pauseRuns (#1995)', async () => {
    const { sent, w } = writer()
    await w.setPauseRuns('webshop', 'orders-api', true)
    await w.setPauseRuns('webshop', 'orders-api', false)
    await w.setPaused('webshop', 'orders-api', false, true)
    expect(sent.map((s) => [s.url, (s.init.headers as Record<string, string>)['Content-Type']])).toEqual([
      [url, 'application/json-patch+json'],
      [url, 'application/json-patch+json'],
      [url, 'application/json-patch+json'],
    ])
    expect(sent.map((s) => JSON.parse(s.init.body as string))).toEqual([
      [
        { op: 'add', path: '/spec/paused', value: true },
        { op: 'add', path: '/spec/pauseRuns', value: true },
      ],
      [{ op: 'add', path: '/spec/pauseRuns', value: false }],
      [
        { op: 'add', path: '/spec/paused', value: false },
        { op: 'add', path: '/spec/pauseRuns', value: false },
      ],
    ])
  })

  it('asks for a re-run with a merge patch of the annotation', async () => {
    const { sent, w } = writer()
    await w.rerunRelease('webshop', 'orders-api', 'v1')
    expect((sent[0].init.headers as Record<string, string>)['Content-Type']).toBe('application/merge-patch+json')
    expect(JSON.parse(sent[0].init.body as string)).toEqual({ metadata: { annotations: { [RERUN_RELEASE]: 'v1' } } })
  })

  // #72: running a schedule now is the annotation with the schedule's name.
  it('asks to run a schedule now with a merge patch of the annotation', async () => {
    const { sent, w } = writer()
    await w.runSchedule('webshop', 'orders-api', 'dependencies')
    expect(sent[0].init.method).toBe('PATCH')
    expect((sent[0].init.headers as Record<string, string>)['Content-Type']).toBe('application/merge-patch+json')
    expect(JSON.parse(sent[0].init.body as string)).toEqual({
      metadata: { annotations: { [RUN_SCHEDULE]: 'dependencies' } },
    })
  })

  // #72, step 6: a proposal taken is one schedule appended, whatever the Crew has.
  it('adds a schedule with a JSON patch that appends it, sent as given', async () => {
    const { sent, w } = writer()
    const schedule = { name: 'dependencies', cron: '0 6 * * 1', timeZone: 'Mars/Olympus', goal: 'Update them.' }
    await w.addSchedule('webshop', 'orders-api', schedule)
    expect(sent[0].url).toBe('k8s/apis/theos.factory.io/v1beta1/namespaces/webshop/crews/orders-api')
    expect(sent[0].init.method).toBe('PATCH')
    expect((sent[0].init.headers as Record<string, string>)['Content-Type']).toBe('application/json-patch+json')
    expect(JSON.parse(sent[0].init.body as string)).toEqual([{ op: 'add', path: '/spec/schedules/-', value: schedule }])
    expect(addSchedulePatch(schedule)).toEqual([{ op: 'add', path: '/spec/schedules/-', value: schedule }])
  })

  it('rejects with the API server’s message', async () => {
    const message = 'spec.parallel: Invalid value: 9: should be less than or equal to 5'
    const { w } = writer(422, JSON.stringify({ kind: 'Status', code: 422, message }))
    await expect(w.setParallel('webshop', 'orders-api', 9)).rejects.toThrow(message)
  })
})

// #72, step 6: a Standard's proposal becomes a Crew's schedule in the zone the person gave.
describe('takenSchedule', () => {
  const proposal = { name: 'dependencies', cron: '0 6 * * 1', timeZone: 'UTC', goal: 'Update the dependencies.\n' }
  const cases: [string, string, string][] = [
    ['the zone given, not the proposal’s', 'Europe/Amsterdam', 'Europe/Amsterdam'],
    ['UTC when the person says UTC', 'UTC', 'UTC'],
    ['the zone without the white space around it', '  Asia/Tokyo ', 'Asia/Tokyo'],
    ['a zone it can’t judge, as typed', 'Mars/Olympus', 'Mars/Olympus'],
  ]
  for (const [name, zone, want] of cases) {
    it(`has ${name}`, () => {
      expect(takenSchedule(proposal, zone)).toEqual({
        name: 'dependencies',
        cron: '0 6 * * 1',
        timeZone: want,
        goal: 'Update the dependencies.\n',
      })
    })
  }

  it('copies only a schedule’s four fields', () => {
    const more = { ...proposal, since: '2026-11-09T05:00:00Z' }
    expect(Object.keys(takenSchedule(more, 'UTC')).sort()).toEqual(['cron', 'goal', 'name', 'timeZone'])
  })
})
