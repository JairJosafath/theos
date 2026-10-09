import { describe, expect, it } from 'vitest'
import type { Brief, BriefApp, BriefFailedKind, BriefPhase } from '../k8s/types'
import {
  describeAutopilot,
  describeBrief,
  describeBriefApp,
  describeBriefPhase,
  describeCost,
  describeFailedIssue,
  describeUsage,
  formatCost,
  formatTokens,
  pauseAction,
  sortBriefs,
} from './brief'

function brief(name: string, created: string, extra: Partial<Brief> = {}): Brief {
  return { metadata: { name, creationTimestamp: created }, spec: { idea: `Idea ${name}` }, ...extra }
}

/** A Brief whose autopilot is Off: failed issues wait for a prompt. */
function off(name: string, created: string): Brief {
  return brief(name, created, { spec: { idea: `Idea ${name}`, autopilot: 'Off' } })
}

describe('describeBriefPhase', () => {
  it.each<[BriefPhase | undefined, string, string]>([
    ['Pending', 'Pending', 'waiting'],
    ['Planning', 'Planning', 'progress'],
    ['Applying', 'Applying', 'progress'],
    ['Planned', 'Planned', 'ok'],
    ['NeedsInput', 'Needs your input', 'waiting'],
    ['Failed', 'Failed', 'error'],
    [undefined, 'No status yet', 'unknown'],
  ])('%s', (phase, label, tone) => {
    const info = describeBriefPhase(phase)
    expect(info).toMatchObject({ label, tone })
    expect(info.explanation).not.toBe('')
  })
})

