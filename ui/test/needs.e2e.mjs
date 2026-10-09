// End-to-end check of the shell's Needs you area against a fake Kubernetes
// API: one item of each kind across two projects, each saying why and where it
// is done, and the shell opening on Needs you only while something waits.
//
// Needs a build and Chromium:  npm run test:e2e
// C4_UI_SHOTS=DIR also saves a picture of the list in DIR (docs/screenshots).

import assert from 'node:assert/strict'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { launchChromium } from '../scripts/browser.mjs'
import { createServer, resolveUpstream } from '../server.mjs'
import { fakeApi, listen } from './fakeApi.mjs'
import { RESOURCES } from '../src/k8s/types.ts'

const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')
const NOW = Date.parse('2026-06-01T12:00:00Z')
const at = (minutesAgo) => new Date(NOW - minutesAgo * 60_000).toISOString()
const cond = (type, status, reason, message, minutesAgo = 30) => ({
  type,
  status,
  reason,
  message,
  lastTransitionTime: at(minutesAgo),
})
const GIT = 'https://git.example/factory'

const project = (ns, displayName) => ({ metadata: { name: ns, namespace: ns }, spec: { displayName } })
const container = (ns, name, status) => ({
  metadata: { name, namespace: ns },
  spec: {
    softwareSystemRef: { name: 'store' },
    type: 'service',
    gitProviderRef: { name: 'gitea' },
    frameworkRef: { name: 'go' },
  },
  status: { repositoryURL: `${GIT}/${name}`, ...status },
})

/** One object per kind of item, in two projects, and a healthy one of each that gives none. */
function seed(api) {
  const add = (plural, obj) => api.add(plural, obj)
  add(RESOURCES.projects, project('shop', 'The shop'))
  add(RESOURCES.projects, project('zoo', 'The zoo'))

  add(RESOURCES.briefs, {
    metadata: { name: 'checkout', namespace: 'shop' },
    spec: { idea: 'A checkout' },
    status: { conditions: [cond('NeedsInput', 'True', 'PlannerNeedsInput', 'Which payment provider should it use?')] },
  })
  add(RESOURCES.briefs, {
    metadata: { name: 'loyalty', namespace: 'zoo' },
    spec: { idea: 'Loyalty points' },
    status: { conditions: [cond('NeedsYou', 'True', 'Question', 'Retry tickets#4? It failed twice.', 50)] },
  })
  add(RESOURCES.briefs, {
    metadata: { name: 'healthy', namespace: 'shop' },
    spec: { idea: 'Fine' },
    status: { conditions: [cond('Planned', 'True', 'Applied', 'Round 1 applied')] },
  })

  add(
    RESOURCES.containers,
    container('shop', 'storefront', {
      phase: 'Failed',
      conditions: [
        cond('Scaffolded', 'False', 'ForeignCommits', 'The repository has commits the factory did not make'),
      ],
    }),
  )
  add(
    RESOURCES.containers,
    container('shop', 'orders', {
      phase: 'Ready',
      standard: {
        name: 'go-standard',
        ref: 'v1.0.0',
        update: { ref: 'v1.1.0', pullRequest: 3, pullRequestURL: `${GIT}/orders/pulls/3` },
      },
      conditions: [
        cond('Scaffolded', 'True', 'Created', ''),
        cond('StandardUpdated', 'False', 'ChecksFailed', 'CI failed on the Standard update to v1.1.0'),
      ],
    }),
  )
  add(RESOURCES.containers, container('zoo', 'tickets', { phase: 'Ready', conditions: [] }))

  const task = (ns, name, issue, status) => ({
    metadata: { name, namespace: ns, creationTimestamp: at(120) },
    spec: { containerRef: { name: 'orders' }, agentRef: { name: 'claude' }, issue },
    status,
  })
  add(
    RESOURCES.tasks,
    task('shop', 'orders-12', 12, {
      phase: 'Blocked',
      standardChange: { issue: 20, issueURL: `${GIT}/orders/issues/20`, paths: ['.golangci.yml'], count: 1 },
      conditions: [cond('Progressing', 'False', 'WaitingForStandardChange', 'Waits for #20 to change .golangci.yml')],
    }),
  )
  add(
    RESOURCES.tasks,
    task('zoo', 'tickets-4', 4, {
      phase: 'Failed',
      completedAt: at(40),
      issueURL: `${GIT}/tickets/issues/4`,
      pullRequest: 6,
      pullRequestURL: `${GIT}/tickets/pulls/6`,
      conditions: [cond('Succeeded', 'False', 'ReviewRejected', 'The reviewer rejected it 3 times', 40)],
    }),
  )
  add(RESOURCES.tasks, task('shop', 'orders-13', 13, { phase: 'Merged' }))

  const crew = (ns, name, status) => ({
    metadata: { name, namespace: ns },
    spec: { containerRef: { name }, implementer: { name: 'claude' }, reviewer: { name: 'claude' } },
    status,
  })
  add(
    RESOURCES.crews,
    crew('zoo', 'tickets', {
      phase: 'Idle',
      conditions: [
        cond('Ready', 'True', 'Ready', ''),
        cond(
          'Released',
          'False',
          'NoMergedIssue',
          'v0.2.0 is done but none of its issues was merged by a Task; a person tags it',
          20,
        ),
      ],
    }),
  )
  add(
    RESOURCES.crews,
    crew('shop', 'orders', {
      phase: 'Idle',
      lastRelease: {
        tag: 'v1.4.0',
        commit: '0123456789abcdef',
        time: at(90),
        run: 'Failed',
        runURL: `${GIT}/orders/actions/runs/8`,
      },
      conditions: [cond('Ready', 'True', 'Ready', ''), cond('Released', 'True', 'Released', 'Released v1.4.0')],
    }),
  )
}

