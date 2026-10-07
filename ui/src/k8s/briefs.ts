// The UI's only two writes: create a Brief from an idea, and add a follow-up
// prompt to one. server.mjs passes exactly these two requests on and nothing
// else; the CRD's rules decide whether they are accepted.

import { GROUP, VERSION, resourcePath, RESOURCES, type Brief, type BriefAutopilot } from './types'
import { errorFromResponse } from './watch'

/** Longest idea and prompt the Brief CRD accepts. */
export const MAX_IDEA = 4000
export const MAX_PROMPT = 2000

/**
 * The Brief to POST: only its name, idea and, when chosen, its autopilot mode;
 * the factory fills in the rest.
 */
export function briefManifest(namespace: string, name: string, idea: string, autopilot?: BriefAutopilot): Brief {
  return {
    apiVersion: `${GROUP}/${VERSION}`,
    kind: 'Brief',
    metadata: { name, namespace },
    spec: autopilot ? { idea, autopilot } : { idea },
  }
}

/** The JSON patch that appends one prompt; the only patch server.mjs passes on. */
export function promptPatch(prompt: string): [{ op: 'add'; path: '/spec/prompts/-'; value: string }] {
  return [{ op: 'add', path: '/spec/prompts/-', value: prompt }]
}

const DNS_LABEL = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/

/** Whether name can name a Brief: a DNS label of at most 63 characters. */
export function isBriefName(name: string): boolean {
  return name.length <= 63 && DNS_LABEL.test(name)
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

/** Creates Briefs and adds prompts to them; rejects with the API's message when it refuses. */
export interface BriefWriter {
  /** Unset autopilot leaves the factory's default. */
  create(namespace: string, name: string, idea: string, autopilot?: BriefAutopilot): Promise<void>
  addPrompt(namespace: string, name: string, prompt: string): Promise<void>
}

/** Writes through the /k8s proxy, relative to the page like the watches. */
export class LiveBriefWriter implements BriefWriter {
  private readonly baseUrl: string
  private readonly fetchImpl: typeof fetch

  constructor(baseUrl: string, fetchImpl: typeof fetch = (input, init) => fetch(input, init)) {
    this.baseUrl = baseUrl
    this.fetchImpl = fetchImpl
  }

  async create(namespace: string, name: string, idea: string, autopilot?: BriefAutopilot): Promise<void> {
    await this.send(`${this.baseUrl}${resourcePath(RESOURCES.briefs, namespace)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(briefManifest(namespace, name, idea, autopilot)),
    })
  }

  async addPrompt(namespace: string, name: string, prompt: string): Promise<void> {
    const path = `${resourcePath(RESOURCES.briefs, namespace)}/${encodeURIComponent(name)}`
    await this.send(`${this.baseUrl}${path}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json-patch+json', Accept: 'application/json' },
      body: JSON.stringify(promptPatch(prompt)),
    })
  }

  private async send(url: string, init: RequestInit): Promise<void> {
    const res = await this.fetchImpl(url, init)
    if (!res.ok) throw await errorFromResponse(res)
  }
}