describe('describeBrief', () => {
  it('splits the prompts into planned and waiting', () => {
    const view = describeBrief({
      ...brief('shop', '2026-01-01T00:00:00Z'),
      spec: { idea: 'A shop', prompts: ['Use Stripe', 'Add gift cards', 'Ship to Belgium'] },
      status: { phase: 'Planned', round: 2, promptsPlanned: 1, costMilliUSD: 1234 },
    })
    expect(view.prompts).toEqual([
      { text: 'Use Stripe', planned: true },
      { text: 'Add gift cards', planned: false },
      { text: 'Ship to Belgium', planned: false },
    ])
    expect(view).toMatchObject({ waitingPrompts: 2, round: 2, needsInput: undefined })
    expect(view.usage?.line).toBe('$1.23')
  })

  it('lists the reviewer suggestions and how many wait in all', () => {
    const view = describeBrief({
      ...brief('shop', ''),
      status: {
        phase: 'Planned',
        suggestions: [{ container: 'api', number: 5, title: 'Escape input', url: 'http://g/5', criteria: true }],
        suggestionsWaiting: 60,
      },
    })
    expect(view.suggestions).toEqual([{ container: 'api', number: 5, title: 'Escape input', url: 'http://g/5' }])
    expect(view.suggestionsWaiting).toBe(60)
    expect(view.phase.explanation).toBe(
      'The last round is done; 60 reviewer suggestions wait for your answer in a prompt.',
    )
  })

  it('counts the listed suggestions when no total is given, and has none by default', () => {
    const some = describeBrief({
      ...brief('shop', ''),
      status: { phase: 'Planned', suggestions: [{ container: 'api', number: 5 }] },
    })
    expect(some.suggestionsWaiting).toBe(1)
    expect(some.phase.explanation).toContain('1 reviewer suggestion waits')
    const none = describeBrief({ ...brief('shop', ''), status: { phase: 'Planned' } })
    expect(none).toMatchObject({ suggestions: [], suggestionsWaiting: 0 })
    expect(none.phase.explanation).toBe('The last round is done and no prompt is waiting.')
  })

  it('lists the failed issues with their reasons and marks them in the issue list', () => {
    const view = describeBrief({
      ...off('lens', ''),
      status: {
        phase: 'Planned',
        issues: [
          { repository: 'lens/cel-lens-cli', number: 1, url: 'http://g/cli/1', title: 'CLI' },
          { repository: 'lens/cel-lens-core', number: 1, title: 'Checker' },
          { repository: 'lens/cel-lens-core', number: 2, title: 'Parser' },
        ],
        failed: [
          {
            container: 'cel-lens-cli',
            number: 1,
            url: 'http://g/cli/1',
            title: 'CLI',
            kind: 'failed',
            reason: 'Task cel-lens-cli-1 failed: the agent made no changes',
            tries: 1,
          },
          { container: 'cel-lens-core', number: 2, kind: 'exhausted', reason: '', tries: 3 },
        ],
        failedWaiting: 2,
      },
    })
    expect(view.failed).toEqual([
      {
        container: 'cel-lens-cli',
        number: 1,
        url: 'http://g/cli/1',
        title: 'CLI',
        kind: 'failed',
        reason: 'Task cel-lens-cli-1 failed: the agent made no changes',
        tries: 1,
        note: undefined,
      },
      {
        container: 'cel-lens-core',
        number: 2,
        url: undefined,
        title: undefined,
        kind: 'exhausted',
        reason: 'Agents tried it as often as a Crew does.',
        tries: 3,
        note: 'It won’t be tried again as it is: it will be filed again, reworded or split.',
      },
    ])
    expect(view.failedWaiting).toBe(2)
    expect(view.issues.map((i) => i.failed)).toEqual([true, false, true])
    expect(view.phase.explanation).toBe('The last round is done; 2 failed issues wait for your answer in a prompt.')
  })

  it('counts failed issues and suggestions together, and the listed ones when no total is given', () => {
    const view = describeBrief({
      ...off('lens', ''),
      status: {
        phase: 'Planned',
        failed: [{ container: 'api', number: 3, kind: 'merged' }],
        suggestionsWaiting: 2,
      },
    })
    expect(view.failedWaiting).toBe(1)
    expect(view.failed[0]).toMatchObject({ reason: 'A Task merged it already.', tries: 0 })
    expect(view.failed[0].note).toContain('closed')
    expect(view.phase.explanation).toBe(
      'The last round is done; 1 failed issue and 2 reviewer suggestions wait for your answer in a prompt.',
    )
    const one = describeBrief({
      ...off('lens', ''),
      status: { phase: 'Planned', failed: [{ container: 'api', number: 3, kind: 'person' }] },
    })
    expect(one.phase.explanation).toBe('The last round is done; 1 failed issue waits for your answer in a prompt.')
  })

  it('has no failed issues by default and marks none', () => {
    const none = describeBrief({
      ...brief('lens', ''),
      status: { phase: 'Planned', issues: [{ repository: 'lens/api', number: 1 }] },
    })
    expect(none).toMatchObject({ failed: [], failedWaiting: 0 })
    expect(none.issues[0].failed).toBe(false)
  })

  it.each<[BriefFailedKind, string]>([
    ['unclear', 'It has no acceptance criteria.'],
    ['merged', 'A Task merged it already.'],
    ['exhausted', 'Agents tried it as often as a Crew does.'],
    ['failed', 'Its last Task failed.'],
    ['person', 'Handed back by a person; see the issue.'],
    ['byHand', 'Needs a person; see the issue.'],
  ])('gives a %s issue a reason when the operator gave none', (kind, reason) => {
    expect(describeFailedIssue({ container: 'api', number: 1, kind, reason: '  ' }).reason).toBe(reason)
  })

  it('takes the NeedsInput sentence only while it is True', () => {
    const needs = (status: 'True' | 'False') =>
      describeBrief({
        ...brief('shop', ''),
        status: {
          phase: status === 'True' ? 'NeedsInput' : 'Planned',
          conditions: [
            { type: 'NeedsInput', status, reason: 'AgentMissing', message: 'Agent designer does not exist.' },
          ],
        },
      }).needsInput
    expect(needs('True')).toBe('Agent designer does not exist.')
    expect(needs('False')).toBeUndefined()
  })

  it('says why a failed round failed, from the Planned condition', () => {
    const view = describeBrief({
      ...brief('shop', ''),
      status: { phase: 'Failed', conditions: [{ type: 'Planned', status: 'False', message: 'The plan is invalid.' }] },
    })
    expect(view.failure).toBe('The plan is invalid.')
  })

  it('says what still runs while paused, from the Paused condition, and nothing once resumed', () => {
    const view = (paused: boolean, status?: 'True' | 'False', message = 'Paused; 1 Task still running: shop-api-4') =>
      describeBrief({
        ...brief('shop', ''),
        spec: { idea: 'Idea', paused },
        status: { conditions: status ? [{ type: 'Paused', status, reason: 'Paused', message }] : [] },
      })
    expect(view(true, 'True')).toMatchObject({ paused: true, pausedNote: 'Paused; 1 Task still running: shop-api-4' })
    expect(view(true, 'True', 'Paused; nothing is running').pausedNote).toBe('Paused; nothing is running')
    // Just paused: the operator has not set the condition yet, or still says Not paused.
    expect(view(true).pausedNote).toBe('Pausing; the factory has not said yet what still runs.')
    expect(view(true, 'False', 'Not paused').pausedNote).toBe('Pausing; the factory has not said yet what still runs.')
    expect(view(false, 'False', 'Not paused')).toMatchObject({ paused: false, pausedNote: undefined })
    expect(view(false, 'True')).toMatchObject({ paused: false, pausedNote: undefined })
  })

  it('copes with a Brief the factory has not reported on', () => {
    const view = describeBrief(brief('new', ''))
    expect(view).toMatchObject({
      phase: { label: 'No status yet' },
      prompts: [],
      choices: [],
      created: [],
      issues: [],
      round: 0,
      paused: false,
      pausedNote: undefined,
      usage: undefined,
      rounds: [],
    })
  })
})

