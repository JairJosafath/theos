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
    // #43: a new Container waits out the settle window, which is no error.
    const settling = describeCondition({ type: 'Scaffolded', status: 'False', reason: 'Settling' })
    expect(settling.tone).toBe('waiting')
    expect(settling.title).toMatch(/relationships applied with it/)
  })

  it('says plainly when a relationship was added after scaffolding', () => {
    const info = describeCondition({ type: 'Applied', status: 'False', reason: 'AddedAfterScaffold' })
    expect(info.title).toBe('Added after scaffolding, so the repository does not include it')
    expect(info.tone).toBe('error')
  })

  it('says what an UnsupportedCI Container failed on (#1179)', () => {
    const info = describeCondition({ type: 'Scaffolded', status: 'False', reason: 'UnsupportedCI' })
    expect(info.title).toBe('A template has CI files, but none for the Git host’s CI system')
    expect(info.tone).toBe('error')
  })

  // #172: StandardUpdated on a Ready Container whose Standard comes into the repository it adopted
  // as a pull request (#97), with the operator's messages.
  it.each([
    [
      'PullRequestOpen',
      'Pull request https://git.example/shop/api/pulls/12 brings the files of go-standard at v1.2.0 into the repository; a person merges it, or closes it to leave the repository without them. No agent works on the repository until it merges',
      'A pull request brings its Standard’s files into the adopted repository, and waits for a person to merge or close it',
      'waiting',
    ],
    [
      'StandardFilesDiffer',
      'Pull request https://git.example/shop/api/pulls/12 merged, but main holds Makefile with other content than go-standard renders at v1.2.0, and go-standard protects them.',
      'The pull request with its Standard’s files merged, but main holds a file the Standard protects with other content',
      'error',
    ],
    [
      'UpdateClosed',
      'Pull request https://git.example/shop/api/pulls/12 was closed without merging, so the repository is without AGENTS.md, Makefile as go-standard renders them at v1.2.0.',
      'Its Standard’s pull request was closed without merging',
      'error',
    ],
    [
      'UnsupportedFramework',
      'standard "go-standard" supports only the Frameworks it lists, and the Container names no frameworkRef',
      'Its Standard fits only the Frameworks it lists, and this container names none of them',
      'error',
    ],
  ] as const)(
    'says what StandardUpdated=False, %s means on an adopting Container (#172)',
    (reason, message, title, tone) => {
      expect(describeCondition({ type: 'StandardUpdated', status: 'False', reason, message })).toEqual({
        title,
        tone,
        detail: message,
      })
    },
  )

  it('reads the reasons that mean something else on another condition as they read there (#172)', () => {
    // A Standard that doesn't fit at creation fails ChoicesValid, with that condition's sentence.
    expect(describeCondition({ type: 'ChoicesValid', status: 'False', reason: 'UnsupportedFramework' }).title).toBe(
      'One of its templates does not support this type of container',
    )
    // An operations update a person closed has no sentence of its own, as before.
    expect(describeCondition({ type: 'OperationsApplied', status: 'False', reason: 'UpdateClosed' }).title).toBe(
      'Operations applied: no',
    )
  })

  it('reads an ordinary update’s UpdateClosed as a pull request closed without merging too (#172)', () => {
    // The condition is all describeCondition sees, and status.standard, which tells the two apart, is
    // not in it: one sentence, true of both. Red as before; what differs is the page's next step.
    expect(
      describeCondition({
        type: 'StandardUpdated',
        status: 'False',
        reason: 'UpdateClosed',
        message: 'Pull request https://git.example/shop/api/pulls/7 was closed without merging',
      }),
    ).toMatchObject({ title: 'Its Standard’s pull request was closed without merging', tone: 'error' })
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
    // An acronym keeps its capitals, such as a TestEnvironment's CI condition (test-environments, step 8).
    expect(splitWords('CI')).toBe('CI')
    expect(splitWords('CIRunners')).toBe('CI runners')
    expect(splitWords('NoGitProvider')).toBe('No git provider')
    expect(splitTemplateRef('python-fastapi@v1.2.0')).toEqual({ name: 'python-fastapi', version: 'v1.2.0' })
    expect(splitTemplateRef('plain')).toEqual({ name: 'plain' })
  })
})
