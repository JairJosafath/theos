import { describe, expect, it } from 'vitest'
import source from './App.tsx?raw'
import demoSource from './lib/demo.ts?raw'

// Decision 10 of docs/plans/ui-lifecycle.md: no timer re-renders the tree. A
// running clock lives in the components that show one (useNow), so App, which
// holds every view, re-renders only when the URL or the data changes.
describe('App', () => {
  it('keeps no clock and starts no timer', () => {
    expect(source).not.toMatch(/\buseNow\b|\bsetInterval\b|\bsetTimeout\b|requestAnimationFrame/)
  })

  it('loads the demo fixture only in demo mode, with a dynamic import', () => {
    expect(source).not.toMatch(/^import .*\/demo\//m)
    expect(demoSource).not.toMatch(/^import .*\/demo\//m)
    expect(demoSource).toContain("import('../demo/demoSource')")
  })
})
