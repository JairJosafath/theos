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

describe('operationsOf', () => {
  it('fills in the defaults for a Container without a block', () => {
    const view = operationsOf(container())
    expect(values(container())).toEqual({
      retention: '35 days',
      copy: 'none',
      rpo: '1h',
      rto: '4h',
      alerts: 'nobody',
    })
    expect(view.rows.every((r) => r.isDefault)).toBe(true)
  })

  it('shows the spec and marks only the fields left out as defaults', () => {
    const view = operationsOf(
      container({ backups: { retentionDays: 0, copyToRegion: 'eu-west-1' }, recovery: { rpo: '15m' } }),
    )
    expect(view.rows.map((r) => [r.key, r.value, r.isDefault])).toEqual([
      ['retention', 'point-in-time recovery only', false],
      ['copy', 'eu-west-1', false],
      ['rpo', '15m', false],
      ['rto', '4h', true],
      ['alerts', 'nobody', true],
    ])
  })

  it('shows the spec, not what the scaffold rendered with', () => {
    const c = container(
      { backups: { retentionDays: 90 } },
      {
        operations: {
          backups: { retentionDays: 35, copyToRegion: '' },
          recovery: { rpo: '1h', rto: '4h' },
          alerts: { email: 'ops@example.com' },
        },
      },
    )
    expect(values(c).retention).toBe('90 days')
    expect(values(c).alerts).toBe('nobody')
    expect(operationsOf(c).rows.find((r) => r.key === 'alerts')?.isDefault).toBe(true)
  })

  it('reads an empty region or email written out as the default', () => {
    const c = container({ backups: { copyToRegion: '' }, alerts: { email: '' } })
    const rows = operationsOf(c).rows
    expect(rows.find((r) => r.key === 'copy')).toMatchObject({ value: 'none', isDefault: true })
    expect(rows.find((r) => r.key === 'alerts')).toMatchObject({ value: 'nobody', isDefault: true })
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
