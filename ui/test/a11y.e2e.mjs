// Accessibility of the new shell (?shell=next) against a fake Kubernetes API
// serving the demo fixture: axe finds no WCAG 2.1 A or AA violation in any
// area or object page, in the light theme or the dark one, and everything
// the keyboard reaches shows that it has the focus.
//
// Needs a build and Chromium:  npm run test:e2e

import assert from 'node:assert/strict'
import path from 'node:path'
import { after, before, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { AxeBuilder } from '@axe-core/playwright'
import { launchChromium } from '../scripts/browser.mjs'
import { createServer, resolveUpstream } from '../server.mjs'
import { listen } from './fakeApi.mjs'
import { fixtureApi } from './fixture.mjs'

const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')
const NOW = Date.parse('2026-06-01T12:00:00Z')
const WCAG = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']

/** What a violation's nodes are, in a line each, so a failure says where to look. */
function describeViolations(violations) {
  return violations
    .map(
      (v) =>
        `${v.id} (${v.impact}): ${v.help}\n${v.nodes.map((n) => `  ${n.target.join(' ')}: ${n.any[0]?.message ?? n.failureSummary}`).join('\n')}`,
    )
    .join('\n')
}

describe('accessibility of the new shell', () => {
  let api
  let ui
  let browser
  let base
  let pages

  before(async () => {
    const { api: fake, story } = await fixtureApi(NOW)
    api = fake
    const ns = story.namespace
    const obj = (kind, name) => [`${kind} ${name}`, `ns=${ns}&kind=${kind}&name=${name}`, 'region']
    pages = [
      ['Needs you', 'area=needs', 'heading'],
      ['Briefs', 'area=briefs', 'heading'],
      ['System context: the big picture', 'area=story', 'heading'],
      ['Containers of Webshop Core', 'area=story&view=containers&system=webshop-core', 'heading'],
      ['Tasks', 'area=work', 'heading'],
      ['Agent slots', 'area=factory', 'heading'],
      ['About this factory', 'area=about', 'heading'],
      obj('Brief', story.briefs[0].metadata.name),
      obj('Container', 'storefront'),
      obj('Crew', story.crews[0].metadata.name),
      obj('Task', story.tasks[0].metadata.name),
      obj('Project', ns),
      obj('Standard', 'python-standard'),
    ]
    ui = createServer({
      distDir: dist,
      upstream: resolveUpstream({ K8S_API: await listen(api.server) }),
      log: () => {},
    })
    base = await listen(ui)
    browser = await launchChromium()
  })

  after(async () => {
    await browser?.close()
    ui?.closeAllConnections()
    api?.server.closeAllConnections()
    await new Promise((r) => ui.close(r))
    await new Promise((r) => api.server.close(r))
  })

  for (const theme of ['light', 'dark']) {
    it(`has no WCAG 2.1 AA violation in the ${theme} theme`, async () => {
      const context = await browser.newContext({
        viewport: { width: 1440, height: 900 },
        reducedMotion: 'reduce',
        colorScheme: theme,
      })
      const page = await context.newPage()
      await page.clock.setFixedTime(NOW)
      const found = []
      try {
        for (const [name, query, role] of pages) {
          await page.goto(`${base}/?shell=next&${query}`)
          await page.getByRole(role, { name, exact: true }).first().waitFor({ timeout: 10000 })
          const { violations } = await new AxeBuilder({ page }).withTags(WCAG).analyze()
          if (violations.length) found.push(`${query}:\n${describeViolations(violations)}`)
        }
      } finally {
        await context.close()
      }
      assert.equal(found.length, 0, `axe found violations in the ${theme} theme:\n${found.join('\n\n')}`)
    })
  }

  it('shows where the focus is on everything the keyboard reaches', async () => {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' })
    const page = await context.newPage()
    await page.clock.setFixedTime(NOW)
    const unmarked = []
    let reached = 0
    try {
      for (const [name, query, role] of pages) {
        await page.goto(`${base}/?shell=next&${query}`)
        await page.getByRole(role, { name, exact: true }).first().waitFor({ timeout: 10000 })
        const seen = new Set()
        for (let i = 0; i < 80; i++) {
          await page.keyboard.press('Tab')
          const focus = await page.evaluate(() => {
            const el = globalThis.document.activeElement
            if (!el || el === globalThis.document.body) return undefined
            const css = globalThis.getComputedStyle(el)
            const ring = css.outlineStyle !== 'none' && parseFloat(css.outlineWidth) >= 2
            // A diagram box draws its ring as a box-shadow, or as the stroke of its shape.
            const drawn = css.boxShadow !== 'none' || el.classList.contains('node')
            const id = `${el.tagName.toLowerCase()}${el.className ? `.${String(el.className).split(' ').join('.')}` : ''} "${(el.textContent ?? '').trim().slice(0, 40)}"`
            return { id, marked: ring || drawn }
          })
          if (!focus || seen.has(focus.id)) break
          seen.add(focus.id)
          reached++
          if (!focus.marked) unmarked.push(`${query}: ${focus.id}`)
        }
      }
    } finally {
      await context.close()
    }
    assert.ok(reached > pages.length * 5, `Tab reached only ${reached} elements`)
    assert.deepEqual(unmarked, [], 'focused elements without a visible focus ring')
  })
})
