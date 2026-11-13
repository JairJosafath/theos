// What a Crew's page says of the schedules its Container's Standard proposes
// (design.md, "Schedules"; docs/plans/crew-schedules.md, step 6): which the
// Crew is offered, which names it already uses, and where its schedule of a
// name differs from the proposal. The only link between the two is the name,
// so this says "in use", never where a schedule came from: one of a
// proposal's name may be the proposal taken, one a person wrote, or one taken
// from another Standard.

import type { Container, Crew, CrewSchedule, Standard } from '../k8s/types'
import { choicesOf } from './choices'
import { compareText } from './order'

/** The fields of a schedule a Crew may have changed, in the order the page shows them. */
export type ScheduleField = 'cron' | 'timeZone' | 'goal'

/** One field in which a Crew's schedule differs from the proposal of its name. */
export interface ScheduleDifference {
  field: ScheduleField
  /** What the Crew's schedule has. */
  crew: string
  /** What the proposal has. */
  proposal: string
}

/** A proposal whose name the Crew uses, beside the Crew's schedule of that name. */
export interface ProposalInUse {
  proposal: CrewSchedule
  schedule: CrewSchedule
  /** Empty when the two have the same cron, time zone and goal. */
  differs: ScheduleDifference[]
}

/** A Standard's proposals as one Crew's page shows them, each list by name. */
export interface ScheduleProposals {
  /** The Standard of the Crew's Container, by name; undefined when it has none or the Container isn't found. */
  standard?: string
  /** The proposals whose name is none of the Crew's schedules: a person may take one. */
  offered: CrewSchedule[]
  /** The proposals whose name the Crew uses. */
  inUse: ProposalInUse[]
}

/** The zone a schedule runs in: UTC when it names none, as the CRD defaults it. */
export function scheduleZone(schedule: CrewSchedule): string {
  return schedule.timeZone || 'UTC'
}

/**
 * The zone a Standard wrote into `proposal`, or undefined when it reads UTC.
 * The API server writes UTC into a proposal that names no zone, so UTC there
 * may be nobody's choice; any other zone was written.
 */
export function writtenZone(proposal: CrewSchedule): string | undefined {
  const zone = scheduleZone(proposal)
  return zone === 'UTC' ? undefined : zone
}

/**
 * Where `schedule` differs from `proposal`: the cron as written, the time zone
 * (UTC for one that names none) and the goal, whose leading and trailing
 * white space doesn't count, since YAML's block styles end a text differently.
 */
export function scheduleDifferences(schedule: CrewSchedule, proposal: CrewSchedule): ScheduleDifference[] {
  const pairs: [ScheduleField, string, string][] = [
    ['cron', schedule.cron, proposal.cron],
    ['timeZone', scheduleZone(schedule), scheduleZone(proposal)],
    ['goal', schedule.goal.trim(), proposal.goal.trim()],
  ]
  return pairs.filter(([, crew, prop]) => crew !== prop).map(([field, crew, prop]) => ({ field, crew, proposal: prop }))
}

/**
 * The proposals of the Standard of `crew`'s Container, in `crew`'s namespace:
 * offered when the Crew has no schedule of the name, in use when it has one.
 * Both lists are by name in code-point order (compareText), the same for
 * every reader. Nothing here is a rule the page enforces: the CRD decides
 * whether a taken schedule is accepted.
 */
export function scheduleProposals(crew: Crew, containers: Container[], standards: Standard[]): ScheduleProposals {
  const namespace = crew.metadata.namespace ?? ''
  const inNamespace = (o: { metadata: { namespace?: string } }) => (o.metadata.namespace ?? '') === namespace
  const container = containers.find((c) => inNamespace(c) && c.metadata.name === crew.spec.containerRef.name)
  const name = container ? choicesOf(container).standard : undefined
  const standard = name ? standards.find((s) => inNamespace(s) && s.metadata.name === name) : undefined
  const out: ScheduleProposals = { standard: name, offered: [], inUse: [] }
  const proposals = [...(standard?.spec.schedules ?? [])].sort((a, b) => compareText(a.name, b.name))
  for (const proposal of proposals) {
    const schedule = crew.spec.schedules?.find((s) => s.name === proposal.name)
    if (schedule) out.inUse.push({ proposal, schedule, differs: scheduleDifferences(schedule, proposal) })
    else out.offered.push(proposal)
  }
  return out
}
