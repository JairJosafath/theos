// Launches Playwright's Chromium, also on a device without its system libraries.
//
// Chromium needs a few shared libraries a minimal Linux lacks (libnspr4, libnss3,
// libasound2). `sudo npx playwright install-deps chromium` installs them, but a
// headless agent has no root. So when the loader can't find a library, this
// downloads the Debian packages that hold the missing ones with `apt-get download`
// (no root), unpacks them with `dpkg-deb -x` into a cache folder and launches
// again with LD_LIBRARY_PATH pointing there. Where that isn't possible, it fails
// saying which libraries are missing and the command that installs them.

import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { chromium } from 'playwright'

/** The packages that may hold a library, newest name first (Ubuntu 24.04 added t64). */
export const LIB_PACKAGES = {
  'libnspr4.so': ['libnspr4'],
  'libplc4.so': ['libnspr4'],
  'libplds4.so': ['libnspr4'],
  'libnss3.so': ['libnss3'],
  'libnssutil3.so': ['libnss3'],
  'libsmime3.so': ['libnss3'],
  'libasound.so.2': ['libasound2t64', 'libasound2'],
  'libatk-1.0.so.0': ['libatk1.0-0t64', 'libatk1.0-0'],
  'libatk-bridge-2.0.so.0': ['libatk-bridge2.0-0t64', 'libatk-bridge2.0-0'],
  'libatspi.so.0': ['libatspi2.0-0t64', 'libatspi2.0-0'],
  'libcups.so.2': ['libcups2t64', 'libcups2'],
  'libdbus-1.so.3': ['libdbus-1-3'],
  'libdrm.so.2': ['libdrm2'],
  'libgbm.so.1': ['libgbm1'],
  'libxkbcommon.so.0': ['libxkbcommon0'],
  'libXcomposite.so.1': ['libxcomposite1'],
  'libXdamage.so.1': ['libxdamage1'],
  'libXfixes.so.3': ['libxfixes3'],
  'libXrandr.so.2': ['libxrandr2'],
  'libXext.so.6': ['libxext6'],
  'libX11.so.6': ['libx11-6'],
  'libxcb.so.1': ['libxcb1'],
  'libpango-1.0.so.0': ['libpango-1.0-0'],
  'libcairo.so.2': ['libcairo2'],
  'libexpat.so.1': ['libexpat1'],
  'libglib-2.0.so.0': ['libglib2.0-0t64', 'libglib2.0-0'],
  'libgio-2.0.so.0': ['libglib2.0-0t64', 'libglib2.0-0'],
  'libgobject-2.0.so.0': ['libglib2.0-0t64', 'libglib2.0-0'],
}

/** Where the unpacked libraries live: the XDG cache, which ./dev keeps in .dev/cache. */
export function libsDir(env = process.env) {
  const cache = env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache')
  return path.join(cache, 'c4factory', 'chromium-libs')
}

/** The executable a loader error names, or undefined when the error is something else. */
export function failedExecutable(message) {
  return /(\S+): error while loading shared libraries:/.exec(message ?? '')?.[1]
}

/** The libraries `ldd` reports as "not found", in order, without duplicates. */
export function missingFromLdd(output) {
  const libs = [...output.matchAll(/^\s*(\S+) => not found/gm)].map((m) => m[1])
  return [...new Set(libs)]
}

/** For each missing library, the packages to try; `unknown` lists those without one. */
export function packagesFor(libs) {
  const groups = []
  const unknown = []
  for (const lib of libs) {
    const candidates = LIB_PACKAGES[lib]
    if (!candidates) unknown.push(lib)
    else if (!groups.some((g) => g[0] === candidates[0])) groups.push(candidates)
  }
  return { groups, unknown }
}

/** LD_LIBRARY_PATH with the unpacked library folders first, or the unchanged value. */
export function libraryPath(dirs, current) {
  return [...dirs, ...(current ? [current] : [])].join(':') || undefined
}

