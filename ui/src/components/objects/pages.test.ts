import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import type { BriefWriter } from '../../k8s/briefs'
import type { ContainerWriter } from '../../k8s/containers'
import type { CrewWriter } from '../../k8s/crews'
import type { Connection, StoryData } from '../../k8s/sources'
import type { StandardWriter } from '../../k8s/standards'
import type { UiSession } from '../../k8s/session'
import type { Container, DeployTarget, GitProvider } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { demoStory } from '../../demo/fixture'
import { absoluteTime } from '../../model/format'
import type { LifecycleKind } from '../../model/lifecycle'
import { GitProviderPage } from './CatalogPages'
import { ContainerDeploys, DeployTargetPage } from './DeployPages'
import { ObjectPage } from './ObjectPage'

const NOW = Date.parse('2026-06-01T12:00:00Z')
const LIVE: Connection = { phase: 'live', loaded: true, problems: [] }
const WRITER = {} as BriefWriter
const CONTAINER_WRITER = {} as ContainerWriter
const CREW_WRITER = {} as CrewWriter
const STANDARD_WRITER = {} as StandardWriter

beforeAll(() => {
  // The pages link to each other through the URL the shell is on.
  vi.stubGlobal('window', { location: { pathname: '/', search: '', hash: '' } })
  // The first date formatted in a locale loads ICU's data, about 60 ms on an
  // idle device; the Session page's times did it under the test's 5 s timer,
  // which a loaded device outlasted (#1526). Here it runs under the hook's own.
  absoluteTime(NOW)
})
afterAll(() => {
  vi.unstubAllGlobals()
})

function page(
  kind: LifecycleKind,
  name: string,
  data: StoryData = demoStory(NOW),
  connection = LIVE,
  session?: UiSession,
) {
  return renderToStaticMarkup(
    createElement(
      SessionContext.Provider,
      { value: session },
      createElement(ObjectPage, {
        kind,
        name,
        data,
        connection,
        writer: WRITER,
        containerWriter: CONTAINER_WRITER,
        crewWriter: CREW_WRITER,
        standardWriter: STANDARD_WRITER,
      }),
    ),
  )
}

/** The page's text, without its markup. */
function text(html: string) {
  return html.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')
}

describe('the read-only object pages', () => {
  it.each([
    ['Project', 'webshop', 'Its catalog and Git hosts'],
    ['SoftwareSystem', 'webshop-core', 'What it is for'],
    ['Relationship', 'orders-api-to-cart-cache', 'Its ends'],
    ['Session', demoStory(NOW).sessions[0].metadata.name, 'Who and when'],
    ['Standard', 'python-standard', 'Frameworks it checks'],
    ['Compute', 'small', 'Its template'],
    ['Deployment', 'k8s-manifests', 'Its template'],
    ['Bundle', 'python-service', 'Its entries'],
    ['Domain', 'webshop', 'Its template'],
    ['GitProvider', 'gitea', 'Where repositories go'],
    ['DeployTarget', 'prod', 'Where it ships'],
  ] as const)('opens a %s page', (kind, name, section) => {
    const html = page(kind, name)
    expect(html).toContain(`aria-label="${kind} ${name}"`)
    expect(text(html)).toContain(section)
  })

  it('shows the Project’s counts and versions from status', () => {
    const data = demoStory(NOW)
    data.project!.status!.factory = { operatorVersion: '0.14.0' }
    const t = text(page('Project', 'webshop', data))
    expect(t).toContain('Containers 5, 2 Ready')
    expect(t).toContain('Operator 0.14.0')
    expect(t).toContain('Chart not reported yet')
  })

  it('says why an AddedAfterScaffold Relationship is not applied, and links its Container’s repository', () => {
    const html = page('Relationship', 'orders-api-to-cart-cache')
    expect(html).toContain('aria-current="step"')
    expect(text(html)).toMatch(/Added after the scaffold/)
    expect(text(html)).toMatch(/by hand, in orders-api’s repository/)
    expect(html).toContain('href="http://gitea.gitea.svc.cluster.local:3000/webshop/orders-api"')
  })

  it('shows no stepper on a Relationship the factory does not apply', () => {
    const html = page('Relationship', 'customer-uses-webshop-core')
    expect(html).not.toContain('aria-label="Lifecycle"')
    expect(text(html)).toMatch(/it describes the model/)
  })

  it('lists the Containers that name a catalog entry, each linked to its page', () => {
    const html = page('Standard', 'python-standard')
    expect(html).toContain('href="/?ns=webshop&amp;kind=Container&amp;name=orders-api"')
    expect(text(html)).toContain('ruff.toml, .c4/checks/')
  })

  it('shows the Standard’s change to each file left out on a Container’s page, with actions off', () => {
    const data = demoStory(NOW)
    const c = data.containers.find((x) => x.metadata.name === 'orders-api')!
    c.status = {
      ...c.status,
      standard: {
        name: 'python-standard',
        ref: 'v1.3.0',
        behind: [
          {
            path: 'ruff.toml',
            ref: 'v1.1.0',
            change: '--- ruff.toml (v1.1.0)\n+++ ruff.toml (v1.3.0)\n+line-length = 120\n',
          },
        ],
      },
    }
    const html = page('Container', 'orders-api', data)
    expect(text(html)).toContain('Standard files left out')
    expect(html).toContain('<details class="left-out-change"><summary>What the Standard changed')
    expect(html).toContain('+line-length = 120')
  })
})

