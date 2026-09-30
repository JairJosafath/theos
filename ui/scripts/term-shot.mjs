#!/usr/bin/env node
// Renders terminal output (with ANSI colours) as a terminal window and saves a
// screenshot of it: evidence for `make test`, `kubectl get` and the like.
//
//   node scripts/term-shot.mjs <input.txt|-> <out.png> [options]
//
//   --title <text>        window title (default: the input file name)
//   --width <px>          image width (default 1100); long lines wrap
//   --no-wrap             don't wrap: widen the image to the longest line
//                         (--width is then the minimum), good for wide tables
//   --font-size <px>      text size (default 13)
//   --scale <n>           device scale factor, e.g. 2 for crisp images (default 1)
//   --html <out.html>     also save the HTML page
//
// Use "-" to read from stdin. To keep colours, force them on in the command,
// for example:  make test 2>&1 | tee test.txt    or    kubectl get ... --no-headers
// (many tools drop colours when not writing to a terminal; `script -qc "cmd" out.txt`
// records a real terminal session).

import { readFile, writeFile, mkdir } from 'node:fs/promises'
import path from 'node:path'
import { parseArgs } from 'node:util'
import { chromium } from 'playwright'
import { ansiToHtmlLines, DEFAULT_BG, DEFAULT_FG, escapeHtml } from './ansi.mjs'

const { values, positionals } = parseArgs({
  allowPositionals: true,
  options: {
    title: { type: 'string' },
    width: { type: 'string', default: '1100' },
    'no-wrap': { type: 'boolean', default: false },
    'font-size': { type: 'string', default: '13' },
    scale: { type: 'string', default: '1' },
    html: { type: 'string' },
    help: { type: 'boolean', short: 'h', default: false },
  },
})

if (values.help || positionals.length !== 2) {
  console.error('Usage: node scripts/term-shot.mjs <input.txt|-> <out.png> [--title "make test"] [--width 1100]')
  console.error('         [--no-wrap] [--font-size 13] [--scale 2] [--html out.html]')
  process.exit(values.help ? 0 : 2)
}

const [input, out] = positionals

async function readInput(file) {
  if (file !== '-') return readFile(file, 'utf8')
  const chunks = []
  for await (const chunk of process.stdin) chunks.push(chunk)
  return Buffer.concat(chunks).toString('utf8')
}

/** The page: a macOS-style window with a title bar and the output inside. */
function renderPage(lines, { title, fontSize, wrap }) {
  const body = lines.map((l) => `<div class="line">${l || ' '}</div>`).join('')
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>${escapeHtml(title)}</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  html, body { margin: 0; background: #e8ecf1; }
  body { padding: 28px; font-family: 'JetBrains Mono', 'SF Mono', Menlo, Consolas, 'DejaVu Sans Mono', 'Liberation Mono', monospace; }
  .window { background: ${DEFAULT_BG}; border-radius: 10px; overflow: hidden;
    box-shadow: 0 1px 2px rgba(15, 23, 42, .12), 0 14px 36px rgba(15, 23, 42, .22); border: 1px solid #0b0e13; }
  .bar { position: relative; height: 38px; display: flex; align-items: center; padding: 0 14px;
    background: #1a1f27; border-bottom: 1px solid #262c36; }
  .dots { display: flex; gap: 8px; }
  .dots span { width: 12px; height: 12px; border-radius: 50%; }
  .title { position: absolute; left: 90px; right: 90px; text-align: center; color: #9aa4b2;
    font: 500 12.5px/1 system-ui, -apple-system, 'Segoe UI', sans-serif; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  pre { margin: 0; padding: 16px 20px 20px; color: ${DEFAULT_FG}; font: inherit; font-size: ${fontSize}px; line-height: 1.55;
    white-space: ${wrap ? 'pre-wrap' : 'pre'}; overflow-wrap: ${wrap ? 'anywhere' : 'normal'}; tab-size: 8; }
  body.nowrap { width: max-content; min-width: 100%; }
  .line { min-height: 1.55em; }
</style>
</head>
<body class="${wrap ? '' : 'nowrap'}">
<div class="window">
  <div class="bar">
    <div class="dots"><span style="background:#ff5f57"></span><span style="background:#febc2e"></span><span style="background:#28c840"></span></div>
    <div class="title">${escapeHtml(title)}</div>
  </div>
  <pre>${body}</pre>
</div>
</body>
</html>`
}

const text = await readInput(input)
const title = values.title ?? (input === '-' ? 'terminal' : path.basename(input))
const html = renderPage(ansiToHtmlLines(text), {
  title,
  fontSize: Number(values['font-size']),
  wrap: !values['no-wrap'],
})
if (values.html) await writeFile(values.html, html)

const browser = await chromium.launch()
try {
  const page = await browser.newPage({
    // The height grows with the content; the screenshot takes the full page.
    viewport: { width: Number(values.width), height: 200 },
    deviceScaleFactor: Number(values.scale),
  })
  await page.setContent(html, { waitUntil: 'load' })
  if (values['no-wrap']) {
    // Grow the viewport to the widest line, so nothing is cut off on the right.
    const needed = await page.evaluate(() => Math.ceil(globalThis.document.body.scrollWidth))
    if (needed > Number(values.width)) await page.setViewportSize({ width: needed, height: 200 })
  }
  await mkdir(path.dirname(path.resolve(out)), { recursive: true })
  await page.screenshot({ path: out, fullPage: true })
  const lines = text.split('\n').length
  console.log(`Saved ${out} (${lines} lines)`)
} finally {
  await browser.close()
}
