import { describe, expect, it } from 'vitest'
import { combineConnection, mergeConnections, problemsOf, readOf, unreadOf } from './sources'
import { ApiError, type WatchState } from './watch'

const state = (loaded: boolean, error?: Error): WatchState<unknown> => ({ items: [], phase: 'live', loaded, error })
const live: WatchState<unknown> = { items: [], phase: 'live', loaded: true }
const refused: WatchState<unknown> = {
  items: [],
  phase: 'connecting',
  loaded: false,
  error: new ApiError(403, 'standards.c4.factory.io is forbidden'),
}

describe('combineConnection', () => {
  it('is loaded with the required lists, and settled only once the optional ones loaded or failed', () => {
    const waiting = combineConnection([
      { what: 'tasks', state: state(true) },
      { what: 'briefs', state: state(false), optional: true },
    ])
    expect([waiting.loaded, waiting.settled]).toEqual([true, false])
    const failed = combineConnection([
      { what: 'tasks', state: state(true) },
      { what: 'briefs', state: state(false, new Error('forbidden')), optional: true },
    ])
    expect([failed.loaded, failed.settled, failed.problems]).toEqual([true, true, []])
  })

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

describe('problemsOf', () => {
  it('finds the failed reads, required or optional, of the kinds a panel shows', () => {
    const c = combineConnection([
      { what: 'containers', state: refused, resource: 'containers' },
      { what: 'standards', state: refused, optional: true, resource: 'standards' },
      { what: 'tasks', state: live, resource: 'tasks' },
    ])
    expect(problemsOf(c, 'containers', 'standards').map((p) => p.what)).toEqual(['containers', 'standards'])
    expect(problemsOf(c, 'tasks')).toEqual([])
  })
})

describe('readOf', () => {
  it('is true for kinds whose lists loaded or failed, while another still loads', () => {
    const c = combineConnection([
      { what: 'containers', state: live, resource: 'containers' },
      { what: 'sessions', state: refused, resource: 'sessions' },
      { what: 'tasks', state: state(false), resource: 'tasks' },
    ])
    expect(c.loaded).toBe(false)
    expect(readOf(c, 'containers', 'sessions')).toBe(true)
    expect(readOf(c, 'containers', 'tasks')).toBe(false)
  })

  it('falls back to loaded where the source says nothing of its lists', () => {
    expect(readOf({ phase: 'demo', loaded: true, problems: [] }, 'tasks')).toBe(true)
    expect(readOf({ phase: 'connecting', loaded: false, problems: [] }, 'tasks')).toBe(false)
  })
})
