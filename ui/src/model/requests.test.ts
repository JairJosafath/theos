import { describe, expect, it } from 'vitest'
import type { Brief, BriefRelease, BriefRequestSource } from '../k8s/types'
import { describeBrief, describeNeedsYou } from './brief'
import {
  answerRequest,
  answerSourceRequest,
  describeRequest,
  describeRequests,
  entryWithRequests,
  httpUrl,
  requestAnswered,
  requestOfName,
  requestsSummary,
  sourceRequestAnswered,
  splitNames,
  splitRequest,
  withRequests,
} from './requests'

const lens: BriefRelease = {
  container: 'cel-lens',
  github: { repository: 'ada/cel-lens', tokenSecretRef: { name: 'gh' } },
  requests: { mode: 'Ask', authors: ['ada'], accepted: [3] },
}
const cli: BriefRelease = {
  container: 'cli',
  github: { repository: 'ada/cel-lens', tokenSecretRef: { name: 'gh' } },
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
  const base = { repository: 'ada/cel-lens', number: 12, author: 'ada', title: 'Dark mode' }
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
    expect(view).toMatchObject({ key: 'ada/cel-lens#12', label: 'ada/cel-lens #12' })
    expect([view.stageLabel, view.tone, view.detail]).toEqual([label, tone, detail])
  })
})

