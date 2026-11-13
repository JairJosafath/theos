// A Crew's writes from its page (design.md, UI actions): pause or resume it or
// its running Tasks, its parallel and release settings, a re-run of its failed release runs,
// a run of one of its schedules now, and one more schedule: a proposal of its
// Container's Standard, taken.
// server.mjs passes on only these requests; the CRD's rules decide whether they
// are accepted, so this file copies none of them: a refused write shows the API
// server's message.

import { pausedPatch, pauseRunsPatch } from './briefs'
import {
  RESOURCES,
  findCondition,
  resourcePath,
  type Brief,
  type BriefRelease,
  type Crew,
  type CrewSchedule,
  type PublishSource,
} from './types'
import { errorFromResponse } from './watch'

/** The annotation that asks the operator to re-run a failed release's runs, once per value. */
export const RERUN_RELEASE = 'theos.factory.io/rerun-release'

/** The annotation that asks the operator to run one of the Crew's schedules now; its value is the schedule's name. */
export const RUN_SCHEDULE = 'theos.factory.io/run-schedule'

/** The label naming the Brief that made a Crew. */
export const LABEL_BRIEF = 'theos.factory.io/brief'

/** The annotation holding the release target a Crew's Brief last set on it (docs/plans/done/brief-release-target.md). */
export const BRIEF_PUBLISH_RELEASES = 'theos.factory.io/brief-publish-releases'

/** Where a Crew publishes its releases; undefined publishes nowhere. */
export type PublishReleases = NonNullable<Crew['spec']['publishReleases']>

/**
 * A Crew whose Brief sets where it publishes, as its ReleaseTarget condition
 * says: `set` while the two agree; `held` while the Crew was changed under the
 * Brief and publishes nothing. `target` is the one the Brief last set,
 * undefined when it can't be read.
 */
export interface BriefTarget {
  brief: string
  held: boolean
  target?: PublishReleases
}

/** Who sets the Crew's release target: its Brief, or undefined for the Crew itself. */
export function briefTarget(crew: Crew): BriefTarget | undefined {
  const cond = findCondition(crew.status?.conditions, 'ReleaseTarget')
  const brief = crew.metadata.labels?.[LABEL_BRIEF]
  if (!cond || !brief) return undefined
  let target: PublishReleases | undefined
  try {
    const mark = JSON.parse(crew.metadata.annotations?.[BRIEF_PUBLISH_RELEASES] ?? '') as Partial<PublishReleases>
    if (mark?.github?.repository) target = mark as PublishReleases
  } catch {
    target = undefined
  }
  return { brief, held: cond.status === 'False', target }
}

/**
 * The Brief's spec.releases with `container`'s entry set to `publish`, its
 * requests kept, or without it when `publish` is undefined. Undefined when the
 * Brief has no entry for it.
 */
export function briefReleases(
  brief: Brief,
  container: string,
  publish: PublishReleases | undefined,
): BriefRelease[] | undefined {
  const releases = brief.spec.releases ?? []
  const i = releases.findIndex((r) => r.container === container)
  if (i < 0) return undefined
  if (!publish) return releases.filter((_, j) => j !== i)
  const { requests } = releases[i]
  const entry: BriefRelease = { container, ...publish, ...(requests ? { requests } : {}) }
  return releases.map((r, j) => (j === i ? entry : r))
}

/** A Crew's release settings as its form edits them; the token Secret by name only. */
export interface CrewReleaseSettings {
  tagReleases: boolean
  prepareReleases: boolean
  /**
   * Where releases are published; undefined publishes nowhere. latest says a
   * published release becomes the repository's Latest on GitHub; source,
   * the folder its source goes to, undefined publishing none.
   */
  publish?: { repository: string; tokenSecret: string; tagPrefix: string; latest: boolean; source?: PublishSource }
}

/** What goes before a release's tag in its source's tag: tagPrefix, else path + "/", and nothing for ".". */
export function sourceTagPrefix(source: PublishSource): string {
  if (source.tagPrefix !== undefined) return source.tagPrefix
  return source.path === '.' ? '' : `${source.path}/`
}

