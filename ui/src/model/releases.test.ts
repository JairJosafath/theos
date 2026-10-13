import { describe, expect, it } from 'vitest'
import type { Crew } from '../k8s/types'
import { publishFailure } from './releases'

const crew = (state: string, reason: string): Crew => ({
  metadata: { name: 'cel-lens', namespace: 'cel-lens' },
  spec: { containerRef: { name: 'cel-lens' }, implementer: { name: 'i' }, reviewer: { name: 'r' } },
  status: {
    lastRelease: { tag: 'v0.2.0', commit: 'a04e6f1', time: '2026-06-01T09:00:00Z', publish: { state } },
    conditions: [
      { type: 'Published', status: 'False', reason, message: '', lastTransitionTime: '2026-06-01T09:00:00Z' },
    ],
  },
})

describe('publishFailure', () => {
  it.each([
    ['Unauthorized', 'GitHub refused the token for the target repository'],
    ['RepositoryRenamed', 'the target repository was renamed'],
    ['RepositoryReplaced', 'the target repository is another one than the Crew published to before'],
  ])('says %s in words', (reason, words) => {
    expect(publishFailure(crew('Failed', reason))).toBe(words)
  })

  it('says nothing of a publish that did not fail', () => {
    expect(publishFailure(crew('Pending', 'Waiting'))).toBeUndefined()
  })
})
