import { describe, expect, it } from 'vitest'
import { AREAS, areaLabel, inNamespace, isArea } from './areas'

describe('areas', () => {
  it('lists the six areas in the order of the plan', () => {
    expect(AREAS.map((a) => a.label)).toEqual(['Needs you', 'Briefs', 'Story', 'Work', 'Factory', 'About'])
  })

  it('reads only known areas from the URL', () => {
    expect(isArea('work')).toBe(true)
    expect(isArea('containers')).toBe(false)
    expect(isArea(null)).toBe(false)
  })

  it("names an area, and says which ones show one project's namespace", () => {
    expect(areaLabel('needs')).toBe('Needs you')
    expect(AREAS.filter((a) => inNamespace(a.key)).map((a) => a.key)).toEqual(['briefs', 'story'])
  })
})
