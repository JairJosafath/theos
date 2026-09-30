// Demo mode's data source: the fixture, kept "alive" by moving the heartbeat of
// Active sessions forward every 30 seconds, as a real Session client would.

import type { Connection, Snapshot, Source, StoryData } from '../k8s/sources'
import type { Project } from '../k8s/types'
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

export class DemoStorySource extends StaticSource<StoryData> {
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

  private heartbeat(): void {
    const at = new Date().toISOString()
    const data = this.snapshot.data
    this.snapshot = {
      ...this.snapshot,
      data: {
        ...data,
        sessions: data.sessions.map((s) =>
          s.status?.phase === 'Active' ? { ...s, status: { ...s.status, lastHeartbeat: at } } : s,
        ),
      },
    }
    for (const listener of this.listeners) listener()
  }
}

export class DemoProjectsSource extends StaticSource<Project[]> {
  constructor(now = Date.now()) {
    const project = demoStory(now).project
    super(project ? [project] : [])
  }
}
