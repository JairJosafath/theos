// A Brief for display: its phase in plain words, what it waits for, which of
// its prompts the planner has taken, and what it made.

import {
  findCondition,
  type Brief,
  type BriefApp,
  type BriefAutopilot,
  type BriefFailedIssue,
  type BriefFailedKind,
  type BriefItemKind,
  type BriefNeedsYou,
  type BriefPhase,
  type BriefRelease,
  type BriefTendedIssue,
  type TokenUsage,
} from '../k8s/types'
import { plural, type PhaseInfo, type Tone } from './describe'
import { describeRequests, splitRequest, type RequestsView } from './requests'
import { compareText } from './order'

/** The Brief phase as a badge label, colour tone and one-line explanation. */
export function describeBriefPhase(phase: BriefPhase | undefined): PhaseInfo {
  switch (phase) {
    case 'Pending':
      return { label: 'Pending', tone: 'waiting', explanation: 'Waiting for the planner to start the first round.' }
    case 'Planning':
      return {
        label: 'Planning',
        tone: 'progress',
        explanation: 'The planner is turning the idea and prompts into a plan right now.',
      }
    case 'Applying':
      return {
        label: 'Applying',
        tone: 'progress',
        explanation: 'The factory is creating what the plan says: systems, containers, crews and issues.',
      }
    case 'Planned':
      return { label: 'Planned', tone: 'ok', explanation: 'The last round is done and no prompt is waiting.' }
    case 'NeedsInput':
      return { label: 'Needs your input', tone: 'waiting', explanation: 'It waits for you; the note says what for.' }
    case 'Failed':
      return { label: 'Failed', tone: 'error', explanation: 'A round failed; the note says why.' }
    default:
      return { label: 'No status yet', tone: 'unknown', explanation: 'The factory has not reported on this brief yet.' }
  }
}

export interface BriefPrompt {
  text: string
  /** Taken by the last applied round; otherwise it waits for the next one. */
  planned: boolean
}

export interface BriefView {
  name: string
  idea: string
  phase: PhaseInfo
  /** What the Brief waits for, in one sentence, while it needs input. */
  needsInput?: string
  /** Why the last round failed, while it has. */
  failure?: string
  /** spec.paused: the Brief and its Crews start nothing new. */
  paused: boolean
  /** spec.pauseRuns: its running Tasks pause too, at their next tool boundary. */
  pauseRuns: boolean
  /** While paused, the Paused condition's message: which Tasks still run, or that nothing does. */
  pausedNote?: string
  round: number
  prompts: BriefPrompt[]
  /** Every prompt the Brief was written, the retired ones too: spec.prompts shrinks, this doesn't. */
  promptsWritten: number
  waitingPrompts: number
  choices: string[]
  created: { kind: string; name: string }[]
  /** The issues the Brief filed; failed marks one handed back to a person, as far as listed. */
  issues: { repository: string; number: number; url?: string; title?: string; failed: boolean }[]
  /** Issues handed back to a person that wait for an answer in a prompt, as far as listed. */
  failed: BriefFailedView[]
  /** How many failed issues wait in all; can exceed the listed ones. */
  failedWaiting: number
  /** What to do about the failed issues, by the autopilot mode. */
  failedHint: string
  /**
   * The Needs you box: what the Brief waits for (NeedsInput) first, then what
   * only the person can decide, each with what the Brief would do and why.
   */
  needsYou: BriefNeedsYouView[]
  /** How many items need the person in all; can exceed the listed ones. */
  needsYouWaiting: number
  /** What the Brief did by itself: its tending rounds, newest first. */
  tended: BriefTendedRoundView[]
  autopilot: AutopilotInfo
  /** Reviewers' suggestions that wait for an answer in a prompt, as far as listed. */
  suggestions: { container: string; number: number; title?: string; url?: string }[]
  /** How many suggestions wait in all; can exceed the listed ones. */
  suggestionsWaiting: number
  /** Each app's releases: a line per Container the Brief made, then per spec.releases entry. */
  apps: BriefAppView[]
  /** The GitHub issues it takes as requests, and whether it can read them. */
  requests: RequestsView
  /** spec.releases, which accepting or declining a request sends whole. */
  releases: BriefRelease[]
  /** The resourceVersion the view was worked out from, which a releases write tests. */
  resourceVersion?: string
  repositoryURL?: string
  /** What planning used over all rounds: cost, tokens and the largest agent run. */
  usage?: UsageView
  /** Every round, newest first, with what it used. */
  rounds: BriefRoundView[]
}