describe('pauseAction', () => {
  it('pauses a running Brief and resumes a paused one', () => {
    expect(pauseAction({ paused: false })).toMatchObject({ label: 'Pause', paused: true })
    expect(pauseAction({ paused: true })).toMatchObject({ label: 'Resume', paused: false })
  })
})

describe('formatCost', () => {
  it.each([
    [0, '$0.00'],
    [5, '<$0.01'],
    [10, '$0.01'],
    [420, '$0.42'],
    [12_345, '$12.35'],
  ])('%d', (milli, want) => {
    expect(formatCost(milli)).toBe(want)
  })
})

describe('sortBriefs', () => {
  it('puts the newest first, then by name', () => {
    const sorted = sortBriefs([
      brief('old', '2026-01-01T00:00:00Z'),
      brief('b', '2026-02-01T00:00:00Z'),
      brief('a', '2026-02-01T00:00:00Z'),
      brief('pending', ''),
    ])
    expect(sorted.map((b) => b.metadata.name)).toEqual(['a', 'b', 'old', 'pending'])
  })
})

describe('describeCost', () => {
  it.each([
    [undefined, undefined, undefined],
    [1234, 0, '$1.23'],
    [0, 2, 'cost not reported by the agent'],
    [1234, 1, '$1.23, plus 1 run whose cost the agent didn’t report'],
    [1234, 3, '$1.23, plus 3 runs whose cost the agent didn’t report'],
  ])('%s with %s runs not reported is %s', (milli, without, want) => {
    expect(describeCost(milli, without)).toBe(want)
  })

  it('says on the Brief when the agent reported no cost', () => {
    const view = describeBrief({
      ...brief('shop', '2026-01-01T00:00:00Z'),
      status: { phase: 'Planned', round: 1, runsWithoutCost: 1 },
    })
    expect(view.usage?.line).toBe('cost not reported by the agent')
  })
})

