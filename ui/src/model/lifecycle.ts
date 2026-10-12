// Each kind's lifecycle (docs/plans/done/ui-lifecycle.md, decision 3; #705's table,
// "Lifecycle of each level"): the phases on its path, the side exits off it,
// what moves it along, and what a person does at each step. Object pages read
// it from here, so no page invents its own. Pure, and read only from status.

import type {
  Brief,
  BriefPhase,
  Condition,
  Container,
  ContainerPhase,
  Crew,
  CrewPhase,
  Relationship,
  SessionPhase,
  Task,
  TaskPhase,
} from '../k8s/types'
import { findCondition } from '../k8s/types'
import { describeBriefPhase } from './brief'
import {
  describeContainerPhase,
  describeCrewPhase,
  describeSessionPhase,
  describeTaskPhase,
  type Tone,
} from './describe'

export type LifecycleKind =
  | 'Brief'
  | 'Container'
  | 'Crew'
  | 'Task'
  | 'Project'
  | 'SoftwareSystem'
  | 'Relationship'
  | 'Session'
  | 'Standard'
  | 'Compute'
  | 'Deployment'
  | 'GitProvider'

interface StepDef {
  phase: string
  /** What a person does while the object is at this step. */
  person: string
}

interface LifecycleDef {
  path: StepDef[]
  /** Phases off the path, such as Failed. */
  exits: StepDef[]
  /** What moves the object along its path. */
  moves: string
  describe: (phase: string | undefined) => { label: string; tone: Tone; explanation: string }
  /** For a kind with no phases (an empty path): what a person does with it, always. */
  still?: string
}

/** A kind with no phases: no path, no exits, and the same thing to do always. */
function phaseless(moves: string, still: string): LifecycleDef {
  return {
    path: [],
    exits: [],
    moves,
    still,
    describe: () => ({ label: '', tone: 'unknown', explanation: '' }),
  }
}

const SESSION_TONES: Record<SessionPhase, Tone> = { Active: 'ok', Stale: 'waiting' }

/** The Applied reasons of a Container to DataStore Relationship, as steps. */
const APPLIED: Record<string, { label: string; tone: Tone; explanation: string }> = {
  WaitingForScaffold: {
    label: 'Waiting for the scaffold',
    tone: 'waiting',
    explanation: 'Its Connector is applied when the Container is scaffolded.',
  },
  AppliedAtScaffold: {
    label: 'Applied at the scaffold',
    tone: 'ok',
    explanation: 'Its Connector was part of the Container’s scaffold.',
  },
  AddedAfterScaffold: {
    label: 'Added after the scaffold',
    tone: 'waiting',
    explanation: 'The Container was scaffolded before it existed; the factory scaffolds once, so it is not applied.',
  },
}

const CATALOG_STILL =
  'Nothing: Containers name it, and it is rendered into their repositories when they are scaffolded. A new template ref reaches only new Containers.'