/** A Brief round, for display: its summary, how it ended and what its planner runs used. */
export interface BriefRoundView {
  round: number
  summary?: string
  /** The reviewers' suggestions a prompt named that the round couldn't answer, a line each. */
  unanswered?: string[]
  /** How the round ended, in a word or two, when it says. */
  outcome?: string
  usage?: UsageView
}

const ROUND_OUTCOMES: Record<
  NonNullable<NonNullable<NonNullable<Brief['status']>['history']>[number]['outcome']>,
  string
> = {
  Applied: 'Applied',
  PlannerNeedsInput: 'Asked you',
  RoundFailed: 'Failed',
  Tended: 'Tended',
}

/**
 * A Brief for display. plannerLimit is its planner Agent's
 * spec.sessionTokenLimit, when known, to mark a round's agent run past it.
 */
export function describeBrief(brief: Brief, plannerLimit?: number): BriefView {
  const status = brief.status ?? {}
  const conditions = status.conditions
  const needs = findCondition(conditions, 'NeedsInput')
  const planned = findCondition(conditions, 'Planned')
  const pausedCondition = findCondition(conditions, 'Paused')
  const paused = Boolean(brief.spec.paused)
  // The status counts every prompt written, the retired ones too.
  const taken = (status.promptsPlanned ?? 0) - (brief.spec.promptsRetired ?? 0)
  const prompts = (brief.spec.prompts ?? []).map((text, i) => ({ text, planned: i < taken }))
  const suggestions = (status.suggestions ?? []).map(({ container, number, title, url }) => ({
    container,
    number,
    title,
    url,
  }))
  const suggestionsWaiting = status.suggestionsWaiting ?? suggestions.length
  const listed = (status.needsYou ?? []).map((n) => describeNeedsYou(n, status))
  const asked = new Set(listed.flatMap((n) => (n.issue ? [`${n.issue.container}#${n.issue.number}`] : [])))
  const failed = (status.failed ?? []).map((f) => ({
    ...describeFailedIssue(f),
    asked: asked.has(`${f.container}#${f.number}`),
  }))
  const failedWaiting = status.failedWaiting ?? failed.length
  const needsInput = needs?.status === 'True' ? needs.message?.trim() || 'It waits for you.' : undefined
  const needsYou: BriefNeedsYouView[] = needsInput
    ? [{ kind: 'Input', label: 'The Brief', question: needsInput }, ...listed]
    : listed
  const needsYouWaiting = (status.needsYouWaiting ?? listed.length) + (needsInput ? 1 : 0)
  const autopilot = describeAutopilot(brief.spec.autopilot, status.autopilot)
  const phase = describeBriefPhase(status.phase)
  const askedCount = status.needsYouWaiting ?? listed.length
  // With autopilot on or asking, the failed issues that wait for the person
  // are under Needs you already; the rest the Brief decides by itself. With
  // no mode known, the operator's not having reported it yet, the count
  // claims none and the failed-issues hint says how to answer them.
  const unanswered = autopilot.mode === 'Off' ? failedWaiting : 0
  if (status.phase === 'Planned' && (unanswered > 0 || suggestionsWaiting > 0 || askedCount > 0)) {
    const waiting = [
      askedCount ? `${askedCount} ${askedCount === 1 ? 'item' : 'items'} under Needs you` : '',
      unanswered ? plural(unanswered, 'failed issue') : '',
      suggestionsWaiting ? plural(suggestionsWaiting, 'reviewer suggestion') : '',
    ].filter(Boolean)
    const verb = askedCount + unanswered + suggestionsWaiting === 1 ? 'waits' : 'wait'
    phase.explanation = `The last round is done; ${waiting.join(', ').replace(/, ([^,]*)$/, ' and $1')} ${verb} for your answer in a prompt.`
  }
  return {
    name: brief.metadata.name,
    idea: brief.spec.idea,
    phase,
    needsInput,
    failure: status.phase === 'Failed' ? planned?.message?.trim() || undefined : undefined,
    paused,
    pauseRuns: Boolean(brief.spec.pauseRuns),
    pausedNote: paused
      ? (pausedCondition?.status === 'True' && pausedCondition.message?.trim()) ||
        'Pausing; the factory has not said yet what still runs.'
      : undefined,
    round: status.round ?? 0,
    prompts,
    promptsWritten: prompts.length + (brief.spec.promptsRetired ?? 0),
    waitingPrompts: prompts.filter((p) => !p.planned).length,
    choices: status.choices ?? [],
    created: status.created ?? [],
    issues: (status.issues ?? []).map(({ repository, number, url, title }) => ({
      repository,
      number,
      url,
      title,
      failed: (status.failed ?? []).some((f) => isSameIssue(f, repository, number, url)),
    })),
    failed,
    failedWaiting,
    failedHint: autopilot.mode ? FAILED_HINTS[autopilot.mode] : DEFAULT_FAILED_HINT,
    needsYou,
    needsYouWaiting,
    tended: tendedRounds(status.history ?? [], status.tended ?? [], status.failed ?? []),
    autopilot,
    suggestions,
    suggestionsWaiting,
    apps: (status.apps ?? []).map((a) => describeBriefApp(a)),
    requests: describeRequests(brief),
    releases: brief.spec.releases ?? [],
    resourceVersion: brief.metadata.resourceVersion,
    repositoryURL: status.repositoryURL,
    usage: describeUsage(status.costMilliUSD, status.runsWithoutCost, status.tokens, plannerLimit),
    rounds: (status.history ?? [])
      .map((h) => ({
        round: h.round,
        summary: h.summary?.trim() || undefined,
        unanswered: h.unanswered?.length ? h.unanswered : undefined,
        outcome: h.outcome ? ROUND_OUTCOMES[h.outcome] : undefined,
        usage: describeUsage(h.costMilliUSD, h.runsWithoutCost, h.tokens, plannerLimit),
      }))
      .reverse(),
  }
}

