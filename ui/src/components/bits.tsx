// Small building blocks shared by the cards and panels.

import type { Condition, ContainerPhase, Session } from '../k8s/types'
import {
  describeCondition,
  describeContainerPhase,
  describeSessionPhase,
  splitWords,
  timeAgo,
  type Tone,
} from '../model/describe'
import { AgentIcon, CheckIcon, ClockIcon, CrossIcon, HumanIcon, QuestionIcon } from './icons'

export function PhaseBadge({ phase, size = 'md' }: { phase: ContainerPhase | undefined; size?: 'sm' | 'md' }) {
  const info = describeContainerPhase(phase)
  return (
    <span className={`badge badge-${info.tone} badge-${size}`} title={info.explanation}>
      <span className="badge-dot" aria-hidden="true" />
      {info.label}
    </span>
  )
}

export function ActorIcon({ actor, size = 16 }: { actor: Session['spec']['actor']; size?: number }) {
  return actor === 'agent' ? <AgentIcon size={size} title="Agent" /> : <HumanIcon size={size} title="Human" />
}

function ToneIcon({ tone }: { tone: Tone }) {
  switch (tone) {
    case 'ok':
      return <CheckIcon size={14} />
    case 'error':
      return <CrossIcon size={14} />
    case 'waiting':
    case 'progress':
      return <ClockIcon size={14} />
    default:
      return <QuestionIcon size={14} />
  }
}

/** A list of conditions, each as a plain sentence with the technical names beside it. */
export function ConditionList({ conditions, empty }: { conditions: Condition[]; empty: string }) {
  if (!conditions.length) return <p className="muted small">{empty}</p>
  return (
    <ul className="checks">
      {conditions.map((c) => {
        const info = describeCondition(c)
        return (
          <li key={c.type} className={`check check-${info.tone}`}>
            <span className="check-icon" aria-hidden="true">
              <ToneIcon tone={info.tone} />
            </span>
            <span className="check-body">
              <span className="check-title">{info.title}</span>
              {info.detail ? <span className="check-detail">{info.detail}</span> : null}
              <span className="check-tech">
                {splitWords(c.type)}: {c.status === 'True' ? 'yes' : c.status === 'False' ? 'no' : 'unknown'}
                {c.reason ? ` · ${c.reason}` : ''}
              </span>
            </span>
          </li>
        )
      })}
    </ul>
  )
}

/** One Session: who (agent or human), what, and how recently they checked in. */
export function SessionRow({ session, now, compact = false }: { session: Session; now: number; compact?: boolean }) {
  const phase = session.status?.phase
  const info = describeSessionPhase(phase)
  const stale = phase === 'Stale'
  const actor = session.spec.actor === 'agent' ? 'Agent' : 'Human'
  return (
    <li className={`session ${stale ? 'session-stale' : ''} ${compact ? 'session-compact' : ''}`}>
      <span className={`session-avatar session-avatar-${session.spec.actor}`}>
        <ActorIcon actor={session.spec.actor} size={compact ? 14 : 16} />
      </span>
      <span className="session-body">
        <span className="session-summary">{session.spec.summary || 'No summary given'}</span>
        <span className="session-meta">
          {actor}
          <span className="sep">·</span>
          <span className={`session-phase session-phase-${phase ?? 'unknown'}`} title={info.explanation}>
            {info.label}
          </span>
          <span className="sep">·</span>
          <span title={session.status?.lastHeartbeat ?? ''}>
            {session.status?.lastHeartbeat
              ? `heartbeat ${timeAgo(session.status.lastHeartbeat, now)}`
              : 'no heartbeat yet'}
          </span>
        </span>
        {session.status?.openInEditor ? (
          <a className="session-open" href={session.status.openInEditor} target="_blank" rel="noreferrer">
            Open in editor
          </a>
        ) : null}
      </span>
    </li>
  )
}
