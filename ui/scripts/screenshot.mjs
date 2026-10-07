#!/usr/bin/env node
// Takes a screenshot of a web page with Playwright and Chromium.
//
//   node scripts/screenshot.mjs <url> <out.png> [options]
//
//   --width <px>          viewport width (default 1440)
//   --height <px>         viewport height (default 900)
//   --full                capture the whole page, not just the viewport
//   --wait-for <css>      wait until this selector is visible before capturing
//   --delay <ms>          extra wait after load and --wait-for (default 300)
//   --dark                emulate prefers-color-scheme: dark (default light)
//   --scale <n>           device scale factor, e.g. 2 for crisp images (default 1)
//   --hover <css>         hover over this element before capturing
//   --click <css>         click this element before capturing (after --hover)
//   --timeout <ms>        give up after this long (default 30000)
//
// Pages that keep connections open (such as the UI's watches) never go
// "network idle", so this waits for `load`, then --wait-for, then --delay.
// Needs Chromium once: npx playwright install chromium-headless-shell. Missing system
// libraries are fetched without root (see browser.mjs).

import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import { parseArgs } from 'node:util'
import { launchChromiumOrExit } from './browser.mjs'

const { values, positionals } = parseArgs({
  allowPositionals: true,
  options: {
    width: { type: 'string', default: '1440' },
    height: { type: 'string', default: '900' },
    full: { type: 'boolean', default: false },
    'wait-for': { type: 'string' },
    delay: { type: 'string', default: '300' },
    dark: { type: 'boolean', default: false },
    scale: { type: 'string', default: '1' },
    hover: { type: 'string' },
    click: { type: 'string' },
    timeout: { type: 'string', default: '30000' },
    help: { type: 'boolean', short: 'h', default: false },
  },
})

if (values.help || positionals.length !== 2) {
  console.error('Usage: node scripts/screenshot.mjs <url> <out.png> [--width 1440] [--height 900] [--full]')
  console.error('         [--wait-for <css>] [--delay <ms>] [--dark] [--scale <n>] [--hover <css>] [--click <css>]')
  process.exit(values.help ? 0 : 2)
}

const [url, out] = positionals
const timeout = Number(values.timeout)

const browser = await launchChromiumOrExit()
try {
  const context = await browser.newContext({
    viewport: { width: Number(values.width), height: Number(values.height) },
    deviceScaleFactor: Number(values.scale),
    colorScheme: values.dark ? 'dark' : 'light',
    reducedMotion: 'reduce',
  })
  const page = await context.newPage()
  page.setDefaultTimeout(timeout)
  const errors = []
  page.on('pageerror', (err) => errors.push(err.message))

  const response = await page.goto(url, { waitUntil: 'load', timeout })
  if (response && !response.ok()) console.warn(`Warning: ${url} answered ${response.status()}`)
  if (values['wait-for']) await page.locator(values['wait-for']).first().waitFor({ state: 'visible', timeout })
  // Web fonts can arrive after load; wait so text is not captured in a fallback font.
  await page.evaluate(() => globalThis.document.fonts?.ready)
  if (values.hover) await page.locator(values.hover).first().hover()
  if (values.click) await page.locator(values.click).first().click()
  await page.waitForTimeout(Number(values.delay))

  await mkdir(path.dirname(path.resolve(out)), { recursive: true })
  await page.screenshot({ path: out, fullPage: values.full })
  console.log(`Saved ${out}`)
  if (errors.length) console.warn(`Page errors:\n  ${errors.join('\n  ')}`)
} finally {
  await browser.close()
}