describe('the Bundle page', () => {
  it('lists its entries linked to their pages at their pinned refs, and the Containers that name it', () => {
    const html = page('Bundle', 'python-service')
    const t = text(html)
    expect(t).toContain('Python service')
    expect(t).toMatch(/Framework python-fastapi @ v1.3.0/)
    expect(t).toMatch(/Standard python-standard @ v1.2.0/)
    expect(t).toMatch(/Compute small @ v1.0.0/)
    expect(t).toMatch(/Deployment k8s-manifests @ v1.1.0/)
    expect(html).toContain('href="/?ns=webshop&amp;kind=Standard&amp;name=python-standard"')
    expect(html).toContain('href="/?ns=webshop&amp;kind=Compute&amp;name=small"')
    expect(html).not.toContain('kind=Framework')
    expect(t).toContain('Containers that name it')
    expect(html).toContain('href="/?ns=webshop&amp;kind=Container&amp;name=inventory-api"')
    expect(html).not.toContain('name=orders-api"')
  })

  it('computes the Container types its entries share, as the operator does', () => {
    // Framework python-fastapi lists only service, so service is all they share.
    expect(text(page('Bundle', 'python-service'))).toMatch(/Container types service /)
    const data = demoStory(NOW)
    data.frameworks[0].spec.supportedTypes = ['job', 'worker', 'service']
    // The first entry's order, kept to what the Standard, Compute (no job) and Deployment list too.
    expect(text(page('Bundle', 'python-service', data))).toMatch(/Container types worker, service /)
    data.computes[0].spec.supportedTypes = ['static-site']
    expect(text(page('Bundle', 'python-service', data))).toContain('None: its entries share no Container type.')
  })

  it('says when an entry is not in the catalog, and advertises no type then', () => {
    const data = demoStory(NOW)
    data.bundles[0].spec.standardRef = { name: 'gone-standard' }
    const html = page('Bundle', 'python-service', data)
    expect(text(html)).toMatch(/Standard gone-standard · not in the catalog/)
    expect(html).toContain('href="/?ns=webshop&amp;kind=Standard&amp;name=gone-standard"')
    expect(text(html)).toContain('None: an entry is not in the catalog.')
  })
})

