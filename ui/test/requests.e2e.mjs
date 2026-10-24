// End-to-end check of a Brief's requests against a fake Kubernetes API, through
// the UI server's action table: the page lists status.requests and the
// Requests condition, Accept and Decline on a request waiting in Ask send the
// releases list with the number added behind a test of the resourceVersion,
// the settings set an entry's requests, and a refused write shows the API
// server's message. The page follows the watch: the operator's next status
// shows without a reload.
//
// Needs a build and Chromium:  npm run test:e2e
// C4_UI_SHOTS=DIR also saves a picture of the page in DIR (docs/screenshots).

import { AxeBuilder } from '@axe-core/playwright'
import assert from 'node:assert/strict'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { launchChromium } from '../scripts/browser.mjs'
import { createServer, resolveUpstream } from '../server.mjs'
import { fakeApi, listen } from './fakeApi.mjs'

const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')
const NS = 'lens'
const REPO = 'gray-lenovo/cel-lens'

const lens = {
  container: 'cel-lens',
  github: { repository: REPO, tokenSecretRef: { name: 'cel-lens-github' } },
  requests: { mode: 'Ask', authors: ['gray-lenovo'] },
}

const request = (number, title, stage, more = {}) => ({
  repository: REPO,
  number,
  author: 'gray-lenovo',
  title,
  url: `https://github.com/${REPO}/issues/${number}`,
  stage,
  since: '2026-10-09T10:00:00Z',
  ...more,
})
const waiting = (number, title) => ({
  kind: 'Request',
  name: `${REPO}#${number}`,
  question: `Request ${REPO}#${number} "${title}" by gray-lenovo waits for you.`,
  recommend: `Add ${number} to the accepted requests of ${REPO} to plan it, or to the declined ones.`,
  since: '2026-10-09T10:00:00Z',
})

const brief = {
  apiVersion: 'c4.factory.io/v1beta1',
  kind: 'Brief',
  metadata: { name: 'lens' },
  spec: { idea: 'A lens on CEL expressions.', releases: [lens] },
  status: {
    phase: 'Planned',
    round: 2,
    created: [{ kind: 'Container', name: 'cel-lens' }],
    requests: [
      request(3, 'Export as PNG', 'Planned', { round: 2, issues: ['cel-lens#7'], release: 'v0.3.0' }),
      request(12, 'Dark mode', 'Waiting'),
      request(13, 'Rewrite it in Rust', 'Waiting'),
    ],
    needsYou: [waiting(12, 'Dark mode'), waiting(13, 'Rewrite it in Rust')],
    needsYouWaiting: 2,
    conditions: [
      {
        type: 'Requests',
        status: 'True',
        reason: 'Reading',
        message: `Reading the issues of ${REPO}`,
        lastTransitionTime: '2026-10-09T10:00:00Z',
      },
    ],
  },
}

