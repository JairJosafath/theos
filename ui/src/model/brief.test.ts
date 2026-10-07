import { describe, expect, it } from 'vitest'
import type { Brief, BriefPhase } from '../k8s/types'
import { describeBrief, describeBriefPhase, formatCost, sortBriefs } from './brief'

function brief(name: string, created: string, extra: Partial<Brief> = {}): Brief {
  return { metadata: { name, creationTimestamp: created }, spec: { idea: `Idea ${name}` }, ...extra }
}

describe('describeBriefPhase', () => {
  it.each<[BriefPhase | undefined, string, string]>([
    ['Pending', 'Pending', 'waiting'],
    ['Planning', 'Planning', 'progress'],
    ['Applying', 'Applying', 'progress'],
    ['Planned', 'Planned', 'ok'],
    ['NeedsInput', 'Needs your input', 'waiting'],
    ['Failed', 'Failed', 'error'],
    [undefined, 'No status yet', 'unknown'],
  ])('%s', (phase, label, tone) => {
    const info = describeBriefPhase(phase)
    expect(info).toMatchObject({ label, tone })
    expect(info.explanation).not.toBe('')
  })
})

describe('describeBrief', () => {
  it('splits the prompts into planned and waiting', () => {
    const view = describeBrief({
      ...brief('shop', '2026-01-01T00:00:00Z'),
      spec: { idea: 'A shop', prompts: ['Use Stripe', 'Add gift cards', 'Ship to Belgium'] },
      status: { phase: 'Planned', round: 2, promptsPlanned: 1, costMilliUSD: 1234 },
    })
    expect(view.prompts).toEqual([
      { text: 'Use Stripe', planned: true },
      { text: 'Add gift cards', planned: false },
      { text: 'Ship to Belgium', planned: false },
    ])
    expect(view).toMatchObject({ waitingPrompts: 2, round: 2, cost: '$1.23', needsInput: undefined })
  })

  it('lists the reviewer suggestions and how many wait in all', () => {
    const view = describeBrief({
      ...brief('shop', ''),
      status: {
        phase: 'Planned',
        suggestions: [{ container: 'api', number: 5, title: 'Escape input', url: 'http://g/5', criteria: true }],
        suggestionsWaiting: 60,
      },
    })
    expect(view.suggestions).toEqual([{ container: 'api', number: 5, title: 'Escape input', url: 'http://g/5' }])
    expect(view.suggestionsWaiting).toBe(60)
    expect(view.phase.explanation).toBe(
      'The last round is done; 60 reviewer suggestions wait for your answer in a prompt.',
    )
  })

  it('counts the listed suggestions when no total is given, and has none by default', () => {
    const some = describeBrief({
      ...brief('shop', ''),
      status: { phase: 'Planned', suggestions: [{ container: 'api', number: 5 }] },
    })
    expect(some.suggestionsWaiting).toBe(1)
    expect(some.phase.explanation).toContain('1 reviewer suggestion waits')
    const none = describeBrief({ ...brief('shop', ''), status: { phase: 'Planned' } })
    expect(none).toMatchObject({ suggestions: [], suggestionsWaiting: 0 })
    expect(none.phase.explanation).toBe('The last round is done and no prompt is waiting.')
  })

  it('takes the NeedsInput sentence only while it is True', () => {
    const needs = (status: 'True' | 'False') =>
      describeBrief({
        ...brief('shop', ''),
        status: {
          phase: status === 'True' ? 'NeedsInput' : 'Planned',
          conditions: [
            { type: 'NeedsInput', status, reason: 'AgentMissing', message: 'Agent designer does not exist.' },
          ],
        },
      }).needsInput
    expect(needs('True')).toBe('Agent designer does not exist.')
    expect(needs('False')).toBeUndefined()
  })

  it('says why a failed round failed, from the Planned condition', () => {
    const view = describeBrief({
      ...brief('shop', ''),
      status: { phase: 'Failed', conditions: [{ type: 'Planned', status: 'False', message: 'The plan is invalid.' }] },
    })
    expect(view.failure).toBe('The plan is invalid.')
  })

  it('copes with a Brief the factory has not reported on', () => {
    const view = describeBrief(brief('new', ''))
    expect(view).toMatchObject({
      phase: { label: 'No status yet' },
      prompts: [],
      choices: [],
      created: [],
      issues: [],
      round: 0,
      cost: undefined,
    })
  })
})

describe('formatCost', () => {
  it.each([
    [0, '$0.00'],
    [5, '<$0.01'],
    [10, '$0.01'],
    [420, '$0.42'],
    [12_345, '$12.35'],
  ])('%d', (milli, want) => {
    expect(formatCost(milli)).toBe(want)
  })
})

describe('sortBriefs', () => {
  it('puts the newest first, then by name', () => {
    const sorted = sortBriefs([
      brief('old', '2026-01-01T00:00:00Z'),
      brief('b', '2026-02-01T00:00:00Z'),
      brief('a', '2026-02-01T00:00:00Z'),
      brief('pending', ''),
    ])
    expect(sorted.map((b) => b.metadata.name)).toEqual(['a', 'b', 'old', 'pending'])
  })
})
