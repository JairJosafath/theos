// The build's main chunk holds no demo data: demo mode loads the fixture as a
// chunk of its own (a dynamic import), so a page on a cluster never downloads
// it. Needs a build: npm run test:e2e.

import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'
import { loadFixture } from './fixture.mjs'

const dist = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist')

/** Every name and display name in the demo's story and factory. */
async function fixtureNames() {
  const { demoStory, demoFactory } = await loadFixture()
  const names = new Set()
  const visit = (v) => {
    if (Array.isArray(v)) return v.forEach(visit)
    if (!v || typeof v !== 'object') return
    if (v.metadata?.name) names.add(v.metadata.name)
    if (v.spec?.displayName) names.add(v.spec.displayName)
    Object.values(v).forEach(visit)
  }
  visit(demoStory(0))
  visit(demoFactory(0))
  return [...names]
}

describe('the build', () => {
  it('keeps the demo fixture out of the main chunk, and loads it as a chunk of its own', async () => {
    const html = await readFile(path.join(dist, 'index.html'), 'utf8')
    const main = html.match(/<script[^>]+src="\.\/(assets\/[^"]+\.js)"/)?.[1]
    assert.ok(main, 'index.html loads a main chunk')
    const code = await readFile(path.join(dist, main), 'utf8')
    const names = await fixtureNames()
    assert.ok(names.length > 20, `the fixture has names: ${names.length}`)
    const found = names.filter((n) => code.includes(`"${n}"`) || code.includes(`'${n}'`) || code.includes(`\`${n}\``))
    assert.deepEqual(found, [], `the main chunk holds the fixture's names`)

    const chunks = (await readdir(path.join(dist, 'assets'))).filter((f) => f.endsWith('.js') && `assets/${f}` !== main)
    const demo = []
    for (const f of chunks)
      if ((await readFile(path.join(dist, 'assets', f), 'utf8')).includes('gift-cards')) demo.push(f)
    assert.equal(demo.length, 1, `one other chunk holds the fixture: ${demo.join(', ')}`)
    assert.ok(code.includes(demo[0]), 'the main chunk loads it')
  })
})
