import { describe, expect, it } from 'vitest'
import type { Container, DeployTarget } from '../k8s/types'
import { approveCommand, approvePatch, deploysOf, describeApproval, memberOf, readinessOf, shippingTo } from './deploys'

const target = (spec: Partial<DeployTarget['spec']>, status?: DeployTarget['status']): DeployTarget => ({
  metadata: { name: 'prod', namespace: 'shop' },
  spec: { github: { repository: 'acme/api' }, credentialsSecretRef: { name: 'gh' }, ...spec },
  status,
})

const container = (deploy: Container['spec']['deploy'], deploys?: NonNullable<Container['status']>['deploys']) =>
  ({
    metadata: { name: 'api', namespace: 'shop' },
    spec: {
      softwareSystemRef: { name: 'store' },
      type: 'service',
      gitProviderRef: { name: 'gitea' },
      frameworkRef: { name: 'go' },
      deploy,
    },
    status: { deploys },
  }) as Container

describe('memberOf', () => {
  it('names the github member and its repository', () => {
    expect(memberOf(target({}))).toEqual({ member: 'github', label: 'GitHub releases', where: 'acme/api' })
  })

  it('names the argoCD member and its destination, the server only when it is another cluster (#1588)', () => {
    const t = target({
      github: undefined,
      credentialsSecretRef: undefined,
      argoCD: { destination: { namespace: 'apps' } },
    })
    expect(memberOf(t)).toEqual({ member: 'argoCD', label: 'Argo CD', where: 'apps' })
    const far = target({
      github: undefined,
      argoCD: { destination: { namespace: 'apps', server: 'https://far.example' } },
    })
    expect(memberOf(far).where).toBe('apps on https://far.example')
  })

  it('names a member this UI doesn’t know by its field, showing nothing of it', () => {
    const t = target({ github: undefined })
    ;(t.spec as Record<string, unknown>).cloudFormation = { region: 'eu-west-1' }
    expect(memberOf(t)).toEqual({ member: 'cloudFormation', label: 'cloudFormation', where: '' })
  })
})

describe('describeApproval', () => {
  it.each([
    [{ approval: 'Required' as const }, 'A person approves each version'],
    [{ approval: 'None' as const, tier: 'production' as const }, 'Every release ships by itself'],
    [{ tier: 'production' as const }, 'Not set: production’s default, a person approves each version'],
    [{}, 'Not set: development’s default, every release ships by itself'],
  ])('says %o as %s', (spec, words) => {
    expect(describeApproval(target(spec))).toBe(words)
  })
})

describe('readinessOf', () => {
  it('reads Ready, its reason when False, and not checked before it reports', () => {
    expect(readinessOf(target({})).label).toBe('Not checked yet')
    const ok = { type: 'Ready', status: 'True' as const, reason: 'Ready' }
    expect(readinessOf(target({}, { conditions: [ok] }))).toMatchObject({ label: 'Ready', tone: 'ok' })
    const refused = { type: 'Ready', status: 'False' as const, reason: 'CredentialRefused', message: '401' }
    expect(readinessOf(target({}, { conditions: [refused] }))).toMatchObject({
      label: 'CredentialRefused',
      tone: 'error',
    })
  })
})

describe('deploysOf', () => {
  it('lists spec.deploy in order with its status, the version awaiting approval, then a status left from a removed entry', () => {
    const c = container(
      [{ target: 'staging' }, { target: 'prod', after: 'staging', approved: 'v1.2.0' }],
      [
        { target: 'gone', phase: 'Deployed', version: 'v1.0.0' },
        { target: 'prod', phase: 'Waiting', wantedVersion: 'v1.3.0', reason: 'WaitingForApproval' },
        { target: 'staging', phase: 'Deployed', version: 'v1.3.0', wantedVersion: 'v1.3.0' },
      ],
    )
    expect(deploysOf(c).map((r) => [r.target, r.index, r.after, r.approved, r.awaiting, r.status?.phase])).toEqual([
      ['staging', 0, undefined, undefined, undefined, 'Deployed'],
      ['prod', 1, 'staging', 'v1.2.0', 'v1.3.0', 'Waiting'],
      ['gone', -1, undefined, undefined, undefined, 'Deployed'],
    ])
  })

  it('awaits nothing while an entry waits for another reason', () => {
    const c = container(
      [{ target: 'prod' }],
      [{ target: 'prod', phase: 'Waiting', wantedVersion: 'v1', reason: 'WaitingForPrevious' }],
    )
    expect(deploysOf(c)[0].awaiting).toBeUndefined()
  })

  it('finds the Containers that ship to a target', () => {
    const a = container([{ target: 'prod' }])
    const b = container([{ target: 'staging' }])
    expect(shippingTo('prod', [a, b, container(undefined)])).toEqual([a])
  })
})

describe('the approval', () => {
  it('is one write of the entry’s approved, after a test that the entry names the target (#1163)', () => {
    expect(approvePatch(2, 'prod', 'v1.3.0')).toEqual([
      { op: 'test', path: '/spec/deploy/2/target', value: 'prod' },
      { op: 'add', path: '/spec/deploy/2/approved', value: 'v1.3.0' },
    ])
  })

  it('tests the approval it replaces, when the entry has one (#1163)', () => {
    expect(approvePatch(0, 'prod', 'v1.3.0', 'v1.2.0')).toEqual([
      { op: 'test', path: '/spec/deploy/0/target', value: 'prod' },
      { op: 'test', path: '/spec/deploy/0/approved', value: 'v1.2.0' },
      { op: 'add', path: '/spec/deploy/0/approved', value: 'v1.3.0' },
    ])
  })

  it('is the same patch as a kubectl line', () => {
    const c = container([{ target: 'staging' }, { target: 'prod', approved: 'v1.2.0' }])
    expect(approveCommand(c, deploysOf(c)[1], 'v1.3.0')).toBe(
      `kubectl -n shop patch container api --type=json -p '${JSON.stringify(approvePatch(1, 'prod', 'v1.3.0', 'v1.2.0'))}'`,
    )
  })
})
