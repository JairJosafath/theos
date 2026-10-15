// A Brief's requests: the GitHub issues of its releases repositories it takes
// as input to planning (docs/plans/done/brief-requests.md). For display, how far
// each got; for the Brief page's buttons, the releases list that accepts or
// declines one, and that sets an entry's requests. The CRD judges every value.

import {
  findCondition,
  type Brief,
  type BriefRelease,
  type BriefRequest,
  type BriefRequests,
  type BriefRequestsMode,
  type BriefRequestStage,
} from '../k8s/types'
import type { Tone } from './describe'

/** Each mode of spec.releases[].requests, as a label and one line on what it does. */
export const REQUEST_MODES: Record<BriefRequestsMode, { label: string; explanation: string }> = {
  Off: { label: 'Off', explanation: 'Reads no issue of the repository.' },
  Ask: {
    label: 'Ask',
    explanation: 'Lists each new request under Needs you; it reaches the planner once you accept it.',
  },
  On: { label: 'On', explanation: 'Lets each new request through to the planner when first seen.' },
}

/** A request, for display. */
export interface RequestView {
  /** owner/name#number, as status.needsYou names it. */
  key: string
  /** "owner/name #12". */
  label: string
  repository: string
  number: number
  title: string
  author: string
  url?: string
  stage: BriefRequestStage
  /** The stage in a word or two, and its colour. */
  stageLabel: string
  tone: Tone
  /** What happened to it and what comes next, in a sentence. */
  detail: string
  since?: string
}

/** A Brief's requests, for display. */
export interface RequestsView {
  /** The open requests, oldest first, as far as status lists them. */
  list: RequestView[]
  /** How many are open in all; can exceed the listed ones. */
  waiting: number
  /** Whether an entry of spec.releases takes requests. */
  taking: boolean
  /** The Requests condition while False: why the issues can't be read or answered. */
  failure?: { reason: string; message: string }
  /** The Requests condition while True: which repositories it reads. */
  reading?: string
  /**
   * The answered numbers whose issues are closed, by repository in lower
   * case; an answer drops them, so accepted and declined stay under the CRD's cap.
   */
  settled: Record<string, number[]>
}

const STAGES: Record<BriefRequestStage, { label: string; tone: Tone }> = {
  Waiting: { label: 'Waits for you', tone: 'waiting' },
  Accepted: { label: 'Accepted', tone: 'waiting' },
  Planning: { label: 'Planning', tone: 'progress' },
  Planned: { label: 'Planned', tone: 'ok' },
  Skipped: { label: 'Skipped', tone: 'unknown' },
  Declined: { label: 'Declined', tone: 'unknown' },
  Released: { label: 'Released', tone: 'ok' },
}

export function describeRequest(r: BriefRequest): RequestView {
  const stage = STAGES[r.stage] ?? { label: r.stage, tone: 'unknown' as Tone }
  return {
    key: `${r.repository}#${r.number}`,
    label: `${r.repository} #${r.number}`,
    repository: r.repository,
    number: r.number,
    title: r.title,
    author: r.author,
    url: r.url || undefined,
    stage: r.stage,
    stageLabel: stage.label,
    tone: stage.tone,
    detail: requestDetail(r),
    since: r.since,
  }
}

/** What happened to a request and what comes next. */
function requestDetail(r: BriefRequest): string {
  const round = r.round ? `Round ${r.round}` : 'A round'
  const issues = r.issues?.length ? ` as ${r.issues.join(', ')}` : ''
  const release = r.release ? `, in release ${r.release}` : ''
  switch (r.stage) {
    case 'Waiting':
      return 'Waits for you to accept or decline it.'
    case 'Accepted':
      return 'Accepted; it waits for a round to plan it.'
    case 'Planning':
      return `${round} is planning it.`
    case 'Planned':
      return `${round} planned it${issues}${release}.`
    case 'Skipped':
      return `${round} skipped it${r.why?.trim() ? `: ${r.why.trim()}` : '.'}`
    case 'Declined':
      return 'Declined; the Brief says so on the issue and closes it.'
    case 'Released':
      return `Shipped${r.release ? ` in ${r.release}` : ''}; the Brief says so on the issue and closes it.`
    default:
      return ''
  }
}