const LIFECYCLES: Record<LifecycleKind, LifecycleDef> = {
  Brief: {
    path: [
      {
        phase: 'Pending',
        person: 'Nothing yet: the first round starts by itself. You can add a prompt, or pause the Brief.',
      },
      {
        phase: 'Planning',
        person: 'Nothing: wait for the round. A prompt you add now waits for the next round.',
      },
      {
        phase: 'Applying',
        person: 'Nothing: wait for the round. A prompt you add now waits for the next round.',
      },
      {
        phase: 'Planned',
        person:
          'Add a prompt to steer the next round; take or skip suggestions; answer what needs you; pause or resume.',
      },
    ],
    exits: [
      { phase: 'NeedsInput', person: "Answer the planner's question in a prompt; it starts the next round." },
      { phase: 'Failed', person: 'Read why the round failed, then add a prompt; it starts the next round.' },
    ],
    moves: 'A new prompt starts a round; with autopilot on, the Brief starts tending rounds by itself.',
    describe: (phase) => describeBriefPhase(phase as BriefPhase | undefined),
  },
  Container: {
    path: [
      { phase: 'Pending', person: 'Nothing, unless a condition below names something missing: create it.' },
      { phase: 'Scaffolding', person: 'Nothing: the factory is creating the repository.' },
      {
        phase: 'Ready',
        person: 'Work in its repository. When a Standard update pull request leaves files out, bring them up to date.',
      },
    ],
    exits: [{ phase: 'Failed', person: 'Fix what the failed condition names, then retry the scaffold.' }],
    moves: 'Scaffolded once, when created; a bump of its Standard’s ref opens an update pull request.',
    describe: (phase) => describeContainerPhase(phase as ContainerPhase | undefined),
  },
  Crew: {
    path: [
      {
        phase: 'Waiting',
        person:
          'Fix what its Ready condition names, such as a Container that isn’t Ready yet; the Crew goes on by itself.',
      },
      {
        phase: 'Idle',
        person:
          'Nothing is ready to pick up. Label an issue with acceptance criteria status/ready in its Container’s repository, and the Crew takes it.',
      },
      { phase: 'Working', person: 'Nothing: its Tasks run. Follow them below.' },
    ],
    exits: [
      {
        phase: 'Paused',
        person: 'Resume it when it should pick up issues again; a Task already started runs to its end.',
      },
    ],
    moves:
      'Ready issues and free places: it starts a Task per ready issue, urgent and lowest number first, as many at once as its parallel count.',
    describe: (phase) => describeCrewPhase(phase as CrewPhase | undefined),
  },
  Task: {
    path: [
      { phase: 'Pending', person: 'Nothing: it waits for its Container, an earlier Task or a free agent slot.' },
      { phase: 'Implementing', person: 'Nothing: the agent writes the change in a sandbox.' },
      {
        phase: 'Checking',
        person: 'Nothing: CI runs on the pull request; failed checks go back to the agent in the next round.',
      },
      {
        phase: 'Reviewing',
        person: 'Nothing: an agent reviews the pull request; changes it asks for start the next round.',
      },
      { phase: 'Merged', person: 'Nothing: the change is in. Take or skip the follow-up issues the review proposed.' },
    ],
    exits: [
      {
        phase: 'Blocked',
        person: 'Review and merge the Standard change it filed; the Task goes on when it merges.',
      },
      {
        phase: 'Failed',
        person: 'Read why it failed. To try again, give the work a new Task, or do it by hand.',
      },
    ],
    moves: 'The agent, CI and the reviewer, round by round, up to its implementer’s maxRounds.',
    describe: (phase) => describeTaskPhase(phase as TaskPhase | undefined),
  },
  Project: phaseless(
    'One per namespace, made by the chart or the planner; it counts what its namespace holds.',
    'Nothing: pick it to see its story. Its conditions say when something about the namespace is wrong.',
  ),
  SoftwareSystem: phaseless(
    'Part of the model; its counts follow its Containers.',
    'Nothing: its Containers are where the work is. Open one below.',
  ),
  Relationship: {
    path: [
      { phase: 'WaitingForScaffold', person: 'Nothing: its Connector is applied when the Container is scaffolded.' },
      { phase: 'AppliedAtScaffold', person: 'Nothing: its Connector is in the Container’s repository.' },
    ],
    exits: [
      {
        phase: 'AddedAfterScaffold',
        person:
          'Add what its Connector would have, by hand, in the Container’s repository: the factory scaffolds a Container once and never again.',
      },
    ],
    moves: 'Only a Container to DataStore relationship moves: the Container’s scaffold applies its Connector.',
    describe: (phase) =>
      (phase ? APPLIED[phase] : undefined) ?? {
        label: 'Not reported yet',
        tone: 'unknown',
        explanation: 'The factory has not reported whether it is applied.',
      },
  },
  Session: {
    path: [{ phase: 'Active', person: 'Open it in the editor to join the work.' }],
    exits: [
      {
        phase: 'Stale',
        person: 'Nothing: its client stopped sending heartbeats. It turns Active again when they come back.',
      },
    ],
    moves: 'Its client (c4session) sends heartbeats; the operator marks it Stale when they stop.',
    describe: (phase) => ({
      ...describeSessionPhase(phase as SessionPhase | undefined),
      tone: SESSION_TONES[phase as SessionPhase] ?? 'unknown',
    }),
  },
  Standard: phaseless(
    'A catalog entry. A new template ref opens an update pull request on each Ready Container that names it; the factory merges it once its checks pass.',
    'Nothing: its Containers’ update pull requests carry a new ref; open a Container to see its own.',
  ),
  Compute: phaseless('A catalog entry, rendered into a Container once, at its scaffold.', CATALOG_STILL),
  Deployment: phaseless('A catalog entry, rendered into a Container once, at its scaffold.', CATALOG_STILL),
  GitProvider: phaseless(
    'Where the factory creates each Container’s repository, with the credentials of the Secret it names.',
    'Nothing, unless a Container fails to create its repository: then check the Secret it names, in the cluster.',
  ),
}

