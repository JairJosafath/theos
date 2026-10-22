import { describe, expect, it } from 'vitest'
import type { Condition, Container } from '../k8s/types'
import { describeRetention, operationsOf } from './operations'

function container(spec?: Container['spec']['operations'], status?: Container['status'] & object): Container {
  return {
    metadata: { name: 'orders-api', namespace: 'webshop' },
    spec: {
      softwareSystemRef: { name: 'webshop-core' },
      type: 'service',
      gitProviderRef: { name: 'gitea' },
      frameworkRef: { name: 'python-fastapi' },
      operations: spec,
    },
    status,
  }
}

const values = (c: Container) => Object.fromEntries(operationsOf(c).rows.map((r) => [r.key, r.value]))

describe('describeRetention', () => {
  it('names zero point-in-time recovery only', () => {
    expect(describeRetention(0)).toBe('point-in-time recovery only')
    expect(describeRetention(1)).toBe('1 day')
    expect(describeRetention(35)).toBe('35 days')
  })
})

const rendered = {
  backups: { retentionDays: 35, copyToRegion: '' },
  recovery: { rpo: '1h', rto: '4h' },
  alerts: { email: 'ops@example.com' },
}

describe('operationsOf', () => {
  it('shows the spec as written, nothing filled in, before the operator resolves it (#1210)', () => {
    const view = operationsOf(container())
    expect(view.resolved).toBe(false)
    expect(values(container())).toEqual({
      retention: 'default',
      copy: 'default',
      rpo: 'default',
      rto: 'default',
      alerts: 'default',
    })
    expect(view.rows.every((r) => r.isDefault)).toBe(true)
  })

  it('shows what the spec sets and marks only the fields left out as defaults', () => {
    const view = operationsOf(
      container({ backups: { retentionDays: 0, copyToRegion: 'eu-west-1' }, recovery: { rpo: '15m' } }),
    )
    expect(view.resolved).toBe(false)
    expect(view.rows.map((r) => [r.key, r.value, r.isDefault])).toEqual([
      ['retention', 'point-in-time recovery only', false],
      ['copy', 'eu-west-1', false],
      ['rpo', '15m', false],
      ['rto', 'default', true],
      ['alerts', 'default', true],
    ])
  })

  it('shows status.operations, every field filled in, once the operator resolved it (#1210)', () => {
    const view = operationsOf(container({ alerts: { email: 'ops@example.com' } }, { operations: rendered }))
    expect(view.resolved).toBe(true)
    expect(view.rows.map((r) => [r.key, r.value, r.isDefault])).toEqual([
      ['retention', '35 days', true],
      ['copy', 'none', true],
      ['rpo', '1h', true],
      ['rto', '4h', true],
      ['alerts', 'ops@example.com', false],
    ])
  })

  it('marks no default while a changed block waits to be applied', () => {
    const changed: Condition = {
      type: 'OperationsApplied',
      status: 'False',
      reason: 'ChangedAfterScaffold',
      message: 'spec.operations changed after the scaffold',
    }
    const c = container({ backups: { retentionDays: 90 } }, { operations: rendered, conditions: [changed] })
    expect(values(c).retention).toBe('35 days')
    expect(operationsOf(c).rows.some((r) => r.isDefault)).toBe(false)
  })

  it('reads an empty region or email written out as left out', () => {
    const c = container({ backups: { copyToRegion: '' }, alerts: { email: '' } })
    const rows = operationsOf(c).rows
    expect(rows.find((r) => r.key === 'copy')).toMatchObject({ value: 'default', isDefault: true })
    expect(rows.find((r) => r.key === 'alerts')).toMatchObject({ value: 'default', isDefault: true })
  })

  it('carries OperationsApplied only when False', () => {
    const changed: Condition = {
      type: 'OperationsApplied',
      status: 'False',
      reason: 'ChangedAfterScaffold',
      message: 'spec.operations changed after the scaffold',
    }
    const ok: Condition = { type: 'OperationsApplied', status: 'True', reason: 'AppliedAtScaffold' }
    expect(operationsOf(container(undefined, { conditions: [changed] })).notApplied).toEqual(changed)
    expect(operationsOf(container(undefined, { conditions: [ok] })).notApplied).toBeUndefined()
    expect(operationsOf(container()).notApplied).toBeUndefined()
  })
})
