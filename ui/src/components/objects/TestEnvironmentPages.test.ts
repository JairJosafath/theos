import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import type { BriefWriter } from '../../k8s/briefs'
import type { ContainerWriter } from '../../k8s/containers'
import type { CrewWriter } from '../../k8s/crews'
import type { Connection, StoryData } from '../../k8s/sources'
import type { StandardWriter } from '../../k8s/standards'
import type { Task } from '../../k8s/types'
import { demoStory } from '../../demo/fixture'
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

function page(kind: LifecycleKind, name: string, data: StoryData = demoStory(NOW), connection = LIVE) {
  return renderToStaticMarkup(
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
  )
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
