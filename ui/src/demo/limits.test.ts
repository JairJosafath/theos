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
  'x-kubernetes-validations'?: { rule: string }[]
  properties?: Record<string, Schema>
}

/** The Brief CRD's spec schema. */
function briefSpec(): Record<string, Schema> {
  const crd = parse(briefCRD)
  const version = crd.spec.versions.find((v: { name: string }) => v.name === 'v1beta1')
  return version.schema.openAPIV3Schema.properties.spec.properties
}

describe('the demo limits', () => {
  const spec = briefSpec()

  it('match the Brief CRD', () => {
    expect({
      idea: { min: spec.idea.minLength, max: spec.idea.maxLength },
      prompt: { min: spec.prompts.items?.minLength, max: spec.prompts.items?.maxLength },
      // maxItems is one more, so the CEL rule refuses the 51st with its message (#855).
      prompts: Number(
        spec.prompts['x-kubernetes-validations']?.map((v) => /^size\(self\) <= (\d+)$/.exec(v.rule)?.[1]).find(Boolean),
      ),
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
