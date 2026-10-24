import { describe, expect, it } from 'vitest'
import type { Container, DeployTarget } from '../k8s/types'
import {
  approveCommand,
  approvePatch,
  deploysOf,
  describeApproval,
  githubHosts,
  githubImage,
  githubReleasesURL,
  gitlabHosts,
  gitlabImage,
  gitlabReleasesURL,
  memberOf,
  readinessOf,
  shippingTo,
  sourceOf,
} from './deploys'

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

  it('names the gitlab member and its project (#1807)', () => {
    const t = target({ github: undefined, gitlab: { project: 'acme/apps/api' } })
    expect(memberOf(t)).toEqual({ member: 'gitlab', label: 'GitLab releases', where: 'acme/apps/api' })
  })

  it('names the argoCD member and its destination, the server only when it is another cluster (#1588, #1675)', () => {
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

  it('names the cloudFormation member by its region, and the account when it guards one (#1609)', () => {
    const t = target({ github: undefined, cloudFormation: { region: 'eu-west-1' } })
    expect(memberOf(t)).toEqual({ member: 'cloudFormation', label: 'AWS CloudFormation', where: 'eu-west-1' })
    const guarded = target({ github: undefined, cloudFormation: { region: 'eu-west-1', account: '123456789012' } })
    expect(memberOf(guarded).where).toBe('eu-west-1, account 123456789012')
  })

  it('names a member this UI doesn’t know by its field, showing nothing of it', () => {
    const t = target({ github: undefined })
    ;(t.spec as Record<string, unknown>).terraform = { workspace: 'shop' }
    expect(memberOf(t)).toEqual({ member: 'terraform', label: 'terraform', where: '' })
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
  it('is one write of the entry’s approved, after tests of its target and the approval it replaces (#1163)', () => {
    expect(approvePatch(0, { target: 'prod', approved: 'v1.2.0' }, 'v1.3.0')).toEqual([
      { op: 'test', path: '/spec/deploy/0/target', value: 'prod' },
      { op: 'test', path: '/spec/deploy/0/approved', value: 'v1.2.0' },
      { op: 'add', path: '/spec/deploy/0/approved', value: 'v1.3.0' },
    ])
  })

  it('tests the whole entry as read where it had no approval, so a newer first one fails it (#1461)', () => {
    expect(approvePatch(2, { target: 'prod', after: 'staging' }, 'v1.3.0')).toEqual([
      { op: 'test', path: '/spec/deploy/2/target', value: 'prod' },
      { op: 'test', path: '/spec/deploy/2', value: { target: 'prod', after: 'staging' } },
      { op: 'add', path: '/spec/deploy/2/approved', value: 'v1.3.0' },
    ])
  })

  it('is the same patch as a kubectl line', () => {
    const entry = { target: 'prod', approved: 'v1.2.0' }
    const c = container([{ target: 'staging' }, entry])
    expect(deploysOf(c)[1].entry).toBe(entry)
    expect(approveCommand(c, 1, entry, 'v1.3.0')).toBe(
      `kubectl -n shop patch container api --type=json -p '${JSON.stringify(approvePatch(1, entry, 'v1.3.0'))}'`,
    )
  })
})

describe('sourceOf and githubImage (#1603)', () => {
  it('fills the source as the operator does: the Container’s name, the folder and /, nothing for the whole repository', () => {
    expect(sourceOf(target({}))).toBeUndefined()
    expect(sourceOf(target({ github: { repository: 'acme/api', source: {} } }))).toEqual({
      path: '<container>',
      tagPrefix: '<container>/',
    })
    expect(sourceOf(target({ github: { repository: 'acme/api', source: { path: 'cli' } } }))).toEqual({
      path: 'cli',
      tagPrefix: 'cli/',
    })
    expect(sourceOf(target({ github: { repository: 'acme/api', source: { path: '.' } } }))).toEqual({
      path: '.',
      tagPrefix: '',
    })
    expect(sourceOf(target({ github: { repository: 'acme/api', source: { tagPrefix: '' } } }))).toEqual({
      path: '<container>',
      tagPrefix: '',
    })
  })

  it('names the GHCR image as model.GitHubImage does: lower-cased, the Container’s name unless tagPrefix is ""', () => {
    expect(githubImage(target({ github: { repository: 'Acme/API' } }), 'Orders')).toBe('ghcr.io/acme/api/orders')
    expect(githubImage(target({ github: { repository: 'acme/api', tagPrefix: 'x-' } }), 'orders')).toBe(
      'ghcr.io/acme/api/orders',
    )
    expect(githubImage(target({ github: { repository: 'Acme/API', tagPrefix: '' } }), 'orders')).toBe(
      'ghcr.io/acme/api',
    )
    expect(githubImage(target({ github: undefined, argoCD: { destination: { namespace: 'a' } } }))).toBeUndefined()
  })
})

describe('a GitHub Enterprise Server target (#1809)', () => {
  const ghes = target({ github: { url: 'https://ghe.example.com', repository: 'Acme/API' } })

  it('builds the releases link and the registry from url, as model.GitHubHostsOf does', () => {
    expect(githubHosts(ghes)).toEqual({ web: 'https://ghe.example.com', registry: 'containers.ghe.example.com' })
    expect(githubReleasesURL(ghes)).toBe('https://ghe.example.com/Acme/API/releases')
    expect(githubImage(ghes, 'Orders')).toBe('containers.ghe.example.com/acme/api/orders')
    const port = target({ github: { url: 'https://ghe.example.com:8443/', repository: 'acme/api', tagPrefix: '' } })
    expect(githubReleasesURL(port)).toBe('https://ghe.example.com:8443/acme/api/releases')
    expect(githubImage(port)).toBe('containers.ghe.example.com:8443/acme/api')
  })

  it('keeps github.com and ghcr.io while url is unset', () => {
    const dotcom = target({ github: { repository: 'Acme/API' } })
    expect(githubHosts(dotcom)).toEqual({ web: 'https://github.com', registry: 'ghcr.io' })
    expect(githubReleasesURL(dotcom)).toBe('https://github.com/Acme/API/releases')
    expect(githubImage(dotcom, 'Orders')).toBe('ghcr.io/acme/api/orders')
    expect(
      githubReleasesURL(target({ github: undefined, argoCD: { destination: { namespace: 'a' } } })),
    ).toBeUndefined()
  })
})

describe('a gitlab target (#1807)', () => {
  const gitlab = (member: NonNullable<DeployTarget['spec']['gitlab']>) => target({ github: undefined, gitlab: member })

  it('publishes to gitlab.com and registry.gitlab.com when url is unset', () => {
    const t = gitlab({ project: 'Acme/Apps/API' })
    expect(gitlabHosts(t)).toEqual({ web: 'https://gitlab.com', registry: 'registry.gitlab.com' })
    expect(gitlabReleasesURL(t)).toBe('https://gitlab.com/Acme/Apps/API/-/releases')
    expect(gitlabImage(t, 'Orders')).toBe('registry.gitlab.com/acme/apps/api/orders')
    expect(gitlabImage(t)).toBe('registry.gitlab.com/acme/apps/api/<container>')
  })

  it('publishes to the instance at url, a trailing slash or a relative URL root kept, whose registry only the project tells', () => {
    const t = gitlab({ url: 'https://git.example.com/', project: 'acme/api' })
    expect(gitlabHosts(t)).toEqual({ web: 'https://git.example.com' })
    expect(gitlabReleasesURL(t)).toBe('https://git.example.com/acme/api/-/releases')
    expect(gitlabImage(t, 'orders')).toBe('<registry>/acme/api/orders')
    const rooted = gitlab({ url: 'https://example.com:8443/gitlab', project: 'acme/api', tagPrefix: '' })
    expect(gitlabReleasesURL(rooted)).toBe('https://example.com:8443/gitlab/acme/api/-/releases')
    expect(gitlabImage(rooted, 'orders')).toBe('<registry>/acme/api')
  })

  it('reads the source as a github target’s, and none when unset', () => {
    expect(sourceOf(gitlab({ project: 'acme/api' }))).toBeUndefined()
    expect(sourceOf(gitlab({ project: 'acme/api', source: {} }))).toEqual({
      path: '<container>',
      tagPrefix: '<container>/',
    })
    expect(sourceOf(gitlab({ project: 'acme/api', source: { path: '.', tagPrefix: 'src-' } }))).toEqual({
      path: '.',
      tagPrefix: 'src-',
    })
  })

  it('is undefined for another member', () => {
    const gh = target({})
    expect(gitlabHosts(gh)).toBeUndefined()
    expect(gitlabReleasesURL(gh)).toBeUndefined()
    expect(gitlabImage(gh)).toBeUndefined()
    expect(githubImage(gitlab({ project: 'acme/api' }))).toBeUndefined()
  })
})
