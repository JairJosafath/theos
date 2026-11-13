import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import type { BriefWriter } from '../../k8s/briefs'
import type { ContainerWriter } from '../../k8s/containers'
import type { CrewWriter } from '../../k8s/crews'
import type { Connection, StoryData } from '../../k8s/sources'
import type { StandardWriter } from '../../k8s/standards'
import type { Task, TestEnvironment } from '../../k8s/types'
import { demoStory } from '../../demo/fixture'
import { FixedNow } from '../../lib/hooks'
import type { LifecycleKind } from '../../model/lifecycle'
import { ObjectPage } from './ObjectPage'
import { TaskEnvironments } from './TestEnvironmentPages'

const NOW = Date.parse('2026-11-06T12:00:00Z')
const LIVE: Connection = { phase: 'live', loaded: true, problems: [] }

beforeAll(() => {
  // The pages link to each other through the URL the shell is on.
  vi.stubGlobal('window', { location: { pathname: '/', search: '', hash: '' } })
})
afterAll(() => {
  vi.unstubAllGlobals()
})

/** An object page at NOW, the fixture's clock, so today's spend is the fixture's today. */
function page(kind: LifecycleKind, name: string, data: StoryData = demoStory(NOW), connection = LIVE) {
  return renderToStaticMarkup(
    createElement(
      FixedNow.Provider,
      { value: NOW },
      createElement(ObjectPage, {
        kind,
        name,
        data,
        connection,
        writer: {} as BriefWriter,
        containerWriter: {} as ContainerWriter,
        crewWriter: {} as CrewWriter,
        standardWriter: {} as StandardWriter,
      }),
    ),
  )
}

/** The demo's environment of this name, to change for one test. */
function envOf(data: StoryData, name: string): TestEnvironment {
  const env = data.testEnvironments.find((e) => e.metadata.name === name)
  if (!env) throw new Error(`the demo has no TestEnvironment ${name}`)
  return env
}

/** The page's text, without its markup. */
const text = (html: string) => html.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')

describe('the Project page’s test environments (test-environments, step 6)', () => {
  it('lists each with Ready, its live leases against its limits, today’s spend and who holds a lease', () => {
    const html = page('Project', 'webshop')
    const t = text(html)
    expect(t).toContain('Where its runs test')
    expect(t).toContain(
      'browser Ready · 1 of 2 leased · Nothing today: a local lease costs nothing · 2 leases at once, each 180 min at most',
    )
    expect(t).toContain('Ready held by orders-api-24')
    expect(html).toContain('href="/?ns=webshop&amp;kind=TestEnvironment&amp;name=browser"')
    expect(html).toContain('href="/?ns=webshop&amp;kind=Task&amp;name=orders-api-24"')
    expect(t).toContain(
      'android Ready · 0 of 1 leased · Nothing today: a local lease costs nothing · 1 lease at once, each 180 min at most',
    )
  })

  it('says when every lease is taken, and why a lease waits', () => {
    const data = demoStory(NOW)
    const android = data.testEnvironments.find((e) => e.metadata.name === 'android')!
    android.status = {
      ...android.status,
      leases: [
        {
          id: 'android-0a1b2c3d4e',
          holder: 'orders-api-24',
          state: 'Pending',
          message: 'the pod is not scheduled: 0/3 nodes are available: 3 Insufficient devices.kubevirt.io/kvm.',
        },
      ],
    }
    const t = text(page('Project', 'webshop', data))
    expect(t).toContain('android Ready · 1 of 1 leased, all taken')
    expect(t).toContain('Pending held by orders-api-24')
    expect(t).toContain('3 Insufficient devices.kubevirt.io/kvm.')
  })

  it('says it has none, and a refused read instead of none', () => {
    const data = { ...demoStory(NOW), testEnvironments: [] }
    expect(text(page('Project', 'webshop', data))).toContain('None: no TestEnvironment in this project')
    const refused: Connection = {
      ...LIVE,
      unread: [
        {
          what: 'test environments',
          code: 403,
          message: 'testenvironments.theos.factory.io is forbidden',
          resource: 'testenvironments',
        },
      ],
    }
    const t = text(page('Project', 'webshop', data, refused))
    expect(t).toContain('Could not read test environments (403): testenvironments.theos.factory.io is forbidden')
    expect(t).not.toContain('None: no TestEnvironment')
  })
})