describe('the Domain page', () => {
  it('shows its template and where it lands, and the systems that name it with how many Containers hold it', () => {
    const html = page('Domain', 'webshop')
    const t = text(html)
    expect(t).toContain('Webshop')
    expect(t).toMatch(/Template http:\/\/gitea[^ ]*\/templates\/webshop-domain/)
    expect(t).toMatch(/Pinned at v1.0.0/)
    expect(t).toMatch(/Lands under docs\/agents\/domain\/webshop\//)
    expect(t).toContain('Systems that name it')
    expect(html).toContain('href="/?ns=webshop&amp;kind=SoftwareSystem&amp;name=webshop-core"')
    // orders-api was scaffolded with it; the system's four other Containers were not.
    expect(t).toMatch(/webshop-core · 1 of 5 Containers scaffolded with it/)
  })

  it('lists no system when none names it', () => {
    const data = demoStory(NOW)
    data.systems[0].spec!.domainRefs = []
    const html = page('Domain', 'webshop', data)
    expect(html).not.toContain('kind=SoftwareSystem')
  })
})

describe('a system’s Domains', () => {
  it('links each Domain to its page with the catalog’s pinned ref', () => {
    const html = page('SoftwareSystem', 'webshop-core')
    expect(html).toContain('href="/?ns=webshop&amp;kind=Domain&amp;name=webshop"')
    expect(text(html)).toMatch(/Domains webshop @ v1.0.0/)
  })

  it('says when a named Domain is not in the catalog, and None when the system names none', () => {
    const data = demoStory(NOW)
    data.systems[0].spec!.domainRefs = ['webshop', 'payments']
    expect(text(page('SoftwareSystem', 'webshop-core', data))).toMatch(/payments · not in the catalog/)
    data.systems[0].spec!.domainRefs = undefined
    expect(text(page('SoftwareSystem', 'webshop-core', data))).toMatch(/Domains None\. Containers/)
  })
})

describe('a Container’s Domains', () => {
  it('shows the Domains it was scaffolded with, linked, at the ref in scaffoldedFrom, and that they are applied', () => {
    const html = page('Container', 'orders-api')
    const t = text(html)
    expect(html).toContain('href="/?ns=webshop&amp;kind=Domain&amp;name=webshop"')
    expect(t).toMatch(/Domains webshop @ v1.0.0 Scaffolded from/)
    expect(t).toContain('Its system’s Domains are the ones it was scaffolded with')
  })

  it('says a Domain named after the scaffold is not in the repository, with the operator’s message', () => {
    const html = page('Container', 'inventory-api')
    const t = text(html)
    // Scaffolded with none, so the row says so; the condition explains the difference.
    expect(t).toMatch(/Domains None\. Scaffolded from/)
    expect(t).toContain(
      'Its system’s Domains changed after the scaffold; the repository keeps the ones it was scaffolded with',
    )
    expect(t).toContain('Domains added after the scaffold: webshop; scaffolding happens once')
    // The Relationship wording for the same reason stays its own.
    expect(t).not.toContain('Added after scaffolding, so the repository does not include it')
  })

  it('says a Container not yet scaffolded has no Domains yet', () => {
    const data = demoStory(NOW)
    const pending = data.containers.find((c) => !c.status?.scaffoldedFrom?.length)!
    expect(text(page('Container', pending.metadata.name, data))).toMatch(/Domains Not scaffolded yet\. Scaffolded from/)
  })
})

describe('a bundled Container’s page', () => {
  it('shows the Bundle and the sides it expanded to, each from the bundle, beside scaffoldedFrom', () => {
    const html = page('Container', 'inventory-api')
    const t = text(html)
    expect(html).toContain('href="/?ns=webshop&amp;kind=Bundle&amp;name=python-service"')
    expect(t).toMatch(/Bundle python-service Framework python-fastapi @ v1.3.0 · from bundle python-service/)
    expect(t).toMatch(/Standard python-standard @ v1.2.0 · from bundle python-service/)
    expect(t).toMatch(/Compute small · from bundle python-service/)
    expect(t).toMatch(/Deployment k8s-manifests · from bundle python-service/)
    expect(html).toContain('href="/?ns=webshop&amp;kind=Standard&amp;name=python-standard"')
    expect(t).toMatch(/Scaffolded from python-fastapi @ v1.3.0 python-standard @ v1.2.0/)
    // Its Standard comes from the bundle, so the checks section is there.
    expect(t).toContain('When CI runs the full check')
    expect(t).toContain('Its Bundle still names what it was scaffolded from')
  })

  it('says the Bundle is not expanded yet before the operator fills status.bundle', () => {
    const data = demoStory(NOW)
    const c = data.containers.find((x) => x.metadata.name === 'inventory-api')!
    c.status = { phase: 'Pending' }
    const t = text(page('Container', 'inventory-api', data))
    expect(t).toMatch(/Bundle python-service · not expanded yet/)
    expect(t).not.toContain('Standard python-standard')
    expect(t).not.toContain('When CI runs the full check')
  })

  it('shows BundleCurrent=False with its message, and enforces nothing', () => {
    const data = demoStory(NOW)
    const c = data.containers.find((x) => x.metadata.name === 'inventory-api')!
    c.status!.conditions = c.status!.conditions!.map((cond) =>
      cond.type === 'BundleCurrent'
        ? {
            ...cond,
            status: 'False',
            reason: 'BundleChanged',
            message: 'Bundle python-service now names compute medium',
          }
        : cond,
    )
    const t = text(page('Container', 'inventory-api', data))
    expect(t).toContain(
      'Its Bundle changed or is gone since the scaffold; the repository keeps what it was scaffolded from',
    )
    expect(t).toContain('Bundle python-service now names compute medium')
    // What it was scaffolded from stays what the page shows.
    expect(t).toMatch(/Compute small · from bundle python-service/)
  })
})

describe('the GitProvider page', () => {
  it('names the Secret, and renders none of its data or any field it does not name', () => {
    const provider = demoStory(NOW).gitProviders[0]
    // Whatever else the object carries (a token pasted where it doesn't
    // belong, an annotation), the page shows only the fields it names.
    const leaky = {
      ...provider,
      metadata: { ...provider.metadata, annotations: { token: 'sekrit-annotation' } },
      spec: { ...provider.spec, token: 'sekrit-token', password: 'sekrit-password' },
      data: { token: btoa('sekrit-data') },
    } as GitProvider
    const html = renderToStaticMarkup(createElement(GitProviderPage, { provider: leaky, containers: [] }))
    expect(text(html)).toContain('Secret gitea-credentials')
    expect(text(html)).toContain('the UI never reads Secrets')
    expect(html).not.toMatch(/sekrit|c2Vrcml0/)
  })
})

describe('github targets (#1603)', () => {
  const github = (member: DeployTarget['spec']['github']): DeployTarget => ({
    metadata: { name: 'oss', namespace: 'webshop' },
    spec: { github: member, credentialsSecretRef: { name: 'oss-github' } },
  })
  const shipped = (name: string, type: Container['spec']['type'], image?: string): Container => ({
    metadata: { name, namespace: 'webshop' },
    spec: {
      softwareSystemRef: { name: 'webshop' },
      type,
      gitProviderRef: { name: 'gitea' },
      frameworkRef: { name: 'go' },
      deploy: [{ target: 'oss' }],
    },
    status: {
      deploys: [
        {
          target: 'oss',
          phase: 'Deployed',
          version: 'v0.1.0',
          wantedVersion: 'v0.1.0',
          url: `https://github.com/webshop-co/webshop/releases/tag/${name}-v0.1.0`,
          image,
        },
      ],
    },
  })
  // text() spaces each tag and keeps entities; read it as a person does.
  const plain = (html: string) => text(html).replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/ ,/g, ',')
  const deploys = (c: Container) =>
    text(renderToStaticMarkup(createElement(ContainerDeploys, { container: c, targets: [], writer: CONTAINER_WRITER })))

  it('shows the source, the tags, the image name and the Secret’s key names, unset parts as the operator fills them', () => {
    const html = renderToStaticMarkup(
      createElement(DeployTargetPage, {
        target: github({ repository: 'Webshop-Co/Webshop', source: {} }),
        containers: [],
      }),
    )
    const t = plain(html)
    expect(t).toContain('Tags <container>-v1.2.3 (tagPrefix not set: the Container’s name and -)')
    expect(t).toContain('Source folder <container>, tagged <container>/v1.2.3 (path not set: each Container’s name)')
    expect(t).toContain(
      'Image ghcr.io/webshop-co/webshop/<container> · copied, for a Container whose release builds an image, when the Secret holds packagesToken',
    )
    expect(t).toContain('Secret oss-github')
    expect(t).toMatch(
      /The operator reads its keys token \(a fine-grained token\), or appId, installationId and privateKey \(a GitHub App\); and packagesToken, when there, for images on its registry\./,
    )
  })

  it('shows a set source path and tagPrefix, the bare tags and image of a repository of one app, and no Latest', () => {
    const t = plain(
      renderToStaticMarkup(
        createElement(DeployTargetPage, {
          target: github({
            repository: 'webshop-co/orders-cli',
            tagPrefix: '',
            latest: false,
            source: { path: '.', tagPrefix: 'src-' },
          }),
          containers: [],
        }),
      ),
    )
    expect(t).toContain('Tags v1.2.3 · never marked Latest')
    expect(t).toContain('Source folder ., tagged src-v1.2.3')
    expect(t).not.toContain('path not set')
    expect(t).toContain('Image ghcr.io/webshop-co/orders-cli ·')
  })

  it('says a target without source publishes the release only', () => {
    const t = plain(
      renderToStaticMarkup(
        createElement(DeployTargetPage, { target: github({ repository: 'webshop-co/webshop' }), containers: [] }),
      ),
    )
    expect(t).toContain('Source not published: the release only')
  })

  it('shows a Container of each artifact kind Deployed to a github target, with the image a k8s-manifests one copied', () => {
    const cli = deploys(shipped('orders-cli', 'cli'))
    const manifests = deploys(
      shipped('orders-api', 'service', 'ghcr.io/webshop-co/webshop/orders-api@sha256:' + 'a'.repeat(64)),
    )
    const service = deploys(shipped('orders-svc', 'service'))
    for (const t of [cli, manifests, service]) {
      expect(t).toContain('oss Deployed')
      expect(t).toContain('Runs v0.1.0 Wanted v0.1.0')
    }
    expect(manifests).toContain('Image ghcr.io/webshop-co/webshop/orders-api@sha256:' + 'a'.repeat(64))
    expect(cli).not.toContain('Image')
    expect(service).not.toContain('Image')
  })
})

