// A Container's effective catalog choices and what a Bundle supports: the two
// rules the UI shares with the operator (api/v1alpha1 Container.Choices and
// internal/model BundleSupportedTypes), so every page reads the same answer
// whether a Container names its four sides or a Bundle.

import type { Bundle, CatalogEntry, Container, ContainerType } from '../k8s/types'

/** What a Container chose, by entry name; a bundled one before its expansion has only the bundle. */
export interface Choices {
  bundle?: string
  framework?: string
  standard?: string
  compute?: string
  deployment?: string
}

/**
 * The Container's spec refs, or, when it names a Bundle, what status.bundle
 * says the Bundle expanded to. The UI enforces nothing here: a bundled
 * Container whose Bundle changed since still shows what it was scaffolded
 * from, and its BundleCurrent condition says so.
 */
export function choicesOf(c: Container): Choices {
  const spec = c.spec
  if (!spec.bundleRef) {
    return {
      framework: spec.frameworkRef?.name,
      standard: spec.standardRef?.name,
      compute: spec.computeRef?.name,
      deployment: spec.deploymentRef?.name,
    }
  }
  const b = c.status?.bundle
  return {
    bundle: spec.bundleRef.name,
    framework: b?.framework,
    standard: b?.standard,
    compute: b?.compute,
    deployment: b?.deployment,
  }
}

/** The catalog a Bundle's entries are looked up in. */
export interface Catalog {
  frameworks: CatalogEntry[]
  standards: CatalogEntry[]
  computes: CatalogEntry[]
  deployments: CatalogEntry[]
}

export type BundleSide = 'framework' | 'standard' | 'compute' | 'deployment'

/** One of a Bundle's entries: its name, and the catalog entry when the catalog has it. */
export interface BundleEntry {
  side: BundleSide
  name: string
  entry?: CatalogEntry
}

const SIDES: { side: BundleSide; ref: (b: Bundle) => string | undefined; list: (c: Catalog) => CatalogEntry[] }[] = [
  { side: 'framework', ref: (b) => b.spec.frameworkRef?.name, list: (c) => c.frameworks },
  { side: 'standard', ref: (b) => b.spec.standardRef?.name, list: (c) => c.standards },
  { side: 'compute', ref: (b) => b.spec.computeRef?.name, list: (c) => c.computes },
  { side: 'deployment', ref: (b) => b.spec.deploymentRef?.name, list: (c) => c.deployments },
]

/** The entries a Bundle names, in the order its sides come, each with its catalog entry when found. */
export function bundleEntries(bundle: Bundle, catalog: Catalog): BundleEntry[] {
  return SIDES.flatMap(({ side, ref, list }) => {
    const name = ref(bundle)
    return name ? [{ side, name, entry: list(catalog).find((e) => e.metadata.name === name) }] : []
  })
}

/**
 * The Container types a Bundle supports: those its first entry lists that
 * every other entry found in the catalog lists too, in the first entry's
 * order. No entries found, or none shared, gives none. An entry the catalog
 * lacks is left out of the rule, as the page says it is missing.
 */
export function bundleTypes(bundle: Bundle, catalog: Catalog): ContainerType[] {
  const entries = bundleEntries(bundle, catalog).flatMap((e) => (e.entry ? [e.entry] : []))
  if (!entries.length) return []
  const [first, ...rest] = entries
  const out: ContainerType[] = []
  for (const t of first.spec.supportedTypes) {
    if (out.includes(t)) continue
    if (rest.every((e) => e.spec.supportedTypes.includes(t))) out.push(t)
  }
  return out
}