/** What the Brief card's Pause or Resume button does: the spec.paused it sets, its label and why. */
export interface PauseAction {
  label: 'Pause' | 'Resume'
  /** The value it patches spec.paused to. */
  paused: boolean
  explanation: string
}

export function pauseAction(view: Pick<BriefView, 'paused'>): PauseAction {
  return view.paused
    ? { label: 'Resume', paused: false, explanation: 'Resume this brief and the crews it paused' }
    : {
        label: 'Pause',
        paused: true,
        explanation: 'Pause this brief and its crews: they start nothing new, and running Tasks finish',
      }
}

/** What the Pause runs or Resume runs button does, on a Brief's card and a Crew's: the spec.pauseRuns it sets, its label and why. */
export interface PauseRunsAction {
  label: 'Pause runs' | 'Resume runs'
  /** The value it patches spec.pauseRuns to; true sets spec.paused too. */
  pauseRuns: boolean
  explanation: string
}

export function pauseRunsAction(pauseRuns: boolean, kind: 'brief' | 'crew'): PauseRunsAction {
  const whose = kind === 'brief' ? 'this brief’s and its crews’' : 'this crew’s'
  return pauseRuns
    ? {
        label: 'Resume runs',
        pauseRuns: false,
        explanation: `Resume ${whose} paused Tasks where they stopped; the ${kind} itself stays paused until you resume it`,
      }
    : {
        label: 'Pause runs',
        pauseRuns: true,
        explanation: `Pause the ${kind} and stop ${whose} running Tasks at their next tool call, each saved to resume where it stopped`,
      }
}

/** A Brief's autopilot mode as a label and one-line explanation; unset says it is the factory's default. */
export interface AutopilotInfo {
  /** The mode in effect: the one the Brief sets, else the one its status reports; none before the operator reports it. */
  mode?: BriefAutopilot
  /** Whether the Brief sets none and the operator's default applies. */
  isDefault: boolean
  label: string
  explanation: string
}

export const AUTOPILOT_MODES: Record<BriefAutopilot, { label: string; explanation: string }> = {
  Off: {
    label: 'Autopilot off',
    explanation: 'It does nothing without a prompt: failed issues wait for your answer.',
  },
  Ask: {
    label: 'Autopilot asks',
    explanation:
      'It decides its failed issues by itself and lists each answer under Needs you; a prompt says to apply them.',
  },
  On: {
    label: 'Autopilot on',
    explanation:
      'It retries, closes or waits on its failed issues by itself, and asks you only what only you can decide.',
  },
}