describe('formatTokens', () => {
  it.each([
    [0, '0'],
    [950, '950'],
    [1_000, '1k'],
    [9_500, '9.5k'],
    [9_960, '10k'],
    [142_000, '142k'],
    [999_499, '999k'],
    [999_500, '1M'],
    [1_400_000, '1.4M'],
    [12_300_000, '12M'],
  ])('%d', (n, want) => {
    expect(formatTokens(n)).toBe(want)
  })
})

describe('describeUsage', () => {
  it('shows the cost, the tokens with their cache reads, and the largest agent run', () => {
    const usage = describeUsage(6_120, 0, {
      input: 40_000,
      output: 160_000,
      cacheRead: 1_100_000,
      cacheWrite: 100_000,
      largestSession: 112_000,
    })
    expect(usage).toEqual({
      cost: '$6.12',
      tokens: '1.4M tokens, 1.1M of them cache reads',
      largestRun: 'largest run 112k',
      pastLimit: false,
      line: '$6.12 · 1.4M tokens, 1.1M of them cache reads · largest run 112k',
    })
  })

  it('marks a largest agent run past the Agent’s limit, and none when the limit is unknown', () => {
    const usage = describeUsage(6_120, 0, { output: 1_400_000, largestSession: 142_000 }, 120_000)
    expect(usage?.pastLimit).toBe(true)
    expect(usage?.largestRun).toBe('largest run 142k, past the 120k limit')
    expect(describeUsage(6_120, 0, { output: 1_400_000, largestSession: 120_000 }, 120_000)?.pastLimit).toBe(false)
    expect(describeUsage(6_120, 0, { output: 1_400_000, largestSession: 142_000 }, 200_000)?.pastLimit).toBe(false)
    expect(describeUsage(6_120, 0, { output: 1_400_000, largestSession: 942_000 })).toMatchObject({
      pastLimit: false,
      largestRun: 'largest run 942k',
    })
  })

  it('says how often an agent run handed off, and nothing when none did', () => {
    const usage = describeUsage(6_120, 0, { output: 1_400_000, largestSession: 142_000, sessionHandoffs: 2 }, 120_000)
    expect(usage?.handoffs).toBe('handed off 2×')
    expect(usage?.line).toBe('$6.12 · 1.4M tokens · largest run 142k, past the 120k limit · handed off 2×')
    const none = describeUsage(6_120, 0, { output: 1_400_000, largestSession: 112_000, sessionHandoffs: 0 })
    expect(none?.handoffs).toBeUndefined()
    expect(none?.line).toBe('$6.12 · 1.4M tokens · largest run 112k')
  })

  it.each([
    ['no run yet', 0, 0, {}, undefined],
    ['a run before the operator counted tokens', 420, 0, undefined, '$0.42'],
    ['every run without tokens', 420, 0, { runsWithout: 2 }, '$0.42 · tokens not reported by the agent'],
    [
      'some runs without tokens',
      420,
      0,
      { output: 2_000, runsWithout: 1 },
      '$0.42 · 2k tokens, plus 1 run whose tokens the agent didn’t report',
    ],
    ['runs without cost or tokens', 0, 2, { runsWithout: 2 }, 'cost and tokens not reported by the agent'],
  ])('%s', (_, milli, without, tokens, want) => {
    expect(describeUsage(milli, without, tokens)?.line).toBe(want)
  })

  it('shows each round of a Brief, newest first, with what its planner used', () => {
    const view = describeBrief({
      ...brief('shop', '2026-01-01T00:00:00Z'),
      status: {
        phase: 'NeedsInput',
        round: 2,
        history: [
          {
            round: 1,
            summary: ' Planned the shop ',
            outcome: 'Applied',
            costMilliUSD: 312,
            tokens: { cacheRead: 486_000, output: 14_000, largestSession: 150_000 },
          },
          { round: 2, outcome: 'PlannerNeedsInput', tokens: { runsWithout: 1, sessionHandoffs: 1 } },
        ],
      },
    })
    expect(view.rounds).toEqual([
      {
        round: 2,
        summary: undefined,
        outcome: 'Asked you',
        usage: {
          cost: undefined,
          tokens: 'tokens not reported by the agent',
          largestRun: undefined,
          pastLimit: false,
          handoffs: 'handed off 1×',
          line: 'tokens not reported by the agent · handed off 1×',
        },
      },
      {
        round: 1,
        summary: 'Planned the shop',
        outcome: 'Applied',
        usage: expect.objectContaining({
          line: '$0.31 · 500k tokens, 486k of them cache reads · largest run 150k',
          pastLimit: false,
          handoffs: undefined,
        }),
      },
    ])
  })
})

