// End-to-end check of the shell against a fake Kubernetes API serving the
// demo fixture: each area opens by its sidebar link and Back returns through
// them, every area is reachable by keyboard, an object opens in the drawer over
// the area, and the links from before the areas open the area they showed.
//
// Needs a build and Chromium:  npm run test:e2e
// THEOS_UI_SHOTS=DIR also saves a picture of each area in DIR (docs/screenshots).

import assert from 'node:assert/strict'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { launchChromium } from '../scripts/browser.mjs'
import { createServer, resolveUpstream } from '../server.mjs'
import { listen } from './fakeApi.mjs'
import { fixtureApi } from './fixture.mjs'

const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')
const NOW = Date.parse('2026-06-01T12:00:00Z')

/** Each area's link text, its `area=`, and a heading only that area shows. */
const AREAS = [
  ['Needs you', 'needs', 'Needs you'],
  ['Briefs', 'briefs', 'Briefs'],
  ['Story', 'story', 'System context: the big picture'],
  ['Work', 'work', 'Tasks'],
  ['Factory', 'factory', 'Agent slots'],
  ['About', 'about', 'About this factory'],
]

describe('the shell', () => {
  let api
  let ui
  let browser
  let page
  let base
  let namespace
  let session
  const errors = []
  const requests = []

  before(async () => {
    const fixture = await fixtureApi(NOW)
    api = fixture.api
    const story = fixture.story
    namespace = story.namespace
    session = story.sessions[0].metadata.name
    ui = createServer({
      distDir: dist,
      upstream: resolveUpstream({ K8S_API: await listen(api.server) }),
      log: () => {},
    })
    base = await listen(ui)
    browser = await launchChromium()
    page = await browser.newPage({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' })
    // The fixture's clock, so its times read as they do in demo mode.
    await page.clock.setFixedTime(NOW)
    page.on('pageerror', (err) => errors.push(err.message))
    page.on('request', (req) => requests.push(req.url()))
  })

  after(async () => {
    await browser?.close()
    ui?.closeAllConnections()
    api?.server.closeAllConnections()
    await new Promise((r) => ui.close(r))
    await new Promise((r) => api.server.close(r))
  })

  const nav = () => page.getByRole('navigation', { name: 'Areas' })
  const current = () => nav().locator('[aria-current="page"]')
  // The name of the area marked current, without the Needs you count.
  const currentLabel = () => current().locator('.nav-label').textContent()
  // An area's sidebar link; Needs you's name carries its count ("Needs you, 4 waiting").
  const areaLink = (label) => nav().getByRole('link', { name: new RegExp(`^${label}( ?, \\d+ waiting)?$`) })
  const areaOf = () => new URL(page.url()).searchParams.get('area')
  const heading = (text) => page.getByRole('heading', { name: text, exact: true }).first()

  it('opens on Needs you while something waits on a person, with the freshness line', async () => {
    await page.goto(`${base}/`)
    await page.locator('.need').first().waitFor({ timeout: 10000 })
    assert.equal(await currentLabel(), 'Needs you')
    assert.match(await current().textContent(), /^Needs you, \d+ waiting$/)
    assert.equal(areaOf(), 'needs')
    const fresh = page.locator('.freshness')
    await fresh.getByText('Live').waitFor({ timeout: 5000 })
    await fresh.getByText(/^Updated /).waitFor({ timeout: 5000 })
    await fresh.getByText('All namespaces').waitFor({ timeout: 5000 })
    await page.goto(`${base}/?area=story`)
    await page.locator('.freshness').getByText(`Namespace ${namespace}`).waitFor({ timeout: 5000 })
  })

  it('opens each area by its link, and goes back with Back', async () => {
    for (const [label, area, title] of AREAS) {
      await areaLink(label).click()
      await heading(title).waitFor({ timeout: 10000 })
      assert.equal(areaOf(), area)
      assert.equal(await currentLabel(), label)
      assert.match(await page.title(), new RegExp(`^${label} · theos$`))
      if (process.env.THEOS_UI_SHOTS) {
        await page.waitForTimeout(300)
        await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, `ui-shell-${area}.png`) })
      }
    }
    for (const [label, area, title] of AREAS.slice(0, -1).reverse()) {
      await page.goBack()
      await heading(title).waitFor({ timeout: 10000 })
      assert.equal(areaOf(), area)
      assert.equal(await currentLabel(), label)
    }
  })

  it('says every namespace in Work, and none in About', async () => {
    await page.goto(`${base}/?area=work`)
    await page.locator('.freshness').getByText('All namespaces').waitFor({ timeout: 5000 })
    await page.goto(`${base}/?area=about`)
    await heading('About this factory').waitFor({ timeout: 5000 })
    assert.equal(
      await page
        .locator('.freshness')
        .getByText(/Namespace|All namespaces/)
        .count(),
      0,
    )
  })

  it('reaches every area by keyboard', async () => {
    await page.goto(`${base}/`)
    await page.locator('.need').first().waitFor({ timeout: 10000 })
    for (const [label, area, title] of AREAS) {
      // Tab forward from wherever the focus is until the area's link has it.
      let focused = ''
      for (let i = 0; i < 30 && focused !== label; i++) {
        await page.keyboard.press('Tab')
        focused = await page.evaluate(() => {
          const el = globalThis.document.activeElement
          return el?.closest('nav[aria-label="Areas"]') ? el.querySelector('.nav-label')?.textContent : ''
        })
      }
      assert.equal(focused, label, `Tab reaches ${label}`)
      await page.keyboard.press('Enter')
      await heading(title).waitFor({ timeout: 10000 })
      assert.equal(areaOf(), area)
    }
  })

  it('shows the versions and where to read about them in About', async () => {
    await page.goto(`${base}/?area=about`)
    const about = page.locator('.about')
    await about.getByText('theos.factory.io/v1beta1').waitFor({ timeout: 5000 })
    const href = (name) => about.getByRole('link', { name }).getAttribute('href')
    // The test build is unreleased, so main's docs and every release.
    assert.equal(await href('Release notes'), 'https://github.com/JairJosafath/theos/releases')
    assert.match(await href(/The design/), /\/blob\/main\/docs\/design\.md$/)
    assert.equal(await href('The repository'), 'https://github.com/JairJosafath/theos')
  })

  it('opens an object in the drawer over the area, full width on request, and Esc leaves the area', async () => {
    await page.goto(`${base}/?area=work&ns=${namespace}&kind=Container&name=orders-api`)
    const drawer = page.getByRole('dialog', { name: 'Container orders-api' })
    await drawer.getByRole('heading', { name: 'orders-api' }).waitFor({ timeout: 10000 })
    // The area stays behind it, and current.
    assert.equal(await currentLabel(), 'Work')
    await heading('Tasks').waitFor({ timeout: 10000 })
    await page.keyboard.press('Escape')
    await drawer.waitFor({ state: 'detached', timeout: 5000 })
    let params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('kind'), params.get('name'), params.get('ns')], [null, null, null])
    assert.equal(areaOf(), 'work')

    // Back opens it again, and Open full page shows it full width.
    await page.goBack()
    await drawer.getByRole('link', { name: 'Open full page' }).click()
    await drawer.waitFor({ state: 'detached', timeout: 5000 })
    await page.getByRole('main').getByRole('heading', { name: 'orders-api' }).waitFor({ timeout: 10000 })
    params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('kind'), params.get('full')], ['Container', '1'])
    await areaLink('Work').click()
    await heading('Tasks').waitFor({ timeout: 10000 })
    params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('kind'), params.get('full')], [null, null])
  })

  it('opens a Brief page from its card, and a Container page from what the Brief made', async () => {
    await page.goto(`${base}/?area=briefs`)
    // A Brief's card is a link to its page (docs/plans/ui-shell.md, step 2).
    await page.getByRole('link', { name: /^Brief webshop: / }).click()
    const header = page.getByRole('region', { name: 'Brief webshop', exact: true })
    await header.waitFor({ timeout: 10000 })
    let params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('ns'), params.get('kind'), params.get('name')], [namespace, 'Brief', 'webshop'])
    assert.match(await page.title(), /^Brief webshop · theos$/)
    // The lifecycle with Planned current, the rounds, and its writes: pause and prompt.
    assert.equal(await header.locator('[aria-current="step"]').textContent(), 'Planned')
    await header.getByRole('button', { name: /^(Pause|Resume) brief webshop$/ }).waitFor({ timeout: 5000 })
    await page.getByRole('heading', { name: 'Rounds' }).first().waitFor({ timeout: 5000 })
    // Its sections are tabs: the timeline is its activity, what it made is related to it.
    await header.getByRole('tab', { name: 'Activity' }).click()
    await header.getByText(/^Round 1 applied$/).waitFor({ timeout: 5000 })
    assert.equal(new URL(page.url()).searchParams.get('otab'), 'activity')
    await header.getByRole('tab', { name: /^Related/ }).click()
    if (process.env.THEOS_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-object-brief.png'), fullPage: true })
    }

    // It made a Container and a Crew both named storefront.
    await header.locator('a[href*="kind=Container"]', { hasText: /^storefront$/ }).click()
    const container = page.getByRole('region', { name: 'Container storefront', exact: true })
    await container.waitFor({ timeout: 10000 })
    params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('kind'), params.get('name')], ['Container', 'storefront'])
    // Failed, with the condition's reason and the exact retry line; another object opens on its first tab.
    assert.equal(await container.locator('[aria-current="step"]').textContent(), 'Failed')
    assert.equal(params.get('otab'), null)
    await container.getByRole('tab', { name: 'Status' }).click()
    await container.getByText('Choices valid: no · UnsupportedType').waitFor({ timeout: 5000 })
    assert.equal(
      await container.locator('.command-line code').textContent(),
      `kubectl -n ${namespace} annotate containers.theos.factory.io storefront theos.factory.io/retry=$(date +%s) --overwrite`,
    )
    await container.getByRole('link', { name: 'Made by Brief webshop' }).waitFor({ timeout: 5000 })
    if (process.env.THEOS_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-object-container.png'), fullPage: true })
    }

    // Back returns through both pages to the Briefs.
    await page.goBack()
    await header.waitFor({ timeout: 10000 })
    await page.goBack()
    await heading('Briefs').waitFor({ timeout: 10000 })
  })

  it('opens a Ready Container page with its relationships and templates', async () => {
    await page.goto(`${base}/?ns=${namespace}&kind=Container&name=orders-api`)
    const container = page.getByRole('region', { name: 'Container orders-api', exact: true })
    await container.waitFor({ timeout: 10000 })
    assert.equal(await container.locator('[aria-current="step"]').textContent(), 'Ready')
    await page.getByRole('heading', { name: 'What it is made from' }).waitFor({ timeout: 5000 })
    await container.getByRole('tab', { name: /^Related/ }).click()
    await container
      .getByText(/Applied: True, AppliedAtScaffold/)
      .first()
      .waitFor({ timeout: 5000 })
    // What a person does now names no command: the region holds the tabs too, whose deploys may.
    assert.equal(await container.locator('.object-next .command-line').count(), 0)
  })

  it('opens a Crew page from Work, and a Task page from the Crew', async () => {
    await page.goto(`${base}/?area=work&tab=crews`)
    await page.getByRole('link', { name: 'Orders API crew', exact: true }).click()
    const crew = page.getByRole('region', { name: 'Crew orders-api', exact: true })
    await crew.waitFor({ timeout: 10000 })
    // The page opens in the drawer over Work, which shows some of the same.
    const drawer = page.getByRole('dialog')
    let params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('ns'), params.get('kind'), params.get('name')], [namespace, 'Crew', 'orders-api'])
    assert.match(await page.title(), /^Crew orders-api · theos$/)
    // Working, its Tasks, the places in use and its last release.
    assert.equal(await crew.locator('[aria-current="step"]').textContent(), 'Working')
    await drawer.getByText('2 of 2 busy').waitFor({ timeout: 5000 })
    await crew.getByRole('tab', { name: 'Activity' }).click()
    await heading('Last release: v0.2.0').waitFor({ timeout: 5000 })
    await drawer.locator('dd', { hasText: /^succeeded Open the run/ }).waitFor({ timeout: 5000 })
    if (process.env.THEOS_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-object-crew.png'), fullPage: true })
    }

    await crew.getByRole('tab', { name: /^Related/ }).click()
    await crew.getByRole('link', { name: 'orders-api-13', exact: true }).click()
    const task = page.getByRole('region', { name: 'Task orders-api-13', exact: true })
    await task.waitFor({ timeout: 10000 })
    params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('kind'), params.get('name')], ['Task', 'orders-api-13'])
    // Failed, with its issue to answer, the history, its agent runs and the handoffs.
    assert.equal(await task.locator('[aria-current="step"]').textContent(), 'Failed')
    await task.getByRole('link', { name: 'Open issue #13' }).waitFor({ timeout: 5000 })
    await drawer.getByText('3 of 3', { exact: true }).waitFor({ timeout: 5000 })
    await drawer.getByText('2 times an agent run handed off to a fresh one at the token limit of 130k').waitFor({
      timeout: 5000,
    })
    // The Task recorded its runs, so the page names the one that handed off.
    await drawer.getByText('Run 3 (round 2, implement) handed off 2 times, at 131k and 138k tokens.').waitFor({
      timeout: 5000,
    })
    // Its history and its agent runs are its activity.
    await task.getByRole('tab', { name: /^Activity/ }).click()
    await task.getByText('Failed, round 3 · RoundsExhausted').waitFor({ timeout: 5000 })
    // Three implement runs and the one review of round 1.
    await task.getByText('run 4', { exact: true }).waitFor({ timeout: 5000 })
    assert.equal(await task.getByText('run 5', { exact: true }).count(), 0)
    await task.getByText(/round 2, implement by claude-implementer · ended .* · handed off 2 times/).waitFor({
      timeout: 5000,
    })
    assert.equal(await drawer.locator('.command-line code').count(), 0)
    await task.getByRole('link', { name: 'Crew orders-api' }).waitFor({ timeout: 5000 })
    if (process.env.THEOS_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-object-task.png'), fullPage: true })
    }

    await page.goBack()
    await crew.waitFor({ timeout: 10000 })
  })

  it('opens a Task page from a board card in Work, and lists the releases', async () => {
    await page.goto(`${base}/?area=work`)
    // Work's releases are its Releases tab, the board its first (docs/plans/ui-shell.md, step 2).
    await page.getByRole('tab', { name: 'Releases' }).click()
    const releases = page.getByRole('region', { name: 'Releases', exact: true })
    await releases.getByText('v0.2.0', { exact: true }).waitFor({ timeout: 10000 })
    assert.equal(
      await releases.getByRole('link', { name: 'Crew orders-api', exact: true }).getAttribute('href'),
      // The Crew opens over Work's Releases tab.
      `/?area=work&tab=releases&ns=${namespace}&kind=Crew&name=orders-api`,
    )
    if (process.env.THEOS_UI_SHOTS) {
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-shell-work-board.png'), fullPage: true })
    }
    await page.getByRole('tab', { name: /^Board/ }).click()
    await page.locator('.board a.task-card', { hasText: 'orders-api-14' }).click()
    const task = page.getByRole('region', { name: 'Task orders-api-14', exact: true })
    await task.waitFor({ timeout: 10000 })
    const params = new URL(page.url()).searchParams
    assert.deepEqual([params.get('ns'), params.get('kind'), params.get('name')], [namespace, 'Task', 'orders-api-14'])
    // The region shows "Loading the Task…" before the Task arrives: textContent
    // waits for the one current step (strict, so two would fail) where count would not.
    assert.equal(await task.locator('[aria-current="step"]').textContent({ timeout: 10000 }), 'Implementing')
    await page.goBack()
    await heading('Tasks').waitFor({ timeout: 10000 })
  })

  it('tells the story in one line, and opens a page from a diagram node', async () => {
    await page.goto(`${base}/?area=story`)
    const line = page.getByRole('navigation', { name: 'The project’s story' })
    await line.getByText('Releases').waitFor({ timeout: 10000 })
    assert.deepEqual(await line.locator('.storyline-label').allTextContents(), [
      'Briefs',
      'Plan',
      'Model',
      'Repositories',
      'Tasks',
      'Releases',
    ])
    // A system's box opens its page; Back returns to the diagram.
    await page.locator('[data-node-id="SoftwareSystem/webshop-core"]').click()
    await page.getByRole('region', { name: 'SoftwareSystem webshop-core', exact: true }).waitFor({ timeout: 10000 })
    await page.goBack()
    await heading('System context: the big picture').waitFor({ timeout: 10000 })

    // A container's box, by keyboard, opens the Container's page.
    await page.goto(`${base}/?area=story&view=containers&system=webshop-core`)
    const node = page.locator('[data-node-id="Container/orders-api"]')
    assert.equal(await node.getAttribute('role'), 'link')
    assert.equal(await node.getAttribute('aria-pressed'), null)
    await node.focus()
    await page.keyboard.press('Enter')
    const container = page.getByRole('region', { name: 'Container orders-api', exact: true })
    await container.waitFor({ timeout: 10000 })
    const params = new URL(page.url()).searchParams
    assert.deepEqual(
      [params.get('area'), params.get('ns'), params.get('kind'), params.get('name')],
      ['story', namespace, 'Container', 'orders-api'],
    )
  })

  it('gives a paused Crew the patch that resumes it', async () => {
    await page.goto(`${base}/?ns=${namespace}&kind=Crew&name=storefront`)
    const crew = page.getByRole('region', { name: 'Crew storefront', exact: true })
    await crew.waitFor({ timeout: 10000 })
    assert.equal(await crew.locator('[aria-current="step"]').textContent(), 'Paused')
    assert.equal(
      await crew.locator('.command-line code').textContent(),
      `kubectl -n ${namespace} patch crews.theos.factory.io storefront --type merge -p '{"spec":{"paused":false}}'`,
    )
  })

  it('opens a read-only page of each remaining kind', async () => {
    // Each with the tab its section is on: the first, unless it says.
    const PAGES = [
      ['Project', namespace, 'Its catalog and Git hosts', 'related'],
      ['SoftwareSystem', 'webshop-core', 'What it is for'],
      ['Relationship', 'orders-api-to-cart-cache', 'Its ends', 'related'],
      ['Session', session, 'Who and when'],
      ['Standard', 'python-standard', 'Its template'],
      ['Compute', 'small', 'Its template'],
      ['Deployment', 'k8s-manifests', 'Its template'],
      ['GitProvider', 'gitea', 'Where repositories go'],
    ]
    for (const [kind, name, section, tab] of PAGES) {
      await page.goto(`${base}/?ns=${namespace}&kind=${kind}&name=${name}${tab ? `&otab=${tab}` : ''}`)
      await page.getByRole('region', { name: `${kind} ${name}`, exact: true }).waitFor({ timeout: 10000 })
      await page.getByRole('heading', { name: section }).first().waitFor({ timeout: 5000 })
      assert.match(await page.title(), new RegExp(`^${kind} ${name} · theos$`))
      if (process.env.THEOS_UI_SHOTS) {
        await page.waitForTimeout(300)
        await page.screenshot({
          path: path.join(process.env.THEOS_UI_SHOTS, `ui-object-${kind.toLowerCase()}.png`),
          fullPage: true,
        })
      }
    }
  })

  it('walks from a Container to its Standard, and up to the Project', async () => {
    await page.goto(`${base}/?ns=${namespace}&kind=Container&name=orders-api`)
    await page.getByRole('link', { name: 'python-standard', exact: true }).click()
    const standard = page.getByRole('region', { name: 'Standard python-standard', exact: true })
    await standard.waitFor({ timeout: 10000 })
    await standard.getByRole('tab', { name: /^Related/ }).click()
    await standard.getByRole('link', { name: 'orders-api', exact: true }).waitFor({ timeout: 5000 })
    await standard.getByRole('link', { name: `Project ${namespace}` }).click()
    const project = page.getByRole('region', { name: `Project ${namespace}`, exact: true })
    await project.waitFor({ timeout: 10000 })
    await project.getByRole('tab', { name: /^Related/ }).click()
    await project.getByRole('link', { name: 'webshop-core', exact: true }).waitFor({ timeout: 5000 })
  })

  it('shows a GitProvider’s Secret by name, and never reads a Secret', async () => {
    await page.goto(`${base}/?ns=${namespace}&kind=GitProvider&name=gitea`)
    const header = page.getByRole('region', { name: 'GitProvider gitea', exact: true })
    await header.waitFor({ timeout: 10000 })
    await page.getByText('the UI never reads Secrets').waitFor({ timeout: 5000 })
    assert.match(await page.getByRole('dialog').textContent(), /Secret gitea-credentials/)
    assert.deepEqual(
      requests.filter((u) => /secrets/i.test(u)),
      [],
      'no request for a Secret',
    )
  })

  it('says a refused read of an optional kind in its panel', async () => {
    api.refuse('standards')
    await page.goto(`${base}/?ns=${namespace}&kind=Standard&name=python-standard`)
    await page.getByText(/^Could not read standards\. The UI is not allowed/).waitFor({ timeout: 10000 })
    await page.goto(`${base}/?ns=${namespace}&kind=Project&name=${namespace}&otab=related`)
    const catalog = page.getByRole('region', { name: 'Its catalog and Git hosts' })
    await catalog.getByText(/^Could not read standards \(403\)/).waitFor({ timeout: 10000 })
    await catalog.getByRole('link', { name: 'small', exact: true }).waitFor({ timeout: 5000 })
    if (process.env.THEOS_UI_SHOTS) {
      await page.waitForTimeout(300)
      await catalog.screenshot({ path: path.join(process.env.THEOS_UI_SHOTS, 'ui-object-refused-read.png') })
    }
  })

  it('opens each area by its default link, and the links from before the areas', async () => {
    const containers = /^Containers of /
    const LINKS = [
      ...AREAS.map(([label, area, title]) => [`?area=${area}`, area, label, title]),
      ['?view=context', 'story', 'Story', 'System context: the big picture'],
      ['?view=containers', 'story', 'Story', containers],
      ['?system=webshop-core', 'story', 'Story', containers],
      ['?view=factory', 'work', 'Work', 'Tasks'],
    ]
    for (const [query, area, label, title] of LINKS) {
      await page.goto(`${base}/${query}`)
      await page
        .getByRole('heading', { name: title, exact: typeof title === 'string' })
        .first()
        .waitFor({ timeout: 10000 })
      assert.equal(areaOf(), area, `${query} opens ${area}`)
      assert.equal(await currentLabel(), label, `${query} marks ${label}`)
    }
    // A Task selected in the factory view opens its page.
    await page.goto(`${base}/?view=factory&task=${namespace}/orders-api-14`)
    await page.getByRole('region', { name: 'Task orders-api-14', exact: true }).waitFor({ timeout: 10000 })
    const params = new URL(page.url()).searchParams
    assert.deepEqual(
      [params.get('area'), params.get('ns'), params.get('kind'), params.get('name'), params.get('task')],
      ['work', namespace, 'Task', 'orders-api-14', null],
    )
    // In the drawer over Work, which stays current.
    assert.equal(await currentLabel(), 'Work')
  })

  it('raises no page error', () => {
    assert.deepEqual(errors, [])
  })
})