/** What an unset autopilot does: the operator decides, so the UI names no mode until status reports it. */
export const DEFAULT_AUTOPILOT_HINT = 'The factory’s default mode decides whether it tends its failed issues by itself.'

/**
 * The autopilot mode in effect: mode, the Brief's spec.autopilot, wins; unset,
 * reported, its status.autopilot, names the operator's default.
 */
export function describeAutopilot(mode: BriefAutopilot | undefined, reported?: BriefAutopilot): AutopilotInfo {
  if (mode) return { mode, isDefault: false, ...AUTOPILOT_MODES[mode] }
  if (reported) {
    const { label, explanation } = AUTOPILOT_MODES[reported]
    return { mode: reported, isDefault: true, label: `${label} (factory default)`, explanation }
  }
  return { isDefault: true, label: 'Autopilot: factory default', explanation: DEFAULT_AUTOPILOT_HINT }
}

/** What to do about failed issues, by the mode in effect. */
const FAILED_HINTS: Record<BriefAutopilot, string> = {
  Off: 'Say in a prompt whether to try again, such as "try the CLI issue again now that the engine\'s checker is merged".',
  Ask: 'The Brief decides these and asks you under Needs you; a prompt can answer them first.',
  On: 'The Brief decides these by itself; a prompt overrules it, such as "file the CLI issue again".',
}

/** What to do about failed issues before the operator reports the mode in effect. */
const DEFAULT_FAILED_HINT =
  'The Brief asks you under Needs you about those it can’t decide; a prompt can answer any of them, such as "file the CLI issue again".'

/** An entry of the Needs you box, for display. */
export interface BriefNeedsYouView {
  /** Input is what the Brief itself waits for: a missing Agent or Secret, or the planner's question. */
  kind: BriefItemKind | 'Input'
  /** What it is about: "Task cel-lens-core-8", "cel-lens-cli #3". */
  label: string
  /** The issue's address, for an issue the Brief lists as failed. */
  url?: string
  /** The issue it is about, with its title when the Brief lists it; unset for any other kind. */
  issue?: { container: string; number: number; title?: string }
  /** The GitHub issue a request is, for a request waiting in Ask; unset for any other kind. */
  request?: { repository: string; number: number }
  /** The Container it is about, settled on the Container's page; unset for any other kind. */
  container?: string
  /** The answer the Brief would give the issue, from the tending round that asked. */
  recommended?: 'retry' | 'close' | 'wait'
  question: string
  /** What the Brief would do. */
  recommend?: string
  why?: string
  since?: string
}

export function describeNeedsYou(
  entry: BriefNeedsYou,
  status: Pick<NonNullable<Brief['status']>, 'failed' | 'issues' | 'tended' | 'requests'>,
): BriefNeedsYouView {
  const issue = entry.kind === 'Issue' ? splitIssue(entry.name) : undefined
  const request = entry.kind === 'Request' ? splitRequest(entry.name) : undefined
  const same = (t: { container: string; number: number }) =>
    issue !== undefined && t.container === issue.container && t.number === issue.number
  const failed = (status.failed ?? []).find(same)
  const filed =
    issue &&
    (status.issues ?? []).find((i) => i.number === issue.number && i.repository.split('/').pop() === issue.container)
  // The newest round that asked about it says what it would do.
  const tended = (status.tended ?? []).filter((t) => same(t) && t.answer === 'ask').at(-1)
  const requested =
    request &&
    (status.requests ?? []).find(
      (r) => r.number === request.number && r.repository.toLowerCase() === request.repository.toLowerCase(),
    )
  return {
    kind: entry.kind,
    label: issue
      ? `${issue.container} #${issue.number}`
      : request
        ? `${request.repository} #${request.number}`
        : `${entry.kind} ${entry.name}`,
    url: failed?.url || requested?.url || undefined,
    issue: issue && { ...issue, title: failed?.title || filed?.title || undefined },
    request,
    container: entry.kind === 'Container' ? entry.name : undefined,
    recommended: tended?.recommend?.answer,
    question: entry.question.trim(),
    recommend: entry.recommend?.trim() || undefined,
    why: entry.why?.trim() || undefined,
    since: entry.since,
  }
}

