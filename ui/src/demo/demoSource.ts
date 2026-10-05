// Demo mode's data source: the fixtures, kept "alive" by moving the heartbeat of
// Active sessions forward every 30 seconds, as a real Session client would. One
// project: the webshop.

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
