// The object page the URL names, from the watched data of its namespace: the
// page of its kind once it is loaded, or what keeps it from showing.

import type { BriefWriter } from '../../k8s/briefs'
import type { ContainerWriter } from '../../k8s/containers'
import type { StoryData } from '../../k8s/sources'
import { BriefPage } from './BriefPage'
import type { LifecycleKind } from '../../model/lifecycle'
import { ContainerPage } from './ContainerPage'
import { CrewPage } from './CrewPage'
import { TaskPage } from './TaskPage'
import '../objects.css'

export function ObjectPage({
  kind,
  name,
  data,
  loaded,
  writer,
  containerWriter,
}: {
  kind: LifecycleKind
  name: string
  data: StoryData
  loaded: boolean
  writer: BriefWriter
  containerWriter: ContainerWriter
}) {
  if (kind === 'Brief') {
    const brief = data.briefs.find((b) => b.metadata.name === name)
    if (brief) return <BriefPage brief={brief} agents={data.agents} writer={writer} />
  } else if (kind === 'Crew') {
    const crew = data.crews.find((c) => c.metadata.name === name)
    if (crew)
      return (
        <CrewPage
          crew={crew}
          agents={data.agents}
          tasks={data.tasks}
          containers={data.containers}
          briefs={data.briefs}
        />
      )
  } else if (kind === 'Task') {
    const task = data.tasks.find((t) => t.metadata.name === name)
    if (task) return <TaskPage task={task} agents={data.agents} />
  } else {
    const container = data.containers.find((c) => c.metadata.name === name)
    if (container)
      return (
        <ContainerPage
          key={`${data.namespace}/${name}`}
          container={container}
          relationships={data.relationships}
          sessions={data.sessions}
          frameworks={data.frameworks}
          briefs={data.briefs}
          crews={data.crews}
          writer={containerWriter}
        />
      )
  }
  return (
    <main className="shell-page">
      <section className="panel">
        <div className="eyebrow">
          {kind} · {data.namespace}
        </div>
        <h1 className="panel-title mono">{name}</h1>
        <p className="panel-text">
          {loaded
            ? `There is no ${kind} named ${name} in ${data.namespace}: it may have been deleted.`
            : `Loading the ${kind}…`}
        </p>
      </section>
    </main>
  )
}