/** "cel-lens-cli#3" as its Container and number; undefined when it isn't one. */
function splitIssue(name: string): { container: string; number: number } | undefined {
  const m = /^(.+)#(\d+)$/.exec(name)
  return m ? { container: m[1], number: Number(m[2]) } : undefined
}

/** A tending round, for display: what the planner summed up, and its answer per issue. */
export interface BriefTendedRoundView {
  round: number
  summary?: string
  answers: { label: string; url?: string; text: string }[]
}

/** What each answer did, in a few words. */
const ANSWER_DONE: Record<BriefTendedIssue['answer'], string> = {
  retry: 'Tried again',
  close: 'Closed',
  wait: 'Waits',
  ask: 'Asked you',
}

/** The Brief's tending rounds, newest first, each with the answers status.tended remembers for it. */
export function tendedRounds(
  history: NonNullable<NonNullable<Brief['status']>['history']>,
  tended: BriefTendedIssue[],
  failed: BriefFailedIssue[],
): BriefTendedRoundView[] {
  return history
    .filter((h) => h.outcome === 'Tended')
    .map((h) => ({
      round: h.round,
      summary: h.summary?.trim() || undefined,
      answers: tended
        .filter((t) => t.round === h.round)
        .map((t) => {
          const url = failed.find((f) => f.container === t.container && f.number === t.number)?.url
          const would = t.answer === 'ask' && t.recommend ? `; it would ${ANSWER_WOULD[t.recommend.answer]}` : ''
          const why = t.why?.trim()
          return {
            label: `${t.container} #${t.number}`,
            url: url || undefined,
            text: `${ANSWER_DONE[t.answer]}${would}${why ? `: ${why}` : ''}`,
          }
        }),
    }))
    .reverse()
}

const ANSWER_WOULD: Record<'retry' | 'close' | 'wait', string> = {
  retry: 'try it again',
  close: 'close it',
  wait: 'wait',
}

/** An issue handed back to a person, for display. */
export interface BriefFailedView {
  container: string
  number: number
  title?: string
  url?: string
  kind: BriefFailedKind
  /** Why it was handed back, in a sentence. */
  reason: string
  tries: number
  /** What answering it in a prompt does, when that isn't trying it again. */
  note?: string
  /** Whether Needs you lists it too, where it is answered. */
  asked?: boolean
}

export function describeFailedIssue(issue: BriefFailedIssue): BriefFailedView {
  return {
    container: issue.container,
    number: issue.number,
    title: issue.title,
    url: issue.url,
    kind: issue.kind,
    reason: issue.reason?.trim() || FAILED_REASONS[issue.kind] || 'Handed back; see the issue.',
    tries: issue.tries ?? 0,
    note: FAILED_NOTES[issue.kind],
  }
}

/** The reason to show when the operator gave none. */
const FAILED_REASONS: Record<BriefFailedKind, string> = {
  unclear: 'It has no acceptance criteria.',
  merged: 'A Task merged it already.',
  exhausted: 'Agents tried it as often as a Crew does.',
  failed: 'Its last Task failed.',
  person: 'Handed back by a person; see the issue.',
  byHand: 'Needs a person; see the issue.',
}

/** The kinds a prompt can't send back to work as they are: the next round closes them. */
const FAILED_NOTES: Partial<Record<BriefFailedKind, string>> = {
  exhausted: 'It won’t be tried again as it is: it will be filed again, reworded or split.',
  merged: 'It won’t be tried again: it will be closed, and what is left filed as a new issue.',
  byHand: 'Agents can’t do it: once you have done it or dropped it, say so in a prompt to close it.',
}

/**
 * Whether a failed issue is the filed one: the same address, or else the same
 * number in the repository named after its Container.
 */
function isSameIssue(f: BriefFailedIssue, repository: string, number: number, url?: string): boolean {
  if (f.url && url) return f.url === url
  return f.number === number && repository.split('/').pop() === f.container
}

