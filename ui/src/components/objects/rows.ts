// What the object pages list, worked out from status: a Brief's rounds as a
// timeline, a Container's Relationships, and a Brief's planner limit.

import type { Agent, Brief, Relationship } from '../../k8s/types'
import type { ChildRef, TimelineEntry } from '../ObjectHeader'

/** The sessionTokenLimit of the Brief's planner Agent, when known. */
export function plannerLimit(agents: Agent[], brief: Brief): number | undefined {
  return agents.find((a) => a.metadata.name === brief.spec.agents?.planner?.name)?.spec.sessionTokenLimit
}

const OUTCOMES = {
  Applied: { title: 'applied', tone: 'ok' },
  Tended: { title: 'tended its failed issues', tone: 'ok' },
  PlannerNeedsInput: { title: 'asked you', tone: 'waiting' },
  RoundFailed: { title: 'failed', tone: 'error' },
} as const

/** The Brief's rounds, oldest first, as the header's timeline. */
export function briefTimeline(brief: Brief): TimelineEntry[] {
  return (brief.status?.history ?? []).map((h) => {
    const outcome = h.outcome ? OUTCOMES[h.outcome] : undefined
    return {
      key: String(h.round),
      title: `Round ${h.round}${outcome ? ` ${outcome.title}` : ''}`,
      detail: h.needsInput ?? h.summary,
      tone: outcome?.tone,
    }
  })
}

/** The Container's Relationships, each with why it is or isn't in the scaffold. */
export function relationshipsOf(relationships: Relationship[], container: string): ChildRef[] {
  return relationships
    .filter(
      (r) =>
        (r.spec.source.kind === 'Container' && r.spec.source.name === container) ||
        (r.spec.target.kind === 'Container' && r.spec.target.name === container),
    )
    .map((r) => {
      const applied = r.status?.conditions?.find((c) => c.type === 'Applied')
      const other =
        r.spec.source.kind === 'Container' && r.spec.source.name === container ? r.spec.target : r.spec.source
      const arrow = other === r.spec.target ? `to ${other.kind} ${other.name}` : `from ${other.kind} ${other.name}`
      return {
        kind: 'Relationship',
        name: r.metadata.name,
        note: `${arrow} · Applied: ${applied ? `${applied.status}${applied.reason ? `, ${applied.reason}` : ''}` : 'not reported yet'}`,
      }
    })
}