describe('deploys (#1163)', () => {
  it('shows a DeployTarget’s member, tier, approval, Ready and the Containers that ship to it', () => {
    const t = text(page('DeployTarget', 'prod'))
    expect(t).toContain('Tool GitHub releases (github)')
    expect(t).toContain('Where webshop-co/orders-api')
    expect(t).toContain('Tier production')
    expect(t).toContain('Approval Not set: production’s default, a person approves each version')
    expect(t).toContain('Ready Ready GitHub accepts the token for webshop-co/orders-api')
    expect(t).toContain('Secret github-prod')
    expect(t).toContain('domain: orders.webshop.example')
    expect(t).toContain('orders-api · Waiting, runs v0.1.0')
  })

  it('names a DeployTarget’s Secret and renders none of its data or any field it does not name', () => {
    const prod = demoStory(NOW).deployTargets[1]
    const leaky = {
      ...prod,
      metadata: { ...prod.metadata, annotations: { token: 'sekrit-annotation' } },
      spec: { ...prod.spec, token: 'sekrit-token' },
      data: { token: btoa('sekrit-data') },
    } as DeployTarget
    const html = renderToStaticMarkup(createElement(DeployTargetPage, { target: leaky, containers: [] }))
    expect(text(html)).toContain('Secret github-prod')
    expect(html).not.toMatch(/sekrit|c2Vrcml0/)
  })

  it('renders an argoCD target, which names no Secret, instead of throwing (#1675)', () => {
    const argo: DeployTarget = {
      metadata: { name: 'kind-cluster', namespace: 'webshop' },
      spec: { argoCD: { destination: { namespace: 'webshop-apps' } }, tier: 'development' },
    }
    const html = renderToStaticMarkup(createElement(DeployTargetPage, { target: argo, containers: [] }))
    const t = text(html)
    expect(t).toContain('Tool Argo CD (argoCD)')
    expect(t).toContain('Where webshop-apps')
    expect(t).toContain('Credentials none')
    expect(t).not.toContain('Credentials Secret')
  })

  it('shows a cloudFormation target’s region and account (#1609)', () => {
    const aws: DeployTarget = {
      metadata: { name: 'aws-dev', namespace: 'webshop' },
      spec: {
        cloudFormation: { region: 'eu-west-1', account: '123456789012' },
        credentialsSecretRef: { name: 'aws-dev-keys' },
      },
    }
    const t = text(renderToStaticMarkup(createElement(DeployTargetPage, { target: aws, containers: [] })))
    expect(t).toContain('Tool AWS CloudFormation (cloudFormation)')
    expect(t).toContain('Where eu-west-1, account 123456789012')
    expect(t).toContain('aws-dev-keys')
  })

  it('lists the Project’s deploy targets with their member, tier, approval and Ready', () => {
    const html = page('Project', 'webshop')
    expect(html).toContain('href="/?ns=webshop&amp;kind=DeployTarget&amp;name=staging"')
    expect(text(html)).toContain(
      'prod Ready · GitHub releases webshop-co/orders-api · production · Not set: production’s default',
    )
  })

  it('shows a Container’s deploys, and the kubectl line that approves the version waiting, with actions off', () => {
    const html = page('Container', 'orders-api')
    const t = text(html)
    expect(t).toContain('Where it ships')
    expect(t).toContain('staging Deployed Healthy')
    expect(html).toContain('href="https://github.com/webshop-co/orders-staging/releases/tag/v0.2.0"')
    expect(t).toContain('prod Waiting Healthy')
    expect(t).toContain('Runs v0.1.0 Wanted v0.2.0 After staging Approved v0.1.0')
    expect(t).toContain('v0.2.0 waits for a person’s approval to ship to prod. To approve it, run:')
    expect(html).toContain('/spec/deploy/1/approved')
    expect(html).not.toContain('Approve v0.2.0')
  })

  it('offers the approval as a button with actions on', () => {
    const html = page('Container', 'orders-api', demoStory(NOW), LIVE, {
      actions: true,
      mode: 'service-account',
      user: null,
    })
    expect(text(html)).toContain('Approve v0.2.0…')
    expect(text(html)).toContain('Ship v0.2.0 of orders-api to prod?')
    expect(html).not.toContain('kubectl -n webshop patch')
  })

  it('asks a person signed out to sign in, and still gives the kubectl line', () => {
    const html = page('Container', 'orders-api', demoStory(NOW), LIVE, {
      actions: true,
      mode: 'oidc',
      user: null,
      login: '/ui/login',
      logout: '/ui/logout',
    })
    expect(text(html)).toContain('Sign in to approve it here, or run:')
    expect(html).toContain('kubectl -n webshop patch container orders-api')
  })

  it('shows no deploys on a Container that names no target', () => {
    expect(text(page('Container', 'storefront'))).not.toContain('Where it ships')
  })
})