/** The UI in front of a fake API, and the API, on free ports. */
async function serve(fill) {
  const api = fakeApi('shop')
  fill(api)
  const ui = createServer({
    distDir: dist,
    upstream: resolveUpstream({ K8S_API: await listen(api.server) }),
    log: () => {},
  })
  const base = await listen(ui)
  const close = async () => {
    ui.closeAllConnections()
    api.server.closeAllConnections()
    await new Promise((r) => ui.close(r))
    await new Promise((r) => api.server.close(r))
  }
  return { base, close }
}

describe('Needs you', () => {
  let flagged
  let quiet
  let browser
  let page
  const errors = []

  before(async () => {
    flagged = await serve(seed)
    quiet = await serve((api) => api.add(RESOURCES.projects, project('shop', 'The shop')))
    browser = await launchChromium()
    const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, reducedMotion: 'reduce' })
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    page = await context.newPage()
    await page.clock.setFixedTime(NOW)
    page.on('pageerror', (err) => errors.push(err.message))
  })

  after(async () => {
    await browser?.close()
    await flagged?.close()
    await quiet?.close()
  })

  const item = (kind, name) => page.getByRole('listitem', { name: `${kind} ${name}` })

  it('opens on Needs you and lists one item of each kind, by project', async () => {
    await page.goto(`${flagged.base}/`)
    await page.locator('.need').first().waitFor({ timeout: 10000 })
    assert.equal(new URL(page.url()).searchParams.get('area'), 'needs')
    const projects = await page.locator('.needs-project-title').allTextContents()
    assert.deepEqual(projects, ['The shopshop', 'The zoozoo'])

    const want = [
      ['Brief', 'checkout', 'PlannerNeedsInput', 'Which payment provider should it use?'],
      ['Brief', 'loyalty', 'Question', 'Retry tickets#4? It failed twice.'],
      ['Container', 'storefront', 'ForeignCommits', 'The repository has commits the factory did not make'],
      ['Container', 'orders', 'ChecksFailed', 'CI failed on the Standard update to v1.1.0'],
      ['Task', 'orders-12', 'WaitingForStandardChange', 'Waits for #20 to change .golangci.yml'],
      ['Task', 'tickets-4', 'ReviewRejected', 'The reviewer rejected it 3 times'],
      [
        'Crew',
        'tickets',
        'NoMergedIssue',
        'v0.2.0 is done but none of its issues was merged by a Task; a person tags it',
      ],
      ['Crew', 'orders', 'ReleaseRunFailed', 'status.lastRelease.run is Failed for v1.4.0'],
    ]
    assert.equal(await page.locator('.need').count(), want.length)
    for (const [kind, name, reason, message] of want) {
      const li = item(kind, name)
      assert.equal(await li.locator('.need-reason').textContent(), reason, `${kind} ${name}'s reason`)
      assert.match(await li.locator('.need-why').textContent(), new RegExp(message.replace(/[?.#]/g, '\\$&')))
      assert.ok((await li.getByRole('link').count()) > 0, `${kind} ${name} links somewhere`)
    }
    for (const name of ['healthy', 'tickets', 'orders-13']) {
      assert.equal(
        await page.getByRole('listitem', { name: new RegExp(` ${name}$`) }).count(),
        name === 'tickets' ? 1 : 0,
      )
    }
    if (process.env.C4_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.C4_UI_SHOTS, 'ui-needs-you.png'), fullPage: true })
    }
  })

  it('links out to where an item is done', async () => {
    const href = (kind, name, label) => item(kind, name).getByRole('link', { name: label }).getAttribute('href')
    assert.equal(await href('Container', 'orders', /update pull request/), `${GIT}/orders/pulls/3`)
    assert.equal(await href('Task', 'orders-12', /Standard change/), `${GIT}/orders/issues/20`)
    assert.equal(await href('Task', 'tickets-4', /pull request/), `${GIT}/tickets/pulls/6`)
    assert.equal(await href('Crew', 'tickets', /milestones/), `${GIT}/tickets/milestones`)
    assert.equal(await href('Crew', 'orders', /failed run/), `${GIT}/orders/actions/runs/8`)
  })

  it('shows a copyable kubectl line where no page can do it', async () => {
    const li = item('Container', 'storefront')
    const line = 'kubectl -n shop annotate container storefront c4.factory.io/retry=$(date +%s) --overwrite'
    assert.equal(await li.locator('.need-command code').textContent(), line)
    await li.getByRole('button', { name: 'Copy' }).click()
    await li.getByRole('button', { name: 'Copied' }).waitFor({ timeout: 5000 })
    assert.equal(await page.evaluate(() => globalThis.navigator.clipboard.readText()), line)
    assert.equal(
      await item('Crew', 'orders').locator('.need-command code').textContent(),
      'kubectl -n shop annotate crew orders c4.factory.io/rerun-release=$(date +%s) --overwrite',
    )
  })

  it('opens the place in the UI where an item is seen', async () => {
    await item('Task', 'tickets-4').getByRole('link', { name: 'Open the Task' }).click()
    const task = page.getByRole('region', { name: 'Task tickets-4', exact: true })
    await task.waitFor({ timeout: 10000 })
    const params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('ns'), params.get('kind'), params.get('name')], ['zoo', 'Task', 'tickets-4'])
    assert.equal(await task.locator('[aria-current="step"]').textContent(), 'Failed')
    await page.goBack()
    await item('Crew', 'orders').getByRole('link', { name: 'Open the Crew' }).click()
    const crew = page.getByRole('region', { name: 'Crew orders', exact: true })
    await crew.waitFor({ timeout: 10000 })
    // The page gives the same next step: re-run the failed release run.
    assert.match(
      await crew.locator('.command-line code').textContent(),
      /^kubectl -n shop annotate crews\.c4\.factory\.io orders c4\.factory\.io\/rerun-release=/,
    )
    await page.goBack()
    await item('Brief', 'checkout').getByRole('link', { name: 'Open the Brief' }).click()
    await page.getByRole('heading', { name: 'checkout' }).first().waitFor({ timeout: 10000 })
    const brief = new URL(page.url()).searchParams
    assert.deepEqual([brief.get('ns'), brief.get('kind'), brief.get('name')], ['shop', 'Brief', 'checkout'])
  })

  it('opens on Story when nothing waits on a person', async () => {
    await page.goto(`${quiet.base}/`)
    await page.getByRole('heading', { name: 'System context: the big picture' }).waitFor({ timeout: 10000 })
    assert.equal(new URL(page.url()).searchParams.get('area'), 'story')
    await page.goto(`${quiet.base}/?area=needs`)
    await page.getByText('Nothing waits on you.').waitFor({ timeout: 10000 })
  })

  it('raises no page error', () => {
    assert.deepEqual(errors, [])
  })
})
