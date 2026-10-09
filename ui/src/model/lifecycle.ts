// Each kind's lifecycle (docs/plans/ui-lifecycle.md, decision 3; #705's table,
// "Lifecycle of each level"): the phases on its path, the side exits off it,
// what moves it along, and what a person does at each step. Object pages read
// it from here, so no page invents its own. Pure, and read only from status.

import type { BriefPhase, Condition, Container, ContainerPhase, Relationship, SessionPhase } from '../k8s/types'
import { describeBriefPhase } from './brief'
import { describeContainerPhase, describeSessionPhase, type Tone } from './describe'

export type LifecycleKind =
  | 'Brief'
  | 'Container'
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
  if (standard?.leftOut?.length) {
    const n = standard.leftOut.length
    return {
      text: `The last Standard update left ${n === 1 ? 'a file' : `${n} files`} out that the repository had changed: ${standard.leftOut.join(', ')}. Bring ${n === 1 ? 'it' : 'them'} up to date by hand.`,
      link: status?.repositoryURL ? { href: status.repositoryURL, label: 'Open the repository' } : undefined,
    }
  }
  return undefined
}
