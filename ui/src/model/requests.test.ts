import { describe, expect, it } from 'vitest'
import type { Brief, BriefRelease } from '../k8s/types'
import { describeBrief, describeNeedsYou } from './brief'
import {
  answerRequest,
  describeRequest,
  describeRequests,
  entryWithRequests,
  requestAnswered,
  requestsSummary,
  splitNames,
  splitRequest,
  withRequests,
} from './requests'

const lens: BriefRelease = {
  container: 'cel-lens',
  github: { repository: 'gray-lenovo/cel-lens', tokenSecretRef: { name: 'gh' } },
  requests: { mode: 'Ask', authors: ['gray-lenovo'], accepted: [3] },
}
const cli: BriefRelease = {
  container: 'cli',
  github: { repository: 'gray-lenovo/cel-lens', tokenSecretRef: { name: 'gh' } },
  tagPrefix: 'cli-',
}

function brief(status: Brief['status'], releases: BriefRelease[] = [lens]): Brief {
  return {
    apiVersion: 'theos.factory.io/v1beta1',
    kind: 'Brief',
    metadata: { name: 'lens', namespace: 'p', resourceVersion: '7' },
    spec: { idea: 'A lens', releases },
    status,
  }
}

describe('describeRequest', () => {
  const base = { repository: 'gray-lenovo/cel-lens', number: 12, author: 'gray-lenovo', title: 'Dark mode' }
  it.each([
    [{ stage: 'Waiting' as const }, 'Waits for you', 'waiting', 'Waits for you to accept or decline it.'],
    [{ stage: 'Accepted' as const }, 'Accepted', 'waiting', 'Accepted; it waits for a round to plan it.'],
    [{ stage: 'Planning' as const, round: 4 }, 'Planning', 'progress', 'Round 4 is planning it.'],
    [
      { stage: 'Planned' as const, round: 4, issues: ['cel-lens#7', 'cel-lens#8'], release: 'v0.3.0' },
      'Planned',
      'ok',
      'Round 4 planned it as cel-lens#7, cel-lens#8, in release v0.3.0.',
    ],
    [
      { stage: 'Skipped' as const, round: 4, why: 'It is there already.' },
      'Skipped',
      'unknown',
      'Round 4 skipped it: It is there already.',
    ],
    [{ stage: 'Declined' as const }, 'Declined', 'unknown', 'Declined; the Brief says so on the issue and closes it.'],
    [
      { stage: 'Released' as const, release: 'v0.3.0' },
      'Released',
      'ok',
      'Shipped in v0.3.0; the Brief says so on the issue and closes it.',
    ],
  ])('%o reads as its stage', (more, label, tone, detail) => {
    const view = describeRequest({ ...base, ...more })
    expect(view).toMatchObject({ key: 'gray-lenovo/cel-lens#12', label: 'gray-lenovo/cel-lens #12' })
    expect([view.stageLabel, view.tone, view.detail]).toEqual([label, tone, detail])
  })
})

describe('describeRequests', () => {
  it('lists the requests, counts the waiting ones and says the condition', () => {
    const view = describeRequests(
      brief({
        requests: [{ repository: 'gray-lenovo/cel-lens', number: 12, author: 'a', title: 't', stage: 'Waiting' }],
        requestsWaiting: 60,
        conditions: [
          {
            type: 'Requests',
            status: 'False',
            reason: 'TokenLacksIssues',
            message: 'Resource not accessible by personal access token',
            lastTransitionTime: '2026-10-09T00:00:00Z',
          },
        ],
      }),
    )
    expect(view.list.map((r) => r.key)).toEqual(['gray-lenovo/cel-lens#12'])
    expect(view.waiting).toBe(60)
    expect(view.taking).toBe(true)
    expect(view.failure).toEqual({
      reason: 'TokenLacksIssues',
      message: 'Resource not accessible by personal access token',
    })
  })

  it('keeps the settled numbers by repository in lower case', () => {
    const view = describeRequests(brief({ requestsSettled: [{ repository: 'Gray-Lenovo/CEL-lens', numbers: [1, 2] }] }))
    expect(view.settled).toEqual({ 'gray-lenovo/cel-lens': [1, 2] })
    expect(describeRequests(brief({})).settled).toEqual({})
  })

  it('takes none without an entry whose mode reads issues', () => {
    expect(describeRequests(brief({}, [cli, { ...lens, requests: { mode: 'Off' } }])).taking).toBe(false)
    expect(describeRequests(brief({}, [cli])).list).toEqual([])
  })
})

