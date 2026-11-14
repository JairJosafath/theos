// A Brief's requests: the GitHub issues of its releases repositories and the
// items of its request sources it takes as input to planning
// (docs/plans/done/brief-requests.md, docs/plans/requests-and-chat.md). For
// display, how far each got; for the Brief page's buttons, the releases list
// that accepts or declines an issue, the request source entry that does so for
// an item, and what sets a releases entry's requests. The CRD judges every value.

import {
  findCondition,
  type Brief,
  type BriefRelease,
  type BriefRequest,
  type BriefRequestSource,
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

/**
 * Which request: a GitHub issue by its repository and number, or an item of a
 * spec.requestSources entry by its source and key (and its number, the digits of the key).
 */
export type RequestRef = { repository: string; number: number } | { source: string; key: string; number: number }

/** A request as its buttons and lines name it: "ada/cel-lens #12" for an issue, "product:ENG-412" for an item. */
export function requestLabel(ref: RequestRef): string {
  return 'source' in ref ? `${ref.source}:${ref.key}` : `${ref.repository} #${ref.number}`
}

/** A request, for display. */
export interface RequestView {
  /** owner/name#number, or source:key, as status.needsYou names it. */
  key: string
  /** "owner/name #12", or "product:ENG-412". */
  label: string
  /** The GitHub repository; unset for an item of a request source. */
  repository?: string
  /** The request source an item is from; unset for a GitHub issue. */
  source?: string
  number: number
  title: string
  /** A GitHub login, or the display name of whoever made the item; unset when nobody is named. */
  author?: string
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
  /** Whether an entry of spec.releases, or a request source, takes requests. */
  taking: boolean
  /** Whether a request source is among the places it takes them from. */
  fromSources: boolean
  /** The Requests condition while False: why the issues can't be read or answered. */
  failure?: { reason: string; message: string }
  /** The Requests condition while True: which repositories it reads. */
  reading?: string
  /**
   * The answered numbers whose issues are closed, by repository in lower
   * case; an answer drops them, so accepted and declined stay under the CRD's cap.
   */
  settled: Record<string, number[]>
  /** The same by request source, under the source's name. */
  settledSources: Record<string, number[]>
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

/**
 * The address when it is an http or https one with a host, so a link never runs
 * a script and never points at nothing (#163). Every link built from a
 * tracker's address goes through it: the request list's and Needs you's.
 */
export function httpUrl(url: string | undefined): string | undefined {
  return url && /^https?:\/\/[^\s/?#\\]/i.test(url) ? url : undefined
}

export function describeRequest(r: BriefRequest): RequestView {
  const stage = STAGES[r.stage] ?? { label: r.stage, tone: 'unknown' as Tone }
  // A request names its repository, or its source and key; one that names
  // neither, which the CRD refuses, still reads as its number.
  const named = r.source
    ? { key: `${r.source}:${r.key ?? r.number}`, label: `${r.source}:${r.key ?? r.number}` }
    : r.repository
      ? { key: `${r.repository}#${r.number}`, label: `${r.repository} #${r.number}` }
      : { key: `#${r.number}`, label: `#${r.number}` }
  return {
    ...named,
    repository: r.repository || undefined,
    source: r.source || undefined,
    number: r.number,
    title: r.title,
    author: r.author?.trim() || undefined,
    url: httpUrl(r.url),
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
    taking:
      (brief.spec.releases ?? []).some((r) => takesRequests(r.requests)) ||
      (brief.spec.requestSources ?? []).some(takesSourceRequests),
    fromSources: (brief.spec.requestSources ?? []).some(takesSourceRequests),
    failure:
      condition?.status === 'False'
        ? { reason: condition.reason ?? 'Unknown', message: condition.message?.trim() ?? '' }
        : undefined,
    reading: condition?.status === 'True' ? condition.message?.trim() || undefined : undefined,
    settled: Object.fromEntries(
      (status.requestsSettled ?? []).map((s) => [s.repository.toLowerCase(), s.numbers ?? []]),
    ),
    settledSources: Object.fromEntries((status.requestSourcesSettled ?? []).map((s) => [s.source, s.numbers ?? []])),
  }
}

/** Whether requests set so read the repository's issues. */
export function takesRequests(requests: BriefRequests | undefined): boolean {
  return Boolean(requests?.mode && requests.mode !== 'Off')
}

/** Whether a request source reads items: unset mode is Ask, the CRD's default. */
export function takesSourceRequests(source: BriefRequestSource): boolean {
  return source.mode !== 'Off'
}

/** "owner/name#12" as its repository and number; undefined when it isn't one. */
export function splitRequest(name: string): { repository: string; number: number } | undefined {
  const m = /^([^/#\s]+\/[^/#\s]+)#(\d+)$/.exec(name)
  return m ? { repository: m[1], number: Number(m[2]) } : undefined
}

/**
 * "product:ENG-412" as its source and key; undefined when it isn't one. A
 * source's name is a DNS label, so the first colon ends it, and no
 * "owner/name#12" has a colon.
 */
export function splitSourceRequest(name: string): { source: string; key: string } | undefined {
  const m = /^([a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?):(\S{1,120})$/.exec(name)
  return m ? { source: m[1], key: m[2] } : undefined
}

/**
 * The request status.needsYou names: an issue by "owner/name#12", an item by
 * "source:key". An item's number is the listed request's, else the digits at
 * the end of its key (ENG-412); undefined when neither names one, so no
 * button answers for a number the page guessed.
 */
export function requestOfName(name: string, listed: BriefRequest[] = []): RequestRef | undefined {
  const issue = splitRequest(name)
  if (issue) return issue
  const item = splitSourceRequest(name)
  if (!item) return undefined
  const found = listed.find((r) => r.source === item.source && r.key === item.key)
  const number = found?.number ?? Number(/^[A-Za-z][A-Za-z0-9_]*-(\d+)$/.exec(item.key)?.[1])
  return number >= 1 ? { ...item, number } : undefined
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

/**
 * An answer to an item of a request source: the entry as the page read it,
 * which the write tests, and its accepted and declined as they become, each
 * undefined when it ends up with no number and is taken out of the entry.
 */
export interface SourceAnswer {
  /** The entry's place in spec.requestSources. */
  index: number
  entry: BriefRequestSource
  accepted?: number[]
  declined?: number[]
}

/**
 * The answer that adds number to the accepted (accept) or declined (decline)
 * numbers of the request source called `source`, and takes it out of the other
 * list; as answerRequest does, it also drops the settled numbers, those of
 * closed items, which keeps both lists under the CRD's cap of 200. Undefined
 * when no entry of that name reads items.
 */
export function answerSourceRequest(
  sources: BriefRequestSource[],
  source: string,
  number: number,
  answer: RequestAnswer,
  settled: number[] = [],
): SourceAnswer | undefined {
  const index = sources.findIndex((s) => s.name === source)
  if (index < 0 || !takesSourceRequests(sources[index])) return undefined
  const entry = sources[index]
  const [to, from] = answer === 'accept' ? (['accepted', 'declined'] as const) : (['declined', 'accepted'] as const)
  const keep = (n: number) => n !== number && !settled.includes(n)
  const rest = (entry[from] ?? []).filter(keep)
  const next: SourceAnswer = { index, entry, [to]: [...(entry[to] ?? []).filter(keep), number] }
  if (rest.length) next[from] = rest
  return next
}

/** How the request source answers the item already, if it does. */
export function sourceRequestAnswered(
  sources: BriefRequestSource[],
  source: string,
  number: number,
): 'accepted' | 'declined' | undefined {
  const entry = sources.find((s) => s.name === source)
  if (entry?.declined?.includes(number)) return 'declined'
  if (entry?.accepted?.includes(number)) return 'accepted'
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

/** An entry's requests in one line, for the settings: "Ask, from ada and acme/maintainers". */
export function requestsSummary(requests: BriefRequests | undefined): string {
  if (!takesRequests(requests)) return 'takes no requests'
  const who = [...(requests!.authors ?? []), ...(requests!.teams ?? [])]
  const names = who.length > 1 ? `${who.slice(0, -1).join(', ')} and ${who.at(-1)}` : (who[0] ?? 'nobody')
  return `requests ${requests!.mode}, from ${names}`
}