/** Whether the kind has phases to show on a stepper. */
export function hasPhases(kind: LifecycleKind): boolean {
  return LIFECYCLES[kind].path.length > 0
}

/** Where a Relationship is on its lifecycle: its Applied condition's reason, when it has one. */
export function relationshipPhase(relationship: Relationship): string | undefined {
  return relationship.status?.conditions?.find((c) => c.type === 'Applied')?.reason
}

/** Whether the factory applies this Relationship: only a Container to DataStore one adds a Connector. */
export function appliesConnector(relationship: Relationship): boolean {
  return relationship.spec.source.kind === 'Container' && relationship.spec.target.kind === 'DataStore'
}

/** Whether the shell has an object page for this kind. */
export function hasPage(kind: string): kind is LifecycleKind {
  return Object.hasOwn(LIFECYCLES, kind)
}

/** done: passed; current: here now; next: still ahead; off: a side exit it isn't on. */
export type StepState = 'done' | 'current' | 'next' | 'off'

export interface StepView {
  phase: string
  label: string
  explanation: string
  tone: Tone
  state: StepState
}

export interface LifecycleView {
  kind: LifecycleKind
  path: StepView[]
  exits: StepView[]
  /** The phase the object reported, as a step; undefined before it reports one. */
  current?: StepView
  /** What a person does now. */
  person: string
  moves: string
}

/**
 * Where an object of this kind is on its lifecycle. On a side exit no step of
 * the path is marked done: status says which exit, not where it left the path.
 */
export function lifecycleOf(kind: LifecycleKind, phase: string | undefined): LifecycleView {
  const def = LIFECYCLES[kind]
  const at = def.path.findIndex((s) => s.phase === phase)
  const exit = def.exits.find((s) => s.phase === phase)
  const view = (s: StepDef, state: StepState): StepView => ({ phase: s.phase, ...def.describe(s.phase), state })
  const path = def.path.map((s, i) =>
    view(s, at < 0 ? (exit ? 'off' : 'next') : i < at ? 'done' : i === at ? 'current' : 'next'),
  )
  const exits = def.exits.map((s) => view(s, s === exit ? 'current' : 'off'))
  const current = path.find((s) => s.state === 'current') ?? exits.find((s) => s.state === 'current')
  const step = def.path[at] ?? exit
  return {
    kind,
    path,
    exits,
    current,
    person: step?.person ?? def.still ?? `Nothing yet: the factory has not reported on this ${kind} yet.`,
    moves: def.moves,
  }
}

/** The conditions that decide a Container's scaffold, in the order the operator checks them. */
const SCAFFOLD_CONDITIONS = ['ReferencesResolved', 'ChoicesValid', 'RepositoryCreated', 'Scaffolded']

/** The condition a Failed Container failed on: the first scaffold condition that is False. */
export function failedCondition(container: Container): Condition | undefined {
  if (container.status?.phase !== 'Failed') return undefined
  for (const type of SCAFFOLD_CONDITIONS) {
    const c = container.status.conditions?.find((x) => x.type === type)
    if (c?.status === 'False') return c
  }
  return undefined
}