describe('the Project page’s remote test environments (test-environments, steps 7 to 10)', () => {
  it('lists each with where it runs, today’s spend against its budget, and a CI job’s runner as a holder', () => {
    const html = page('Project', 'webshop')
    const t = text(html)
    expect(t).toContain(
      'gpu Ready · EC2 g5.xlarge in eu-west-1, account 123456789012 · 1 of 1 leased, all taken · $3.42 of $10.00 today · 1 lease at once, each 120 min at most',
    )
    // A machine running a CI job is held by the runner registered for it, not a Task.
    expect(t).toMatch(
      /Ready held by CI runner 118 of gitea ?, for one job · since 6 min ago .* · \$0\.11 · gpu-ci-7c2e91d4a0/,
    )
    expect(html).toContain('href="/?ns=webshop&amp;kind=GitProvider&amp;name=gitea"')
    expect(html).not.toContain('kind=Task&amp;name=ci')
    expect(t).toContain(
      'devices Ready · Device Farm in us-west-2, account 123456789012 · 1 of 2 leased · $4.08 of $20.00 today',
    )
  })

  it('says the day’s budget is spent', () => {
    const data = demoStory(NOW)
    envOf(data, 'gpu').status!.spentToday = { day: '2026-11-06', milliUSD: 10_250 }
    expect(text(page('Project', 'webshop', data))).toContain('$10.25 of $10.00 today, the budget spent')
  })

  it('says why a credential or CI fails, in words, and of the credential the Secret’s name only', () => {
    const data = demoStory(NOW)
    envOf(data, 'gpu').status!.conditions!.push({
      type: 'CI',
      status: 'False',
      reason: 'NoPublicAddress',
      message: 'no GitProvider’s jobs run here: gitea: its ci block gives no publicURL',
    })
    envOf(data, 'gpu').status!.conditions = envOf(data, 'gpu').status!.conditions!.filter(
      (c) => !(c.type === 'CI' && c.status === 'True'),
    )
    const t = text(page('Project', 'webshop', data))
    expect(t).toContain(
      'ios Credentials invalid · EC2 mac2.metal in us-east-1, account 123456789012 · 0 of 1 leased · $0.00 of $25.00 today',
    )
    expect(t).toContain(
      'Credentials Credential refused the provider refuses the credential in Secret ios-aws: the credential of test environment webshop/ios is of AWS account 210987654321',
    )
    expect(t).toContain('CI No public address no GitProvider’s jobs run here')
    // A valid credential and a CI that serves say nothing more on the list.
    expect(t).not.toContain('Credentials Valid')
  })

  it('lists a Device Farm environment’s test runs under its lease, the newest first, with their outcome', () => {
    const html = page('Project', 'webshop')
    const t = text(html)
    expect(t).toMatch(
      /Running instrumentation run of 5b6c7d8e9f0a ?, asked for by orders-api-24 on orders-api · since 3 min ago .* · the run on Device Farm Failed instrumentation run of a81f3c09d2e4/,
    )
    expect(t).toContain('$0.68 · the run on Device Farm failed: 11 passed, 1 failed of 12 tests on 2 devices')
    expect(t).toContain('Its outcome goes on orders-api-24’s pull request once there is one')
    expect(html).toContain('aria-label="Test runs on devices"')
    expect(html).toContain('href="/?ns=webshop&amp;kind=Container&amp;name=orders-api"')
    expect(html).toContain(
      'href="https://us-west-2.console.aws.amazon.com/devicefarm/home?region=us-west-2#/mobile/projects/0b6f3c1e-1d2a-4c5b-9e8f-7a6b5c4d3e2f/runs/9d8c7b6a-5f4e-4d3c-8b2a-1f0e9d8c7b6a"',
    )
  })
})

