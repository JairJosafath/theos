import { describe, expect, it } from 'vitest'
import type { Condition, Task, TestEnvironment, TestEnvironmentTestRun } from '../k8s/types'
import {
  describeCIPool,
  describeDevices,
  describeLeaseCount,
  describeLimits,
  describeMacHosts,
  describeSpend,
  environmentWait,
  leaseHolder,
  leaseHolders,
  testEnvCI,
  testEnvCredentials,
  testEnvLeases,
  testEnvMember,
  testEnvReadiness,
  testEnvSecret,
  testEnvSpend,
  testEnvTestRuns,
  testRunBadge,
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
    const unknown = { ...env(), spec: { gcp: { zone: 'europe-west4-a' } } } as unknown as TestEnvironment
    expect(testEnvMember(unknown)).toEqual({ member: 'gcp', label: 'gcp', where: '' })
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

describe('a remote test environment for display (test-environments, steps 7 to 10)', () => {
  const AWS: NonNullable<TestEnvironment['spec']['aws']> = {
    region: 'eu-west-1',
    account: '123456789012',
    instanceType: 'g5.xlarge',
    imageId: 'ami-0123456789abcdef0',
    subnetId: 'subnet-0123456789abcdef0',
    credentialsSecretRef: { name: 'gpu-aws' },
  }
  const FARM: NonNullable<TestEnvironment['spec']['deviceFarm']> = {
    projectArn: 'arn:aws:devicefarm:us-west-2:123456789012:project:0b6f3c1e-1d2a-4c5b-9e8f-7a6b5c4d3e2f',
    devicePoolArn: 'arn:aws:devicefarm:us-west-2::devicepool:082d10e5-d7d7-48a5-ba5c-b33d66efa1f5',
    credentialsSecretRef: { name: 'devices-aws' },
  }
  const remote = (
    spec: Partial<TestEnvironment['spec']>,
    status?: TestEnvironment['status'],
    name = 'gpu',
  ): TestEnvironment => ({
    metadata: { name, namespace: 'webshop' },
    spec: { limits: { budgetUSDPerDay: 10 }, ...spec },
    status,
  })
  const conditions = (...cs: [string, Condition['status'], string][]): TestEnvironment['status'] => ({
    conditions: cs.map(([type, status, reason]) => ({ type, status, reason, message: `${type} is ${reason}` })),
  })

  it('names an aws member’s instance type, region and account, an EC2 Mac’s kept host, and a deviceFarm one', () => {
    expect(testEnvMember(remote({ aws: AWS }))).toEqual({
      member: 'aws',
      label: 'An EC2 machine per lease, in your AWS account',
      where: 'EC2 g5.xlarge in eu-west-1, account 123456789012',
    })
    expect(testEnvMember(remote({ aws: { ...AWS, instanceType: 'mac2.metal', account: undefined } }))).toEqual({
      member: 'aws',
      label: 'An EC2 Mac per lease, on a dedicated host the operator keeps, in your AWS account',
      where: 'EC2 mac2.metal in eu-west-1',
    })
    expect(testEnvMember(remote({ deviceFarm: FARM }))).toEqual({
      member: 'deviceFarm',
      label: 'AWS Device Farm’s real phones, which run test packages',
      where: 'Device Farm in us-west-2',
    })
    // Of its credential, the Secret's name only.
    expect(testEnvSecret(remote({ aws: AWS }))).toBe('gpu-aws')
    expect(testEnvSecret(remote({ deviceFarm: FARM }))).toBe('devices-aws')
    expect(testEnvSecret(env())).toBeUndefined()
  })

  it('reads CredentialsValid for a remote member only, its reason in words when it is False', () => {
    expect(testEnvCredentials(env())).toBeUndefined()
    expect(testEnvCredentials(remote({ aws: AWS }))).toEqual({ label: 'Not checked yet', tone: 'unknown' })
    expect(testEnvCredentials(remote({ aws: AWS }, conditions(['CredentialsValid', 'True', 'Valid'])))).toMatchObject({
      label: 'Valid',
      tone: 'ok',
    })
    for (const [reason, label] of [
      ['SecretMissing', 'Secret missing'],
      ['SecretIncomplete', 'Secret incomplete'],
      ['CredentialRefused', 'Credential refused'],
      ['Unreachable', 'Unreachable'],
    ]) {
      const refused = remote({ deviceFarm: FARM }, conditions(['CredentialsValid', 'False', reason]))
      expect(testEnvCredentials(refused)).toMatchObject({ label, tone: 'error' })
      expect(testEnvCredentials(refused)?.condition?.message).toBe(`CredentialsValid is ${reason}`)
    }
    // Ready says the credential is why it isn't, in words too.
    const notReady = remote({ aws: AWS }, conditions(['Ready', 'False', 'CredentialsInvalid']))
    expect(testEnvReadiness(notReady)).toMatchObject({ label: 'Credentials invalid', tone: 'error' })
  })

  it('reads CI for an aws member with a ci block only, Serving or why not in words', () => {
    expect(testEnvCI(env({ ci: { size: 1 } }))).toBeUndefined()
    expect(testEnvCI(remote({ aws: AWS }))).toBeUndefined()
    expect(testEnvCI(remote({ aws: AWS, ci: {} }))).toEqual({ label: 'Not checked yet', tone: 'unknown' })
    expect(testEnvCI(remote({ aws: AWS, ci: {} }, conditions(['CI', 'True', 'Serving'])))).toMatchObject({
      label: 'Serving',
      tone: 'ok',
    })
    for (const [reason, label] of [
      ['NoGitProvider', 'No git provider'],
      ['NoPublicAddress', 'No public address'],
      ['OverBudget', 'Over budget'],
    ]) {
      expect(testEnvCI(remote({ aws: AWS, ci: {} }, conditions(['CI', 'False', reason])))).toMatchObject({
        label,
        tone: 'error',
      })
    }
  })

  it('says today’s spend against limits.budgetUSDPerDay, and when the budget is spent (OverBudget)', () => {
    const spent = (milliUSD: number, day = '2026-11-06') => remote({ aws: AWS }, { spentToday: { day, milliUSD } })
    expect(describeSpend(spent(3_420), NOW)).toBe('$3.42 of $10.00 today')
    expect(testEnvSpend(spent(3_420), NOW)).toEqual({ milliUSD: 3_420, budget: 10, over: false })
    expect(describeSpend(spent(10_000), NOW)).toBe('$10.00 of $10.00 today, the budget spent')
    expect(testEnvSpend(spent(10_200), NOW).over).toBe(true)
    // Yesterday's spend is not today's: a new UTC day starts with the whole budget.
    expect(describeSpend(spent(12_000, '2026-11-05'), NOW)).toBe('$0.00 of $10.00 today')
    expect(testEnvSpend(spent(12_000, '2026-11-05'), NOW).over).toBe(false)
    // A local member has no budget.
    expect(testEnvSpend(env(), NOW)).toEqual({ milliUSD: 0, over: false })
  })

  it('says a remote member’s CI: a machine per job with ci: {}, none without, and none for a Mac or Device Farm', () => {
    expect(describeCIPool(remote({ aws: AWS, ci: {} }))).toBe(
      'A machine per waiting job for runs-on: env-gpu, within its limits and the day’s budget',
    )
    expect(describeCIPool(remote({ aws: AWS }))).toBe('None: no job takes runs-on: env-gpu until ci: {} is set')
    expect(describeCIPool(remote({ aws: { ...AWS, instanceType: 'mac2-m2.metal' } }))).toBe(
      'None: an EC2 Mac runs no CI jobs',
    )
    expect(describeCIPool(remote({ deviceFarm: FARM }))).toBe(
      'None: a Device Farm environment runs test packages, not CI jobs',
    )
  })

  it('says how long an EC2 Mac’s hosts are kept, 24 hours when unset, and nothing for another machine', () => {
    const mac = { ...AWS, instanceType: 'mac2.metal' }
    expect(describeMacHosts(remote({ aws: mac }))).toBe(
      'Each host kept 24 h from its allocation for the leases that follow; the lease that allocates one counts its price for those hours at once',
    )
    expect(describeMacHosts(remote({ aws: { ...mac, mac: { keepHostHours: 48 } } }))).toMatch(/^Each host kept 48 h/)
    expect(describeMacHosts(remote({ aws: AWS }))).toBeUndefined()
    expect(describeMacHosts(remote({ deviceFarm: FARM }))).toBeUndefined()
  })

  it('tells a CI job’s runner from a Task among the holders, and lists the Tasks only', () => {
    expect(leaseHolder('orders-api-24')).toEqual({ kind: 'Task', name: 'orders-api-24' })
    expect(leaseHolder('ci/gitea/118')).toEqual({ kind: 'CI', gitProvider: 'gitea', runner: '118' })
    expect(leaseHolder(undefined)).toBeUndefined()
    const held = remote(
      { aws: AWS, ci: {} },
      {
        leases: [
          { id: 'gpu-ci-7c2e91d4a0', holder: 'ci/gitea/118', state: 'Ready' },
          { id: 'gpu-4f1e2d3c4b', holder: 'game-7', state: 'Ready' },
        ],
      },
    )
    expect(leaseHolders(held)).toEqual(['game-7'])
  })

  it('lists a deviceFarm environment’s test runs newest first, each by its result, else its state', () => {
    const run = (id: string, since: string, more: Partial<TestEnvironmentTestRun> = {}): TestEnvironmentTestRun => ({
      id,
      lease: 'devices-3b9f0e2a71',
      request: id,
      since,
      ...more,
    })
    const farm = remote(
      { deviceFarm: FARM },
      {
        testRuns: [
          run('a', '2026-11-06T11:50:00Z', { state: 'Reported', result: 'Failed' }),
          run('b', '2026-11-06T11:58:00Z', { state: 'Running' }),
        ],
      },
      'devices',
    )
    expect(testEnvTestRuns(farm).map((r) => r.id)).toEqual(['b', 'a'])
    expect(testEnvTestRuns(env())).toEqual([])
    const badge = (more: Partial<TestEnvironmentTestRun>) => testRunBadge(run('x', '', more))
    expect(badge({ state: 'Pending' })).toEqual({ label: 'Pending', tone: 'progress' })
    expect(badge({ state: 'Running' })).toEqual({ label: 'Running', tone: 'progress' })
    expect(badge({ state: 'Reported', result: 'Passed' })).toEqual({ label: 'Passed', tone: 'ok' })
    expect(badge({ state: 'Reported', result: 'Warned' })).toEqual({ label: 'Passed with warnings', tone: 'ok' })
    expect(badge({ state: 'Reported', result: 'Failed' })).toEqual({ label: 'Failed', tone: 'error' })
    expect(badge({ state: 'Reported', result: 'Errored' })).toEqual({ label: 'Errored', tone: 'error' })
    expect(badge({ state: 'Reported', result: 'Stopped' })).toEqual({ label: 'Stopped', tone: 'waiting' })
    expect(badge({ state: 'Rejected', result: 'Rejected' })).toEqual({ label: 'Rejected', tone: 'error' })
    expect(badge({})).toEqual({ label: 'Unknown', tone: 'unknown' })
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
    // A remote environment's waits (steps 7 to 10) read the same way.
    for (const why of ['OverBudget', 'CredentialRefused']) {
      expect(environmentWait(task('WaitingForEnvironment', `${why}: gpu …`))?.why).toBe(why)
    }
  })

  it('is none while the Task waits for something else', () => {
    expect(environmentWait(task('WaitingForSlot', 'Waiting for a free agent slot'))).toBeUndefined()
  })
})
