// Tests for vite.config.ts's test settings: the unit tests run on three
// workers, sized for a heavy-job slot rather than the whole device, so a busy
// device doesn't stall them past their timeout (#1193).
// Run with: node --test test/vite-config.test.mjs

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { loadConfigFromFile } from 'vite'

const root = new URL('..', import.meta.url).pathname
const { config } = await loadConfigFromFile({ command: 'serve', mode: 'test' }, 'vite.config.ts', root, 'silent')

describe('the unit tests', () => {
  it('run on three workers, isolated, at the default timeout (#1193)', () => {
    assert.equal(config.test.maxWorkers, 3)
    assert.equal(config.test.isolate, undefined)
    assert.equal(config.test.testTimeout, undefined)
  })
})
