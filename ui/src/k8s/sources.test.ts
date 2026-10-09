import { describe, expect, it } from 'vitest'
import { combineConnection } from './sources'
import type { WatchState } from './watch'

const state = (loaded: boolean, error?: Error): WatchState<unknown> => ({ items: [], phase: 'live', loaded, error })

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
})