describe('a TestEnvironment’s page', () => {
  it('shows what it has, its leases and limits, its CI pool and how a run uses it', () => {
    const html = page('TestEnvironment', 'browser')
    const t = text(html)
    expect(html).toContain('aria-label="TestEnvironment browser"')
    expect(t).toContain('Provider A pod per lease, on this cluster (local)')
    expect(t).toContain('Image ghcr.io/jairjosafath/theos/env-browser:0.30.0')
    expect(t).toContain('Devices None.')
    expect(t).toContain('Resources requests: cpu 500m, memory 1Gi; limits: cpu 2, memory 4Gi')
    expect(t).toContain('Live 1 of 2 leased')
    expect(t).toContain('Limits 2 leases at once, each 180 min at most')
    expect(t).toContain('Ready held by orders-api-24')
    expect(t).toContain('browser-5f0c3a9e21')
    expect(t).toContain('theos-env run browser -- &lt;command&gt;')
    expect(t).toContain('CI 1 runner pod for runs-on: env-browser')
    // The Task whose run holds it is among its related objects.
    expect(t).toContain('Tasks whose runs hold it')
  })

  it('shows its devices and placement as given, and that every lease is taken', () => {
    const data = demoStory(NOW)
    const android = data.testEnvironments.find((e) => e.metadata.name === 'android')!
    android.spec.local = {
      ...android.spec.local!,
      runtimeClassName: 'kvm',
      nodeSelector: { 'theos.factory.io/kvm': 'true' },
      tolerations: [{ key: 'kvm', operator: 'Exists', effect: 'NoSchedule' }],
    }
    android.status = { ...android.status, leases: [{ id: 'android-1', holder: 'orders-api-24', state: 'Ready' }] }
    const t = text(page('TestEnvironment', 'android', data))
    expect(t).toContain('Devices devices.kubevirt.io/kvm: 1')
    expect(t).toContain('Runtime class kvm')
    expect(t).toContain('Placement node theos.factory.io/kvm=true; tolerates kvm:NoSchedule')
    expect(t).toMatch(/Live 1 of 1 leased ?: a run that wants one more waits, AllLeased/)
    expect(t).toContain('CI None: no runner takes runs-on: env-android until ci.size is set')
  })

  it('says Ready’s reason when it is not, in its own words', () => {
    const data = demoStory(NOW)
    data.testEnvironments[0].status!.conditions = [
      {
        type: 'Ready',
        status: 'False',
        reason: 'FenceMissing',
        message: 'NetworkPolicy theos-test-environment, which fences the environment’s pods, is missing',
      },
    ]
    const t = text(page('TestEnvironment', 'browser', data))
    expect(t).toContain('Ready Fence missing NetworkPolicy theos-test-environment')
  })
})

