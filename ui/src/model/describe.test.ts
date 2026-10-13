import { describe, expect, it } from 'vitest'
import {
  describeCondition,
  describeContainerPhase,
  describeContainerType,
  describeCrewPhase,
  splitTemplateRef,
  splitWords,
  timeAgo,
  titleOf,
} from './describe'

describe('timeAgo', () => {
  const now = Date.parse('2026-09-30T12:00:00Z')
  it.each([
    ['2026-09-30T11:59:48Z', '12 s ago'],
    ['2026-09-30T11:57:00Z', '3 min ago'],
    ['2026-09-30T10:00:00Z', '2 h ago'],
    ['2026-09-26T12:00:00Z', '4 d ago'],
    ['2026-09-30T12:00:05Z', 'just now'],
  ])('%s is %s', (iso, text) => {
    expect(timeAgo(iso, now)).toBe(text)
  })

  it('handles missing and broken times', () => {
    expect(timeAgo(undefined, now)).toBe('never')
    expect(timeAgo('yesterday', now)).toBe('unknown')
  })
})

describe('describeCondition', () => {
  it.each([
    ['NeedsInput', 'False', 'ok'],
    ['NeedsInput', 'True', 'waiting'],
    ['NeedsYou', 'False', 'ok'],
    ['IssuesFailed', 'True', 'waiting'],
    ['Paused', 'False', 'ok'],
  ] as const)('reads a Brief flag %s=%s as %s', (type, status, tone) => {
    expect(describeCondition({ type, status, reason: 'X' }).tone).toBe(tone)
  })

  it('explains a failed type check, keeping the operator message', () => {
    const info = describeCondition({
      type: 'ChoicesValid',
      status: 'False',
      reason: 'UnsupportedType',
      message: 'python-fastapi does not support static-site',
    })
    expect(info).toEqual({
      title: 'One of its templates does not support this type of container',
      tone: 'error',
      detail: 'python-fastapi does not support static-site',
    })
  })

  it('treats "not yet" reasons as waiting, not as errors', () => {
    expect(describeCondition({ type: 'ReferencesResolved', status: 'False', reason: 'NotFound' }).tone).toBe('waiting')
    expect(describeCondition({ type: 'Applied', status: 'False', reason: 'WaitingForScaffold' }).title).toMatch(
      /when the container is scaffolded/,
    )
  })

  it('says plainly when a relationship was added after scaffolding', () => {
    const info = describeCondition({ type: 'Applied', status: 'False', reason: 'AddedAfterScaffold' })
    expect(info.title).toBe('Added after scaffolding, so the repository does not include it')
    expect(info.tone).toBe('error')
  })

  it('falls back to the condition type for unknown conditions', () => {
    expect(describeCondition({ type: 'SomethingNew', status: 'True' }).title).toBe('Something new: yes')
    expect(describeCondition({ type: 'SomethingNew', status: 'Unknown' }).tone).toBe('unknown')
  })
})

describe('small helpers', () => {
  it('names things by displayName first', () => {
    expect(titleOf({ metadata: { name: 'orders-api' }, spec: { displayName: 'Orders API' } })).toBe('Orders API')
    expect(titleOf({ metadata: { name: 'orders-api' }, spec: { displayName: '  ' } })).toBe('orders-api')
    expect(titleOf({ metadata: { name: 'orders-api' } })).toBe('orders-api')
  })

  it('names every Container type in words, and passes unknown ones through', () => {
    expect(describeContainerType('static-site')).toBe('static site')
    expect(describeContainerType('cli')).toBe('command-line tool')
    expect(describeContainerType('extension')).toBe('editor extension')
    expect(describeContainerType('operator')).toBe('Kubernetes operator')
    expect(describeContainerType('function')).toBe('serverless function')
    expect(describeContainerType('plugin')).toBe('plugin')
    expect(describeContainerType(undefined)).toBe('unknown type')
  })

  it('has a badge for every phase, including none yet', () => {
    expect(describeContainerPhase('Failed').tone).toBe('error')
    expect(describeContainerPhase(undefined).label).toBe('No status yet')
  })

  it('has a badge for every Crew phase', () => {
    expect(describeCrewPhase('Working')).toMatchObject({ label: 'Working', tone: 'progress' })
    expect(describeCrewPhase('Idle').tone).toBe('ok')
    expect(describeCrewPhase('Paused').tone).toBe('waiting')
    expect(describeCrewPhase('Waiting').explanation).toMatch(/Ready check/)
    expect(describeCrewPhase(undefined).label).toBe('No status yet')
  })

  it('splits identifiers and template refs', () => {
    expect(splitWords('ReferencesResolved')).toBe('References resolved')
    expect(splitTemplateRef('python-fastapi@v1.2.0')).toEqual({ name: 'python-fastapi', version: 'v1.2.0' })
    expect(splitTemplateRef('plain')).toEqual({ name: 'plain' })
  })
})