/** The settings a Crew has now, as its form starts. */
export function releaseSettings(crew: Crew): CrewReleaseSettings {
  const p = crew.spec.publishReleases
  return {
    tagReleases: Boolean(crew.spec.tagReleases),
    prepareReleases: Boolean(crew.spec.prepareReleases),
    publish: p
      ? {
          repository: p.github.repository,
          tokenSecret: p.github.tokenSecretRef.name,
          tagPrefix: p.tagPrefix ?? '',
          latest: p.latest !== false,
          source: p.source ? { ...p.source } : undefined,
        }
      : undefined,
  }
}

/** `edits` without those the Crew has now, so those fields follow the Crew again. */
export function settledEdits(crew: Crew, edits: Partial<CrewReleaseSettings>): Partial<CrewReleaseSettings> {
  const now = releaseSettings(crew)
  const left: Partial<CrewReleaseSettings> = { ...edits }
  for (const key of Object.keys(edits) as (keyof CrewReleaseSettings)[]) {
    if (JSON.stringify(edits[key]) === JSON.stringify(now[key])) delete left[key]
  }
  return Object.keys(left).length === Object.keys(edits).length ? edits : left
}

type Op = { op: 'add' | 'test'; path: string; value: unknown } | { op: 'remove'; path: string }

/**
 * The JSON patch from a Crew's settings to `next`: only what changed, so an
 * unchanged form sends nothing. Turning publishing off removes the field.
 * publishReleases is sent whole, so a patch that sets or removes it starts
 * with a test of `from`, the resourceVersion of the Crew its publish fields
 * were worked out from (the Crew's own by default): a Crew changed since is
 * refused rather than lose that change. server.mjs refuses it without one.
 */
export function releasePatch(crew: Crew, next: CrewReleaseSettings, from?: string): Op[] {
  const now = releaseSettings(crew)
  const ops: Op[] = []
  if (next.tagReleases !== now.tagReleases) ops.push({ op: 'add', path: '/spec/tagReleases', value: next.tagReleases })
  if (next.prepareReleases !== now.prepareReleases) {
    ops.push({ op: 'add', path: '/spec/prepareReleases', value: next.prepareReleases })
  }
  const same =
    next.publish?.repository === now.publish?.repository &&
    next.publish?.tokenSecret === now.publish?.tokenSecret &&
    next.publish?.tagPrefix === now.publish?.tagPrefix &&
    next.publish?.latest === now.publish?.latest &&
    next.publish?.source?.path === now.publish?.source?.path &&
    next.publish?.source?.tagPrefix === now.publish?.source?.tagPrefix
  if (same) return ops
  // The test leads the whole patch, where server.mjs looks for it, also when a
  // switch changed in the same save (theos#31).
  const publish = publishPatch(crew, publishValue(next.publish), from)
  const test = publish[0].op === 'test' ? publish.slice(0, 1) : []
  return [...test, ...ops, ...publish.slice(test.length)]
}

/** The settings form's publish fields as a Crew's spec.publishReleases. */
export function publishValue(publish: CrewReleaseSettings['publish']): PublishReleases | undefined {
  if (!publish) return undefined
  const { repository, tokenSecret, tagPrefix, latest, source } = publish
  const value: PublishReleases = { github: { repository, tokenSecretRef: { name: tokenSecret } } }
  if (tagPrefix) value.tagPrefix = tagPrefix
  if (!latest) value.latest = false
  if (source) value.source = source.tagPrefix === undefined ? { path: source.path } : { ...source }
  return value
}

/**
 * The JSON patch that sets the Crew's spec.publishReleases to `value`, or
 * removes it, after a test of `from` (the Crew's resourceVersion by default).
 */
export function publishPatch(crew: Crew, value: PublishReleases | undefined, from?: string): Op[] {
  const ops: Op[] = [
    value ? { op: 'add', path: '/spec/publishReleases', value } : { op: 'remove', path: '/spec/publishReleases' },
  ]
  const resourceVersion = from ?? crew.metadata.resourceVersion
  if (resourceVersion !== undefined)
    ops.unshift({ op: 'test', path: '/metadata/resourceVersion', value: resourceVersion })
  return ops
}

/**
 * A Standard's proposal as the schedule a Crew takes it as: its name, cron and
 * goal as they are written, in the time zone the person gave. The proposal's
 * own zone is not copied: a Standard can't know where its Crews are, and the
 * API server writes UTC into a proposal that names none.
 */
