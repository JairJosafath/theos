// The UI's writes to a Brief: create one from an idea, add a follow-up prompt
// to one, pause or resume one and, with actions on, set its autopilot,
// parallel and releases. server.mjs passes exactly these requests on and
// nothing else; the CRD's rules decide whether they are accepted, so this file
// copies none of them: a refused write shows the API server's message.

import { GROUP, VERSION, resourcePath, RESOURCES, type Brief, type BriefAutopilot, type BriefRelease } from './types'
import { errorFromResponse } from './watch'

/** What a new Brief may choose besides its idea; each unset leaves the factory's default. */
export interface BriefOptions {
  autopilot?: BriefAutopilot
  /** How many issues each of its Crews works on at once; sent as given, so the API server judges it. */
  parallel?: number | string
}

/**
 * The Brief to POST: only its name, idea and, when chosen, its autopilot mode
 * and parallel; the factory fills in the rest.
 */
export function briefManifest(namespace: string, name: string, idea: string, options: BriefOptions = {}): Brief {
  const spec: Brief['spec'] = { idea }
  if (options.autopilot) spec.autopilot = options.autopilot
  if (options.parallel !== undefined && options.parallel !== '') spec.parallel = options.parallel as number
  return {
    apiVersion: `${GROUP}/${VERSION}`,
    kind: 'Brief',
    metadata: { name, namespace },
    spec,
  }
}

/** The JSON patch that appends one prompt; server.mjs passes on only this and pausedPatch. */
export function promptPatch(prompt: string): [{ op: 'add'; path: '/spec/prompts/-'; value: string }] {
  return [{ op: 'add', path: '/spec/prompts/-', value: prompt }]
}

/** The JSON patch that pauses (true) or resumes (false) a Brief and every Crew it made. */
export function pausedPatch(paused: boolean): [{ op: 'add'; path: '/spec/paused'; value: boolean }] {
  return [{ op: 'add', path: '/spec/paused', value: paused }]
}

/** A Brief's settings the UI sets with actions on; each one given is set, the rest left as they are. */
export interface BriefSettings {
  autopilot?: BriefAutopilot
  /** Sent as given, so the API server judges it. */
  parallel?: number | string
  /** The whole list: an entry added or removed is the list without or with it. */
  releases?: BriefRelease[]
  /**
   * The resourceVersion of the Brief the releases were worked out from. A
   * releases write sends it as a precondition, so it never replaces a list
   * someone else changed meanwhile; server.mjs refuses releases without it.
   */
  resourceVersion?: string
}

type SettingsOp = { op: 'add' | 'test'; path: string; value: unknown }

/**
 * The JSON patch that sets each of the settings given; server.mjs passes on
 * only these paths. With releases it starts with a test of the Brief's
 * resourceVersion, so a patch worked out from a stale Brief is refused.
 */
export function settingsPatch(settings: BriefSettings): SettingsOp[] {
  const ops: SettingsOp[] = []
  if (settings.releases !== undefined && settings.resourceVersion !== undefined)
    ops.push({ op: 'test', path: '/metadata/resourceVersion', value: settings.resourceVersion })
  if (settings.autopilot !== undefined) ops.push({ op: 'add', path: '/spec/autopilot', value: settings.autopilot })
  if (settings.parallel !== undefined) ops.push({ op: 'add', path: '/spec/parallel', value: settings.parallel })
  if (settings.releases !== undefined) ops.push({ op: 'add', path: '/spec/releases', value: settings.releases })
  return ops
}

// Words that say little about the app, skipped when naming it.
const FILLER = new Set(
  'a an the and or for to of in on at by with where which that who whose is are be can i we my our me us app apps want build make'.split(
    ' ',
  ),
)
const NAME_WORDS = 3
const NAME_MAX = 40

/**
 * A name for a new Brief from the first few telling words of its idea, such as
 * "plant-watering-reminders", made unique against the names taken; "app" when
 * the idea has no usable words.
 */
export function nameFromIdea(idea: string, taken: string[]): string {
  const words = (
    idea
      .toLowerCase()
      .normalize('NFKD')
      .replace(/[̀-ͯ]/g, '')
      .match(/[a-z0-9]+/g) ?? []
  ).filter((w) => !FILLER.has(w))
  let base = ''
  for (const word of words.slice(0, NAME_WORDS)) {
    const next = base ? `${base}-${word}` : word
    if (next.length > NAME_MAX) break
    base = next
  }
  base ||= 'app'
  const used = new Set(taken)
  if (!used.has(base)) return base
  for (let n = 2; ; n++) {
    const name = `${base}-${n}`
    if (!used.has(name)) return name
  }
}

/** Creates Briefs, adds prompts to them, pauses or resumes them and sets their settings; rejects with the API's message when it refuses. */
export interface BriefWriter {
  /** Each unset option leaves the factory's default. */
  create(namespace: string, name: string, idea: string, options?: BriefOptions): Promise<void>
  addPrompt(namespace: string, name: string, prompt: string): Promise<void>
  /** Sets spec.paused: true pauses the Brief and its Crews, false resumes them. */
  setPaused(namespace: string, name: string, paused: boolean): Promise<void>
  /** Sets spec.autopilot, spec.parallel or spec.releases; the UI server refuses it with actions off. */
  setSettings(namespace: string, name: string, settings: BriefSettings): Promise<void>
}

/** Writes through the /k8s proxy, relative to the page like the watches. */
export class LiveBriefWriter implements BriefWriter {
  private readonly baseUrl: string
  private readonly fetchImpl: typeof fetch

  constructor(baseUrl: string, fetchImpl: typeof fetch = (input, init) => fetch(input, init)) {
    this.baseUrl = baseUrl
    this.fetchImpl = fetchImpl
  }

  async create(namespace: string, name: string, idea: string, options?: BriefOptions): Promise<void> {
    await this.send(`${this.baseUrl}${resourcePath(RESOURCES.briefs, namespace)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(briefManifest(namespace, name, idea, options)),
    })
  }

  async addPrompt(namespace: string, name: string, prompt: string): Promise<void> {
    await this.patch(namespace, name, promptPatch(prompt))
  }

  async setPaused(namespace: string, name: string, paused: boolean): Promise<void> {
    await this.patch(namespace, name, pausedPatch(paused))
  }

  async setSettings(namespace: string, name: string, settings: BriefSettings): Promise<void> {
    await this.patch(namespace, name, settingsPatch(settings))
  }

  private async patch(namespace: string, name: string, ops: unknown[]): Promise<void> {
    const path = `${resourcePath(RESOURCES.briefs, namespace)}/${encodeURIComponent(name)}`
    await this.send(`${this.baseUrl}${path}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json-patch+json', Accept: 'application/json' },
      body: JSON.stringify(ops),
    })
  }

  private async send(url: string, init: RequestInit): Promise<void> {
    const res = await this.fetchImpl(url, init)
    if (!res.ok) throw await errorFromResponse(res)
  }
}
