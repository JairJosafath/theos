import { describe, expect, it } from 'vitest'
import type { Brief, Relationship } from '../../k8s/types'
import { briefTimeline, relationshipsOf } from './rows'

describe('briefTimeline', () => {
  it('turns each round into an entry, with the question when it asked one', () => {
    const brief = {
      metadata: { name: 'shop' },
      spec: { idea: 'a shop' },
      status: {
        history: [
          { round: 1, outcome: 'Applied', summary: 'Planned the shop' },
          { round: 2, outcome: 'PlannerNeedsInput', summary: 'Asked', needsInput: 'Which payment provider?' },
          { round: 3, outcome: 'RoundFailed', summary: 'The plan did not validate' },
        ],
      },
    } as Brief
    expect(briefTimeline(brief)).toEqual([
      { key: '1', title: 'Round 1 applied', detail: 'Planned the shop', tone: 'ok' },
      { key: '2', title: 'Round 2 asked you', detail: 'Which payment provider?', tone: 'waiting' },
      { key: '3', title: 'Round 3 failed', detail: 'The plan did not validate', tone: 'error' },
    ])
  })
})

describe('relationshipsOf', () => {
  const rel = (name: string, source: string, target: string, applied?: [string, string]): Relationship => ({
    metadata: { name },
    spec: { source: { kind: 'Container', name: source }, target: { kind: 'DataStore', name: target } },
    status: applied ? { conditions: [{ type: 'Applied', status: applied[0] as 'True', reason: applied[1] }] } : {},
  })

  it('lists the Container’s Relationships with their Applied status and reason', () => {
    const rows = relationshipsOf(
      [
        rel('api-db', 'api', 'db', ['True', 'AppliedAtScaffold']),
        rel('api-cache', 'api', 'cache', ['False', 'AddedAfterScaffold']),
        rel('web-db', 'web', 'db'),
      ],
      'api',
    )
    expect(rows).toEqual([
      { kind: 'Relationship', name: 'api-db', note: 'to DataStore db · Applied: True, AppliedAtScaffold' },
      { kind: 'Relationship', name: 'api-cache', note: 'to DataStore cache · Applied: False, AddedAfterScaffold' },
    ])
  })
})