describe('a Brief’s requests in the browser', () => {
  let api
  let ui
  let browser
  let page
  let base
  let apiUrl
  const errors = []

  before(async () => {
    api = fakeApi(NS)
    apiUrl = await listen(api.server)
    api.add('projects', { metadata: { name: NS }, spec: { displayName: 'Lens' } })
    api.add('briefs', structuredClone(brief))
    ui = createServer({
      distDir: dist,
      upstream: resolveUpstream({ K8S_API: apiUrl }),
      actions: 'service-account',
      log: () => {},
    })
    base = await listen(ui)
    browser = await launchChromium()
    // A context of its own, as axe needs.
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: 'reduce' })
    page = await context.newPage()
    page.on('pageerror', (err) => errors.push(err.message))
    await page.goto(`${base}/?ns=${NS}&kind=Brief&name=lens`)
    await page.getByRole('region', { name: 'Brief lens', exact: true }).waitFor({ timeout: 10000 })
  })

  after(async () => {
    await browser?.close()
    for (const s of [ui, api?.server]) s?.closeAllConnections()
    await Promise.all([ui, api.server].map((s) => new Promise((r) => s.close(r))))
  })

  const list = () => page.getByRole('region', { name: 'Requests from GitHub' })
  const settings = () => page.getByRole('region', { name: 'Settings of brief lens' })
  const button = (name) => page.getByRole('button', { name, exact: true })

  /** The Brief as the fake API has it now. */
  const current = async () => (await fetch(`${apiUrl}/apis/c4.factory.io/v1beta1/namespaces/${NS}/briefs/lens`)).json()

  /** Clicks, and resolves to the JSON patch the API got for it, without its test of the resourceVersion. */
  async function patchOf(click) {
    const count = api.writes.length
    await click()
    for (let i = 0; i < 100 && api.writes.length <= count; i++) await new Promise((r) => setTimeout(r, 50))
    const write = api.writes[count]
    assert.ok(write, 'the fake API got no write')
    assert.equal(write.method, 'PATCH')
    assert.equal(write.path, `/apis/c4.factory.io/v1beta1/namespaces/${NS}/briefs/lens`)
    const [test, ...ops] = write.body
    assert.deepEqual({ ...test, value: undefined }, { op: 'test', path: '/metadata/resourceVersion', value: undefined })
    assert.match(test.value, /^\d+$/)
    return ops
  }

  it('lists the requests, each with its stage', async () => {
    await list().getByText('3 open').waitFor({ timeout: 5000 })
    await list().getByText('Round 2 planned it as cel-lens#7, in release v0.3.0.').waitFor()
    assert.equal(await list().getByText('Waits for you', { exact: true }).count(), 2)
    assert.equal(
      await list()
        .getByRole('link', { name: `${REPO} #12` })
        .getAttribute('href'),
      `https://github.com/${REPO}/issues/12`,
    )
    if (process.env.C4_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.C4_UI_SHOTS, 'ui-brief-requests.png'), fullPage: true })
    }
  })

  it('has no WCAG 2.1 AA violation, with the requests settings open', async () => {
    await settings().getByText('Requests of cel-lens').click()
    await settings().getByRole('form', { name: 'Requests of cel-lens' }).waitFor()
    const { violations } = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
      .analyze()
    assert.deepEqual(
      violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`),
      [],
    )
    await settings().getByText('Requests of cel-lens').click()
  })

  it('accepts a request by adding its number to accepted', async () => {
    const ops = await patchOf(() => button(`Accept ${REPO} #12`).click())
    assert.deepEqual(ops, [
      { op: 'add', path: '/spec/releases', value: [{ ...lens, requests: { ...lens.requests, accepted: [12] } }] },
    ])
    await page.getByText('Accepted; it waits for a round').waitFor({ timeout: 5000 })
    assert.equal(await button(`Accept ${REPO} #12`).count(), 0)
  })

  it('declines one by adding its number to declined', async () => {
    const ops = await patchOf(() => button(`Decline ${REPO} #13`).click())
    assert.deepEqual(ops, [
      {
        op: 'add',
        path: '/spec/releases',
        value: [{ ...lens, requests: { ...lens.requests, accepted: [12], declined: [13] } }],
      },
    ])
    await page.getByText('Declined; the Brief closes the issue').waitFor({ timeout: 5000 })
  })

  it('shows the operator’s next status as it comes', async () => {
    const now = await current()
    api.modify('briefs', {
      ...now,
      status: {
        ...now.status,
        requests: [now.status.requests[0], request(12, 'Dark mode', 'Accepted')],
        needsYou: [],
        needsYouWaiting: 0,
        conditions: [
          {
            type: 'Requests',
            status: 'False',
            reason: 'TokenLacksIssues',
            message: 'Resource not accessible by personal access token',
            lastTransitionTime: '2026-10-09T11:00:00Z',
          },
        ],
      },
    })
    await list().getByText('2 open').waitFor({ timeout: 5000 })
    await list().getByText('Accepted; it waits for a round to plan it.').waitFor()
    await list().getByText('It can’t read or answer requests (TokenLacksIssues).').waitFor()
    assert.equal(await page.locator('.brief-needs-you').count(), 0)
  })

  it('sets an entry’s requests in the settings, keeping the numbers answered', async () => {
    await settings().getByText('requests Ask, from gray-lenovo').waitFor({ timeout: 5000 })
    await settings().getByText('Requests of cel-lens').click()
    const form = settings().getByRole('form', { name: 'Requests of cel-lens' })
    await form.getByLabel(/^Requests from its GitHub issues/).selectOption('On')
    await form.getByLabel(/^Teams/).fill('gray-org/maintainers')
    const ops = await patchOf(() => form.getByRole('button', { name: 'Set requests' }).click())
    assert.deepEqual(ops, [
      {
        op: 'add',
        path: '/spec/releases',
        value: [
          {
            ...lens,
            requests: {
              mode: 'On',
              authors: ['gray-lenovo'],
              teams: ['gray-org/maintainers'],
              accepted: [12],
              declined: [13],
            },
          },
        ],
      },
    ])
    await settings().getByText('requests On, from gray-lenovo and gray-org/maintainers').waitFor({ timeout: 5000 })
  })

  it('shows the API server’s message when it refuses requests', async () => {
    // A saved change starts the form again from the entry, folded.
    await settings().getByText('Requests of cel-lens').click()
    const form = settings().getByRole('form', { name: 'Requests of cel-lens' })
    await form.getByLabel(/^Requests from its GitHub issues/).selectOption('Ask')
    await form.getByLabel(/^Authors/).fill('')
    await form.getByLabel(/^Teams/).fill('')
    const writes = api.writes.length
    await patchOf(() => form.getByRole('button', { name: 'Set requests' }).click())
    assert.equal(api.writes.length, writes + 1)
    const alert = settings().getByRole('alert')
    await alert.waitFor({ timeout: 5000 })
    assert.equal(
      (await alert.textContent()).trim(),
      'Brief.c4.factory.io "lens" is invalid: spec.releases[0].requests: Invalid value: "object": requests need authors or teams unless mode is Off',
    )
    // Nothing changed.
    assert.equal((await current()).spec.releases[0].requests.mode, 'On')
  })

  it('raises no page error', () => {
    assert.deepEqual(errors, [])
  })
})
