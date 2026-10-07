// A Brief for display: its phase in plain words, what it waits for, which of
// its prompts the planner has taken, and what it made.

import { findCondition, type Brief, type BriefPhase } from '../k8s/types'
import { plural, type PhaseInfo } from './describe'

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
  issues: { repository: string; number: number; url?: string; title?: string }[]
  /** Reviewers' suggestions that wait for an answer in a prompt, as far as listed. */
  suggestions: { container: string; number: number; title?: string; url?: string }[]
  /** How many suggestions wait in all; can exceed the listed ones. */
  suggestionsWaiting: number
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
  const phase = describeBriefPhase(status.phase)
  if (status.phase === 'Planned' && suggestionsWaiting > 0) {
    phase.explanation = `The last round is done; ${plural(suggestionsWaiting, 'reviewer suggestion waits', 'reviewer suggestions wait')} for your answer in a prompt.`
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
    issues: status.issues ?? [],
    suggestions,
    suggestionsWaiting,
    repositoryURL: status.repositoryURL,
    cost: describeCost(status.costMilliUSD, status.runsWithoutCost),
  }
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
