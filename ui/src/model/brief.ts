// A Brief for display: its phase in plain words, what it waits for, which of
// its prompts the planner has taken, and what it made.

import {
  findCondition,
  type Brief,
  type BriefApp,
  type BriefFailedIssue,
  type BriefFailedKind,
  type BriefPhase,
} from '../k8s/types'
import { plural, type PhaseInfo, type Tone } from './describe'

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
  round: number
  prompts: BriefPrompt[]
  waitingPrompts: number
  choices: string[]
  created: { kind: string; name: string }[]
  /** The issues the Brief filed; failed marks one handed back to a person, as far as listed. */
  issues: { repository: string; number: number; url?: string; title?: string; failed: boolean }[]
  /** Issues handed back to a person that wait for an answer in a prompt, as far as listed. */
  failed: BriefFailedView[]
  /** How many failed issues wait in all; can exceed the listed ones. */
  failedWaiting: number
  /** Reviewers' suggestions that wait for an answer in a prompt, as far as listed. */
  suggestions: { container: string; number: number; title?: string; url?: string }[]
  /** How many suggestions wait in all; can exceed the listed ones. */
  suggestionsWaiting: number
  /** Each app's releases: a line per Container the Brief made, then per spec.releases entry. */
  apps: BriefAppView[]
  repositoryURL?: string
  /** Planning cost over all rounds, such as "$0.42", or that the agent didn't report it. */
  cost?: string
}

export function describeBrief(brief: Brief): BriefView {
  const status = brief.status ?? {}
  const conditions = status.conditions
  const needs = findCondition(conditions, 'NeedsInput')
  const planned = findCondition(conditions, 'Planned')
  const taken = status.promptsPlanned ?? 0
  const prompts = (brief.spec.prompts ?? []).map((text, i) => ({ text, planned: i < taken }))
  const suggestions = (status.suggestions ?? []).map(({ container, number, title, url }) => ({
    container,
    number,
    title,
    url,
  }))
  const suggestionsWaiting = status.suggestionsWaiting ?? suggestions.length
  const failed = (status.failed ?? []).map(describeFailedIssue)
  const failedWaiting = status.failedWaiting ?? failed.length
  const phase = describeBriefPhase(status.phase)
  if (status.phase === 'Planned' && (failedWaiting > 0 || suggestionsWaiting > 0)) {
    const waiting = [
      failedWaiting ? plural(failedWaiting, 'failed issue') : '',
      suggestionsWaiting ? plural(suggestionsWaiting, 'reviewer suggestion') : '',
    ].filter(Boolean)
    const verb = failedWaiting + suggestionsWaiting === 1 ? 'waits' : 'wait'
    phase.explanation = `The last round is done; ${waiting.join(' and ')} ${verb} for your answer in a prompt.`
  }
  return {
    name: brief.metadata.name,
    idea: brief.spec.idea,
    phase,
    needsInput: needs?.status === 'True' ? needs.message?.trim() || 'It waits for you.' : undefined,
    failure: status.phase === 'Failed' ? planned?.message?.trim() || undefined : undefined,
    round: status.round ?? 0,
    prompts,
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
    suggestions,
    suggestionsWaiting,
    apps: (status.apps ?? []).map(describeBriefApp),
    repositoryURL: status.repositoryURL,
    cost: describeCost(status.costMilliUSD, status.runsWithoutCost),
  }
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
}

/** The kinds a prompt can't send back to work as they are: the next round closes them. */
const FAILED_NOTES: Partial<Record<BriefFailedKind, string>> = {
  exhausted: 'It won’t be tried again as it is: it will be filed again, reworded or split.',
  merged: 'It won’t be tried again: it will be closed, and what is left filed as a new issue.',
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
}

export function describeBriefApp(app: BriefApp): BriefAppView {
  const releasesOn = app.releases === 'On'
  const last = app.lastRelease
  return {
    container: app.container,
    releasesOn,
    label: releasesOn ? 'Releases on' : 'Releases off',
    tone: releaseTone(app),
    message: app.message?.trim() || (releasesOn ? 'Releases are on.' : 'Releases are off.'),
    release: last ? { tag: last.tag, url: last.url || undefined, time: last.time } : undefined,
    publishTo: app.publishTo || undefined,
    publishedURL: last?.publishState === 'Published' ? last.publishedURL || undefined : undefined,
  }
}

/** Off is muted; on is ok once the latest release ran and published, an error when either failed. */
function releaseTone(app: BriefApp): Tone {
  if (app.releases !== 'On') return 'unknown'
  const last = app.lastRelease
  if (!last) return 'waiting'
  if (last.run === 'Failed' || last.publishState === 'Failed') return 'error'
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

/** Thousandths of a US dollar as dollars and cents; under a cent shows as "<$0.01". */
export function formatCost(milliUSD: number): string {
  if (milliUSD > 0 && milliUSD < 10) return '<$0.01'
  return `$${(milliUSD / 1000).toFixed(2)}`
}

/** Newest first, then by name. */
export function sortBriefs(briefs: Brief[]): Brief[] {
  return [...briefs].sort(
    (a, b) =>
      (b.metadata.creationTimestamp ?? '').localeCompare(a.metadata.creationTimestamp ?? '') ||
      a.metadata.name.localeCompare(b.metadata.name),
  )
}