describe('a refused read of an optional kind', () => {
  const refused: Connection = {
    ...LIVE,
    unread: [{ what: 'standards', code: 403, message: 'standards.c4.factory.io is forbidden', resource: 'standards' }],
  }

  it('shows on a Bundle page before its entries, instead of “not in the catalog” alone', () => {
    const data = { ...demoStory(NOW), standards: [] }
    const t = text(page('Bundle', 'python-service', data, refused))
    expect(t).toContain('Could not read standards (403): standards.c4.factory.io is forbidden')
    expect(t).toMatch(/Standard python-standard · not in the catalog/)
  })

  it('shows on the kind’s page instead of “not there”', () => {
    const data = { ...demoStory(NOW), standards: [] }
    const t = text(page('Standard', 'python-standard', data, refused))
    expect(t).toContain('Could not read standards. The UI is not allowed to read these.')
    expect(t).toContain('(403: standards.c4.factory.io is forbidden)')
    expect(t).not.toContain('There is no Standard')
  })

  it('shows in the Project page’s catalog, beside the kinds it could read', () => {
    const data = { ...demoStory(NOW), standards: [] }
    const t = text(page('Project', 'webshop', data, refused))
    expect(t).toContain('Standards Could not read standards (403)')
    expect(t).toContain('Computes small')
  })

  it('says a missing object is not there when the read succeeded', () => {
    expect(text(page('Compute', 'large'))).toContain('There is no Compute named large in webshop')
  })
})