export function describeRequests(brief: Brief): RequestsView {
  const status = brief.status ?? {}
  const list = (status.requests ?? []).map(describeRequest)
  const condition = findCondition(status.conditions, 'Requests')
  return {
    list,
    waiting: status.requestsWaiting ?? list.length,
    taking: (brief.spec.releases ?? []).some((r) => takesRequests(r.requests)),
    failure:
      condition?.status === 'False'
        ? { reason: condition.reason ?? 'Unknown', message: condition.message?.trim() ?? '' }
        : undefined,
    reading: condition?.status === 'True' ? condition.message?.trim() || undefined : undefined,
    settled: Object.fromEntries(
      (status.requestsSettled ?? []).map((s) => [s.repository.toLowerCase(), s.numbers ?? []]),
    ),
  }
}

/** Whether requests set so read the repository's issues. */
export function takesRequests(requests: BriefRequests | undefined): boolean {
  return Boolean(requests?.mode && requests.mode !== 'Off')
}

/** "owner/name#12" as its repository and number; undefined when it isn't one. */
export function splitRequest(name: string): { repository: string; number: number } | undefined {
  const m = /^([^/#\s]+\/[^/#\s]+)#(\d+)$/.exec(name)
  return m ? { repository: m[1], number: Number(m[2]) } : undefined
}

/** A person's answer to a request in Ask. */
export type RequestAnswer = 'accept' | 'decline'

/** The entry of releases that takes requests from repository; GitHub names are case-insensitive. */
function requestsEntry(releases: BriefRelease[], repository: string): number {
  const repo = repository.toLowerCase()
  return releases.findIndex((r) => r.github.repository.toLowerCase() === repo && takesRequests(r.requests))
}

/**
 * The releases list with number added to the accepted (accept) or declined
 * (decline) requests of the entry that takes requests from repository, and
 * taken out of the other; undefined when no entry does. The settled numbers,
 * those of closed issues, leave both lists, which keeps them under the CRD's
 * cap of 200 (#824).
 */
export function answerRequest(
  releases: BriefRelease[],
  repository: string,
  number: number,
  answer: RequestAnswer,
  settled: number[] = [],
): BriefRelease[] | undefined {
  const i = requestsEntry(releases, repository)
  if (i < 0) return undefined
  const requests = releases[i].requests!
  const [to, from] = answer === 'accept' ? (['accepted', 'declined'] as const) : (['declined', 'accepted'] as const)
  const keep = (n: number) => n !== number && !settled.includes(n)
  const next: BriefRequests = { ...requests, [to]: [...(requests[to] ?? []).filter(keep), number] }
  const rest = (requests[from] ?? []).filter(keep)
  if (rest.length) next[from] = rest
  else delete next[from]
  return releases.map((r, j) => (j === i ? { ...r, requests: next } : r))
}

/** How spec.releases answers the request already, if it does. */
export function requestAnswered(
  releases: BriefRelease[],
  repository: string,
  number: number,
): 'accepted' | 'declined' | undefined {
  const i = requestsEntry(releases, repository)
  if (i < 0) return undefined
  const { accepted = [], declined = [] } = releases[i].requests!
  if (declined.includes(number)) return 'declined'
  if (accepted.includes(number)) return 'accepted'
  return undefined
}

/** Logins or teams as typed, separated by commas or spaces; the CRD judges each. */
export function splitNames(typed: string): string[] {
  return [...new Set(typed.split(/[\s,]+/).filter(Boolean))]
}

/**
 * An entry's requests set to mode, authors and teams, keeping the numbers
 * already accepted and declined; undefined, no requests, for Off with nothing
 * else to keep.
 */
export function withRequests(
  previous: BriefRequests | undefined,
  mode: BriefRequestsMode,
  authors: string[],
  teams: string[],
): BriefRequests | undefined {
  const next: BriefRequests = { mode }
  if (authors.length) next.authors = authors
  if (teams.length) next.teams = teams
  if (previous?.accepted?.length) next.accepted = previous.accepted
  if (previous?.declined?.length) next.declined = previous.declined
  if (mode === 'Off' && Object.keys(next).length === 1) return undefined
  return next
}

/** A releases entry with requests set, or without any when undefined. */
export function entryWithRequests(release: BriefRelease, requests: BriefRequests | undefined): BriefRelease {
  const next = { ...release }
  if (requests) next.requests = requests
  else delete next.requests
  return next
}

/** An entry's requests in one line, for the settings: "Ask, from gray-lenovo and gray-org/maintainers". */
export function requestsSummary(requests: BriefRequests | undefined): string {
  if (!takesRequests(requests)) return 'takes no requests'
  const who = [...(requests!.authors ?? []), ...(requests!.teams ?? [])]
  const names = who.length > 1 ? `${who.slice(0, -1).join(', ')} and ${who.at(-1)}` : (who[0] ?? 'nobody')
  return `requests ${requests!.mode}, from ${names}`
}