describe('describeRequests', () => {
  it('lists the requests, counts the waiting ones and says the condition', () => {
    const view = describeRequests(
      brief({
        requests: [{ repository: 'ada/cel-lens', number: 12, author: 'a', title: 't', stage: 'Waiting' }],
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
    expect(view.list.map((r) => r.key)).toEqual(['ada/cel-lens#12'])
    expect(view.waiting).toBe(60)
    expect(view.taking).toBe(true)
    expect(view.failure).toEqual({
      reason: 'TokenLacksIssues',
      message: 'Resource not accessible by personal access token',
    })
  })

  it('keeps the settled numbers by repository in lower case', () => {
    const view = describeRequests(brief({ requestsSettled: [{ repository: 'Ada/CEL-lens', numbers: [1, 2] }] }))
    expect(view.settled).toEqual({ 'ada/cel-lens': [1, 2] })
    expect(describeRequests(brief({})).settled).toEqual({})
  })

  it('takes none without an entry whose mode reads issues', () => {
    expect(describeRequests(brief({}, [cli, { ...lens, requests: { mode: 'Off' } }])).taking).toBe(false)
    expect(describeRequests(brief({}, [cli])).list).toEqual([])
  })
})

describe('answerRequest', () => {
  it('adds the number to the accepted or declined of the entry that takes requests', () => {
    expect(answerRequest([cli, lens], 'Ada/CEL-lens', 12, 'accept')).toEqual([
      cli,
      { ...lens, requests: { mode: 'Ask', authors: ['ada'], accepted: [3, 12] } },
    ])
    expect(answerRequest([cli, lens], 'ada/cel-lens', 12, 'decline')).toEqual([
      cli,
      { ...lens, requests: { mode: 'Ask', authors: ['ada'], accepted: [3], declined: [12] } },
    ])
  })

  it('moves a number from one list to the other, never both', () => {
    expect(answerRequest([lens], 'ada/cel-lens', 3, 'decline')).toEqual([
      { ...lens, requests: { mode: 'Ask', authors: ['ada'], declined: [3] } },
    ])
  })

  it('drops the settled numbers, those of closed issues, from both lists', () => {
    const full: BriefRelease = {
      ...lens,
      requests: { mode: 'Ask', authors: ['ada'], accepted: [1, 3], declined: [2, 4] },
    }
    expect(answerRequest([full], 'ada/cel-lens', 12, 'decline', [1, 2, 4])).toEqual([
      { ...lens, requests: { mode: 'Ask', authors: ['ada'], accepted: [3], declined: [12] } },
    ])
  })

  it('answers again once an entry answered 200 requests whose issues closed', () => {
    const answered = Array.from({ length: 200 }, (_, i) => i + 1)
    const full: BriefRelease = { ...lens, requests: { mode: 'Ask', authors: ['ada'], declined: answered } }
    const releases = answerRequest([full], 'ada/cel-lens', 201, 'decline', answered)!
    expect(releases[0].requests!.declined).toEqual([201])
  })

  it('has nothing to answer with when no entry takes requests from the repository', () => {
    expect(answerRequest([cli], 'ada/cel-lens', 12, 'accept')).toBeUndefined()
    expect(answerRequest([lens], 'other/repo', 12, 'accept')).toBeUndefined()
  })
})

describe('requestAnswered', () => {
  it('says what spec.releases answers already', () => {
    const releases = answerRequest([lens], 'ada/cel-lens', 9, 'decline')!
    expect(requestAnswered(releases, 'ada/cel-lens', 3)).toBe('accepted')
    expect(requestAnswered(releases, 'ada/cel-lens', 9)).toBe('declined')
    expect(requestAnswered(releases, 'ada/cel-lens', 12)).toBeUndefined()
  })
})

describe('the settings helpers', () => {
  it('split names on commas and spaces', () => {
    expect(splitNames(' ada, a-colleague  acme/maintainers,,ada')).toEqual(['ada', 'a-colleague', 'acme/maintainers'])
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
          repository: 'ada/cel-lens',
          number: 12,
          author: 'a',
          title: 'Dark mode',
          url: 'https://github.com/ada/cel-lens/issues/12',
          stage: 'Waiting' as const,
        },
      ],
    }
    const view = describeNeedsYou(
      { kind: 'Request', name: 'ada/cel-lens#12', question: 'Request waits for you.' },
      status,
    )
    expect(view).toMatchObject({
      label: 'ada/cel-lens #12',
      request: { repository: 'ada/cel-lens', number: 12 },
      url: 'https://github.com/ada/cel-lens/issues/12',
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

// Requests from a request source (#163): named by their source and key, never by a repository.
const product: BriefRequestSource = {
  name: 'product',
  linear: { team: 'ENG', label: 'factory' },
  credentialsSecretRef: { name: 'product-linear' },
  mode: 'Ask',
  accepted: [409],
  declined: [415],
}
const support: BriefRequestSource = {
  name: 'support',
  jira: { site: 'https://acme.example', cloudID: 'c1', project: 'SHOP', label: 'factory' },
  credentialsSecretRef: { name: 'acme-jira' },
}
const item = { source: 'product', key: 'ENG-412', number: 412, title: 'Dark mode', stage: 'Waiting' as const }

function sourced(status: Brief['status'], requestSources = [product, support]): Brief {
  return {
    apiVersion: 'theos.factory.io/v1beta1',
    kind: 'Brief',
    metadata: { name: 'lens', namespace: 'p', resourceVersion: '7' },
    spec: { idea: 'A lens', requestSources },
    status,
  }
}

describe('a request from a request source (#163)', () => {
  it('reads as its source and key, never as undefined #412', () => {
    const view = describeRequest({ ...item, author: 'Sam Sample' })
    expect(view).toMatchObject({ key: 'product:ENG-412', label: 'product:ENG-412', source: 'product', number: 412 })
    expect(view.repository).toBeUndefined()
    expect(JSON.stringify(view)).not.toContain('undefined')
    expect(view.label).not.toContain('undefined')
  })

  it('has no author when the tracker names nobody, and never reads "undefined"', () => {
    for (const author of [undefined, '', '  ']) {
      const view = describeRequest({ ...item, author })
      expect(view.author).toBeUndefined()
    }
    expect(describeRequest({ ...item, author: ' Sam Sample ' }).author).toBe('Sam Sample')
  })

  it('keeps a GitHub issue’s label as it was', () => {
    const view = describeRequest({
      repository: 'ada/cel-lens',
      number: 12,
      author: 'ada',
      title: 't',
      stage: 'Waiting',
    })
    expect(view).toMatchObject({ key: 'ada/cel-lens#12', label: 'ada/cel-lens #12', author: 'ada' })
    expect(view.source).toBeUndefined()
  })

  it('links only an http or https address', () => {
    expect(describeRequest({ ...item, url: 'https://linear.example/acme/issue/ENG-412' }).url).toBe(
      'https://linear.example/acme/issue/ENG-412',
    )
    expect(describeRequest({ ...item, url: 'javascript:alert(1)' }).url).toBeUndefined()
    expect(describeRequest({ ...item, url: '' }).url).toBeUndefined()
  })

  // Addresses that are no http or https one with a host, such as a tracker's status could carry (#163).
  const notLinks = [
    'javascript:alert(1)',
    'JaVaScRiPt:alert(1)',
    'data:text/html,<b>x</b>',
    'vbscript:msgbox(1)',
    '//evil.example/x',
    ' https://linear.example/x',
    '\thttps://linear.example/x',
    '\u0001https://linear.example/x',
    'http:',
    'http:evil.example',
    'http://',
    'https://',
    'http:///evil.example',
    'https://?x',
    'ftp://linear.example/x',
    '',
  ]

  it('links only an http or https address that has a host, in the list and in Needs you alike (#163)', () => {
    expect(httpUrl(undefined)).toBeUndefined()
    for (const url of [
      'https://linear.example/acme/issue/ENG-412',
      'http://linear.example',
      'HTTPS://Linear.Example/x',
    ]) {
      expect(httpUrl(url)).toBe(url)
      expect(describeRequest({ ...item, url }).url).toBe(url)
      const needs = { kind: 'Request' as const, name: 'product:ENG-412', question: 'q' }
      expect(describeNeedsYou(needs, { requests: [{ ...item, url }] }).url).toBe(url)
    }
    for (const url of notLinks) {
      expect(httpUrl(url), JSON.stringify(url)).toBeUndefined()
      expect(describeRequest({ ...item, url }).url, JSON.stringify(url)).toBeUndefined()
      const needs = { kind: 'Request' as const, name: 'product:ENG-412', question: 'q' }
      expect(describeNeedsYou(needs, { requests: [{ ...item, url }] }).url, JSON.stringify(url)).toBeUndefined()
      // A GitHub request's address is checked the same way.
      const gh = { kind: 'Request' as const, name: 'ada/cel-lens#12', question: 'q' }
      const ghItem = { repository: 'ada/cel-lens', number: 12, title: 't', stage: 'Waiting' as const, url }
      expect(describeNeedsYou(gh, { requests: [ghItem] }).url, JSON.stringify(url)).toBeUndefined()
    }
  })

  it('is listed by the Brief, which takes requests from a source that is not Off', () => {
    const view = describeRequests(
      sourced({ requests: [item], requestSourcesSettled: [{ source: 'product', numbers: [3] }] }),
    )
    expect(view.list.map((r) => r.key)).toEqual(['product:ENG-412'])
    expect([view.taking, view.fromSources]).toEqual([true, true])
    expect(view.settledSources).toEqual({ product: [3] })
    const off = describeRequests(sourced({}, [{ ...product, mode: 'Off' }]))
    expect([off.taking, off.fromSources]).toEqual([false, false])
    // An unset mode is Ask, the CRD's default.
    expect(describeRequests(sourced({}, [support])).taking).toBe(true)
    // A releases entry alone is no source.
    expect(describeRequests(brief({})).fromSources).toBe(false)
  })

  it('is named in Needs you by <source>:<key>, with the number of the listed request', () => {
    const view = describeNeedsYou(
      { kind: 'Request', name: 'product:ENG-412', question: 'Request product:ENG-412 waits for you.' },
      { requests: [{ ...item, number: 412, url: 'https://linear.example/acme/issue/ENG-412' }] },
    )
    expect(view).toMatchObject({
      kind: 'Request',
      label: 'product:ENG-412',
      request: { source: 'product', key: 'ENG-412', number: 412 },
      url: 'https://linear.example/acme/issue/ENG-412',
    })
    expect(view.label).not.toContain('undefined')
  })

  it('takes a number from the key when status lists no such request, and from nowhere else', () => {
    expect(requestOfName('product:ENG-412')).toEqual({ source: 'product', key: 'ENG-412', number: 412 })
    expect(requestOfName('support:SHOP-7', [{ ...item, source: 'product' }])).toEqual({
      source: 'support',
      key: 'SHOP-7',
      number: 7,
    })
    // The listed request's number wins over the digits of the key.
    expect(requestOfName('product:ENG-412', [{ ...item, number: 9 }])).toEqual({
      source: 'product',
      key: 'ENG-412',
      number: 9,
    })
    for (const name of [
      'product:ENG',
      'product:ENG-0',
      'product:',
      ':ENG-1',
      'Product:ENG-1',
      'product ENG-1',
      'cel-lens#x',
    ]) {
      expect(requestOfName(name), name).toBeUndefined()
    }
    expect(requestOfName('ada/cel-lens#12')).toEqual({ repository: 'ada/cel-lens', number: 12 })
  })

  it('is answered by adding its number to the entry’s accepted or declined, and taking it out of the other', () => {
    expect(answerSourceRequest([product, support], 'product', 412, 'accept')).toEqual({
      index: 0,
      entry: product,
      accepted: [409, 412],
      declined: [415],
    })
    expect(answerSourceRequest([product, support], 'product', 412, 'decline')).toEqual({
      index: 0,
      entry: product,
      accepted: [409],
      declined: [415, 412],
    })
    // The other list leaves the answer when it ends up empty; a list the entry has not starts.
    expect(answerSourceRequest([product], 'product', 415, 'accept')).toEqual({
      index: 0,
      entry: product,
      accepted: [409, 415],
    })
    expect(answerSourceRequest([product, support], 'support', 7, 'decline')).toEqual({
      index: 1,
      entry: support,
      declined: [7],
    })
  })

  it('drops the settled numbers from both lists when it answers', () => {
    const full = { ...product, accepted: [1, 2, 3], declined: [4] }
    expect(answerSourceRequest([full], 'product', 9, 'accept', [1, 4])).toEqual({
      index: 0,
      entry: full,
      accepted: [2, 3, 9],
    })
  })

  it('is answered by no entry that is Off, absent or named otherwise', () => {
    expect(answerSourceRequest([{ ...product, mode: 'Off' }], 'product', 412, 'accept')).toBeUndefined()
    expect(answerSourceRequest([product], 'support', 412, 'accept')).toBeUndefined()
    expect(answerSourceRequest([product], 'Product', 412, 'accept')).toBeUndefined()
    expect(answerSourceRequest([], 'product', 412, 'accept')).toBeUndefined()
  })

  it('says answered once the entry has the number', () => {
    expect(sourceRequestAnswered([product], 'product', 409)).toBe('accepted')
    expect(sourceRequestAnswered([product], 'product', 415)).toBe('declined')
    expect(sourceRequestAnswered([product], 'product', 412)).toBeUndefined()
    expect(sourceRequestAnswered([support], 'support', 409)).toBeUndefined()
  })

  it('carries the entries the buttons answer', () => {
    expect(describeBrief(sourced({})).requestSources).toEqual([product, support])
    expect(describeBrief(brief({})).requestSources).toEqual([])
  })
})
