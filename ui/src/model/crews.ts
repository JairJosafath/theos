// Crews as the views show them: who implements and reviews, what each is doing,
// and the running Tasks of a project's Crews.

import type { Agent, Crew, CrewPhase, Task, TaskPhase } from '../k8s/types'
import { findCondition } from '../k8s/types'
import { plural, titleOf } from './describe'
import { agentRole, taskHeadline, taskRoles, type AgentRole } from './factory'

/** A Crew as the views show it: who implements and reviews, and what it is doing. */
export interface CrewSummary {
  /** namespace/name */
  key: string
  namespace: string
  name: string
  title: string
  description?: string
  container: string
  phase?: CrewPhase
  /** spec.paused: it picks up no new issues, though a Task already started runs to its end. */
  paused: boolean
  /** Set when the Ready condition is False: the Crew can't pick up issues, and why. */
  notReady?: { reason?: string; message?: string }
  implementer: AgentRole
  reviewer: AgentRole
  /** spec.parallel: how many Tasks it runs at once (1 when unset). */
  parallel: number
  /** The Tasks working on the Container now, one per place in use. */
  active: { name: string; key: string; issue?: number; headline?: string; found: boolean }[]
  /** Ready issues in pick-up order, and ready ones blocked by open dependencies. */
  ready: number[]
  waiting: number[]
  lastPolled?: string
}

export function crewSummary(crew: Crew, agents: Agent[], tasks: Task[]): CrewSummary {
  const namespace = crew.metadata.namespace ?? ''
  const readyCondition = findCondition(crew.status?.conditions, 'Ready')
  return {
    key: `${namespace}/${crew.metadata.name}`,
    namespace,
    name: crew.metadata.name,
    title: titleOf(crew),
    description: crew.spec.description?.trim() || undefined,
    container: crew.spec.containerRef.name,
    phase: crew.status?.phase,
    paused: Boolean(crew.spec.paused),
    notReady:
      readyCondition?.status === 'False'
        ? { reason: readyCondition.reason, message: readyCondition.message?.trim() || undefined }
        : undefined,
    implementer: agentRole(agents, namespace, crew.spec.implementer.name),
    reviewer: agentRole(agents, namespace, crew.spec.reviewer.name),
    parallel: crew.spec.parallel || 1,
    active: (crew.status?.activeTasks ?? []).map((name) => {
      const task = tasks.find((t) => t.metadata.name === name && (t.metadata.namespace ?? '') === namespace)
      return {
        name,
        key: `${namespace}/${name}`,
        issue: task?.spec.issue || undefined,
        headline: task ? taskHeadline(task).text : undefined,
        found: Boolean(task),
      }
    }),
    ready: crew.status?.ready ?? [],
    waiting: crew.status?.waiting ?? [],
    lastPolled: crew.status?.lastPolled,
  }
}

/** Every Crew, by project and then by name. */
export function crewSummaries(crews: Crew[], agents: Agent[], tasks: Task[]): CrewSummary[] {
  return crews
    .map((c) => crewSummary(c, agents, tasks))
    .sort((a, b) => a.namespace.localeCompare(b.namespace) || a.title.localeCompare(b.title))
}

/** What a Crew is doing, in a few words: "working on #4", "paused", "no ready issues". */
export function crewDoing(crew: CrewSummary): string {
  if (crew.active.length) return `working on ${crew.active.map((a) => (a.issue ? `#${a.issue}` : a.name)).join(', ')}`
  if (crew.notReady) return 'can’t pick up issues'
  if (crew.paused || crew.phase === 'Paused') return 'paused'
  if (crew.phase === 'Idle') {
    return crew.waiting.length
      ? `no ready issues; ${plural(crew.waiting.length, 'waits', 'wait')} on open dependencies`
      : 'no ready issues'
  }
  if (crew.phase === 'Waiting') return 'waiting'
  return 'not started yet'
}

/** "#5, #6": issue numbers as a list. */
export function issueList(numbers: number[]): string {
  return numbers.map((n) => `#${n}`).join(', ')
}

/** A Crew's running Task as the "who's working" panel shows it: who works on it now, with their model. */
export interface ActiveCrewTask {
  /** namespace/name */
  key: string
  name: string
  crew: string
  container: string
  issue?: number
  issueTitle?: string
  phase?: TaskPhase
  /** The implementer while the Task implements, the reviewer while it reviews. */
  agent: AgentRole
}

/** The running Tasks of the Crews of the given Containers, by Container and then issue. */
export function activeCrewTasks(crews: Crew[], tasks: Task[], agents: Agent[], containers: string[]): ActiveCrewTask[] {
  const inScope = new Set(containers)
  const found: ActiveCrewTask[] = []
  for (const crew of crews) {
    const namespace = crew.metadata.namespace ?? ''
    if (!inScope.has(crew.spec.containerRef.name)) continue
    for (const name of crew.status?.activeTasks ?? []) {
      const task = tasks.find((t) => t.metadata.name === name && (t.metadata.namespace ?? '') === namespace)
      if (!task) continue
      const roles = taskRoles(task, agents)
      found.push({
        key: `${namespace}/${name}`,
        name,
        crew: crew.metadata.name,
        container: crew.spec.containerRef.name,
        issue: task.spec.issue || undefined,
        issueTitle: task.status?.issueTitle?.trim() || undefined,
        phase: task.status?.phase,
        agent: task.status?.phase === 'Reviewing' ? roles.reviewer : roles.implementer,
      })
    }
  }
  return found.sort((a, b) => a.container.localeCompare(b.container) || (a.issue ?? 0) - (b.issue ?? 0))
}
