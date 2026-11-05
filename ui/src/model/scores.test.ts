import { describe, expect, it } from 'vitest'
import type { ScoreVerdict, Task } from '../k8s/types'
import { isScoreTask, scoresOf, scorerRates } from './scores'

const criteria = ['Has a test', 'Says why']

function judged(name: string): Task {
  return {
    metadata: { name, namespace: 'shop', creationTimestamp: '2026-11-05T10:00:00Z' },
    spec: { containerRef: { name: 'api' }, agentRef: { name: 'implementer' }, goal: 'Build it' },
    status: { phase: 'Merged' },
  }
}

function score(
  name: string,
  of: string,
  verdicts: ScoreVerdict[] | undefined,
  opts: { scorer?: string; generation?: number; model?: string; phase?: 'Merged' | 'Failed' | 'Implementing' } = {},
): Task {
  return {
    metadata: { name, namespace: 'shop', creationTimestamp: `2026-11-05T11:00:0${name.length % 10}Z` },
    spec: {
      kind: 'Score',
      containerRef: { name: 'api' },
      agentRef: { name: 'reviewer' },
      goal: 'evidence',
      score: {
        scorerRef: { name: opts.scorer ?? 'tests' },
        scorerGeneration: opts.generation ?? 1,
        taskRef: { name: of },
        criteria,
        config: opts.model ? { agent: 'implementer', harness: 'claude-code', model: opts.model } : undefined,
      },
    },
    status: {
      phase: opts.phase ?? 'Merged',
      score: verdicts
        ? { criteria: verdicts.map((v, i) => ({ criterion: criteria[i], verdict: v, reason: `why ${i + 1}` })) }
        : undefined,
    },
  }
}

describe('Scores (measure-and-improve, step 5)', () => {
  it('finds the Scores of a Task, and only its own', () => {
    const tasks = [
      judged('api-4'),
      score('tests-api-4', 'api-4', ['Pass', 'Fail']),
      score('style-api-4', 'api-4', undefined, { scorer: 'style', phase: 'Implementing' }),
      score('tests-api-5', 'api-5', ['Pass', 'Pass']),
    ]
    expect(isScoreTask(tasks[1])).toBe(true)
    expect(isScoreTask(tasks[0])).toBe(false)
    // Made in the same second: by name.
    expect(scoresOf(tasks[0], tasks)).toEqual([
      { scorer: 'style', task: 'style-api-4', phase: 'Implementing', verdicts: [] },
      {
        scorer: 'tests',
        task: 'tests-api-4',
        phase: 'Merged',
        verdicts: [
          { criterion: 'Has a test', verdict: 'Pass', reason: 'why 1' },
          { criterion: 'Says why', verdict: 'Fail', reason: 'why 2' },
        ],
      },
    ])
  })

  it('rates each criterion per configuration, Unclear apart, the newest generation alone', () => {
    const rates = scorerRates([
      score('a', 'api-1', ['Pass', 'Fail'], { model: 'sonnet' }),
      score('bb', 'api-2', ['Pass', 'Unclear'], { model: 'sonnet' }),
      score('ccc', 'api-3', ['Fail', 'Pass'], { model: 'opus' }),
      score('dddd', 'api-4', ['Pass', 'Pass']),
      score('old', 'api-5', ['Fail', 'Fail'], { generation: 0, model: 'opus' }),
      score('going', 'api-6', undefined, { phase: 'Implementing', model: 'opus' }),
    ])
    expect(rates).toHaveLength(1)
    const r = rates[0]
    expect([r.scorer, r.scored, r.criteria, r.configs]).toEqual([
      'tests',
      4,
      criteria,
      ['claude-code · sonnet', 'claude-code · opus', 'not recorded'],
    ])
    expect(r.rates[0]['claude-code · sonnet']).toEqual({ pass: 2, fail: 0, unclear: 0, rate: 1 })
    expect(r.rates[1]['claude-code · sonnet']).toEqual({ pass: 0, fail: 1, unclear: 1, rate: 0 })
    expect(r.rates[0]['claude-code · opus']).toEqual({ pass: 0, fail: 1, unclear: 0, rate: 0 })
    expect(r.rates[1]['not recorded']).toEqual({ pass: 1, fail: 0, unclear: 0, rate: 1 })
  })

  it('rates nothing without finished Score Tasks', () => {
    expect(scorerRates([judged('api-4'), score('going', 'api-4', undefined, { phase: 'Implementing' })])).toEqual([])
  })
})
