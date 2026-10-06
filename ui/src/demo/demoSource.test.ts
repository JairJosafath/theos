import { describe, expect, it } from 'vitest'
import { DemoStorySource } from './demoSource'
import { DEMO_NAMESPACE } from './fixture'

describe('DemoStorySource as a BriefWriter', () => {
  it('adds a new Brief as Pending and tells its listeners', async () => {
    const source = new DemoStorySource()
    let changes = 0
    source.subscribe(() => changes++)
    await source.create(DEMO_NAMESPACE, 'plant-care', 'Remind me to water the plants')
    const brief = source.getSnapshot().data.briefs.find((b) => b.metadata.name === 'plant-care')
    expect(brief).toMatchObject({ spec: { idea: 'Remind me to water the plants' }, status: { phase: 'Pending' } })
    expect(changes).toBe(1)
  })

  it('refuses a name that is taken or invalid, as the API would', async () => {
    const source = new DemoStorySource()
    await expect(source.create(DEMO_NAMESPACE, 'webshop', 'Again')).rejects.toThrow('already exists')
    await expect(source.create(DEMO_NAMESPACE, 'Web Shop', 'Again')).rejects.toThrow('lowercase')
    await expect(source.create(DEMO_NAMESPACE, 'empty', '  ')).rejects.toThrow('spec.idea')
  })

  it('appends a prompt to a Brief', async () => {
    const source = new DemoStorySource()
    await source.addPrompt(DEMO_NAMESPACE, 'returns', 'Only unopened items')
    const brief = source.getSnapshot().data.briefs.find((b) => b.metadata.name === 'returns')
    expect(brief?.spec.prompts).toEqual(['Only unopened items'])
    await expect(source.addPrompt(DEMO_NAMESPACE, 'nope', 'x')).rejects.toThrow('not found')
  })
})
