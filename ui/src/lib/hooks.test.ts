import { afterEach, describe, expect, it, vi } from 'vitest'
import { hrefFor, parseUrl, type UrlState } from './hooks'

const base: UrlState = { demo: false, view: 'context' }

describe('parseUrl', () => {
  it.each<[string, string, UrlState]>([
    ['nothing: the shell picks the area', '', base],
    ['an area', '?area=factory', { ...base, area: 'factory' }],
    ['an unknown area', '?area=nowhere', base],
    ['an object page', '?ns=shop&kind=Task&name=api-7', { ...base, ns: 'shop', kind: 'Task', name: 'api-7' }],
    ['demo mode', '?demo=1', { ...base, demo: true }],
    // Links from before the areas.
    ['the system context', '?view=context', { ...base, area: 'story' }],
    ['the containers', '?view=containers', { ...base, area: 'story', view: 'containers' }],
    [
      "a system's containers",
      '?view=containers&system=core',
      { ...base, area: 'story', view: 'containers', system: 'core' },
    ],
    ['a system alone', '?system=core', { ...base, area: 'story', view: 'containers', system: 'core' }],
    ['a project', '?project=shop', { ...base, area: 'story', project: 'shop' }],
    ['the factory view', '?view=factory', { ...base, area: 'work' }],
    [
      'a Task in the factory view',
      '?view=factory&task=shop/api-7',
      { ...base, area: 'work', ns: 'shop', kind: 'Task', name: 'api-7' },
    ],
    ['a Task that names no namespace', '?view=factory&task=api-7', { ...base, area: 'work' }],
    [
      'a Task beside an object',
      '?view=factory&task=shop/api-7&kind=Crew&name=api',
      { ...base, area: 'work', kind: 'Crew', name: 'api' },
    ],
    ['the shell switch, gone', '?shell=next', base],
    ['the shell switch with an area', '?shell=next&area=work', { ...base, area: 'work' }],
    ['an area beside an old view', '?area=briefs&view=factory', { ...base, area: 'briefs' }],
    ['an area beside the containers', '?area=work&view=containers', { ...base, area: 'work', view: 'containers' }],
  ])('reads %s', (_, search, want) => {
    expect(parseUrl(search)).toEqual(want)
  })
})

describe('hrefFor', () => {
  afterEach(() => vi.unstubAllGlobals())
  const at = (search: string) => vi.stubGlobal('window', { location: { pathname: '/', search, hash: '' } })

  it.each([
    ['', '/'],
    ['?shell=next', '/'],
    ['?view=context', '/?area=story'],
    ['?view=containers&system=core', '/?area=story&view=containers&system=core'],
    ['?view=factory', '/?area=work'],
    ['?view=factory&task=shop/api-7', '/?area=work&ns=shop&kind=Task&name=api-7'],
    ['?demo=1&project=shop', '/?demo=1&area=story&project=shop'],
  ])('writes %s as %s', (search, want) => {
    at(search)
    expect(hrefFor({})).toBe(want)
  })

  it('never writes the shell switch or a selected Task', () => {
    at('?shell=next&area=work&task=shop/api-7')
    expect(hrefFor({ area: 'factory' })).toBe('/?area=factory')
  })
})