describe('describeBriefApp', () => {
  const released: BriefApp = {
    container: 'api',
    crew: 'api',
    releases: 'On',
    publishTo: 'acme/api',
    lastRelease: {
      tag: 'v1.0.0',
      url: 'http://g/acme/api/releases/tag/v1.0.0',
      time: '2026-01-01T00:00:00Z',
      run: 'Succeeded',
      publishState: 'Published',
      publishedURL: 'https://github.com/acme/api/releases/tag/v1.0.0',
    },
    message: 'Released v1.0.0 and published it to acme/api.',
  }

  it('links a released and published app to both releases', () => {
    expect(describeBriefApp(released)).toEqual({
      container: 'api',
      releasesOn: true,
      label: 'Releases on',
      tone: 'ok',
      message: 'Released v1.0.0 and published it to acme/api.',
      release: { tag: 'v1.0.0', url: 'http://g/acme/api/releases/tag/v1.0.0', time: '2026-01-01T00:00:00Z' },
      publishTo: 'acme/api',
      publishedURL: 'https://github.com/acme/api/releases/tag/v1.0.0',
    })
  })

  it('shows an app with releases off, muted, with its message', () => {
    const view = describeBriefApp({ container: 'web', crew: 'web', releases: 'Off', message: 'Releases are off.' })
    expect(view).toMatchObject({
      releasesOn: false,
      label: 'Releases off',
      tone: 'unknown',
      message: 'Releases are off.',
    })
    expect(view.release).toBeUndefined()
    expect(view.publishTo).toBeUndefined()
  })

  it.each<[string, Partial<NonNullable<BriefApp['lastRelease']>> | undefined, string]>([
    ['no release yet', undefined, 'waiting'],
    ['CI running', { run: 'Running', publishState: undefined }, 'progress'],
    ['CI failed', { run: 'Failed', publishState: undefined }, 'error'],
    ['publishing', { publishState: 'Publishing', publishedURL: undefined }, 'progress'],
    ['publish failed', { publishState: 'Failed', publishedURL: undefined }, 'error'],
    ['released, not published', { publishState: undefined, publishedURL: undefined }, 'ok'],
  ])('tones %s as %s', (_, change, tone) => {
    const lastRelease = change && { ...released.lastRelease!, ...change }
    expect(describeBriefApp({ ...released, lastRelease }).tone).toBe(tone)
  })

  it('links the published release only once it is published', () => {
    const view = describeBriefApp({
      ...released,
      lastRelease: { ...released.lastRelease!, publishState: 'Publishing' },
    })
    expect(view.publishedURL).toBeUndefined()
    expect(view.publishTo).toBe('acme/api')
  })

  it('is listed on the Brief, in order', () => {
    const view = describeBrief({
      ...brief('shop', '2026-01-01T00:00:00Z'),
      status: { apps: [released, { container: 'web', releases: 'Off' }] },
    })
    expect(view.apps.map((a) => [a.container, a.label, a.message])).toEqual([
      ['api', 'Releases on', 'Released v1.0.0 and published it to acme/api.'],
      ['web', 'Releases off', 'Releases are off.'],
    ])
    expect(describeBrief(brief('none', '2026-01-01T00:00:00Z')).apps).toEqual([])
  })
})

