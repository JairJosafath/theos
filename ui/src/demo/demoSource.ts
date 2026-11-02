// Demo mode's data source: the fixtures, kept "alive" by moving the heartbeat of
// Active sessions forward every 30 seconds, as a real Session client would. One
// project: the webshop. The story source also takes the UI's three writes in
// memory, so the Brief box and its Pause button work without a cluster.

import { briefManifest, type BriefOptions, type BriefSettings, type BriefWriter } from '../k8s/briefs'
import type { FactoryHealth } from '../k8s/health'
import type { Connection, FactoryData, Snapshot, Source, StoryData } from '../k8s/sources'
import type { Condition, Project } from '../k8s/types'
import { demoFactory, demoHealth } from './factoryFixture'
import { demoStory } from './fixture'
import { BRIEF_LIMITS, isBriefName } from './limits'

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

  /**
   * A new Brief shows up as Pending, as the API would answer before the operator
   * picks it up; one the CRD would refuse is refused with a message like the API server's.
   */
  async create(namespace: string, name: string, idea: string, options?: BriefOptions): Promise<void> {
    const data = this.snapshot.data
    const invalid = (detail: string) => new Error(`Brief.c4.factory.io "${name}" is invalid: ${detail}`)
    if (!isBriefName(name))
      throw invalid(
        `metadata.name: Invalid value: "${name}": a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters, '-' or '.'`,
      )
    if (data.briefs.some((b) => b.metadata.name === name))
      throw new Error(`briefs.c4.factory.io "${name}" already exists`)
    const { idea: ideaLimit } = BRIEF_LIMITS
    if (idea.length < ideaLimit.min)
      throw invalid(`spec.idea: Invalid value: "": should be at least ${ideaLimit.min} chars long`)
    if (idea.length > ideaLimit.max) throw invalid(`spec.idea: Too long: may not be more than ${ideaLimit.max} bytes`)
    checkParallel(options?.parallel, invalid)
    const brief = briefManifest(namespace, name, idea, options)
    brief.metadata = { ...brief.metadata, uid: `demo-${name}`, creationTimestamp: new Date().toISOString() }
    brief.status = { phase: 'Pending' }
    this.update({ ...data, briefs: [...data.briefs, brief] })
  }

  async addPrompt(_namespace: string, name: string, prompt: string): Promise<void> {
    const data = this.snapshot.data
    const brief = data.briefs.find((b) => b.metadata.name === name)
    if (!brief) throw new Error(`briefs.c4.factory.io "${name}" not found`)
    const invalid = (detail: string) => new Error(`Brief.c4.factory.io "${name}" is invalid: ${detail}`)
    const prompts = brief.spec.prompts ?? []
    const at = `spec.prompts[${prompts.length}]`
    if (prompt.length < BRIEF_LIMITS.prompt.min)
      throw invalid(`${at}: Invalid value: "": should be at least ${BRIEF_LIMITS.prompt.min} chars long`)
    if (prompt.length > BRIEF_LIMITS.prompt.max)
      throw invalid(`${at}: Too long: may not be more than ${BRIEF_LIMITS.prompt.max} bytes`)
    if (prompts.length >= BRIEF_LIMITS.prompts)
      throw invalid(
        `spec.prompts: Invalid value: a Brief lists at most ${BRIEF_LIMITS.prompts} prompts: in the write that appends, drop the oldest prompts rounds have planned (status.promptsPlanned counts them) and raise promptsRetired by as many`,
      )
    const changed = { ...brief, spec: { ...brief.spec, prompts: [...prompts, prompt] } }
    this.update({ ...data, briefs: data.briefs.map((b) => (b === brief ? changed : b)) })
  }

  /**
   * Sets spec.paused and, as the operator would, the Paused condition; the
   * demo has no Task running. A resume unsets spec.pauseRuns too.
   */
  async setPaused(_namespace: string, name: string, paused: boolean): Promise<void> {
    this.pause(name, paused, paused ? undefined : false)
  }

  /** Sets spec.pauseRuns, and spec.paused when true, as the API would take them. */
  async setPauseRuns(_namespace: string, name: string, pauseRuns: boolean): Promise<void> {
    this.pause(name, pauseRuns ? true : undefined, pauseRuns)
  }

  /** Sets spec.paused and spec.pauseRuns where given, and the Paused condition as the operator would. */
  private pause(name: string, paused: boolean | undefined, pauseRuns: boolean | undefined) {
    const data = this.snapshot.data
    const brief = data.briefs.find((b) => b.metadata.name === name)
    if (!brief) throw new Error(`briefs.c4.factory.io "${name}" not found`)
    const spec = { ...brief.spec }
    if (paused !== undefined) spec.paused = paused
    if (pauseRuns !== undefined) spec.pauseRuns = pauseRuns
    const condition: Condition = spec.paused
      ? { type: 'Paused', status: 'True', reason: 'Paused', message: 'Paused; nothing is running' }
      : { type: 'Paused', status: 'False', reason: 'Resumed', message: 'Not paused' }
    const conditions = [
      ...(brief.status?.conditions ?? []).filter((c) => c.type !== 'Paused'),
      { ...condition, lastTransitionTime: new Date().toISOString() },
    ]
    const changed = { ...brief, spec, status: { ...brief.status, conditions } }
    this.update({ ...data, briefs: data.briefs.map((b) => (b === brief ? changed : b)) })
  }

  /** Sets the settings given, refusing a parallel the CRD would; the demo has no operator to apply them. */
  async setSettings(_namespace: string, name: string, settings: BriefSettings): Promise<void> {
    const data = this.snapshot.data
    const brief = data.briefs.find((b) => b.metadata.name === name)
    if (!brief) throw new Error(`briefs.c4.factory.io "${name}" not found`)
    checkParallel(settings.parallel, (detail) => new Error(`Brief.c4.factory.io "${name}" is invalid: ${detail}`))
    const spec = { ...brief.spec }
    if (settings.autopilot !== undefined) spec.autopilot = settings.autopilot
    if (settings.parallel !== undefined) spec.parallel = settings.parallel as number
    if (settings.releases !== undefined) spec.releases = settings.releases
    const changed = { ...brief, spec }
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

/** Throws as the API server would for a parallel out of the CRD's range; unset passes. */
function checkParallel(parallel: number | string | undefined, invalid: (detail: string) => Error): void {
  const { min, max } = BRIEF_LIMITS.parallel
  if (
    parallel !== undefined &&
    !(typeof parallel === 'number' && Number.isInteger(parallel) && parallel >= min && parallel <= max)
  )
    throw invalid(
      `spec.parallel: Invalid value: ${JSON.stringify(parallel)}: should be a whole number from ${min} to ${max}`,
    )
}
