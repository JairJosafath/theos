import { describe, expect, it } from 'vitest'
import type { ChecksMode, Condition, Container } from '../k8s/types'
import { checksOf, describeChecks } from './checks'

const notApplied: Condition = {
  type: 'ChecksApplied',
  status: 'False',
  reason: 'StandardRunsFullChecks',
  message: "The Standard hello-standard can't run the full check only before a release; CI runs it on every change",
}
const applied: Condition = { type: 'ChecksApplied', status: 'True', reason: 'BeforeRelease' }

function container(asked?: ChecksMode, runs?: ChecksMode | 'none', conditions: Condition[] = []): Container {
  return {
    metadata: { name: 'orders-api', namespace: 'webshop' },
    spec: {
      softwareSystemRef: { name: 'webshop-core' },
      type: 'service',
      gitProviderRef: { name: 'gitea' },
      frameworkRef: { name: 'python-fastapi' },
      standardRef: { name: 'python-standard' },
      checks: asked,
    },
    status: {
      standard: runs === 'none' ? undefined : { name: 'python-standard', ref: 'v1.0.0', checks: runs },
      conditions,
    },
  }
}

describe('checksOf', () => {
  it('reads an unset setting and an unset record as EveryChange, nothing pending', () => {
    expect(checksOf(container())).toEqual({ asked: 'EveryChange', runs: 'EveryChange', pending: false })
  })

  it('says nothing runs yet before the scaffold records the Standard', () => {
    expect(checksOf(container('BeforeRelease', 'none', [applied]))).toEqual({ asked: 'BeforeRelease', pending: false })
  })

  it('is pending while the repository still runs the other mode', () => {
    expect(checksOf(container('BeforeRelease', 'EveryChange', [applied])).pending).toBe(true)
    expect(checksOf(container('EveryChange', 'BeforeRelease')).pending).toBe(true)
    expect(checksOf(container('BeforeRelease', 'BeforeRelease', [applied])).pending).toBe(false)
  })

  it('names ChecksApplied when False, and waits for nothing: the Standard keeps the full check on every change', () => {
    const view = checksOf(container('BeforeRelease', 'EveryChange', [notApplied]))
    expect(view.notApplied).toBe(notApplied)
    expect(view.pending).toBe(false)
    expect(checksOf(container('BeforeRelease', 'BeforeRelease', [applied])).notApplied).toBeUndefined()
  })
})

describe('describeChecks', () => {
  it('words each mode', () => {
    expect(describeChecks('EveryChange').label).toBe('On every change')
    expect(describeChecks('BeforeRelease').explanation).toContain('make check-light')
  })
})