describe('the Needs you box', () => {
  const needsInput = {
    type: 'NeedsInput',
    status: 'True' as const,
    reason: 'AgentNotFound',
    message: 'Agent planner is missing: create it.',
  }

  it('lists what the Brief waits for first, then each item with its recommendation and why', () => {
    const view = describeBrief({
      ...brief('lens', '2026-01-01T00:00:00Z'),
      spec: { idea: 'A lens', autopilot: 'Ask' },
      status: {
        phase: 'NeedsInput',
        failed: [{ container: 'cli', number: 3, url: 'http://git/cli/issues/3', kind: 'failed' }],
        needsYou: [
          {
            kind: 'Issue',
            name: 'cli#3',
            question: 'Its Task failed twice on the checker. ',
            recommend: 'Try it again once core#8 merges.',
            why: 'core#8 adds the checker.',
            since: '2026-01-02T00:00:00Z',
          },
          {
            kind: 'Task',
            name: 'core-8',
            question: 'It has waited 26h for Standard change #27.',
            recommend: 'Review or close it.',
          },
        ],
        needsYouWaiting: 4,
        conditions: [needsInput],
      },
    })
    expect(view.needsYou).toEqual([
      { kind: 'Input', label: 'The Brief', question: 'Agent planner is missing: create it.' },
      {
        kind: 'Issue',
        label: 'cli #3',
        url: 'http://git/cli/issues/3',
        question: 'Its Task failed twice on the checker.',
        recommend: 'Try it again once core#8 merges.',
        why: 'core#8 adds the checker.',
        since: '2026-01-02T00:00:00Z',
      },
      {
        kind: 'Task',
        label: 'Task core-8',
        url: undefined,
        question: 'It has waited 26h for Standard change #27.',
        recommend: 'Review or close it.',
        why: undefined,
        since: undefined,
      },
    ])
    // The four the operator counts, and the Brief's own input.
    expect(view.needsYouWaiting).toBe(5)
    expect(view.needsInput).toBe('Agent planner is missing: create it.')
  })

  it('is empty when nothing needs the person', () => {
    const view = describeBrief({ ...brief('lens', '2026-01-01T00:00:00Z'), status: { phase: 'Planned' } })
    expect(view.needsYou).toEqual([])
    expect(view.needsYouWaiting).toBe(0)
  })

  it('holds NeedsInput alone while autopilot is off', () => {
    const view = describeBrief({
      ...off('lens', '2026-01-01T00:00:00Z'),
      status: { phase: 'NeedsInput', conditions: [needsInput] },
    })
    expect(view.needsYou.map((n) => n.kind)).toEqual(['Input'])
    expect(view.needsYouWaiting).toBe(1)
  })

  it('counts the items in the phase explanation', () => {
    const view = describeBrief({
      ...off('lens', '2026-01-01T00:00:00Z'),
      status: {
        phase: 'Planned',
        needsYou: [{ kind: 'Crew', name: 'cli', question: 'Its Agent is missing.' }],
        failedWaiting: 2,
      },
    })
    expect(view.phase.explanation).toBe(
      'The last round is done; 1 item under Needs you and 2 failed issues wait for your answer in a prompt.',
    )
  })

  it('doesn’t count a failed issue twice once the Brief tends them', () => {
    const view = describeBrief({
      ...brief('lens', '2026-01-01T00:00:00Z'),
      spec: { idea: 'A lens', autopilot: 'Ask' },
      status: {
        phase: 'Planned',
        failed: [{ container: 'cli', number: 3, kind: 'failed' }],
        failedWaiting: 1,
        needsYou: [{ kind: 'Issue', name: 'cli#3', question: 'Issue cli#3 needs you.' }],
      },
    })
    expect(view.phase.explanation).toBe(
      'The last round is done; 1 item under Needs you waits for your answer in a prompt.',
    )
  })

  it('with the factory’s default mode, counts a failed issue as waiting unless Needs you lists it', () => {
    const view = describeBrief({
      ...brief('lens', '2026-01-01T00:00:00Z'),
      spec: { idea: 'A lens' },
      status: {
        phase: 'Planned',
        failed: [
          { container: 'cli', number: 3, kind: 'failed' },
          { container: 'cli', number: 4, kind: 'failed' },
        ],
        failedWaiting: 2,
        needsYou: [{ kind: 'Issue', name: 'cli#3', question: 'Issue cli#3 needs you.' }],
      },
    })
    expect(view.phase.explanation).toBe(
      'The last round is done; 1 item under Needs you and 1 failed issue wait for your answer in a prompt.',
    )
  })
})

