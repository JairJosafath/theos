// End-to-end check of a Crew page's actions (design.md, UI actions) against a
// fake Kubernetes API serving the demo fixture: pause and resume, parallel,
// the release settings (where it publishes saved on the Brief that sets it,
// else on the Crew), the re-run of a failed release run, a write the API
// refuses showing its message, with OIDC a signed-out page saying to sign
// in instead of offering the buttons, and its schedules (#144): a proposal of
// its Standard taken, the first schedule of a Crew that has none, and Run now.
//
// Needs a build and Chromium:  npm run test:e2e
// THEOS_UI_SHOTS=DIR also saves pictures of the page in DIR (docs/screenshots).

import assert from 'node:assert/strict'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { oidcFromEnv } from '../oidc.mjs'
import { launchChromium } from '../scripts/browser.mjs'
import { createServer, resolveUpstream } from '../server.mjs'
import { fakeApi, listen } from './fakeApi.mjs'
import { loadFixture } from './fixture.mjs'
import { RESOURCES } from '../src/k8s/types.ts'

const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')
const NOW = Date.parse('2026-06-01T12:00:00Z')
const CREW = 'orders-api'

describe('a Crew page’s actions', () => {
  let api
  let ui
  let oidcUi
  let browser
  let page
  let base
  let oidcBase
  let namespace
  const errors = []

  before(async () => {
    const { demoStory, demoFactory } = await loadFixture()
    const story = demoStory(NOW)
    namespace = story.namespace
    api = fakeApi(namespace)
    // Story keys that differ from the plural.
    const PLURAL = { project: RESOURCES.projects, systems: RESOURCES.softwareSystems }
    for (const data of [story, demoFactory(NOW)]) {
      for (const [k, v] of Object.entries(data)) {
        const plural = PLURAL[k] ?? RESOURCES[k] ?? (Object.values(RESOURCES).includes(k) ? k : undefined)
        if (!plural || !v) continue
        for (const obj of Array.isArray(v) ? v : [v]) api.add(plural, structuredClone(obj))
      }
    }
    const upstream = resolveUpstream({ K8S_API: await listen(api.server) })
    ui = createServer({ distDir: dist, upstream, log: () => {}, actions: 'service-account' })
    base = await listen(ui)
    // Behind an Ingress: nobody signs in here, so the issuer is never asked.
    oidcUi = createServer({
      distDir: dist,
      upstream,
      log: () => {},
      actions: 'oidc',
      oidc: oidcFromEnv({
        OIDC_ISSUER_URL: 'https://issuer.invalid',
        OIDC_CLIENT_ID: 'theos-ui',
        OIDC_CLIENT_SECRET: 'not-a-secret',
      }),
    })
    oidcBase = await listen(oidcUi)
    browser = await launchChromium()
    page = await browser.newPage({ viewport: { width: 1280, height: 1000 }, reducedMotion: 'reduce' })
    await page.clock.setFixedTime(NOW)
    page.on('pageerror', (err) => errors.push(err.message))
  })

  after(async () => {
    await browser?.close()
    for (const server of [ui, oidcUi, api?.server]) {
      server?.closeAllConnections()
      await new Promise((r) => server.close(r))
    }
  })

  const crewPage = (at) => `${at}/?ns=${namespace}&kind=Crew&name=${CREW}`
  const crew = () => api.get('crews', CREW)
  const button = (name) => page.getByRole('button', { name, exact: true })
  const releaseForm = () => page.getByRole('form', { name: `Release settings of crew ${CREW}` })

  /** Waits until the fake API's Crew passes `test`. */
  async function until(test, what) {
    for (let i = 0; i < 100; i++) {
      if (test(crew())) return
      await new Promise((r) => setTimeout(r, 50))
    }
    assert.fail(`the Crew never ${what}`)
  }

  it('pauses and resumes the Crew', async () => {
    await page.goto(crewPage(base))
    await button('Pause crew').click()
    await until((c) => c.spec.paused === true, 'paused')
    await button('Resume crew').waitFor()
    await button('Resume crew').click()
    await until((c) => c.spec.paused === false, 'resumed')
    await button('Pause crew').waitFor()
  })

  it('sets how many issues it works on at once, and shows the API server’s refusal', async () => {
    const field = page.getByLabel('Issues at once')
    await field.fill('4')
    await button('Save').click()
    await until((c) => c.spec.parallel === 4, 'took parallel 4')
    await page.getByText(/of 4 busy/).waitFor()

    await field.fill('9')
    await button('Save').click()
    const alert = page.getByRole('alert')
    await alert.waitFor()
    assert.match(await alert.innerText(), /spec\.parallel in body should be less than or equal to 5/)
    assert.equal(crew().spec.parallel, 4)
    await field.fill('4')
  })

  // Each publishReleases write tests the resourceVersion of the Crew it was worked out from.
  const tested = (ops) => {
    assert.equal(ops[0].op, 'test')
    assert.equal(ops[0].path, '/metadata/resourceVersion')
    assert.match(ops[0].value, /^\d+$/)
    return ops.slice(1)
  }

  // The demo's Brief sets where orders-api publishes (#989), so the page saves the
  // publish fields on the Brief's spec.releases entry; the operator then writes it
  // onto the Crew with its mark, and with no entry the Crew's target is its own
  // (internal/controller/brief_release.go, crew_publish.go). The fake API runs no
  // operator: these do its part.
  const MARK = 'theos.factory.io/brief-publish-releases'
  const brief = () => api.get('briefs', 'webshop')
  const crewPath = () => `/apis/theos.factory.io/v1beta1/namespaces/${namespace}/crews/${CREW}`
  const briefPath = () => `/apis/theos.factory.io/v1beta1/namespaces/${namespace}/briefs/webshop`
  /** The operator writes the Brief's entry onto the Crew, and marks it. */
  function briefSets(publish) {
    const c = structuredClone(crew())
    c.spec.publishReleases = publish
    c.metadata.annotations = { ...c.metadata.annotations, [MARK]: JSON.stringify(publish) }
    api.modify('crews', c)
  }
  /** The person removes the Brief's entry; the operator drops the mark, and the condition goes. */
  function briefLetsGo() {
    const b = structuredClone(brief())
    b.spec.releases = b.spec.releases.filter((r) => r.container !== CREW)
    api.modify('briefs', b)
    const c = structuredClone(crew())
    delete c.metadata.annotations[MARK]
    c.status.conditions = c.status.conditions.filter((x) => x.type !== 'ReleaseTarget')
    api.modify('crews', c)
  }
  /** Resolves to the writes the fake API got after the first `count`, once there are `n`. */
  async function writesAfter(count, n) {
    for (let i = 0; i < 100 && api.writes.length < count + n; i++) await new Promise((r) => setTimeout(r, 50))
    assert.equal(api.writes.length, count + n, `the fake API got ${n} writes`)
    return api.writes.slice(count)
  }

  it('changes the release settings, naming the token Secret only, on the Brief that sets where it publishes', async () => {
    // The last release and its settings are on the page's Activity tab (docs/plans/done/ui-shell.md, step 2).
    await page.getByRole('tab', { name: 'Activity' }).click()
    const form = releaseForm()
    await form.getByText(`Brief webshop sets where releases go: saving changes its spec.releases entry`).waitFor()
    assert.equal(await form.getByLabel('Publish releases to GitHub').count(), 0, 'no switch: the Brief publishes it')
    const count = api.writes.length
    await form.getByLabel('Prepare releases').check()
    await form.getByLabel('Token Secret').fill('orders-api-release-token')
    await form.getByRole('button', { name: 'Save release settings' }).click()
    const [toCrew, toBrief] = await writesAfter(count, 2)
    const publish = {
      github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'orders-api-release-token' } },
    }
    // Prepare is the Crew's own; where it publishes is the Brief's entry.
    assert.equal(toCrew.path, crewPath())
    assert.deepEqual(toCrew.body, [{ op: 'add', path: '/spec/prepareReleases', value: true }])
    assert.equal(toBrief.path, briefPath())
    assert.deepEqual(tested(toBrief.body), [
      { op: 'add', path: '/spec/releases', value: [{ container: CREW, ...publish }] },
    ])
    assert.equal(crew().spec.prepareReleases, true)
    assert.equal(crew().spec.publishReleases.github.tokenSecretRef.name, 'orders-api-github', 'the operator’s to write')

    // Once the operator wrote it, the form follows the Crew again: nothing left to save.
    briefSets(publish)
    await form.getByRole('button', { name: 'Save release settings' }).and(page.locator(':disabled')).waitFor()
    assert.equal(await form.getByLabel('Token Secret').inputValue(), 'orders-api-release-token')
  })

  it('changes a release target of the Crew’s own on the Crew, testing its resourceVersion first', async () => {
    briefLetsGo()
    const form = releaseForm()
    const publishing = form.getByLabel('Publish releases to GitHub')
    await publishing.waitFor()
    assert.equal(await publishing.isChecked(), true, 'it keeps the target the Brief set')
    const count = api.writes.length
    // A switch and the target in one save: the test still leads the patch (theos#31).
    await form.getByLabel('Prepare releases').uncheck()
    await form.getByLabel('Token Secret').fill('orders-api-own-token')
    await form.getByRole('button', { name: 'Save release settings' }).click()
    const [write] = await writesAfter(count, 1)
    assert.equal(write.path, crewPath())
    assert.deepEqual(tested(write.body), [
      { op: 'add', path: '/spec/prepareReleases', value: false },
      {
        op: 'add',
        path: '/spec/publishReleases',
        value: { github: { repository: 'webshop-co/orders-api', tokenSecretRef: { name: 'orders-api-own-token' } } },
      },
    ])
    await until(
      (c) =>
        c.spec.prepareReleases === false &&
        c.spec.publishReleases.github.tokenSecretRef.name === 'orders-api-own-token',
      'took its own target',
    )
    await page.getByText('It publishes them to webshop-co/orders-api.').waitFor()
  })

  it('shows the API server’s message when the release settings break a rule', async () => {
    const form = releaseForm()
    await form.getByLabel('Tag releases').uncheck()
    await form.getByRole('button', { name: 'Save release settings' }).click()
    const alert = form.getByRole('alert')
    await alert.waitFor()
    assert.match(await alert.innerText(), /publishReleases needs tagReleases/)
    assert.equal(crew().spec.tagReleases, true)

    // Off with publishing too, which removes it.
    await form.getByLabel('Publish releases to GitHub').uncheck()
    await form.getByRole('button', { name: 'Save release settings' }).click()
    await until((c) => c.spec.tagReleases === false && !c.spec.publishReleases, 'stopped tagging and publishing')
    assert.deepEqual(tested(api.writes.at(-1).body), [
      { op: 'add', path: '/spec/tagReleases', value: false },
      { op: 'remove', path: '/spec/publishReleases' },
    ])
    await page.getByText('The Crew tags no releases: a person tags them.').waitFor()
  })

  it('offers a re-run only once the release run failed, and asks the operator for it', async () => {
    assert.equal(await page.getByRole('button', { name: /^Re-run/ }).count(), 0)
    const failed = structuredClone(crew())
    failed.status.lastRelease.run = 'Failed'
    api.modify('crews', failed)
    const rerun = button('Re-run v0.2.0’s failed runs')
    await rerun.waitFor()
    if (process.env.THEOS_UI_SHOTS) {
      // From the top, so the sticky top bar is where it belongs.
      await page.mouse.wheel(0, -100000)
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-crew-actions.png'), fullPage: true })
    }
    await rerun.click()
    await page.getByText('Asked the operator to re-run them').waitFor()
    assert.equal(await rerun.isDisabled(), true, 'asked once, until the release’s re-run time changes')
    const asked = crew().metadata.annotations?.['theos.factory.io/rerun-release']
    assert.ok(asked && !Number.isNaN(Date.parse(asked)), `the annotation is a time: ${asked}`)
    assert.equal(api.writes.at(-1).contentType, 'application/merge-patch+json')
  })

  it('says to sign in, instead of offering the buttons, when nobody is signed in with OIDC', async () => {
    await page.goto(crewPage(oidcBase))
    await page.getByText('Sign in to pause this Crew or change its settings.').waitFor()
    const link = page.locator('.crew-sign-in').getByRole('link', { name: 'Sign in' })
    assert.match(await link.getAttribute('href'), /^\/ui\/login\?return=/)
    for (const name of ['Pause crew', 'Resume crew', 'Save', 'Save release settings']) {
      assert.equal(await button(name).count(), 0, `no ${name} button`)
    }
    assert.equal(await page.getByRole('button', { name: /^Re-run/ }).count(), 0)
    if (process.env.THEOS_UI_SHOTS) {
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-crew-sign-in.png') })
    }
    assert.deepEqual(errors, [])
  })

  // #144: the two writes of #72, from the button through server.mjs to an API
  // server. The demo's orders-api Crew has two schedules, one of them the
  // dependencies its Standard proposes; the Standard's flaky-tests is offered.
  const RUN_SCHEDULE = 'theos.factory.io/run-schedule'
  const JSON_PATCH = 'application/json-patch+json'
  const proposalsPanel = () => page.getByRole('region', { name: 'Schedules its Standard proposes' })
  const schedulesPanel = () => page.getByRole('region', { name: 'Work on a timer' })
  /** What taking the Standard's proposal `name` in `timeZone` adds: its name, cron and goal as the fake API has them. */
  const taken = (name, timeZone) => {
    const proposal = api.get('standards', 'python-standard').spec.schedules.find((s) => s.name === name)
    return { name: proposal.name, cron: proposal.cron, timeZone, goal: proposal.goal }
  }
  const takeButton = (name) => proposalsPanel().getByRole('button', { name: `Take ${name}`, exact: true })

  it('takes a proposal of its Standard in the time zone typed: one append, the row gone and the panel saying so', async () => {
    await page.goto(crewPage(base))
    const panel = proposalsPanel()
    const zone = panel.getByLabel('Time zone for flaky-tests')
    await zone.waitFor()
    assert.equal(await zone.inputValue(), '', 'no zone is filled in for the person')
    assert.equal(await takeButton('flaky-tests').isDisabled(), true, 'nothing to take until a zone is typed')
    const had = crew().spec.schedules.map((s) => s.name)
    const count = api.writes.length
    await zone.fill('Europe/Amsterdam')
    await takeButton('flaky-tests').click()

    // The one write the fake API received: the append, as the page sent it.
    const [write] = await writesAfter(count, 1)
    assert.equal(write.method, 'PATCH')
    assert.equal(write.path, crewPath())
    assert.equal(write.contentType, JSON_PATCH)
    assert.deepEqual(write.body, [
      { op: 'add', path: '/spec/schedules/-', value: taken('flaky-tests', 'Europe/Amsterdam') },
    ])
    await until((c) => c.spec.schedules.length === had.length + 1, 'took the schedule')
    assert.deepEqual(
      crew().spec.schedules.map((s) => s.name),
      [...had, 'flaky-tests'],
      'the schedules it had are as they were',
    )

    // The watch brings the Crew's new schedule: the row leaves with its form, and the panel says what happened.
    await zone.waitFor({ state: 'detached' })
    await panel.getByText('Added flaky-tests to the Crew’s schedules.').waitFor()
    assert.equal(await panel.getByRole('heading', { name: 'Not in use by this Crew' }).count(), 0, 'none left to offer')
    await panel
      .getByRole('listitem')
      .filter({ hasText: 'flaky-tests' })
      .getByText('Europe/Amsterdam', { exact: true })
      .waitFor()
    await schedulesPanel().getByText('(Europe/Amsterdam)').waitFor()
  })

  it('takes the first schedule of a Crew that has none, again after the Crew changed under the first try', async () => {
    // A Crew nobody gave a schedule: both proposals are offered, and there is no timer panel.
    const bare = structuredClone(crew())
    delete bare.spec.schedules
    delete bare.status.schedules
    bare.status.conditions = bare.status.conditions.filter((c) => c.type !== 'Scheduled')
    api.modify('crews', bare)
    await page.goto(crewPage(base))
    const panel = proposalsPanel()
    const zone = panel.getByLabel('Time zone for dependencies')
    await zone.waitFor()
    await panel.getByLabel('Time zone for flaky-tests').waitFor()
    assert.equal(await schedulesPanel().count(), 0, 'no schedules panel yet')

    // The operator's status write lands between the UI server's read of the Crew and its patch.
    api.changeBeforeNextWrite('crews', CREW)
    const count = api.writes.length
    await zone.fill('UTC')
    await takeButton('dependencies').click()
    const alert = panel.getByRole('alert')
    await alert.waitFor()
    assert.match(
      await alert.innerText(),
      /changed while its first schedule was being added, so nothing was saved: adding it again the same way works/,
    )
    // A JSON patch can't append to a list that isn't there: the UI server adds the list, guarded by what it read.
    const first = [{ op: 'add', path: '/spec/schedules', value: [taken('dependencies', 'UTC')] }]
    const [lost] = await writesAfter(count, 1)
    assert.deepEqual(tested(lost.body), first)
    assert.equal(crew().spec.schedules, undefined, 'nothing was saved')

    // Again, the same way.
    await takeButton('dependencies').click()
    const [, write] = await writesAfter(count, 2)
    assert.equal(write.path, crewPath())
    assert.equal(write.contentType, JSON_PATCH)
    assert.deepEqual(tested(write.body), first)
    assert.notEqual(write.body[0].value, lost.body[0].value, 'it tests the resourceVersion it read this time')
    await until((c) => c.spec.schedules?.length === 1, 'took its first schedule')
    assert.deepEqual(crew().spec.schedules, [taken('dependencies', 'UTC')])

    await zone.waitFor({ state: 'detached' })
    await panel.getByText('Added dependencies to the Crew’s schedules.').waitFor()
    await panel.getByText(/has the proposal’s cron and goal, and is in UTC/).waitFor()
    // The schedules panel is there now, and the other proposal is still offered.
    await schedulesPanel().getByText('(UTC)').waitFor()
    await panel.getByLabel('Time zone for flaky-tests').waitFor()
  })

  it('asks the operator to run a schedule now, and offers it again once its issue is filed', async () => {
    const scheduled = structuredClone(crew())
    scheduled.spec.schedules = [taken('dependencies', 'UTC')]
    delete scheduled.metadata.annotations?.[RUN_SCHEDULE]
    api.modify('crews', scheduled)
    await page.goto(crewPage(base))
    const row = schedulesPanel()
      .getByRole('listitem')
      .filter({ has: page.getByText('dependencies', { exact: true }) })
    const run = row.getByRole('button', { name: 'Run now', exact: true })
    await run.waitFor()
    const count = api.writes.length
    await run.click()

    const [write] = await writesAfter(count, 1)
    assert.equal(write.method, 'PATCH')
    assert.equal(write.path, crewPath())
    assert.equal(write.contentType, 'application/merge-patch+json')
    assert.deepEqual(write.body, { metadata: { annotations: { [RUN_SCHEDULE]: 'dependencies' } } })
    assert.equal(crew().metadata.annotations[RUN_SCHEDULE], 'dependencies')
    // Asked once: the row waits for the operator.
    const asked = row.getByRole('button', { name: 'Asked to run now', exact: true })
    await asked.waitFor()
    assert.equal(await asked.isDisabled(), true)

    // The operator files the schedule's issue and takes the annotation off. The fake API runs none: this does its part.
    const filed = structuredClone(crew())
    delete filed.metadata.annotations[RUN_SCHEDULE]
    const at = new Date(NOW).toISOString()
    filed.status.schedules = [{ name: 'dependencies', since: at, lastFiled: at, lastIssue: 19 }]
    api.modify('crews', filed)
    await row.getByText(/last filed issue #19/).waitFor()
    await run.waitFor()
    assert.equal(await run.isEnabled(), true)
    assert.deepEqual(errors, [])
  })
})