export function takenSchedule(proposal: CrewSchedule, timeZone: string): CrewSchedule {
  return { name: proposal.name, cron: proposal.cron, timeZone: timeZone.trim(), goal: proposal.goal }
}

/**
 * The JSON patch that adds one schedule to a Crew: an append, so it changes
 * and drops none of the schedules the Crew has. server.mjs makes the list for
 * a Crew without one; a name the Crew already uses is the API server's to refuse.
 */
export function addSchedulePatch(schedule: CrewSchedule): Op[] {
  return [{ op: 'add', path: '/spec/schedules/-', value: schedule }]
}

/** Pauses, sets and re-runs a Crew; each rejects with the API's message when it refuses. */
export interface CrewWriter {
  /**
   * Sets spec.paused: true stops the Crew picking up issues, false resumes it;
   * with `runsPaused` (spec.pauseRuns is set) a resume unsets pauseRuns too.
   */
  setPaused(namespace: string, name: string, paused: boolean, runsPaused?: boolean): Promise<void>
  /** Sets spec.pauseRuns (with spec.paused when true): true pauses the Crew's running Tasks, false resumes them. */
  setPauseRuns(namespace: string, name: string, pauseRuns: boolean): Promise<void>
  /** Sets spec.parallel, sent as given so the API server judges it. */
  setParallel(namespace: string, name: string, parallel: number): Promise<void>
  /** Sends a JSON patch of releasePatch; an empty one sends nothing. */
  setReleases(namespace: string, name: string, ops: Op[]): Promise<void>
  /** Sets the re-run annotation to `value`, new on each click. */
  rerunRelease(namespace: string, name: string, value: string): Promise<void>
  /** Asks the operator to run the Crew's schedule `schedule` now: sets the run-schedule annotation to its name. */
  runSchedule(namespace: string, name: string, schedule: string): Promise<void>
  /** Adds `schedule` to the Crew's spec.schedules, sent as given so the API server judges it. */
  addSchedule(namespace: string, name: string, schedule: CrewSchedule): Promise<void>
}

/** Writes through the /k8s proxy, relative to the page like the watches. */
export class LiveCrewWriter implements CrewWriter {
  private readonly baseUrl: string
  private readonly fetchImpl: typeof fetch

  constructor(baseUrl: string, fetchImpl: typeof fetch = (input, init) => fetch(input, init)) {
    this.baseUrl = baseUrl
    this.fetchImpl = fetchImpl
  }

  setPaused(namespace: string, name: string, paused: boolean, runsPaused?: boolean): Promise<void> {
    return this.patch(namespace, name, 'application/json-patch+json', pausedPatch(paused, runsPaused))
  }

  setPauseRuns(namespace: string, name: string, pauseRuns: boolean): Promise<void> {
    return this.patch(namespace, name, 'application/json-patch+json', pauseRunsPatch(pauseRuns))
  }

  setParallel(namespace: string, name: string, parallel: number): Promise<void> {
    return this.patch(namespace, name, 'application/json-patch+json', [
      { op: 'add', path: '/spec/parallel', value: parallel },
    ])
  }

  async setReleases(namespace: string, name: string, ops: Op[]): Promise<void> {
    if (ops.length) await this.patch(namespace, name, 'application/json-patch+json', ops)
  }

  rerunRelease(namespace: string, name: string, value: string): Promise<void> {
    return this.patch(namespace, name, 'application/merge-patch+json', {
      metadata: { annotations: { [RERUN_RELEASE]: value } },
    })
  }

  runSchedule(namespace: string, name: string, schedule: string): Promise<void> {
    return this.patch(namespace, name, 'application/merge-patch+json', {
      metadata: { annotations: { [RUN_SCHEDULE]: schedule } },
    })
  }

  addSchedule(namespace: string, name: string, schedule: CrewSchedule): Promise<void> {
    return this.patch(namespace, name, 'application/json-patch+json', addSchedulePatch(schedule))
  }

  private async patch(namespace: string, name: string, type: string, body: unknown): Promise<void> {
    const path = `${resourcePath(RESOURCES.crews, namespace)}/${encodeURIComponent(name)}`
    const res = await this.fetchImpl(`${this.baseUrl}${path}`, {
      method: 'PATCH',
      headers: { 'Content-Type': type, Accept: 'application/json' },
      body: JSON.stringify(body),
    })
    if (!res.ok) throw await errorFromResponse(res)
  }
}