/** The command that makes the operator try a Failed Container's scaffold again. */
export function retryCommand(container: Container): string {
  const ns = container.metadata.namespace ?? 'default'
  return `kubectl -n ${ns} annotate containers.c4.factory.io ${container.metadata.name} c4.factory.io/retry=$(date +%s) --overwrite`
}

/** The exact next step for a person on a Container, when there is one. */
export interface NextStep {
  /** What to do, in a sentence. */
  text: string
  /** A command to run, with a copy button. */
  command?: string
  /** Where to do it: a pull request or the repository. */
  link?: { href: string; label: string }
  /** Where to do it in the UI: an object's page, in the same namespace. */
  page?: { kind: string; name: string; label: string }
}

/**
 * What a person does next on this Container: on Failed, fix the cause and
 * retry; on a Standard update that waits (failed checks, refused), its pull
 * request; after one that left files out, those files. Undefined when nothing
 * waits on a person.
 */
export function containerNextStep(container: Container): NextStep | undefined {
  const status = container.status
  if (status?.phase === 'Failed') {
    const why = failedCondition(container)
    const repo = status.repositoryURL ? { href: status.repositoryURL, label: 'Open the repository' } : undefined
    const command = retryCommand(container)
    switch (why?.reason) {
      case 'ForeignCommits':
        return {
          text: 'The repository has commits the factory didn’t make. Empty it, then retry the scaffold.',
          command,
          link: repo,
        }
      case 'LayerConflict':
        return {
          text: 'Two templates write the same file. Change the templates or the catalog entries so each file has one, then retry.',
          command,
        }
      case 'UnpinnedRef':
        return { text: 'Pin the template’s ref to a tag or commit, never a branch, then retry.', command }
      case 'UnsupportedType':
      case 'UnsupportedFramework':
        return {
          text: 'Pick catalog entries that support this Container’s type; the change retries by itself.',
          command,
        }
      default:
        return { text: 'Fix what the failed condition names, then retry the scaffold.', command, link: repo }
    }
  }
  const standard = status?.standard
  const update = standard?.update
  const updated = status?.conditions?.find((c) => c.type === 'StandardUpdated')
  const pr = update?.pullRequestURL
    ? { href: update.pullRequestURL, label: `Open pull request${update.pullRequest ? ` #${update.pullRequest}` : ''}` }
    : undefined
  if (update && updated?.status === 'False' && updated.reason === 'ChecksFailed') {
    return {
      text: `The Standard update to ${update.ref} waits: its checks failed. Fix them in its pull request, or close it to drop the update.`,
      link: pr,
    }
  }
  if (update && updated?.status === 'False' && updated.reason === 'UpdateRefused') {
    return {
      text: `The factory stopped the Standard update to ${update.ref}; the condition says why. Look at its pull request; the update waits until the Standard’s ref changes again.`,
      link: pr,
    }
  }
  const behind = standard?.behind ?? []
  if (standard && behind.length && !update) {
    const n = behind.length
    const files = behind.map((b) => `${b.path} (at ${b.ref})`).join(', ')
    const url = behind.findLast((b) => b.pullRequestURL)?.pullRequestURL
    return {
      text: `${standard.name}’s updates left ${n === 1 ? 'a file' : `${n} files`} out that the repository changed in the same lines: ${files}. Take ${standard.name} ${standard.ref}’s version with the command below, or keep the repository’s with c4.factory.io/standard-keep=${standard.ref} instead.`,
      command: standardTakeCommand(container),
      link: url ? { href: url, label: 'Open the update pull request' } : undefined,
    }
  }
  return undefined
}

/** The command that takes the Standard's version of the files its updates left out (design.md, *A Standard's fixes reach its Containers*). */
export function standardTakeCommand(container: Container): string {
  const ns = container.metadata.namespace ?? 'default'
  return `kubectl -n ${ns} annotate container ${container.metadata.name} c4.factory.io/standard-take=${container.status?.standard?.ref ?? ''}`
}

/** The command that re-runs a Crew's failed release runs (design.md, *A failed release run re-runs on request*). */
export function rerunReleaseCommand(crew: Crew): string {
  const ns = crew.metadata.namespace ?? 'default'
  return `kubectl -n ${ns} annotate crews.c4.factory.io ${crew.metadata.name} c4.factory.io/rerun-release=$(date +%s) --overwrite`
}

/** The command that resumes a paused Crew. */
export function resumeCrewCommand(crew: Crew): string {
  const ns = crew.metadata.namespace ?? 'default'
  return `kubectl -n ${ns} patch crews.c4.factory.io ${crew.metadata.name} --type merge -p '{"spec":{"paused":false}}'`
}

/**
 * What a person does next on this Crew: re-run its last release's failed runs;
 * fix what keeps its release from publishing; resume it, through its Brief
 * when the Brief is what paused it; or fix what keeps it Waiting. Undefined
 * when nothing waits on a person.
 */
export function crewNextStep(crew: Crew, briefs: Brief[]): NextStep | undefined {
  const status = crew.status
  const release = status?.lastRelease
  if (release?.run === 'Failed') {
    return {
      text: `Release ${release.tag}’s CI runs failed. Fix what failed, then re-run them; the Crew watches the new attempts.`,
      command: rerunReleaseCommand(crew),
      link: release.runURL ? { href: release.runURL, label: 'Open the failed run' } : undefined,
    }
  }
  if (release?.publish?.state === 'Failed') {
    const to = crew.spec.publishReleases?.github
    return {
      text: `Publishing ${release.tag}${to ? ` to ${to.repository}` : ''} failed${release.publish.message ? `: ${release.publish.message}` : ''}. Fix what it names${to ? `, such as the token in Secret ${to.tokenSecretRef.name}` : ''}; the Crew tries again each time it reads the issues.`,
    }
  }
  if (release?.goModule?.state === 'Invalid') {
    return {
      text: `Go can’t take the module at ${release.tag}${release.goModule.message ? `: ${release.goModule.message}` : ''}. Fix go.mod’s module path; the next release publishes it.`,
    }
  }
  if (status?.phase === 'Paused') {
    const ns = crew.metadata.namespace ?? ''
    const brief = briefs.find(
      (b) =>
        (b.metadata.namespace ?? '') === ns &&
        b.spec.paused &&
        b.status?.created?.some((c) => c.kind === 'Crew' && c.name === crew.metadata.name),
    )
    if (brief) {
      return {
        text: `Its Brief ${brief.metadata.name} is paused, and with it every Crew the Brief made. Resume the Brief to resume them.`,
        page: { kind: 'Brief', name: brief.metadata.name, label: `Open Brief ${brief.metadata.name}` },
      }
    }
    return { text: 'Resume it when it should pick up issues again:', command: resumeCrewCommand(crew) }
  }
  const ready = findCondition(status?.conditions, 'Ready')
  if (status?.phase === 'Waiting' && ready?.status === 'False') {
    const container = crew.spec.containerRef.name
    return {
      text: `It can’t pick up issues${ready.message ? `: ${ready.message}` : ''}.`,
      page:
        ready.reason === 'ContainerNotReady'
          ? { kind: 'Container', name: container, label: `Open Container ${container}` }
          : undefined,
    }
  }
  return undefined
}

/**
 * What a person does next on this Task: a Blocked one waits on the Standard
 * change it filed; a Failed one handed its issue back. Undefined while the
 * loop needs nobody.
 */
export function taskNextStep(task: Task): NextStep | undefined {
  const status = task.status
  const change = status?.standardChange
  if (status?.phase === 'Blocked' && change) {
    return {
      text: `It waits for the Standard change it filed, #${change.issue}, for ${change.paths.join(', ')}. Review and merge it; the Task goes on when it merges.`,
      link: change.issueURL ? { href: change.issueURL, label: `Open the Standard change #${change.issue}` } : undefined,
    }
  }
  if (status?.phase === 'Failed' && task.spec.issue) {
    return {
      text: `Its issue #${task.spec.issue} went back to you, labelled status/needs-human with why. Answer it there, then label it status/ready to try again, or close it.`,
      link: status.issueURL ? { href: status.issueURL, label: `Open issue #${task.spec.issue}` } : undefined,
    }
  }
  return undefined
}
