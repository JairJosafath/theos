import { afterEach, describe, expect, it, vi } from 'vitest'
import { hrefFor, inPageLink, parseUrl, type UrlState } from './hooks'

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
    // The shell's drawer and tabs (docs/plans/ui-shell.md).
    [
      "an object's full page",
      '?area=work&ns=shop&kind=Task&name=api-7&full=1',
      { ...base, area: 'work', ns: 'shop', kind: 'Task', name: 'api-7', full: true },
    ],
    ["an area's tab", '?area=work&tab=crews', { ...base, area: 'work', tab: 'crews' }],
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

  it("writes an object's full page and an area's tab, and full only beside an object", () => {
    at('?area=work&tab=crews')
    expect(hrefFor({ ns: 'shop', kind: 'Task', name: 'api-7' })).toBe(
      '/?area=work&tab=crews&ns=shop&kind=Task&name=api-7',
    )
    expect(hrefFor({ ns: 'shop', kind: 'Task', name: 'api-7', full: true })).toBe(
      '/?area=work&tab=crews&ns=shop&kind=Task&name=api-7&full=1',
    )
    expect(hrefFor({ full: true })).toBe('/?area=work&tab=crews')
  })
})

describe('inPageLink', () => {
  const here = {
    href: 'http://ui.local/?area=work',
    origin: 'http://ui.local',
    pathname: '/',
    search: '?area=work',
  } as Location
  /** A click on a link to `href`, with `init` for its modifiers and the link's own attributes. */
  function click(href: string, init: Partial<MouseEvent> = {}, attrs: Record<string, string> = {}) {
    const a = {
      target: attrs.target ?? '',
      getAttribute: (n: string) => (n === 'href' ? href : (attrs[n] ?? null)),
      hasAttribute: (n: string) => n in attrs,
    }
    const target = { closest: () => a }
    return {
      defaultPrevented: false,
      button: 0,
      metaKey: false,
      ctrlKey: false,
      shiftKey: false,
      altKey: false,
      target,
      ...init,
    } as unknown as MouseEvent
  }

  it("follows a link to another address of the page in place, such as an object's page", () => {
    expect(inPageLink(click('?area=work&ns=shop&kind=Task&name=api-7'), here)?.search).toBe(
      '?area=work&ns=shop&kind=Task&name=api-7',
    )
  })

  it('leaves the browser a click with a modifier or another button, or one handled already', () => {
    const link = '?area=loop'
    expect(inPageLink(click(link, { metaKey: true }), here)).toBeUndefined()
    expect(inPageLink(click(link, { ctrlKey: true }), here)).toBeUndefined()
    expect(inPageLink(click(link, { button: 1 }), here)).toBeUndefined()
    expect(inPageLink(click(link, { defaultPrevented: true }), here)).toBeUndefined()
  })

  it('leaves the browser a link to another site, a new tab, a download or a jump within the page', () => {
    expect(inPageLink(click('https://gitea.local/webshop/orders-api'), here)).toBeUndefined()
    expect(inPageLink(click('?area=loop', {}, { target: '_blank' }), here)).toBeUndefined()
    expect(inPageLink(click('?area=loop', {}, { download: '' }), here)).toBeUndefined()
    expect(inPageLink(click('#content'), here)).toBeUndefined()
  })

  it('loads the page again into or out of demo mode', () => {
    expect(inPageLink(click('?demo=1&area=work'), here)).toBeUndefined()
    expect(inPageLink(click('?'), { ...here, search: '?demo=1', href: 'http://ui.local/?demo=1' })).toBeUndefined()
  })
})
