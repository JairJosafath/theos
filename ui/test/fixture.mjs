// The demo fixture (src/demo), bundled for Node so the scripts and tests that
// run outside Vite can read the same objects demo mode shows.

import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { build } from 'rolldown'

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
