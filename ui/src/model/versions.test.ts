import { describe, expect, it } from 'vitest'
import type { Project } from '../k8s/types'
import { aboutLinks, DEV_VERSION, factoryVersions, REPO_URL, runningRelease, versionSkew } from './versions'

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
      'theos.factory.io/v1beta1',
    )
    expect(v).toEqual({ ui: '1.4.0', operator: '1.4.0', chart: '1.4.1', api: 'theos.factory.io/v1beta1' })
  })

  it('leaves them unknown when no Project reported one', () => {
    const v = factoryVersions([project('new')], DEV_VERSION, 'theos.factory.io/v1beta1')
    expect(v.operator).toBeUndefined()
    expect(v.chart).toBeUndefined()
  })

  it('leaves the chart unknown when the operator was not installed by the chart', () => {
    const v = factoryVersions([project('webshop', { operatorVersion: DEV_VERSION })], DEV_VERSION, 'x/v1')
    expect(v.operator).toBe(DEV_VERSION)
    expect(v.chart).toBeUndefined()
  })
})

describe('aboutLinks', () => {
  it('links the docs and release notes of the release the operator reported', () => {
    expect(runningRelease({ ui: DEV_VERSION, operator: '1.4.0' })).toBe('1.4.0')
    expect(aboutLinks({ ui: '1.3.0', operator: '1.4.0' })).toEqual({
      docs: `${REPO_URL}/blob/v1.4.0/docs/design.md`,
      releaseNotes: `${REPO_URL}/releases/tag/v1.4.0`,
      repo: REPO_URL,
    })
  })

  it("falls back to the UI's release while the operator is unknown or unreleased", () => {
    expect(aboutLinks({ ui: '1.3.0', operator: DEV_VERSION }).releaseNotes).toBe(`${REPO_URL}/releases/tag/v1.3.0`)
    expect(aboutLinks({ ui: '1.3.0', operator: undefined }).releaseNotes).toBe(`${REPO_URL}/releases/tag/v1.3.0`)
  })

  it('links main and every release when nothing is released', () => {
    expect(runningRelease({ ui: DEV_VERSION, operator: DEV_VERSION })).toBeUndefined()
    expect(aboutLinks({ ui: DEV_VERSION, operator: undefined })).toEqual({
      docs: `${REPO_URL}/blob/main/docs/design.md`,
      releaseNotes: `${REPO_URL}/releases`,
      repo: REPO_URL,
    })
  })
})
