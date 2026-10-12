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

/** Why publishing a release failed, in words: the reasons of a Crew's Published condition. */
export const PUBLISH_REASON: Record<string, string> = {
  SecretMissing: 'the token Secret is missing',
  SecretUnreadable: 'the token Secret can’t be read',
  Unauthorized: 'GitHub refused the token',
  RepositoryNotFound: 'GitHub has no such repository the token can see',
  EmptyRepository: 'the repository has no commit yet',
  NoReleaseFiles: 'the release has no files to publish',
  TagTaken: 'the release’s tag is taken by one the factory didn’t make',
  GitHubError: 'GitHub failed',
  SourceTagTaken: 'the source’s tag is taken by a commit with other files',
  FolderOverlaps: 'its source folder overlaps another Crew’s',
  SourceTooLarge: 'a file of the source is too large for GitHub',
  BranchProtected: 'the repository’s default branch is protected',
}

/** Why the Crew's last publish failed, in words; undefined unless it failed for a reason known here. */
export function publishFailure(crew: Crew): string | undefined {
  if (crew.status?.lastRelease?.publish?.state !== 'Failed') return undefined
  const reason = crew.status.conditions?.find((c) => c.type === 'Published' && c.status === 'False')?.reason
  return reason ? PUBLISH_REASON[reason] : undefined
}