/** An app of a Brief and how its releases go, for display. */
export interface BriefAppView {
  container: string
  releasesOn: boolean
  /** "Releases on" or "Releases off", toned by how the latest release went. */
  label: string
  tone: Tone
  /** The operator's sentence on how the releases go. */
  message: string
  /** The latest release, linking to its page on the Git host. */
  release?: { tag: string; url?: string; time?: string }
  /** Where the releases are published, owner/name, linking to the published release once there is one. */
  publishTo?: string
  publishedURL?: string
  /** "3 at a time": how many issues its Crew works on at once; unset while it has no Crew. */
  parallel?: string
  /** The latest release's Go module, toned by whether Go can fetch it; unset for an app that isn't Go. */
  module?: { state: string; path?: string; message?: string; tone: Tone }
  /** What the next cut ships, and when (describeNextRelease); unset without a cadence. */
  nextRelease?: NonNullable<BriefApp['nextRelease']>
}

export function describeBriefApp(app: BriefApp): BriefAppView {
  const releasesOn = app.releases === 'On'
  const last = app.lastRelease
  return {
    ...(app.nextRelease ? { nextRelease: app.nextRelease } : {}),
    container: app.container,
    releasesOn,
    label: releasesOn ? 'Releases on' : 'Releases off',
    tone: releaseTone(app),
    message: app.message?.trim() || (releasesOn ? 'Releases are on.' : 'Releases are off.'),
    release: last ? { tag: last.tag, url: last.url || undefined, time: last.time } : undefined,
    publishTo: app.publishTo || undefined,
    publishedURL: last?.publishState === 'Published' ? last.publishedURL || undefined : undefined,
    parallel: app.parallel ? `${app.parallel} at a time` : undefined,
    module:
      last?.moduleState && last.moduleState !== 'NotGo'
        ? {
            state: last.moduleState,
            path: last.modulePath || undefined,
            message: last.moduleMessage || undefined,
            tone: moduleTone(last.moduleState),
          }
        : undefined,
  }
}

/**
 * "next release v0.15.0 at 18:00 UTC, 4 merged": the cut's time in the
 * cadence's time zone, as the person set it, with its weekday when it isn't
 * today there, as the operator says it.
 */
