import { describe, expect, it } from 'vitest'
import { AREAS, areaLabel, groupLabel, inNamespace, isArea } from './areas'

describe('areas', () => {
  it('lists the seven areas in the order of the plan', () => {
    expect(AREAS.map((a) => a.label)).toEqual(['Needs you', 'Loop', 'Briefs', 'Story', 'Work', 'Factory', 'About'])
  })

  it('reads only known areas from the URL', () => {
    expect(isArea('work')).toBe(true)
    expect(isArea('containers')).toBe(false)
    expect(isArea(null)).toBe(false)
  })

  it('sits each area in its sidebar group: Needs you alone, Build, Operate, About at the bottom', () => {
    expect(AREAS.map((a) => `${a.key}:${a.group}`)).toEqual([
      'needs:needs',
      'loop:build',
      'briefs:build',
      'story:build',
      'work:build',
      'factory:operate',
      'about:bottom',
    ])
    expect([groupLabel('work'), groupLabel('factory'), groupLabel('needs'), groupLabel('about')]).toEqual([
      'Build',
      'Operate',
      undefined,
      undefined,
    ])
  })

  it("names an area, and says which ones show one project's namespace", () => {
    expect(areaLabel('needs')).toBe('Needs you')
    expect(AREAS.filter((a) => inNamespace(a.key)).map((a) => a.key)).toEqual(['loop', 'briefs', 'story'])
  })
})
