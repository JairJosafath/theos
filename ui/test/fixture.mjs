// The demo fixture (src/demo), bundled for Node so the scripts and tests that
// run outside Vite can read the same objects demo mode shows.

import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { build } from 'rolldown'
import { RESOURCES } from '../src/k8s/types.ts'
import { fakeApi } from './fakeApi.mjs'

const src = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src')

/** Resolves to `{ demoStory, demoFactory }` of src/demo. */
export async function loadFixture() {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'c4ui-fixture-'))
  try {
    const entry = path.join(dir, 'entry.ts')
    await writeFile(
      entry,
      `export { demoStory } from ${JSON.stringify(path.join(src, 'demo/fixture.ts'))}\n` +
        `export { demoFactory } from ${JSON.stringify(path.join(src, 'demo/factoryFixture.ts'))}\n`,
    )
    const file = path.join(dir, 'fixture.mjs')
    await build({ input: entry, output: { file, format: 'esm' }, logLevel: 'silent' })
    return await import(pathToFileURL(file).href)
  } finally {
    await rm(dir, { recursive: true, force: true })
  }
}

/**
 * A fake API (test/fakeApi.mjs) holding the demo fixture at `now`, the
 * project and the factory both, and the story the fixture made, for the
 * names a test opens.
 */
export async function fixtureApi(now) {
  const { demoStory, demoFactory } = await loadFixture()
  const story = demoStory(now)
  const api = fakeApi(story.namespace)
  // Story keys that differ from the plural.
  const PLURAL = { project: RESOURCES.projects, systems: RESOURCES.softwareSystems }
  for (const data of [story, demoFactory(now)]) {
    for (const [k, v] of Object.entries(data)) {
      const plural = PLURAL[k] ?? RESOURCES[k] ?? (Object.values(RESOURCES).includes(k) ? k : undefined)
      if (!plural || !v) continue
      for (const obj of Array.isArray(v) ? v : [v]) api.add(plural, structuredClone(obj))
    }
  }
  return { api, story }
}
