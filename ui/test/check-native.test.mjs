// Tests for scripts/check-native.mjs: an install that lacks a platform package
// the lockfile lists for this platform fails, naming it; one that has them all,
// or lacks only other platforms' packages, passes (#912).
// Run with: node --test test/check-native.test.mjs

import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, it } from 'node:test'

const script = new URL('../scripts/check-native.mjs', import.meta.url).pathname
const libc = process.report.getReport().header.glibcVersionRuntime ? 'glibc' : 'musl'
const otherLibc = libc === 'glibc' ? 'musl' : 'glibc'

const packages = {
  '': { name: 'ui' },
  'node_modules/tool': { version: '1.0.0' },
  'node_modules/native-here': {
    optional: true,
    os: [process.platform],
    cpu: [process.arch],
    ...(process.platform === 'linux' && { libc: [libc] }),
  },
  'node_modules/native-not-aix': { optional: true, os: ['!aix'] },
  'node_modules/native-aix': { optional: true, os: ['aix'], cpu: [process.arch] },
  'node_modules/native-other-cpu': { optional: true, os: [process.platform], cpu: ['s390x', 'mips'] },
  ...(process.platform === 'linux' && {
    'node_modules/native-other-libc': { optional: true, os: ['linux'], cpu: [process.arch], libc: [otherLibc] },
  }),
}

let dir
afterEach(() => rmSync(dir, { recursive: true, force: true }))

// install(names): a directory with the lockfile and the named packages.
function install(names) {
  dir = mkdtempSync(join(tmpdir(), 'check-native-'))
  writeFileSync(join(dir, 'package-lock.json'), JSON.stringify({ lockfileVersion: 3, packages }))
  for (const name of names) {
    mkdirSync(join(dir, 'node_modules', name), { recursive: true })
    writeFileSync(join(dir, 'node_modules', name, 'package.json'), '{}')
  }
  return spawnSync(process.execPath, [script, dir], { encoding: 'utf8' })
}

describe('check-native', () => {
  it("passes when every one of this platform's packages is installed", () => {
    const run = install(['tool', 'native-here', 'native-not-aix'])
    assert.equal(run.status, 0, run.stderr)
  })

  it('fails naming each missing package of this platform', () => {
    const run = install(['tool'])
    assert.equal(run.status, 1)
    assert.match(run.stderr, /node_modules\/native-here/)
    assert.match(run.stderr, /node_modules\/native-not-aix/)
    assert.doesNotMatch(run.stderr, /native-aix|native-other-cpu|native-other-libc/)
  })

  it("fails when only this platform's binding is missing", () => {
    const run = install(['tool', 'native-not-aix'])
    assert.equal(run.status, 1)
    assert.match(run.stderr, /npm skipped 1 package\(s\)/)
  })
})