describe('answerRequest', () => {
  it('adds the number to the accepted or declined of the entry that takes requests', () => {
    expect(answerRequest([cli, lens], 'Gray-Lenovo/CEL-lens', 12, 'accept')).toEqual([
      cli,
      { ...lens, requests: { mode: 'Ask', authors: ['gray-lenovo'], accepted: [3, 12] } },
    ])
    expect(answerRequest([cli, lens], 'gray-lenovo/cel-lens', 12, 'decline')).toEqual([
      cli,
      { ...lens, requests: { mode: 'Ask', authors: ['gray-lenovo'], accepted: [3], declined: [12] } },
    ])
  })

  it('moves a number from one list to the other, never both', () => {
    expect(answerRequest([lens], 'gray-lenovo/cel-lens', 3, 'decline')).toEqual([
      { ...lens, requests: { mode: 'Ask', authors: ['gray-lenovo'], declined: [3] } },
    ])
  })

  it('drops the settled numbers, those of closed issues, from both lists', () => {
    const full: BriefRelease = {
      ...lens,
      requests: { mode: 'Ask', authors: ['gray-lenovo'], accepted: [1, 3], declined: [2, 4] },
    }
    expect(answerRequest([full], 'gray-lenovo/cel-lens', 12, 'decline', [1, 2, 4])).toEqual([
      { ...lens, requests: { mode: 'Ask', authors: ['gray-lenovo'], accepted: [3], declined: [12] } },
    ])
  })

  it('answers again once an entry answered 200 requests whose issues closed', () => {
    const answered = Array.from({ length: 200 }, (_, i) => i + 1)
    const full: BriefRelease = { ...lens, requests: { mode: 'Ask', authors: ['gray-lenovo'], declined: answered } }
    const releases = answerRequest([full], 'gray-lenovo/cel-lens', 201, 'decline', answered)!
    expect(releases[0].requests!.declined).toEqual([201])
  })

  it('has nothing to answer with when no entry takes requests from the repository', () => {
    expect(answerRequest([cli], 'gray-lenovo/cel-lens', 12, 'accept')).toBeUndefined()
    expect(answerRequest([lens], 'other/repo', 12, 'accept')).toBeUndefined()
  })
})

describe('requestAnswered', () => {
  it('says what spec.releases answers already', () => {
    const releases = answerRequest([lens], 'gray-lenovo/cel-lens', 9, 'decline')!
    expect(requestAnswered(releases, 'gray-lenovo/cel-lens', 3)).toBe('accepted')
    expect(requestAnswered(releases, 'gray-lenovo/cel-lens', 9)).toBe('declined')
    expect(requestAnswered(releases, 'gray-lenovo/cel-lens', 12)).toBeUndefined()
  })
})

describe('the settings helpers', () => {
  it('split names on commas and spaces', () => {
    expect(splitNames(' gray-lenovo, a-colleague  gray-org/maintainers,,gray-lenovo')).toEqual([
      'gray-lenovo',
      'a-colleague',
      'gray-org/maintainers',
    ])
    expect(splitNames('  ')).toEqual([])
  })

  it('keep the numbers already answered, and drop requests that say nothing', () => {
    expect(withRequests(lens.requests, 'On', ['b'], ['o/t'])).toEqual({
      mode: 'On',
      authors: ['b'],
      teams: ['o/t'],
      accepted: [3],
    })
    expect(withRequests(undefined, 'Off', [], [])).toBeUndefined()
    // Off with authors kept says what the person typed; the CRD takes it.
    expect(withRequests(undefined, 'Off', ['b'], [])).toEqual({ mode: 'Off', authors: ['b'] })
    // Ask with nobody is sent as typed, so the API server refuses it with its message.
    expect(withRequests(undefined, 'Ask', [], [])).toEqual({ mode: 'Ask' })
  })

  it('set or drop an entry’s requests', () => {
    expect(entryWithRequests(lens, undefined)).toEqual({ container: 'cel-lens', github: lens.github })
    expect(entryWithRequests(cli, { mode: 'On', authors: ['b'] }).requests).toEqual({ mode: 'On', authors: ['b'] })
  })

  it('sum an entry’s requests up in a line', () => {
    expect(requestsSummary(undefined)).toBe('takes no requests')
    expect(requestsSummary({ mode: 'Off', authors: ['a'] })).toBe('takes no requests')
    expect(requestsSummary({ mode: 'Ask', authors: ['a', 'b'], teams: ['o/t'] })).toBe(
      'requests Ask, from a, b and o/t',
    )
  })
})

describe('a request under Needs you', () => {
  it('names its issue and links it', () => {
    const status = {
      requests: [
        {
          repository: 'gray-lenovo/cel-lens',
          number: 12,
          author: 'a',
          title: 'Dark mode',
          url: 'https://github.com/gray-lenovo/cel-lens/issues/12',
          stage: 'Waiting' as const,
        },
      ],
    }
    const view = describeNeedsYou(
      { kind: 'Request', name: 'gray-lenovo/cel-lens#12', question: 'Request waits for you.' },
      status,
    )
    expect(view).toMatchObject({
      label: 'gray-lenovo/cel-lens #12',
      request: { repository: 'gray-lenovo/cel-lens', number: 12 },
      url: 'https://github.com/gray-lenovo/cel-lens/issues/12',
    })
    expect(view.issue).toBeUndefined()
    expect(splitRequest('cel-lens#12')).toBeUndefined()
  })

  it('carries the releases list and resourceVersion the buttons send', () => {
    const view = describeBrief(brief({}))
    expect(view.releases).toEqual([lens])
    expect(view.resourceVersion).toBe('7')
  })
})
