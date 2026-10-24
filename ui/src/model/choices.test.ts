import { describe, expect, it } from 'vitest'
import type { Bundle, CatalogEntry, Container } from '../k8s/types'
import { bundleEntries, bundleTypes, choicesOf } from './choices'

function container(spec: Partial<Container['spec']>, status?: Container['status']): Container {
  return {
    apiVersion: 'c4.factory.io/v1beta1',
    kind: 'Container',
    metadata: { name: 'c', namespace: 'ns' },
    spec: { softwareSystemRef: { name: 's' }, type: 'service', gitProviderRef: { name: 'g' }, ...spec },
    status,
  }
}

function entry(name: string, supportedTypes: CatalogEntry['spec']['supportedTypes']): CatalogEntry {
  return {
    apiVersion: 'c4.factory.io/v1beta1',
    kind: 'Compute',
    metadata: { name, namespace: 'ns' },
    spec: { supportedTypes, template: { url: 'https://example.com/t.git', ref: 'v1' } },
  }
}

function bundle(spec: Bundle['spec']): Bundle {
  return { apiVersion: 'c4.factory.io/v1beta1', kind: 'Bundle', metadata: { name: 'b', namespace: 'ns' }, spec }
}

describe('choicesOf', () => {
  it('reads the spec refs of a Container that named its sides', () => {
    const c = container({
      frameworkRef: { name: 'fw' },
      standardRef: { name: 'std' },
      computeRef: { name: 'small' },
      deploymentRef: { name: 'k8s' },
    })
    expect(choicesOf(c)).toEqual({ framework: 'fw', standard: 'std', compute: 'small', deployment: 'k8s' })
  })

  it('reads status.bundle of a bundled Container, and only the bundle before its expansion', () => {
    expect(choicesOf(container({ bundleRef: { name: 'b' } }))).toEqual({ bundle: 'b' })
    const c = container(
      { bundleRef: { name: 'b' } },
      { bundle: { name: 'b', framework: 'fw', standard: 'std', compute: 'small' } },
    )
    expect(choicesOf(c)).toEqual({ bundle: 'b', framework: 'fw', standard: 'std', compute: 'small' })
  })
})

describe('bundleTypes', () => {
  const catalog = {
    frameworks: [entry('fw', ['service', 'worker', 'job'])],
    standards: [entry('std', ['worker', 'service'])],
    computes: [entry('small', ['service', 'worker', 'static-site'])],
    deployments: [entry('k8s', ['service'])],
  }

  it('is the first entry’s types that every other entry lists, in the first entry’s order', () => {
    const b = bundle({
      frameworkRef: { name: 'fw' },
      standardRef: { name: 'std' },
      computeRef: { name: 'small' },
    })
    expect(bundleTypes(b, catalog)).toEqual(['service', 'worker'])
    expect(bundleTypes(bundle({ ...b.spec, deploymentRef: { name: 'k8s' } }), catalog)).toEqual(['service'])
  })

  it('is none when the entries share no type, or one is not in the catalog', () => {
    expect(bundleTypes(bundle({ frameworkRef: { name: 'fw' }, computeRef: { name: 'static' } }), catalog)).toEqual([])
    expect(bundleTypes(bundle({ frameworkRef: { name: 'missing' } }), catalog)).toEqual([])
    const only = { ...catalog, standards: [entry('std', ['static-site'])] }
    expect(bundleTypes(bundle({ frameworkRef: { name: 'fw' }, standardRef: { name: 'std' } }), only)).toEqual([])
  })

  it('names each entry with its catalog entry, or without one when the catalog lacks it', () => {
    const b = bundle({ frameworkRef: { name: 'fw' }, standardRef: { name: 'gone' } })
    expect(bundleEntries(b, catalog).map((e) => [e.side, e.name, e.entry?.metadata.name])).toEqual([
      ['framework', 'fw', 'fw'],
      ['standard', 'gone', undefined],
    ])
  })
})
