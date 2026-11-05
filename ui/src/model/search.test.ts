import { describe, expect, it } from 'vitest'
import { emptyFactory, emptyStory } from '../k8s/sources'
import type { Project } from '../k8s/types'
import { matchPalette, objectTarget, paletteItems, wordScore } from './search'

const project = (ns: string, displayName?: string): Project =>
  ({ metadata: { name: ns, namespace: ns }, spec: { displayName } }) as Project
const obj = (name: string, namespace: string, displayName?: string) => ({
  metadata: { name, namespace },
  spec: { displayName },
})

function items() {
  const story = {
    ...emptyStory('webshop'),
    containers: [obj('orders-api', 'webshop', 'Orders API'), obj('storefront', 'webshop')],
    people: [obj('customer', 'webshop', 'Customer')],
    tasks: [obj('orders-api-21', 'webshop')],
  } as unknown as ReturnType<typeof emptyStory>
  const factory = {
    ...emptyFactory(),
    // The story's Task again, and a Crew and a Task of another project.
    tasks: [obj('orders-api-21', 'webshop'), obj('lens-api-3', 'lens')],
    crews: [obj('orders-api', 'webshop')],
    agents: [obj('implementer', 'webshop')],
  } as unknown as ReturnType<typeof emptyFactory>
  return paletteItems({ projects: [project('webshop', 'Webshop'), project('lens')], story, factory, area: 'work' })
}

const labels = (query: string) => matchPalette(query, items()).map((i) => i.label)

describe('the command palette’s entries', () => {
  it('lists the areas, the projects, then every object once', () => {
    const all = items()
    expect(all.slice(0, 7).map((i) => i.label)).toEqual([
      'Needs you',
      'Loop',
      'Briefs',
      'Story',
      'Work',
      'Factory',
      'About',
    ])
    expect(all.filter((i) => i.type === 'project').map((i) => [i.label, i.hint])).toEqual([
      ['Webshop', 'Project · webshop'],
      ['lens', 'Project · lens'],
    ])
    const objects = all.filter((i) => i.type === 'object').map((i) => `${i.label} ${i.hint}`)
    expect(objects).toEqual([
      'Person customer webshop',
      'Container orders-api webshop',
      'Container storefront webshop',
      'Task orders-api-21 webshop',
      'Crew orders-api webshop',
      'Agent implementer webshop',
      'Task lens-api-3 lens',
    ])
  })

  it('goes to an object’s page in the drawer, a project’s Story, or the area that draws the kind', () => {
    expect(objectTarget('Task', 'orders-api-21', 'webshop')).toEqual({
      ns: 'webshop',
      kind: 'Task',
      name: 'orders-api-21',
      full: undefined,
    })
    expect(objectTarget('Person', 'customer', 'webshop')).toMatchObject({
      area: 'story',
      project: 'webshop',
      kind: undefined,
    })
    expect(objectTarget('Agent', 'implementer', 'webshop')).toMatchObject({ area: 'work', kind: undefined })
    const lens = items().find((i) => i.key === 'project/lens')!
    // From Work, an area of every project, a project opens its Story.
    expect(lens.target).toMatchObject({ area: 'story', project: 'lens', kind: undefined })
    const loop = items().find((i) => i.key === 'area/loop')!
    expect(loop.target).toMatchObject({ area: 'loop', kind: undefined, name: undefined, tab: undefined })
  })

  it('keeps a project picked from Loop or Briefs in that area', () => {
    const fromLoop = paletteItems({ projects: [project('lens')], area: 'loop' })
    expect(fromLoop.find((i) => i.type === 'project')!.target).toMatchObject({ area: 'loop', project: 'lens' })
  })
})

describe('matching a query', () => {
  it('ignores case and matches kind, name and namespace', () => {
    expect(labels('ORDERS')).toContain('Task orders-api-21')
    expect(labels('task orders')).toEqual(['Task orders-api-21'])
    expect(labels('lens')).toEqual(['lens', 'Task lens-api-3'])
    // An area matches by what it is about, after the object named so.
    expect(labels('crew')).toEqual(['Crew orders-api', 'Work'])
  })

  it('matches a display name too', () => {
    expect(labels('Orders API')).toContain('Container orders-api')
    expect(labels('customer')).toEqual(['Person customer'])
  })

  it('ranks the name’s start first, the closest name before a longer one, and an area before an object', () => {
    expect(labels('orders').slice(0, 3)).toEqual(['Container orders-api', 'Crew orders-api', 'Task orders-api-21'])
    expect(labels('work')[0]).toBe('Work')
  })

  it('finds a query’s letters in order, fuzzily, after the substring matches', () => {
    expect(labels('oapi21')).toEqual(['Task orders-api-21'])
    expect(labels('stfr')).toEqual(['Container storefront'])
    expect(labels('zzz')).toEqual([])
  })

  it('scores a word: the label’s start, a word’s start, a substring, then letters in order', () => {
    expect(wordScore('ord', 'orders-api', 'orders-api webshop')).toBe(0)
    expect(wordScore('api', 'orders-api', 'orders-api webshop')).toBe(1)
    expect(wordScore('rder', 'orders-api', 'orders-api webshop')).toBe(2)
    expect(wordScore('oap', 'orders-api', 'orders-api webshop')).toBeGreaterThan(3)
    expect(wordScore('x', 'orders-api', 'orders-api webshop')).toBeUndefined()
  })

  it('lists everything in order for an empty query, up to the limit', () => {
    expect(matchPalette('  ', items()).map((i) => i.label)).toEqual(items().map((i) => i.label))
    expect(matchPalette('', items(), 3)).toHaveLength(3)
  })
})
