// A Crew's writes from its page (design.md, UI actions): pause or resume it,
// its parallel and release settings, and a re-run of its failed release runs.
// server.mjs passes on only these requests; the CRD's rules decide whether they
// are accepted, so this file copies none of them: a refused write shows the API
// server's message.

import { RESOURCES, resourcePath, type Crew } from './types'
import { errorFromResponse } from './watch'

/** The annotation that asks the operator to re-run a failed release's runs, once per value. */
export const RERUN_RELEASE = 'c4.factory.io/rerun-release'

/** A Crew's release settings as its form edits them; the token Secret by name only. */
export interface CrewReleaseSettings {
  tagReleases: boolean
  prepareReleases: boolean
  /**
   * Where releases are published; undefined publishes nowhere. latest says a
   * published release becomes the repository's Latest on GitHub.
   */
  publish?: { repository: string; tokenSecret: string; tagPrefix: string; latest: boolean }
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
    next.publish?.latest === now.publish?.latest
  if (!same && next.publish) {
    const { repository, tokenSecret, tagPrefix, latest } = next.publish
    const value: NonNullable<Crew['spec']['publishReleases']> = {
      github: { repository, tokenSecretRef: { name: tokenSecret } },
    }
    if (tagPrefix) value.tagPrefix = tagPrefix
    if (!latest) value.latest = false
    ops.push({ op: 'add', path: '/spec/publishReleases', value })
  } else if (!same) {
    ops.push({ op: 'remove', path: '/spec/publishReleases' })
  }
  const resourceVersion = from ?? crew.metadata.resourceVersion
  if (!same && resourceVersion !== undefined) {
    ops.unshift({ op: 'test', path: '/metadata/resourceVersion', value: resourceVersion })
  }
  return ops
}

/** Pauses, sets and re-runs a Crew; each rejects with the API's message when it refuses. */
export interface CrewWriter {
  /** Sets spec.paused: true stops the Crew picking up issues, false resumes it. */
  setPaused(namespace: string, name: string, paused: boolean): Promise<void>
  /** Sets spec.parallel, sent as given so the API server judges it. */
  setParallel(namespace: string, name: string, parallel: number): Promise<void>
  /** Sends a JSON patch of releasePatch; an empty one sends nothing. */
  setReleases(namespace: string, name: string, ops: Op[]): Promise<void>
  /** Sets the re-run annotation to `value`, new on each click. */
  rerunRelease(namespace: string, name: string, value: string): Promise<void>
}

/** Writes through the /k8s proxy, relative to the page like the watches. */
export class LiveCrewWriter implements CrewWriter {
  private readonly baseUrl: string
  private readonly fetchImpl: typeof fetch

  constructor(baseUrl: string, fetchImpl: typeof fetch = (input, init) => fetch(input, init)) {
    this.baseUrl = baseUrl
    this.fetchImpl = fetchImpl
  }

  setPaused(namespace: string, name: string, paused: boolean): Promise<void> {
    return this.patch(namespace, name, 'application/json-patch+json', [
      { op: 'add', path: '/spec/paused', value: paused },
    ])
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