/** The folders under root that hold shared libraries. */
function libDirs(root) {
  if (!existsSync(root)) return []
  const dirs = []
  const walk = (dir) => {
    const entries = readdirSync(dir, { withFileTypes: true })
    if (entries.some((e) => !e.isDirectory() && /\.so(\.|$)/.test(e.name))) dirs.push(dir)
    for (const e of entries) if (e.isDirectory()) walk(path.join(dir, e.name))
  }
  walk(root)
  return dirs
}

function envWith(dirs) {
  const env = { ...process.env }
  const value = libraryPath(dirs, process.env.LD_LIBRARY_PATH)
  if (value) env.LD_LIBRARY_PATH = value
  return env
}

function has(cmd) {
  try {
    execFileSync('sh', ['-c', `command -v ${cmd}`], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
}

function missingLibs(executable, env) {
  try {
    return missingFromLdd(execFileSync('ldd', [executable], { env, encoding: 'utf8' }))
  } catch {
    return []
  }
}

/** Downloads and unpacks one package of each group into root; returns what failed. */
function unpack(groups, root) {
  mkdirSync(root, { recursive: true })
  // A folder of its own, so two scripts unpacking at once don't share downloads.
  const debs = mkdtempSync(path.join(root, 'debs-'))
  try {
    const failed = []
    for (const candidates of groups) {
      const pkg = candidates.find((p) => {
        try {
          execFileSync('apt-get', ['download', p], { cwd: debs, stdio: 'ignore' })
          return true
        } catch {
          return false
        }
      })
      if (!pkg) failed.push(candidates.join(' or '))
    }
    for (const deb of readdirSync(debs).filter((f) => f.endsWith('.deb'))) {
      execFileSync('dpkg-deb', ['-x', path.join(debs, deb), path.join(root, 'root')])
    }
    return failed
  } finally {
    rmSync(debs, { recursive: true, force: true })
  }
}

function missingError(libs, unknown, failed) {
  const lines = [
    `Chromium can't start: this device lacks ${libs.join(', ')}.`,
    'Install them once with:  sudo npx playwright install-deps chromium',
  ]
  if (!has('apt-get') || !has('dpkg-deb')) {
    lines.push('Without root, this script fetches them itself, but that needs apt-get and dpkg-deb.')
  } else {
    if (failed.length) lines.push(`apt-get download found no package: ${failed.join(', ')}.`)
    if (unknown.length) lines.push(`No known package holds: ${unknown.join(', ')} (see ui/scripts/browser.mjs).`)
  }
  const err = new Error(lines.join('\n'))
  err.code = 'C4_CHROMIUM_LIBS'
  return err
}

/**
 * chromium.launch(options), fetching missing system libraries without root first.
 * The libraries stay in libsDir(), so only the first launch on a device downloads.
 */
export async function launchChromium(options = {}) {
  const root = libsDir()
  let dirs = libDirs(root)
  try {
    return await chromium.launch({ ...options, env: { ...envWith(dirs), ...options.env } })
  } catch (err) {
    const executable = failedExecutable(err.message)
    if (!executable) throw err
    let libs = missingLibs(executable, envWith(dirs))
    if (!libs.length) throw err
    // A package unpacked here can need another that is missing too: ldd lists the
    // whole chain, so a few rounds settle it.
    for (let round = 0; libs.length; round++) {
      const { groups, unknown } = packagesFor(libs)
      if (round === 3 || unknown.length || !has('apt-get') || !has('dpkg-deb')) throw missingError(libs, unknown, [])
      console.error(`Chromium lacks ${libs.join(', ')}; unpacking them into ${root} (no root needed).`)
      const failed = unpack(groups, root)
      if (failed.length) throw missingError(libs, [], failed)
      dirs = libDirs(root)
      libs = missingLibs(executable, envWith(dirs))
    }
    return chromium.launch({ ...options, env: { ...envWith(dirs), ...options.env } })
  }
}

/** launchChromium for a command-line script: a missing library ends it with the advice alone. */
export async function launchChromiumOrExit(options) {
  try {
    return await launchChromium(options)
  } catch (err) {
    if (err.code !== 'C4_CHROMIUM_LIBS') throw err
    console.error(err.message)
    process.exit(1)
  }
}