describe('kinds without conditions', () => {
  it.each([
    ['Standard', 'python-standard'],
    ['GitProvider', 'gitea'],
  ] as const)('a %s page shows no Conditions section', (kind, name) => {
    expect(text(page(kind, name))).not.toContain('Conditions')
  })
})

describe('a failed re-watch of an optional kind', () => {
  it('says the failure above what was loaded, and keeps it', () => {
    const blip: Connection = {
      ...LIVE,
      unread: [{ what: 'computes', message: 'Cannot reach the Kubernetes API', resource: 'computes' }],
    }
    const t = text(page('Project', 'webshop', demoStory(NOW), blip))
    expect(t).toContain('Computes Could not read computes: Cannot reach the Kubernetes API small')
  })
})

describe('an optional kind still loading', () => {
  it('says it is loading, not that the object is not there', () => {
    const loading: Connection = { ...LIVE, settled: false }
    const data = { ...demoStory(NOW), computes: [] }
    expect(text(page('Compute', 'small', data, loading))).toContain('Loading the Compute')
  })
})

describe('gitlab targets (#1807)', () => {
  const gitlab = (member: DeployTarget['spec']['gitlab']): DeployTarget => ({
    metadata: { name: 'lab', namespace: 'webshop' },
    spec: { gitlab: member, credentialsSecretRef: { name: 'lab-gitlab' } },
  })
  const plain = (html: string) => text(html).replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/ ,/g, ',')
  const page = (target: DeployTarget) =>
    renderToStaticMarkup(createElement(DeployTargetPage, { target, containers: [] }))

  it('shows a gitlab.com project: its releases page, tags, source, registry image and the Secret’s key, unset parts as the operator fills them', () => {
    const html = page(gitlab({ project: 'Webshop-Co/Apps/Webshop', source: {} }))
    const t = plain(html)
    expect(t).toContain('Tool GitLab releases (gitlab)')
    expect(t).toContain('Where Webshop-Co/Apps/Webshop')
    expect(html).toContain('href="https://gitlab.com/Webshop-Co/Apps/Webshop/-/releases"')
    expect(t).toContain('Tags <container>-v1.2.3 (tagPrefix not set: the Container’s name and -)')
    expect(t).not.toContain('Latest')
    expect(t).toContain('Source folder <container>, tagged <container>/v1.2.3 (path not set: each Container’s name)')
    expect(t).toContain(
      'Image registry.gitlab.com/webshop-co/apps/webshop/<container> · copied to the project’s container registry, for a Container whose release builds an image, when the registry is on and the token has write_registry',
    )
    expect(t).not.toContain('the instance names its registry')
    expect(t).toContain('Secret lab-gitlab')
    expect(t).toMatch(
      /The operator reads its key token, a project access token with the Maintainer role and the scopes api, read_registry and write_registry/,
    )
    expect(t).not.toContain('packagesToken')
  })

  it('shows a self-hosted instance at url without source: its releases page there, the release only, and a registry only the project tells', () => {
    const html = page(
      gitlab({ url: 'https://git.example.com/gitlab', project: 'webshop-co/orders-cli', tagPrefix: '' }),
    )
    const t = plain(html)
    expect(html).toContain('href="https://git.example.com/gitlab/webshop-co/orders-cli/-/releases"')
    expect(t).toContain('Tags v1.2.3')
    expect(t).toContain('Source not published: the release only')
    expect(t).toContain('Image <registry>/webshop-co/orders-cli ·')
    expect(t).toContain('the instance names its registry')
  })
})
