// The versions of the UI and the factory it reads, for the footer's version
// line, and when the two differ. The operator reports its version and the
// chart's in Project status (status.factory); the UI's own is stamped by its
// build. Nothing here names a release.

import type { Project } from '../k8s/types'

/** What a build says when no release stamped it, the UI's and the operator's alike. */
export const DEV_VERSION = 'dev'

export interface Versions {
  ui: string
  /** Undefined until some Project reports one. */
  operator?: string
  /** Undefined until some Project reports one, or when the chart didn't install the operator. */
  chart?: string
  /** The `group/version` of the resources the UI reads. */
  api: string
}

/**
 * The versions for the footer: the UI's, the CRD API's, and the operator's and
 * chart's from the first Project that reported them (one operator writes them
 * all, so any will do).
 */
export function factoryVersions(projects: Project[], ui: string, api: string): Versions {
  const factory = projects.find((p) => p.status?.factory?.operatorVersion)?.status?.factory
  return { ui, api, operator: factory?.operatorVersion || undefined, chart: factory?.chartVersion || undefined }
}

/**
 * Why the UI and the operator may not agree, or undefined when they are the
 * same version or the operator hasn't reported one. Two unreleased builds
 * (both `dev`) are the same version.
 */
export function versionSkew(v: Pick<Versions, 'ui' | 'operator'>): string | undefined {
  if (!v.operator || v.operator === v.ui) return undefined
  return (
    `This UI is ${v.ui} and the operator ${v.operator}: a UI and an operator of different releases may ` +
    `read the resources differently. Upgrade them together, with the chart.`
  )
}

/** Where the factory's code, docs and releases live. */
export const REPO_URL = 'https://github.com/JairJosafath/theos'

/**
 * The release the factory runs, for the About area's links: the operator's
 * version when it reported a release, else the UI's; undefined for
 * unreleased (`dev`) builds.
 */
export function runningRelease(v: Pick<Versions, 'ui' | 'operator'>): string | undefined {
  return [v.operator, v.ui].find((x) => x && x !== DEV_VERSION)
}

/**
 * The About area's links: the docs and release notes of the running release
 * (its tag is `v` and the version), or main's docs and every release's notes
 * when no release is known; and the repository.
 */
export function aboutLinks(v: Pick<Versions, 'ui' | 'operator'>): { docs: string; releaseNotes: string; repo: string } {
  const release = runningRelease(v)
  const tag = release ? `v${release.replace(/^v/, '')}` : undefined
  return {
    docs: `${REPO_URL}/blob/${tag ?? 'main'}/docs/design.md`,
    releaseNotes: tag ? `${REPO_URL}/releases/tag/${tag}` : `${REPO_URL}/releases`,
    repo: REPO_URL,
  }
}
