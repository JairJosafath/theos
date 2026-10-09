import { describe, expect, it } from 'vitest'
import type { Project } from '../k8s/types'
import { DEV_VERSION, factoryVersions, versionSkew } from './versions'

const project = (name: string, factory?: { operatorVersion?: string; chartVersion?: string }): Project => ({
  metadata: { name, namespace: name },
  status: factory ? { factory } : undefined,
})

describe('versionSkew', () => {
  it('says nothing when the UI and the operator are the same release', () => {
    expect(versionSkew({ ui: '1.4.0', operator: '1.4.0' })).toBeUndefined()
  })

  it('warns when they differ, naming both', () => {
    const warning = versionSkew({ ui: '1.4.0', operator: '1.5.0' })
    expect(warning).toContain('1.4.0')
    expect(warning).toContain('1.5.0')
  })

  it('says nothing when both are unreleased builds', () => {
    expect(versionSkew({ ui: DEV_VERSION, operator: DEV_VERSION })).toBeUndefined()
  })

  it('warns when only one of them is an unreleased build', () => {
    expect(versionSkew({ ui: DEV_VERSION, operator: '1.4.0' })).toContain('1.4.0')
  })

  it('says nothing while no Project has reported the operator', () => {
    expect(versionSkew({ ui: '1.4.0', operator: undefined })).toBeUndefined()
  })
})

describe('factoryVersions', () => {
  it('takes the operator and chart from the first Project that reported them', () => {
    const v = factoryVersions(
      [project('new'), project('webshop', { operatorVersion: '1.4.0', chartVersion: '1.4.1' })],
      '1.4.0',
      'c4.factory.io/v1alpha1',
    )
    expect(v).toEqual({ ui: '1.4.0', operator: '1.4.0', chart: '1.4.1', api: 'c4.factory.io/v1alpha1' })
  })

  it('leaves them unknown when no Project reported one', () => {
    const v = factoryVersions([project('new')], DEV_VERSION, 'c4.factory.io/v1alpha1')
    expect(v.operator).toBeUndefined()
    expect(v.chart).toBeUndefined()
  })

  it('leaves the chart unknown when the operator was not installed by the chart', () => {
    const v = factoryVersions([project('webshop', { operatorVersion: DEV_VERSION })], DEV_VERSION, 'x/v1')
    expect(v.operator).toBe(DEV_VERSION)
    expect(v.chart).toBeUndefined()
  })
})