describe('a remote TestEnvironment’s page (test-environments, steps 7 to 10)', () => {
  it('shows an aws member’s machine, its credential by the Secret’s name, its budget and its CI', () => {
    const html = page('TestEnvironment', 'gpu')
    const t = text(html)
    expect(t).toContain('Provider An EC2 machine per lease, in your AWS account (aws)')
    expect(t).toContain(
      'Region eu-west-1 Account 123456789012 Instance type g5.xlarge Machine image ami-0123456789abcdef0 Subnet subnet-0123456789abcdef0',
    )
    expect(t).not.toContain('Mac hosts')
    expect(t).toContain('Credentials Secret gpu-aws · its name only: the UI never reads Secrets')
    expect(t).toMatch(/reads its keys accessKeyId ?, secretAccessKey ?and, when there, sessionToken/)
    expect(t).toContain('Credentials valid Valid the provider takes the credential in Secret gpu-aws')
    expect(t).toContain('Spent $3.42 of $10.00 today · by the provider’s count')
    expect(t).toMatch(/Live 1 of 1 leased ?: a run that wants one more waits, AllLeased/)
    expect(t).toContain('Ready held by CI runner 118 of gitea')
    // Its CI is a machine per job, and its CI condition says it serves.
    expect(t).toContain(
      'CI A machine per waiting job for runs-on: env-gpu, within its limits and the day’s budget Serving a machine is leased for each job labelled env-gpu of GitProvider gitea that waits',
    )
    expect(t).toContain('theos-env run gpu -- &lt;command&gt;')
    // The CI job's runner is not a Task whose run holds it; the Status tab names its CI condition as CI.
    expect(t).toContain('Tasks whose runs hold it Nothing yet.')
    expect(t).toContain('CI: yes')
  })

  it('says a spent budget, and a CI that serves no job, in words', () => {
    const data = demoStory(NOW)
    const gpu = envOf(data, 'gpu')
    gpu.status!.spentToday = { day: '2026-11-06', milliUSD: 10_000 }
    gpu.status!.conditions = gpu.status!.conditions!.map((c) =>
      c.type === 'CI'
        ? {
            type: 'CI',
            status: 'False',
            reason: 'OverBudget',
            message:
              "2 jobs labelled env-gpu wait, and the day's budget is spent: no machine starts until tomorrow, or a higher budget",
          }
        : c,
    )
    const t = text(page('TestEnvironment', 'gpu', data))
    expect(t).toMatch(
      /Spent \$10\.00 of \$10\.00 today, the budget spent ?: no lease starts until tomorrow \(UTC\), or a higher limits\.budgetUSDPerDay, OverBudget/,
    )
    expect(t).toContain('Over budget 2 jobs labelled env-gpu wait')
  })

  it('shows a Device Farm member’s project and devices, its test runs and how a run asks for one', () => {
    const html = page('TestEnvironment', 'devices')
    const t = text(html)
    expect(t).toContain('Provider AWS Device Farm’s real phones, which run test packages (deviceFarm)')
    expect(t).toContain(
      'Project arn:aws:devicefarm:us-west-2:123456789012:project:0b6f3c1e-1d2a-4c5b-9e8f-7a6b5c4d3e2f Device pool arn:aws:devicefarm:us-west-2:123456789012:devicepool:',
    )
    expect(t).toContain('Credentials Secret devices-aws')
    expect(t).toContain('Spent $4.08 of $20.00 today')
    expect(t).toContain('Its test runs Running instrumentation run of 5b6c7d8e9f0a')
    expect(t).toContain('Failed instrumentation run of a81f3c09d2e4')
    expect(t).toContain('failed: 11 passed, 1 failed of 12 tests on 2 devices')
    expect(t).toContain('theos-env test devices --type &lt;type&gt; --app &lt;file&gt; --tests &lt;file&gt;')
    expect(t).not.toContain('theos-env run devices')
    expect(t).toContain('CI None: a Device Farm environment runs test packages, not CI jobs')
    expect(t).toContain('Tasks whose runs hold it Task orders-api-24 · Ready')
  })

  it('says a Device Farm environment has no test run yet', () => {
    const data = demoStory(NOW)
    envOf(data, 'devices').status!.testRuns = []
    expect(text(page('TestEnvironment', 'devices', data))).toContain(
      'Its test runs None yet: no run that holds a lease asked for one.',
    )
    // Only a Device Farm environment has test runs.
    expect(text(page('TestEnvironment', 'gpu'))).not.toContain('Its test runs')
  })

  it('says why its credential is refused and how an EC2 Mac’s hosts are kept, and shows no credential', () => {
    const data = demoStory(NOW)
    // A key put where the CRD has no field (the API server prunes it) is never shown: the page names fields one by one.
    Object.assign(envOf(data, 'ios').spec.aws!, { secretAccessKey: 'not-a-real-key-value' })
    const html = page('TestEnvironment', 'ios', data)
    const t = text(html)
    expect(t).toContain(
      'Provider An EC2 Mac per lease, on a dedicated host the operator keeps, in your AWS account (aws)',
    )
    expect(t).toContain(
      'Mac hosts Each host kept 48 h from its allocation for the leases that follow; the lease that allocates one counts its price for those hours at once',
    )
    expect(t).toContain(
      'Credentials valid Credential refused the provider refuses the credential in Secret ios-aws: the credential of test environment webshop/ios is of AWS account 210987654321, the environment names account 123456789012',
    )
    expect(t).toContain('Ready Credentials invalid its CredentialsValid condition is False: CredentialRefused')
    expect(t).toContain('CI None: an EC2 Mac runs no CI jobs')
    expect(html).not.toContain('not-a-real-key-value')
    // keepHostHours unset is EC2's minimum, 24.
    delete envOf(data, 'ios').spec.aws!.mac
    expect(text(page('TestEnvironment', 'ios', data))).toContain(
      'Mac hosts Each host kept 24 h from its allocation for the leases that follow; the lease that allocates one counts its price for those hours at once (keepHostHours not set: EC2’s minimum)',
    )
  })
})

describe('a Task’s test environments', () => {
  it('shows the leases its run holds and the environments its runs leased', () => {
    const html = page('Task', 'orders-api-24')
    const t = text(html)
    expect(t).toContain('Test environments')
    expect(t).toContain('Its run holds browser · lease browser-5f0c3a9e21 · run orders-api-24-r1-implement')
    expect(t).toContain('Its runs leased browser')
    expect(html).toContain('href="/?ns=webshop&amp;kind=TestEnvironment&amp;name=browser"')
  })

  it('says why its run waits for one, in the operator’s words', () => {
    const waiting: Task = {
      metadata: { name: 'game-7', namespace: 'webshop' },
      spec: { containerRef: { name: 'game' }, agentRef: { name: 'dev' }, issue: 7 },
      status: {
        phase: 'Implementing',
        conditions: [
          {
            type: 'Progressing',
            status: 'True',
            reason: 'WaitingForEnvironment',
            message: 'AllLeased: every lease of android is taken; it starts when one is returned',
          },
        ],
      },
    }
    const t = text(renderToStaticMarkup(createElement(TaskEnvironments, { task: waiting })))
    expect(t).toContain(
      'AllLeased Its run waits for a test environment, at no cost of a round: AllLeased: every lease of android is taken',
    )
  })

  it('shows nothing on a Task whose runs lease none', () => {
    expect(text(page('Task', 'orders-api-15'))).not.toContain('Test environments')
  })
})
