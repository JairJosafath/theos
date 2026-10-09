import { describe, expect, it } from 'vitest'
import { combineConnection, mergeConnections, unreadOf } from './sources'
import { ApiError, type WatchState } from './watch'

const live: WatchState<unknown> = { items: [], phase: 'live', loaded: true }
const refused: WatchState<unknown> = {
  items: [],
  phase: 'connecting',
  loaded: false,
  error: new ApiError(403, 'standards.c4.factory.io is forbidden'),
}

describe('combineConnection', () => {
  it('keeps a refused optional read for its panel, and raises no banner for it', () => {
    const c = combineConnection([
      { what: 'containers', state: live },
      { what: 'standards', state: refused, optional: true, resource: 'standards' },
    ])
    expect(c.phase).toBe('live')
    expect(c.problems).toEqual([])
    expect(unreadOf(c, 'standards')).toEqual({
      what: 'standards',
      message: 'standards.c4.factory.io is forbidden',
      code: 403,
      resource: 'standards',
    })
    expect(unreadOf(c, 'computes')).toBeUndefined()
  })

  it('raises a refused required read as a problem', () => {
    const c = combineConnection([{ what: 'containers', state: refused, resource: 'containers' }])
    expect(c.problems.map((p) => p.code)).toEqual([403])
    expect(c.unread).toEqual([])
  })
})

describe('mergeConnections', () => {
  it('keeps both sources’ unread kinds', () => {
    const a = combineConnection([{ what: 'standards', state: refused, optional: true, resource: 'standards' }])
    const b = combineConnection([{ what: 'computes', state: refused, optional: true, resource: 'computes' }])
    expect(
      mergeConnections(a, b)
        .unread?.map((p) => p.resource)
        .sort(),
    ).toEqual(['computes', 'standards'])
  })
})