export function describeNextRelease(next: NonNullable<BriefApp['nextRelease']>, now: Date = new Date()): string {
  const zone = next.timeZone || 'UTC'
  const at = new Date(next.time)
  let time: string
  let day: (d: Date) => string
  try {
    time = new Intl.DateTimeFormat('en-GB', {
      timeZone: zone,
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).format(at)
    const days = new Intl.DateTimeFormat('en-GB', {
      timeZone: zone,
      weekday: 'long',
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
    })
    day = (d) => days.format(d)
  } catch {
    // A time zone this browser doesn't know: say the time in UTC.
    return describeNextRelease({ ...next, timeZone: 'UTC' }, now)
  }
  const when = day(at) === day(now) ? `${time} ${zone}` : `${time} ${zone} on ${weekdayIn(at, zone)}`
  const merged = next.merged ? `${next.merged} merged` : 'none merged yet'
  return `next release ${next.tag} at ${when}, ${merged}`
}

function weekdayIn(at: Date, zone: string): string {
  return new Intl.DateTimeFormat('en-GB', { timeZone: zone, weekday: 'long' }).format(at)
}

/** A module nobody can fetch is an error, one on its way is progress. */
function moduleTone(state: string): Tone {
  if (state === 'Published') return 'ok'
  if (state === 'Invalid' || state === 'NotFetchable') return 'error'
  return 'progress'
}

/** Off is muted; on is ok once the latest release ran and published, an error when either failed or Go can't fetch its module. */
function releaseTone(app: BriefApp): Tone {
  if (app.releases !== 'On') return 'unknown'
  const last = app.lastRelease
  if (!last) return 'waiting'
  if (last.run === 'Failed' || last.publishState === 'Failed') return 'error'
  if (last.moduleState === 'Invalid' || last.moduleState === 'NotFetchable') return 'error'
  if (last.run === 'Waiting' || last.run === 'Running') return 'progress'
  if (last.publishState === 'Pending' || last.publishState === 'Publishing') return 'progress'
  return 'ok'
}

/**
 * What runs cost, as their agents reported it: dollars and cents, or that the
 * agent didn't report it for some or all runs. Undefined before any run.
 */
export function describeCost(milliUSD = 0, runsWithoutCost = 0): string | undefined {
  if (runsWithoutCost === 0) return milliUSD ? formatCost(milliUSD) : undefined
  if (!milliUSD) return 'cost not reported by the agent'
  const runs = runsWithoutCost === 1 ? '1 run' : `${runsWithoutCost} runs`
  return `${formatCost(milliUSD)}, plus ${runs} whose cost the agent didn’t report`
}

/** What agent runs used, for display: each part of the line, and whether an agent run got too long. */
export interface UsageView {
  /** Dollars and cents, or that the agent didn't report the cost. */
  cost?: string
  /** All tokens, how many of them were cache reads, or that the agent didn't report them. */
  tokens?: string
  /** How large the largest agent run got, and the limit when it passed it. */
  largestRun?: string
  /** Whether the largest agent run passed the Agent's limit. */
  pastLimit: boolean
  /** How often an agent run handed its work to a fresh one, such as "handed off 2×"; none when none did. */
  handoffs?: string
  /** The parts joined, such as "$6.12 · 1.4M tokens, 1.1M of them cache reads · largest run 142k · handed off 2×". */
  line: string
}

/**
 * What agent runs used, as their agents reported it: the cost, the tokens,
 * the largest agent run, marked when it passed limit (the Agent's
 * spec.sessionTokenLimit, when the caller knows it), and how often an agent
 * run handed off to a fresh one. Undefined before any run.
 */
export function describeUsage(
  milliUSD = 0,
  runsWithoutCost = 0,
  tokens: TokenUsage = {},
  limit?: number,
): UsageView | undefined {
  let cost = describeCost(milliUSD, runsWithoutCost)
  const total = (tokens.input ?? 0) + (tokens.output ?? 0) + (tokens.cacheRead ?? 0) + (tokens.cacheWrite ?? 0)
  const without = tokens.runsWithout ?? 0
  let used: string | undefined
  if (total) {
    used = `${formatTokens(total)} tokens`
    if (tokens.cacheRead) used += `, ${formatTokens(tokens.cacheRead)} of them cache reads`
    if (without) used += `, plus ${plural(without, 'run')} whose tokens the agent didn’t report`
  } else if (without) {
    used = 'tokens not reported by the agent'
    if (!milliUSD && runsWithoutCost) {
      cost = undefined
      used = 'cost and tokens not reported by the agent'
    }
  }
  const largest = tokens.largestSession ?? 0
  const pastLimit = limit !== undefined && largest > limit
  const largestRun = largest
    ? `largest run ${formatTokens(largest)}${pastLimit ? `, past the ${formatTokens(limit)} limit` : ''}`
    : undefined
  const handoffs = tokens.sessionHandoffs ? `handed off ${tokens.sessionHandoffs}×` : undefined
  const parts = [cost, used, largestRun, handoffs].filter(Boolean)
  if (!parts.length) return undefined
  return { cost, tokens: used, largestRun, pastLimit, handoffs, line: parts.join(' · ') }
}

/** A token count in a few characters: 950, 9.5k, 142k, 1.4M. */
export function formatTokens(n: number): string {
  if (n < 1000) return `${n}`
  const [value, unit] = n < 999_500 ? [n / 1000, 'k'] : [n / 1_000_000, 'M']
  const shown = value < 9.95 ? value.toFixed(1).replace(/\.0$/, '') : `${Math.round(value)}`
  return `${shown}${unit}`
}

/**
 * Thousandths of a US dollar as dollars and cents; under a cent shows as
 * "<$0.01". A cost under zero, or one that is no number (NaN, an infinity),
 * is no cost and shows as none, "–", as the report's page writes it
 * (reportMoney in internal/model/report_page.go): "$-1.50" and "$NaN" would
 * pass for a price. Both sides are held to testdata/report/money.json.
 */
export function formatCost(milliUSD: number): string {
  if (!Number.isFinite(milliUSD) || milliUSD < 0) return '–'
  if (milliUSD > 0 && milliUSD < 10) return '<$0.01'
  return `$${(milliUSD / 1000).toFixed(2)}`
}

/** Newest first, then by name. */
export function sortBriefs(briefs: Brief[]): Brief[] {
  return [...briefs].sort(
    (a, b) =>
      compareText(b.metadata.creationTimestamp ?? '', a.metadata.creationTimestamp ?? '') ||
      compareText(a.metadata.name, b.metadata.name),
  )
}
