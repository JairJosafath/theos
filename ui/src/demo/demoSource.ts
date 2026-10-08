// Demo mode's data source: the fixtures, kept "alive" by moving the heartbeat of
// Active sessions forward every 30 seconds, as a real Session client would. One
// project: the webshop. The story source also takes the UI's two writes in
// memory, so the Brief box works without a cluster.

import {
  briefManifest,
  isBriefName,
  MAX_IDEA,
  MAX_PARALLEL,
  MAX_PROMPT,
  type BriefOptions,
  type BriefWriter,
} from '../k8s/briefs'
import type { FactoryHealth } from '../k8s/health'
import type { Connection, FactoryData, Snapshot, Source, StoryData } from '../k8s/sources'
import type { Project } from '../k8s/types'
import { demoFactory, demoHealth } from './factoryFixture'
import { demoStory } from './fixture'

const DEMO_CONNECTION: Connection = { phase: 'demo', loaded: true, problems: [] }
const HEARTBEAT_MS = 30_000

class StaticSource<T> implements Source<T> {
  protected snapshot: Snapshot<T>
  protected readonly listeners = new Set<() => void>()

  constructor(data: T) {
    this.snapshot = { data, connection: DEMO_CONNECTION }
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  getSnapshot(): Snapshot<T> {
    return this.snapshot
  }

  start(): void {}

  stop(): void {}
}

const MAX_PROMPTS = 50

export class DemoStorySource extends StaticSource<StoryData> implements BriefWriter {
  private timer?: ReturnType<typeof setInterval>

  constructor(now = Date.now()) {
    super(demoStory(now))
  }

  start(): void {
    if (this.timer) return
    this.timer = setInterval(() => this.heartbeat(), HEARTBEAT_MS)
  }

  stop(): void {
    clearInterval(this.timer)
    this.timer = undefined
  }

  /** A new Brief shows up as Pending, as the API would answer before the operator picks it up. */
  async create(namespace: string, name: string, idea: string, options?: BriefOptions): Promise<void> {
    const data = this.snapshot.data
    if (!isBriefName(name)) throw new Error(`Brief name "${name}" must be lowercase letters, digits and '-'`)
    if (data.briefs.some((b) => b.metadata.name === name))
      throw new Error(`briefs.c4.factory.io "${name}" already exists`)
    if (!idea.trim() || idea.length > MAX_IDEA) throw new Error(`spec.idea must be 1 to ${MAX_IDEA} characters`)
    const parallel = options?.parallel
    if (parallel !== undefined && !(Number.isInteger(parallel) && parallel >= 1 && parallel <= MAX_PARALLEL))
      throw new Error(`spec.parallel must be 1 to ${MAX_PARALLEL}`)
    const brief = briefManifest(namespace, name, idea, options)
    brief.metadata = { ...brief.metadata, uid: `demo-${name}`, creationTimestamp: new Date().toISOString() }
    brief.status = { phase: 'Pending' }
    this.update({ ...data, briefs: [...data.briefs, brief] })
  }

  async addPrompt(_namespace: string, name: string, prompt: string): Promise<void> {
    const data = this.snapshot.data
    const brief = data.briefs.find((b) => b.metadata.name === name)
    if (!brief) throw new Error(`briefs.c4.factory.io "${name}" not found`)
    if (!prompt.trim() || prompt.length > MAX_PROMPT) throw new Error(`a prompt must be 1 to ${MAX_PROMPT} characters`)
    const prompts = brief.spec.prompts ?? []
    if (prompts.length >= MAX_PROMPTS) throw new Error(`spec.prompts: at most ${MAX_PROMPTS} prompts`)
    const changed = { ...brief, spec: { ...brief.spec, prompts: [...prompts, prompt] } }
    this.update({ ...data, briefs: data.briefs.map((b) => (b === brief ? changed : b)) })
  }

  private update(data: StoryData): void {
    this.snapshot = { ...this.snapshot, data }
    for (const listener of this.listeners) listener()
  }

  private heartbeat(): void {
    const at = new Date().toISOString()
    const data = this.snapshot.data
    this.update({
      ...data,
      sessions: data.sessions.map((s) =>
        s.status?.phase === 'Active' ? { ...s, status: { ...s.status, lastHeartbeat: at } } : s,
      ),
    })
  }
}

export class DemoProjectsSource extends StaticSource<Project[]> {
  constructor(now = Date.now()) {
    super([demoStory(now).project].filter((p): p is Project => Boolean(p)))
  }
}

export class DemoFactorySource extends StaticSource<FactoryData> {
  constructor(now = Date.now()) {
    super(demoFactory(now))
  }
}

export class DemoHealthSource extends StaticSource<FactoryHealth | undefined> {
  constructor(now = Date.now()) {
    super(demoHealth(now))
  }
}
