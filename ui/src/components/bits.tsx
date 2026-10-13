// Small building blocks shared by the cards and panels.

import type { Condition, ContainerPhase, CrewPhase, Session, StandardFile } from '../k8s/types'
import {
  describeCondition,
  describeContainerPhase,
  describeCrewPhase,
  describeSessionPhase,
  splitWords,
  timeAgo,
  type Tone,
} from '../model/describe'
import type { UsageView } from '../model/brief'
import { absoluteTime } from '../model/format'
import { useNow } from '../lib/hooks'
import type { AgentRole } from '../model/factory'
import { AgentIcon, CheckIcon, ClockIcon, CrossIcon, HumanIcon, QuestionIcon } from './icons'
import './bits.css'

/**
 * A moment, relative and absolute: "12 min ago", with the date and time on
 * hover and to a screen reader, or shown beside it with `full`. It follows
 * the clock itself, so only it re-renders as time passes.
 */
export function When({ at, full }: { at?: string | number; full?: boolean }) {
  const now = useNow()
  const abs = absoluteTime(at)
  if (!abs) return <>{timeAgo(typeof at === 'string' ? at : undefined, now)}</>
  const iso = typeof at === 'number' ? new Date(at).toISOString() : at!
  return (
    <time dateTime={iso} title={abs}>
      {timeAgo(iso, now)}
      {full ? <> ({abs})</> : <span className="visually-hidden"> ({abs})</span>}
    </time>
  )
}

export function PhaseBadge({ phase, size = 'md' }: { phase: ContainerPhase | undefined; size?: 'sm' | 'md' }) {
  const info = describeContainerPhase(phase)
  return (
    <span className={`badge badge-${info.tone} badge-${size}`} title={info.explanation}>
      <span className="badge-dot" aria-hidden="true" />
      {info.label}
    </span>
  )
}

export function CrewPhaseBadge({ phase }: { phase: CrewPhase | undefined }) {
  const info = describeCrewPhase(phase)
  return (
    <span className={`badge badge-${info.tone} badge-sm`} title={info.explanation}>
      <span className="badge-dot" aria-hidden="true" />
      {info.label}
    </span>
  )
}

/** An Agent's name with the model it runs, such as "Claude Code, Sonnet" claude-sonnet-5-5. */
export function AgentName({ role }: { role: AgentRole }) {
  return (
    <span className="agent-name">
      <span title={role.found ? role.name : `Agent ${role.name} was not found`}>{role.title}</span>
      {role.model ? <span className="model">{role.model}</span> : null}
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
export function SessionRow({
  session,
  compact = false,
  href,
}: {
  session: Session
  compact?: boolean
  /** Its object page. */
  href?: string
}) {
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
        <span className="session-summary">
          {href ? (
            <a href={href}>{session.spec.summary || 'No summary given'}</a>
          ) : (
            session.spec.summary || 'No summary given'
          )}
        </span>
        <span className="session-meta">
          {actor}
          <span className="sep">·</span>
          <span className={`session-phase session-phase-${phase ?? 'unknown'}`} title={info.explanation}>
            {info.label}
          </span>
          <span className="sep">·</span>
          <span>
            {session.status?.lastHeartbeat ? (
              <>
                heartbeat <When at={session.status.lastHeartbeat} />
              </>
            ) : (
              'no heartbeat yet'
            )}
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

/** What agent runs used, on one line: cost · tokens · largest run · handoffs, the largest run marked when it passed the limit. */
export function UsageLine({ usage }: { usage: UsageView }) {
  const parts = [
    usage.cost ? <span key="cost">{usage.cost}</span> : null,
    usage.tokens ? <span key="tokens">{usage.tokens}</span> : null,
    usage.largestRun ? (
      <span
        key="largest"
        className={usage.pastLimit ? 'usage-past' : undefined}
        title={
          usage.pastLimit
            ? 'An agent run got longer than its Agent should let it; agents get sloppier in long runs'
            : 'The prompt of the biggest turn of any agent run'
        }
      >
        {usage.largestRun}
      </span>
    ) : null,
    usage.handoffs ? (
      <span key="handoffs" title="Agent runs that got long handed their work to a fresh run">
        {usage.handoffs}
      </span>
    ) : null,
  ].filter(Boolean)
  return (
    <span className="usage">
      {parts.map((p, i) => (
        <span key={i}>
          {i ? <span className="sep">·</span> : null}
          {p}
        </span>
      ))}
    </span>
  )
}

/**
 * The Standard's files left out, each with its ref and, collapsed, what the
 * Standard changed in it and the lines a take drops, as status says them.
 */
export function LeftOutFiles({ files, className }: { files: StandardFile[]; className?: string }) {
  return (
    <ul className={['left-out', className].filter(Boolean).join(' ')}>
      {files.map((f) => (
        <li key={f.path}>
          <span className="mono">{f.path}</span> <span className="muted">at {f.ref}</span>
          {f.change ? (
            <details className="left-out-change">
              <summary>What the Standard changed, and what taking it drops</summary>
              <pre className="pre">{f.change}</pre>
            </details>
          ) : null}
        </li>
      ))}
    </ul>
  )
}
