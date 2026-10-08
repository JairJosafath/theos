// Plain-language wording for newcomers: phases, conditions, times and names.
// Nothing here assumes the reader knows Kubernetes.

import type { Condition, ContainerPhase, ContainerType, CrewPhase, SessionPhase } from '../k8s/types'

/** displayName first; fall back to the stable metadata.name. */
export function titleOf(obj: { metadata: { name: string }; spec?: { displayName?: string } }): string {
  return obj.spec?.displayName?.trim() || obj.metadata.name
}

export type Tone = 'ok' | 'progress' | 'waiting' | 'error' | 'unknown'

export interface PhaseInfo {
  label: string
  tone: Tone
  explanation: string
}

/** The Container phase as a badge label, colour tone and one-line explanation. */
export function describeContainerPhase(phase: ContainerPhase | undefined): PhaseInfo {
  switch (phase) {
    case 'Ready':
      return { label: 'Ready', tone: 'ok', explanation: 'Its repository exists and the starter code is pushed.' }
    case 'Scaffolding':
      return {
        label: 'Scaffolding',
        tone: 'progress',
        explanation: 'The factory is creating its repository and starter code right now.',
      }
    case 'Pending':
      return {
        label: 'Pending',
        tone: 'waiting',
        explanation: 'Waiting: something it needs does not exist yet.',
      }
    case 'Failed':
      return { label: 'Failed', tone: 'error', explanation: 'The factory stopped. The checks below say why.' }
    default:
      return {
        label: 'No status yet',
        tone: 'unknown',
        explanation: 'The factory has not reported on this container yet.',
      }
  }
}

/** The Crew phase as a badge label, colour tone and one-line explanation. */
export function describeCrewPhase(phase: CrewPhase | undefined): PhaseInfo {
  switch (phase) {
    case 'Working':
      return { label: 'Working', tone: 'progress', explanation: 'A Task works on one of its issues.' }
    case 'Idle':
      return { label: 'Idle', tone: 'ok', explanation: 'No issue is ready to pick up.' }
    case 'Paused':
      return { label: 'Paused', tone: 'waiting', explanation: 'Paused: it picks up no new issues.' }
    case 'Waiting':
      return {
        label: 'Waiting',
        tone: 'waiting',
        explanation: 'It can not pick up issues yet; its Ready check says why.',
      }
    default:
      return { label: 'No status yet', tone: 'unknown', explanation: 'The factory has not reported on this crew yet.' }
  }
}

export function describeSessionPhase(phase: SessionPhase | undefined): { label: string; explanation: string } {
  switch (phase) {
    case 'Active':
      return { label: 'Active', explanation: 'Working now: heartbeats arrive every 30 seconds.' }
    case 'Stale':
      return { label: 'Stale', explanation: 'No heartbeat for over 2 minutes: probably stopped.' }
    default:
      return { label: 'Starting', explanation: 'Registered, but has not reported yet.' }
  }
}

const CONTAINER_TYPES: Record<ContainerType, string> = {
  'static-site': 'static site',
  service: 'service',
  job: 'job',
  worker: 'worker',
  'mobile-app': 'mobile app',
  cli: 'command-line tool',
  extension: 'editor extension',
  operator: 'Kubernetes operator',
}

/** The Container type in words, such as "static site". */
export function describeContainerType(type: string | undefined): string {
  return (type && CONTAINER_TYPES[type as ContainerType]) || type || 'unknown type'
}

export interface ConditionInfo {
  /** What the check means when it holds, e.g. "Its Git repository exists". */
  title: string
  tone: Tone
  /** The operator's message, when it adds something. */
  detail?: string
}

type Wording = { yes: string; no: string; unknown: string }

// What each condition means, per kind. Anything missing falls back to its type.
const WORDING: Record<string, Wording> = {
  ReferencesResolved: {
    yes: 'Everything it refers to exists',
    no: 'Something it refers to is missing',
    unknown: 'Not checked yet whether everything it refers to exists',
  },
  ChoicesValid: {
    yes: 'Its templates all support this type of container',
    no: 'One of its templates does not support this type of container',
    unknown: 'Its template choices have not been checked yet',
  },
  RepositoryCreated: {
    yes: 'Its Git repository has been created',
    no: 'Its Git repository has not been created yet',
    unknown: 'Its Git repository has not been created yet',
  },
  Scaffolded: {
    yes: 'The starter code has been pushed',
    no: 'The starter code has not been pushed yet',
    unknown: 'The starter code has not been pushed yet',
  },
  Applied: {
    yes: 'Built into the repository when it was scaffolded',
    no: 'Not built into the repository',
    unknown: 'Not known yet whether this is built into the repository',
  },
}

// Reasons that change the wording of a False condition.
const REASON_WORDING: Record<string, string> = {
  AddedAfterScaffold: 'Added after scaffolding, so the repository does not include it',
  WaitingForScaffold: 'Will be built in when the container is scaffolded',
  Scaffolding: 'Being scaffolded right now',
  ForeignCommits: 'The repository already had commits the factory did not make, so it stopped',
  LayerConflict: 'Two templates wanted to write the same file',
  UnpinnedRef: 'A template points to a branch instead of a fixed version',
  GitError: 'Talking to Git failed',
}

// Reasons that mean "not yet" rather than "wrong".
const WAITING_REASONS = new Set(['WaitingForScaffold', 'Scaffolding', 'NotFound'])

/** Turns a condition into a sentence and a tone for its icon. */
export function describeCondition(condition: Condition): ConditionInfo {
  const wording = WORDING[condition.type]
  const detail = condition.message?.trim() || undefined
  if (condition.status === 'True') {
    return { title: wording?.yes ?? `${splitWords(condition.type)}: yes`, tone: 'ok', detail }
  }
  if (condition.status === 'False') {
    const reasonText = condition.reason ? REASON_WORDING[condition.reason] : undefined
    const tone: Tone = condition.reason && WAITING_REASONS.has(condition.reason) ? 'waiting' : 'error'
    return { title: reasonText ?? wording?.no ?? `${splitWords(condition.type)}: no`, tone, detail }
  }
  return { title: wording?.unknown ?? `${splitWords(condition.type)}: not known yet`, tone: 'unknown', detail }
}

/** "ReferencesResolved" becomes "References resolved". */
export function splitWords(identifier: string): string {
  const words = identifier.replace(/([a-z0-9])([A-Z])/g, '$1 $2').toLowerCase()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/**
 * How long ago a time was, in short words: "12 s ago", "3 min ago", "2 h ago",
 * "4 d ago". Times in the future (clock skew) read as "just now".
 */
export function timeAgo(iso: string | undefined, now: number): string {
  if (!iso) return 'never'
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return 'unknown'
  const seconds = Math.floor((now - then) / 1000)
  if (seconds < 1) return 'just now'
  if (seconds < 60) return `${seconds} s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 48) return `${hours} h ago`
  return `${Math.floor(hours / 24)} d ago`
}

/** "1 container", "2 containers". */
export function plural(count: number, one: string, many = `${one}s`): string {
  return `${count} ${count === 1 ? one : many}`
}

/** "python-fastapi@v1.2.0" split into template name and version. */
export function splitTemplateRef(ref: string): { name: string; version?: string } {
  const at = ref.lastIndexOf('@')
  return at > 0 ? { name: ref.slice(0, at), version: ref.slice(at + 1) } : { name: ref }
}
