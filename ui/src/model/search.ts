// The command palette's entries and how a query finds them
// (docs/plans/done/ui-shell.md): the areas, the projects and every object the page
// watches, matched by kind, name and namespace, case-insensitive, by a
// substring or the query's letters in order.

import type { FactoryData, StoryData } from '../k8s/sources'
import type { K8sObject, Project } from '../k8s/types'
import type { UrlState } from '../lib/hooks'
import { AREAS, type Area } from './areas'
import { titleOf } from './describe'
import { hasPage } from './lifecycle'
import { compareText, compareTitle } from './order'

export interface PaletteItem {
  /** Unique among the entries. */
  key: string
  type: 'area' | 'project' | 'object'
  /** What the entry shows, such as "Task orders-api-21". */
  label: string
  /** Its second line: what an area is about, or the namespace. */
  hint: string
  /** The kind of an object, for its badge. */
  kind?: string
  /** What else the query matches, such as an object's display name. */
  extra?: string
  /** Where picking it goes. */
  target: Partial<UrlState>
}

/** What picking an area, a project or an object closes: the object open in the drawer. */
const NO_OBJECT: Partial<UrlState> = { ns: undefined, kind: undefined, name: undefined, full: undefined }

/** The kinds the page watches, where each one is in the data. */
const STORY_LISTS: [string, (d: StoryData) => K8sObject[]][] = [
  ['Brief', (d) => d.briefs],
  ['Person', (d) => d.people],
  ['SoftwareSystem', (d) => d.systems],
  ['Container', (d) => d.containers],
  ['DataStore', (d) => d.dataStores],
  ['Relationship', (d) => d.relationships],
  ['Crew', (d) => d.crews],
  ['Agent', (d) => d.agents],
  ['Task', (d) => d.tasks],
  ['Session', (d) => d.sessions],
  ['Standard', (d) => d.standards],
  ['Compute', (d) => d.computes],
  ['Deployment', (d) => d.deployments],
  ['Bundle', (d) => d.bundles],
  ['Domain', (d) => d.domains],
  ['GitProvider', (d) => d.gitProviders],
  ['DeployTarget', (d) => d.deployTargets],
  ['TestEnvironment', (d) => d.testEnvironments],
]
const FACTORY_LISTS: [string, (d: FactoryData) => K8sObject[]][] = [
  ['Brief', (d) => d.briefs],
  ['Container', (d) => d.containers],
  ['Crew', (d) => d.crews],
  ['Agent', (d) => d.agents],
  ['Task', (d) => d.tasks],
  ['Session', (d) => d.sessions],
]

/**
 * Where an object goes: its page in the drawer, for a kind with one; a Person
 * or DataStore to its project's Story, where it is drawn; an Agent to Work,
 * where its Crew shows it.
 */
export function objectTarget(kind: string, name: string, ns: string): Partial<UrlState> {
  if (hasPage(kind)) return { ns, kind, name, full: undefined }
  if (kind === 'Agent') return { ...NO_OBJECT, area: 'work', tab: undefined }
  return { ...NO_OBJECT, area: 'story', project: ns, view: 'context', system: undefined, tab: undefined }
}

/** Every entry: the areas, the projects, then each object of the story and the factory once. */
export function paletteItems(input: {
  projects: Project[]
  story?: StoryData
  factory?: FactoryData
  /** The area on screen: a project picked from an area of one project stays in it. */
  area: Area
}): PaletteItem[] {
  const items: PaletteItem[] = AREAS.map((a) => ({
    key: `area/${a.key}`,
    type: 'area',
    label: a.label,
    hint: a.about,
    target: { ...NO_OBJECT, area: a.key, tab: undefined },
  }))
  const projectArea: Area = input.area === 'loop' || input.area === 'briefs' ? input.area : 'story'
  for (const p of input.projects) {
    const ns = p.metadata.namespace ?? p.metadata.name
    items.push({
      key: `project/${ns}`,
      type: 'project',
      label: titleOf(p),
      hint: `Project · ${ns}`,
      extra: ns,
      target: { ...NO_OBJECT, area: projectArea, project: ns, view: 'context', system: undefined, tab: undefined },
    })
  }
  const seen = new Set<string>()
  const add = (kind: string, obj: K8sObject, fallbackNs: string) => {
    const name = obj.metadata.name
    const ns = obj.metadata.namespace ?? fallbackNs
    const key = `object/${kind}/${ns}/${name}`
    if (!name || seen.has(key)) return
    seen.add(key)
    const title = titleOf(obj)
    items.push({
      key,
      type: 'object',
      kind,
      label: `${kind} ${name}`,
      hint: ns,
      extra: title !== name ? title : undefined,
      target: objectTarget(kind, name, ns),
    })
  }
  if (input.story)
    for (const [kind, list] of STORY_LISTS) for (const o of list(input.story)) add(kind, o, input.story.namespace)
  if (input.factory) for (const [kind, list] of FACTORY_LISTS) for (const o of list(input.factory)) add(kind, o, '')
  return items
}

/**
 * How well one word of a query matches: lower is better, undefined is no
 * match. The label starting with it, then a word of the entry starting with
 * it, then anywhere in it, then its letters in order in the label (not in a
 * long hint, where nearly any letters are).
 */
export function wordScore(word: string, label: string, text: string): number | undefined {
  if (label.startsWith(word)) return 0
  if (text.split(/[\s\-_./·]+/).some((w) => w.startsWith(word))) return 1
  if (text.includes(word)) return 2
  // Its letters in order, the closer together the better.
  let at = -1
  let first = -1
  for (const ch of word) {
    at = label.indexOf(ch, at + 1)
    if (at < 0) return undefined
    if (first < 0) first = at
  }
  return 3 + (at - first + 1 - word.length) / Math.max(label.length, 1)
}

const TYPE_ORDER = { area: 0, project: 1, object: 2 }

/** The entries a query finds, best first; every word of it must match. An empty query lists them in order. */
export function matchPalette(query: string, items: PaletteItem[], limit = 50): PaletteItem[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return items.slice(0, limit)
  const scored: { item: PaletteItem; score: number; name: string }[] = []
  for (const item of items) {
    const label = item.label.toLowerCase()
    const text = [item.label, item.hint, item.extra, item.kind].filter(Boolean).join(' ').toLowerCase()
    // The name alone, too, so "orders" ranks "Task orders-api-21" by its name.
    const name = item.type === 'object' && item.kind ? label.slice(item.kind.length + 1) : label
    let score = 0
    for (const w of words) {
      const s = Math.min(wordScore(w, label, text) ?? Infinity, wordScore(w, name, text) ?? Infinity)
      if (s === Infinity) {
        score = Infinity
        break
      }
      score += s
    }
    if (score !== Infinity) scored.push({ item, score, name })
  }
  scored.sort(
    (a, b) =>
      a.score - b.score ||
      TYPE_ORDER[a.item.type] - TYPE_ORDER[b.item.type] ||
      // The shorter name is the closer match: orders-api before orders-api-21.
      a.name.length - b.name.length ||
      compareTitle(a.item.label, b.item.label) ||
      // Two objects of one kind and name in two projects share a label (#175).
      compareText(a.item.key, b.item.key),
  )
  return scored.slice(0, limit).map((s) => s.item)
}