describe('what the Brief did by itself', () => {
  it('lists its tending rounds newest first, with each answer and why', () => {
    const view = describeBrief({
      ...brief('lens', '2026-01-01T00:00:00Z'),
      spec: { idea: 'A lens', autopilot: 'On' },
      status: {
        failed: [{ container: 'cli', number: 1, url: 'http://git/cli/issues/1', kind: 'failed' }],
        history: [
          { round: 1, outcome: 'Applied', summary: 'Planned the lens' },
          { round: 2, outcome: 'Tended', summary: 'Retried the CLI issue ' },
          { round: 3, outcome: 'Applied' },
          { round: 4, outcome: 'Tended' },
        ],
        tended: [
          { container: 'cli', number: 1, answer: 'retry', why: 'The checker is merged now.', at: 'x', round: 2 },
          { container: 'core', number: 2, answer: 'close', at: 'x', round: 2 },
          {
            container: 'core',
            number: 5,
            answer: 'ask',
            why: 'It needs a paid API key.',
            recommend: { answer: 'close' },
            at: 'x',
            round: 4,
          },
          { container: 'core', number: 6, answer: 'wait', why: 'No engine release yet.', at: 'x', round: 4 },
        ],
      },
    })
    expect(view.tended).toEqual([
      {
        round: 4,
        summary: undefined,
        answers: [
          { label: 'core #5', url: undefined, text: 'Asked you; it would close it: It needs a paid API key.' },
          { label: 'core #6', url: undefined, text: 'Waits: No engine release yet.' },
        ],
      },
      {
        round: 2,
        summary: 'Retried the CLI issue',
        answers: [
          { label: 'cli #1', url: 'http://git/cli/issues/1', text: 'Tried again: The checker is merged now.' },
          { label: 'core #2', url: undefined, text: 'Closed' },
        ],
      },
    ])
  })

  it('is empty before any tending round', () => {
    expect(describeBrief(brief('lens', '2026-01-01T00:00:00Z')).tended).toEqual([])
  })
})

describe('describeAutopilot', () => {
  it.each<['Off' | 'Ask' | 'On', string]>([
    ['Off', 'Autopilot off'],
    ['Ask', 'Autopilot asks'],
    ['On', 'Autopilot on'],
  ])('%s', (mode, label) => {
    expect(describeAutopilot(mode)).toMatchObject({ mode, isDefault: false, label })
  })

  it('says the factory’s default applies, naming no mode', () => {
    const info = describeAutopilot(undefined)
    expect(info).toEqual({
      isDefault: true,
      label: 'Autopilot: factory default',
      explanation: 'The factory’s default mode decides whether it tends its failed issues by itself.',
    })
  })

  it('points failed issues at a prompt only while it is off', () => {
    const failed = { failed: [{ container: 'cli', number: 1, kind: 'failed' as const }] }
    const hint = (autopilot?: 'Off' | 'Ask' | 'On') =>
      describeBrief({ ...brief('lens', '2026-01-01T00:00:00Z'), spec: { idea: 'x', autopilot }, status: failed })
        .failedHint
    expect(hint('Off')).toMatch(/^Say in a prompt whether to try again/)
    expect(hint()).toMatch(/Needs you/)
    expect(hint('Ask')).toMatch(/Needs you/)
    expect(hint('On')).toMatch(/by itself; a prompt overrules it/)
  })
})
