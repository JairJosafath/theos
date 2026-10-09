#!/usr/bin/env node
// Screenshots of every view of the built UI, in demo mode and live against a
// fake Kubernetes API that serves the demo fixture, at a fixed clock, so two
// runs of the same UI give the same pictures. A change that should look the
// same compares its before and after:
//
//   npm run build && node scripts/views.mjs /tmp/before
//   ... change ...
//   npm run build && node scripts/views.mjs /tmp/after --compare /tmp/before
//
//   --compare <dir>   compare each picture with the one of the same name in
//                     <dir>; exit 1 naming the ones that differ or are
//                     missing. A page error also exits 1.
//   --only <prefix>   only the pictures whose name starts with this
//
// Views: the system context and each system's containers, with every node
// selected in turn; the factory, with every Task selected in turn; each one
// also with every <details> opened, and the main ones in the dark theme. Live,
// the factory's last-hour history is masked: it shows measured times.
// Needs Chromium once: npx playwright install chromium-headless-shell.

import { mkdir, readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { parseArgs } from 'node:util'
import { createServer, resolveUpstream } from '../server.mjs'
import { fakeApi, listen } from '../test/fakeApi.mjs'
import { loadFixture } from '../test/fixture.mjs'
import { RESOURCES } from '../src/k8s/types.ts'
import { launchChromiumOrExit } from './browser.mjs'

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const { values, positionals } = parseArgs({
  allowPositionals: true,
  options: { compare: { type: 'string' }, only: { type: 'string', default: '' } },
})
if (positionals.length !== 1) {
  console.error('Usage: node scripts/views.mjs <out dir> [--compare <dir>] [--only <prefix>]')
  process.exit(2)
}
const out = path.resolve(positionals[0])
/** The clock of every page and of the fixture. */
const NOW = Date.parse('2026-06-01T12:00:00Z')

const { demoStory, demoFactory } = await loadFixture()
const story = demoStory(NOW)
const factory = demoFactory(NOW)

// Story keys that differ from the plural.
const PLURAL = { project: RESOURCES.projects, systems: RESOURCES.softwareSystems }
const api = fakeApi(story.namespace)
for (const data of [story, factory]) {
  for (const [k, v] of Object.entries(data)) {
    const plural = PLURAL[k] ?? RESOURCES[k] ?? (Object.values(RESOURCES).includes(k) ? k : undefined)
    if (!plural || !v) continue
    for (const obj of Array.isArray(v) ? v : [v]) api.add(plural, structuredClone(obj))
  }
}
const upstream = resolveUpstream({ K8S_API: await listen(api.server) })
/** A UI server per view, so the factory's health history starts empty each time. */
let ui
async function freshUi() {
  if (ui) await closeUi()
  ui = createServer({ distDir: path.join(root, 'dist'), upstream, log: () => {} })
  return listen(ui)
}
function closeUi() {
  ui.closeAllConnections()
  return new Promise((r) => ui.close(r))
}

const systems = story.systems.map((s) => s.metadata.name)
const tasks = factory.tasks.map((t) => `${t.metadata.namespace}/${t.metadata.name}`)
/** name -> query string, without demo=1. */
const views = [
  ['context', ''],
  ...systems.map((s) => [`containers-${s}`, `view=containers&system=${s}`]),
  ['factory', 'view=factory'],
  ...tasks.map((t) => [`factory-task-${t.replace('/', '-')}`, `view=factory&task=${encodeURIComponent(t)}`]),
]

const browser = await launchChromiumOrExit()
const written = []
const pageErrors = []
try {
  for (const mode of ['demo', 'live']) {
    for (const dark of [false, true]) {
      const context = await browser.newContext({
        viewport: { width: 1440, height: 900 },
        colorScheme: dark ? 'dark' : 'light',
        reducedMotion: 'reduce',
      })
      const page = await context.newPage()
      await page.clock.setFixedTime(NOW)
      const errors = []
      page.on('pageerror', (err) => errors.push(err.message))
      const shoot = async (name) => {
        const file = `${mode}-${dark ? 'dark-' : ''}${name}.png`
        if (!file.startsWith(values.only)) return
        await page.evaluate(() => globalThis.document.fonts?.ready)
        await page.waitForTimeout(200)
        // Live, the last hour shows the fake API's measured response times, which vary.
        const mask = mode === 'live' ? [page.locator('.history')] : []
        await page.screenshot({ path: path.join(out, file), fullPage: true, mask })
        written.push(file)
      }
      await mkdir(out, { recursive: true })
      for (const [name, query] of dark ? views.slice(0, 1 + systems.length + 1) : views) {
        const q = [mode === 'demo' ? 'demo=1' : '', query].filter(Boolean).join('&')
        const base = await freshUi()
        await page.goto(`${base}/${q ? `?${q}` : ''}`, { waitUntil: 'load' })
        // Wait until the view has read what it shows.
        await page.locator('.status', { hasText: mode === 'demo' ? 'Demo data' : 'Live' }).waitFor()
        await page.waitForTimeout(500)
        await shoot(name)
        if (dark) continue
        await page.evaluate(() => globalThis.document.querySelectorAll('details').forEach((d) => (d.open = true)))
        await shoot(`${name}-open`)
        if (query.includes('factory')) continue
        const nodes = await page.locator('[data-node-id]').evaluateAll((els) => els.map((e) => e.dataset.nodeId))
        for (const id of nodes) {
          await page
            .locator(`[data-node-id="${id}"]`)
            .first()
            .click({ position: { x: 8, y: 8 } })
          await shoot(`${name}-node-${id.replace(/[^a-z0-9-]+/gi, '_')}`)
          await page.locator('[aria-label="Close details"]').click()
        }
      }
      pageErrors.push(...errors.map((e) => `${mode}${dark ? ', dark' : ''}: ${e}`))
      await context.close()
    }
  }
} finally {
  await browser.close()
  if (ui) await closeUi()
  api.server.closeAllConnections()
  api.server.close()
}
console.log(`Saved ${written.length} pictures in ${out}`)
if (pageErrors.length) {
  console.error(`The page threw:\n  ${pageErrors.join('\n  ')}`)
  process.exit(1)
}

if (values.compare) {
  const differ = []
  for (const file of written) {
    const a = await readFile(path.join(values.compare, file)).catch(() => undefined)
    const b = await readFile(path.join(out, file))
    if (!a || !a.equals(b)) differ.push(file)
  }
  // A view that is gone (a node or Task no longer found) is a difference too.
  const before = (await readdir(values.compare)).filter((f) => f.endsWith('.png') && f.startsWith(values.only))
  for (const file of before) if (!written.includes(file)) differ.push(`${file} (missing)`)
  if (differ.length) {
    console.error(`${differ.length} of ${written.length} differ from ${values.compare}:\n  ${differ.join('\n  ')}`)
    process.exit(1)
  }
  console.log(`All ${written.length} match ${values.compare}`)
}
