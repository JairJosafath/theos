// The releases the Crews tagged: each Crew's last one, as its status has it.

import type { Crew, CrewRelease } from '../k8s/types'

export interface CrewReleaseRow {
  /** "<namespace>/<crew>". */
  key: string
  namespace: string
  crew: string
  container: string
  release: CrewRelease
}

/** Each Crew's last release, newest first. */
export function crewReleases(crews: Crew[]): CrewReleaseRow[] {
  return crews
    .filter((c) => c.status?.lastRelease)
    .map((c) => ({
      key: `${c.metadata.namespace ?? ''}/${c.metadata.name}`,
      namespace: c.metadata.namespace ?? '',
      crew: c.metadata.name,
      container: c.spec.containerRef.name,
      release: c.status!.lastRelease!,
    }))
    .sort((a, b) => b.release.time.localeCompare(a.release.time) || a.key.localeCompare(b.key))
}

/** How the CI runs a release's tag started went, in words. */
export const RELEASE_RUN: Record<NonNullable<CrewRelease['run']>, string> = {
  Waiting: 'waiting to start',
  Running: 'running',
  Succeeded: 'succeeded',
  Failed: 'one failed',
}
