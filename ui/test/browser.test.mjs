// Tests for scripts/browser.mjs, which fetches Chromium's missing system libraries.
// Run with: node --test test/browser.test.mjs

import assert from 'node:assert/strict'
import path from 'node:path'
import { describe, it } from 'node:test'
import { failedExecutable, libraryPath, libsDir, missingFromLdd, packagesFor } from '../scripts/browser.mjs'

describe('failedExecutable', () => {
  it('names the executable a loader error is about', () => {
    const log =
      '[pid=1][err] /dev/cache/ms-playwright/chrome-headless-shell: error while loading shared libraries: libnspr4.so: cannot open shared object file'
    assert.equal(failedExecutable(log), '/dev/cache/ms-playwright/chrome-headless-shell')
  })

  it('ignores other errors', () => {
    assert.equal(failedExecutable("browserType.launch: Executable doesn't exist at /x"), undefined)
    assert.equal(failedExecutable(undefined), undefined)
  })
})

describe('missingFromLdd', () => {
  it('lists the libraries not found, once each', () => {
    const out = [
      '\tlinux-vdso.so.1 (0x00007ffc)',
      '\tlibnspr4.so => not found',
      '\tlibnss3.so => not found',
      '\tlibdl.so.2 => /lib/x86_64-linux-gnu/libdl.so.2 (0x00007f)',
      '\tlibnspr4.so => not found',
      '\tlibasound.so.2 => not found',
    ].join('\n')
    assert.deepEqual(missingFromLdd(out), ['libnspr4.so', 'libnss3.so', 'libasound.so.2'])
  })

  it('finds nothing when every library resolves', () => {
    assert.deepEqual(missingFromLdd('\tlibc.so.6 => /lib/libc.so.6 (0x1)\n'), [])
  })
})

describe('packagesFor', () => {
  it('maps the libraries to packages, one group per package', () => {
    const { groups, unknown } = packagesFor(['libnspr4.so', 'libnss3.so', 'libnssutil3.so', 'libasound.so.2'])
    assert.deepEqual(groups, [['libnspr4'], ['libnss3'], ['libasound2t64', 'libasound2']])
    assert.deepEqual(unknown, [])
  })

  it('reports libraries no known package holds', () => {
    assert.deepEqual(packagesFor(['libmystery.so.9']), { groups: [], unknown: ['libmystery.so.9'] })
  })
})

describe('libraryPath', () => {
  it('puts the unpacked folders before the current value', () => {
    assert.equal(libraryPath(['/a', '/b'], '/c'), '/a:/b:/c')
    assert.equal(libraryPath(['/a'], undefined), '/a')
    assert.equal(libraryPath([], ''), undefined)
  })
})

describe('libsDir', () => {
  it('lives in the XDG cache, else ~/.cache', () => {
    assert.equal(libsDir({ XDG_CACHE_HOME: '/x/.dev/cache' }), '/x/.dev/cache/c4factory/chromium-libs')
    assert.match(libsDir({}), new RegExp(`${path.join('.cache', 'c4factory', 'chromium-libs')}$`))
  })
})
