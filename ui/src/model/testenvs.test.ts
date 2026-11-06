import { describe, expect, it } from 'vitest'
import type { Task, TestEnvironment } from '../k8s/types'
import {
  describeCIPool,
  describeDevices,
  describeLeaseCount,
  describeLimits,
  describeSpend,
  environmentWait,
  leaseHolders,
  testEnvLeases,
  testEnvMember,
  testEnvReadiness,
} from './testenvs'

const NOW = Date.parse('2026-11-06T12:00:00Z')

const env = (spec: Partial<TestEnvironment['spec']> = {}, status?: TestEnvironment['status']): TestEnvironment => ({
  metadata: { name: 'browser', namespace: 'webshop' },
  spec: { local: { image: 'ghcr.io/jairjosafath/theos/env-browser:0.30.0' }, ...spec },
  status,
})

describe('a test environment for display (test-environments, step 6)', () => {
  it('names its member and what it runs, and an unknown member by its field alone', () => {
    expect(testEnvMember(env())).toEqual({
      member: 'local',
      label: 'A pod per lease, on this cluster',
      where: 'ghcr.io/jairjosafath/theos/env-browser:0.30.0',
    })
    const remote = { ...env(), spec: { aws: { region: 'eu-west-1' } } } as unknown as TestEnvironment
    expect(testEnvMember(remote)).toEqual({ member: 'aws', label: 'aws', where: '' })
  })

  it('reads Ready, and a reason in words when it is not', () => {
    expect(testEnvReadiness(env())).toEqual({ label: 'Not checked yet', tone: 'unknown' })
    expect(
      testEnvReadiness(env({}, { conditions: [{ type: 'Ready', status: 'True', reason: 'Ready' }] })),
    ).toMatchObject({ label: 'Ready', tone: 'ok' })
    const unpinned = env({}, { conditions: [{ type: 'Ready', status: 'False', reason: 'ImageNotPinned' }] })
    expect(testEnvReadiness(unpinned)).toMatchObject({ label: 'Image not pinned', tone: 'error' })
  })

  it('counts its live leases against maxLeases, the CRD’s 2 when unset, and says when all are taken', () => {
    expect(describeLeaseCount(env())).toEqual({ text: '0 of 2 leased', full: false })
    const one = env({ limits: { maxLeases: 1 } }, { leases: [{ id: 'android-1', holder: 'game-7', state: 'Ready' }] })
    expect(describeLeaseCount(one)).toEqual({ text: '1 of 1 leased', full: true })
  })

  it('says its limits, the CRD’s defaults where unset', () => {
    expect(describeLimits(env())).toBe('2 leases at once, each 180 min at most')
    expect(describeLimits(env({ limits: { maxLeases: 1, maxLeaseMinutes: 45 } }))).toBe(
      '1 lease at once, each 45 min at most',
    )
  })

  it('says today’s spend: nothing for a local lease, the day’s cost for another, and none of yesterday’s', () => {
    expect(describeSpend(env({}, { spentToday: { day: '2026-11-06' } }), NOW)).toBe(
      'Nothing today: a local lease costs nothing',
    )
    const remote = { metadata: { name: 'gpu' }, spec: { aws: {} } } as unknown as TestEnvironment
    expect(describeSpend({ ...remote, status: { spentToday: { day: '2026-11-06', milliUSD: 1_234 } } }, NOW)).toBe(
      '$1.23 today',
    )
    expect(describeSpend({ ...remote, status: { spentToday: { day: '2026-11-05', milliUSD: 9_000 } } }, NOW)).toBe(
      '$0.00 today',
    )
  })

  it('lists its leases oldest first, and the Tasks that hold them once each', () => {
    const e = env(
      {},
      {
        leases: [
          { id: 'browser-b', holder: 'shop-2', since: '2026-11-06T11:50:00Z' },
          { id: 'browser-a', holder: 'shop-1', since: '2026-11-06T11:40:00Z' },
          { id: 'browser-c', holder: 'shop-1', since: '2026-11-06T11:55:00Z' },
        ],
      },
    )
    expect(testEnvLeases(e).map((l) => l.id)).toEqual(['browser-a', 'browser-b', 'browser-c'])
    expect(leaseHolders(e)).toEqual(['shop-2', 'shop-1'])
  })

  it('says its CI pool and devices', () => {
    expect(describeCIPool(env())).toBe('None: no runner takes runs-on: env-browser until ci.size is set')
    expect(describeCIPool(env({ ci: { size: 2 } }))).toBe('2 runner pods for runs-on: env-browser')
    const gpu = env({
      local: { image: 'env-godot:1', devices: { 'nvidia.com/gpu': '1', 'example.com/dongle': '2' } },
    })
    expect(describeDevices(gpu)).toEqual(['example.com/dongle: 2', 'nvidia.com/gpu: 1'])
  })
})

describe('a Task’s wait for a test environment', () => {
  const task = (reason: string, message: string): Task => ({
    metadata: { name: 'game-7', namespace: 'webshop' },
    spec: { agentRef: { name: 'dev' }, issue: 7 },
    status: { conditions: [{ type: 'Progressing', status: 'True', reason, message }] },
  })

  it('reads why from the start of the message', () => {
    expect(
      environmentWait(
        task('WaitingForEnvironment', 'AllLeased: every lease of android is taken; it starts when one is returned'),
      ),
    ).toEqual({
      why: 'AllLeased',
      message: 'AllLeased: every lease of android is taken; it starts when one is returned',
    })
  })

  it('is none while the Task waits for something else', () => {
    expect(environmentWait(task('WaitingForSlot', 'Waiting for a free agent slot'))).toBeUndefined()
  })
})
