import { describe, expect, it } from 'vitest'
import type { Brief, BriefPhase } from '../k8s/types'
import { describeBrief, describeBriefPhase, describeCost, formatCost, sortBriefs } from './brief'

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

describe('describeCost', () => {
  it.each([
    [undefined, undefined, undefined],
    [1234, 0, '$1.23'],
    [0, 2, 'cost not reported by the agent'],
    [1234, 1, '$1.23, plus 1 run whose cost the agent didn’t report'],
    [1234, 3, '$1.23, plus 3 runs whose cost the agent didn’t report'],
  ])('%s with %s runs not reported is %s', (milli, without, want) => {
    expect(describeCost(milli, without)).toBe(want)
  })

  it('says on the Brief when the agent reported no cost', () => {
    const view = describeBrief({
      ...brief('shop', '2026-01-01T00:00:00Z'),
      status: { phase: 'Planned', round: 1, runsWithoutCost: 1 },
    })
    expect(view.cost).toBe('cost not reported by the agent')
  })
})
