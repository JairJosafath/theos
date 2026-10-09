import { describe, expect, it } from 'vitest'
import { parse } from 'yaml'
// The CRD as `make manifests` wrote it.
import briefCRD from '../../../config/crd/bases/c4.factory.io_briefs.yaml?raw'
import { BRIEF_LIMITS, isBriefName } from './limits'

interface Schema {
  minLength?: number
  maxLength?: number
  minimum?: number
  maximum?: number
  maxItems?: number
  items?: Schema
  properties?: Record<string, Schema>
}

/** The Brief CRD's spec schema. */
function briefSpec(): Record<string, Schema> {
  const crd = parse(briefCRD)
  const version = crd.spec.versions.find((v: { name: string }) => v.name === 'v1alpha1')
  return version.schema.openAPIV3Schema.properties.spec.properties
}

describe('the demo limits', () => {
  const spec = briefSpec()

  it('match the Brief CRD', () => {
    expect({
      idea: { min: spec.idea.minLength, max: spec.idea.maxLength },
      prompt: { min: spec.prompts.items?.minLength, max: spec.prompts.items?.maxLength },
      prompts: spec.prompts.maxItems,
      parallel: { min: spec.parallel.minimum, max: spec.parallel.maximum },
    }).toEqual(BRIEF_LIMITS)
  })
})

describe('isBriefName', () => {
  it.each([
    ['shop', true],
    ['web-shop-2', true],
    ['web.shop', true],
    ['a'.repeat(253), true],
    ['a'.repeat(254), false],
    ['.shop', false],
    ['Shop', false],
    ['-shop', false],
    ['shop-', false],
    ['web_shop', false],
    ['', false],
  ])('%j is %s', (name, want) => {
    expect(isBriefName(name)).toBe(want)
  })
})
