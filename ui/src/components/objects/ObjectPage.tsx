// The object page the URL names, from the watched data of its namespace: the
// page of its kind once it is loaded, or what keeps it from showing (not
// there, still loading, or a read the API server refused).

import type { BriefWriter } from '../../k8s/briefs'
import type { ContainerWriter } from '../../k8s/containers'
import type { CrewWriter } from '../../k8s/crews'
import type { StandardWriter } from '../../k8s/standards'
import { RESOURCES, type K8sObject } from '../../k8s/types'
import { problemsOf, type Connection, type StoryData } from '../../k8s/sources'
import type { LifecycleKind } from '../../model/lifecycle'
import { BriefPage } from './BriefPage'
import { BundlePage, CatalogPage, DomainPage, GitProviderPage } from './CatalogPages'
import { ContainerPage } from './ContainerPage'
import { DeployTargetPage } from './DeployPages'
import { CrewPage } from './CrewPage'
import { ProjectPage, RelationshipPage, SessionPage, SystemPage } from './ModelPages'
import { ReadProblems } from '../Status'
import { StandardActions } from './StandardActions'
import { BenchmarkPage } from './BenchmarkPage'
import { TaskPage } from './TaskPage'
import { TestEnvironmentPage } from './TestEnvironmentPages'
import '../objects.css'

/** Where each kind's objects are in the story data, and the plural its read is reported under. */
const WHERE: Record<LifecycleKind, { list: (d: StoryData) => K8sObject[]; resource: string }> = {
  Brief: { list: (d) => d.briefs, resource: RESOURCES.briefs },
  Container: { list: (d) => d.containers, resource: RESOURCES.containers },
  Crew: { list: (d) => d.crews, resource: RESOURCES.crews },
  Task: { list: (d) => d.tasks, resource: RESOURCES.tasks },
  Project: { list: (d) => (d.project ? [d.project] : []), resource: RESOURCES.projects },
  SoftwareSystem: { list: (d) => d.systems, resource: RESOURCES.softwareSystems },
  Relationship: { list: (d) => d.relationships, resource: RESOURCES.relationships },
  Session: { list: (d) => d.sessions, resource: RESOURCES.sessions },
  Standard: { list: (d) => d.standards, resource: RESOURCES.standards },
  Compute: { list: (d) => d.computes, resource: RESOURCES.computes },
  Deployment: { list: (d) => d.deployments, resource: RESOURCES.deployments },
  Bundle: { list: (d) => d.bundles, resource: RESOURCES.bundles },
  Domain: { list: (d) => d.domains, resource: RESOURCES.domains },
  GitProvider: { list: (d) => d.gitProviders, resource: RESOURCES.gitProviders },
  DeployTarget: { list: (d) => d.deployTargets, resource: RESOURCES.deployTargets },
  Benchmark: { list: (d) => d.benchmarks, resource: RESOURCES.benchmarks },
  TestEnvironment: { list: (d) => d.testEnvironments, resource: RESOURCES.testEnvironments },
}

export function ObjectPage({
  kind,
  name,
  data,
  connection,
  writer,
  containerWriter,
  crewWriter,
  standardWriter,
}: {
  kind: LifecycleKind
  name: string
  data: StoryData
  connection: Connection
  writer: BriefWriter
  containerWriter: ContainerWriter
  crewWriter: CrewWriter
  standardWriter: StandardWriter
}) {
  const page = pageFor(kind, name, data, connection, writer, containerWriter, crewWriter, standardWriter)
  if (page) return page
  const failed = problemsOf(connection, WHERE[kind].resource)
  return (
    <div className="shell-page">
      <section className="panel" aria-label={`${kind} ${name}`}>
        <div className="eyebrow">
          {kind} · {data.namespace}
        </div>
        <h1 className="panel-title mono">{name}</h1>
        {failed.length ? (
          <ReadProblems problems={failed} />
        ) : (
          <p className="panel-text">
            {/* An optional kind's list may come after the required ones. */}
            {(connection.settled ?? connection.loaded)
              ? `There is no ${kind} named ${name} in ${data.namespace}: it may have been deleted.`
              : `Loading the ${kind}…`}
          </p>
        )}
      </section>
    </div>
  )
}

function pageFor(
  kind: LifecycleKind,
  name: string,
  data: StoryData,
  connection: Connection,
  writer: BriefWriter,
  containerWriter: ContainerWriter,
  crewWriter: CrewWriter,
  standardWriter: StandardWriter,
) {
  const named = <T extends K8sObject>(list: T[]) => list.find((o) => o.metadata.name === name)
  switch (kind) {
    case 'Brief': {
      const brief = named(data.briefs)
      return brief && <BriefPage brief={brief} agents={data.agents} writer={writer} />
    }
    case 'Container': {
      const container = named(data.containers)
      return (
        container && (
          <ContainerPage
            key={`${data.namespace}/${name}`}
            container={container}
            relationships={data.relationships}
            sessions={data.sessions}
            frameworks={data.frameworks}
            briefs={data.briefs}
            crews={data.crews}
            deployTargets={data.deployTargets}
            domains={data.domains}
            project={data.project}
            writer={containerWriter}
          />
        )
      )
    }
    case 'Crew': {
      const crew = named(data.crews)
      return (
        crew && (
          <CrewPage
            key={`${data.namespace}/${name}`}
            crew={crew}
            agents={data.agents}
            tasks={data.tasks}
            containers={data.containers}
            standards={data.standards}
            briefs={data.briefs}
            writer={crewWriter}
            briefWriter={writer}
          />
        )
      )
    }
    case 'Task': {
      const task = named(data.tasks)
      return task && <TaskPage task={task} agents={data.agents} tasks={data.tasks} />
    }
    case 'Project': {
      const project = data.project?.metadata.name === name ? data.project : undefined
      return project && <ProjectPage project={project} data={data} connection={connection} />
    }
    case 'SoftwareSystem': {
      const system = named(data.systems)
      return system && <SystemPage system={system} data={data} />
    }
    case 'Relationship': {
      const relationship = named(data.relationships)
      return relationship && <RelationshipPage relationship={relationship} data={data} />
    }
    case 'Session': {
      const session = named(data.sessions)
      return session && <SessionPage session={session} data={data} />
    }
    case 'Standard': {
      const standard = named(data.standards)
      return (
        standard && (
          <CatalogPage
            kind="Standard"
            entry={standard}
            containers={data.containers}
            actions={<StandardActions standard={standard} containers={data.containers} writer={standardWriter} />}
          />
        )
      )
    }
    case 'Compute': {
      const compute = named(data.computes)
      return compute && <CatalogPage kind="Compute" entry={compute} containers={data.containers} />
    }
    case 'Deployment': {
      const deployment = named(data.deployments)
      return deployment && <CatalogPage kind="Deployment" entry={deployment} containers={data.containers} />
    }
    case 'Bundle': {
      const bundle = named(data.bundles)
      return (
        bundle && <BundlePage bundle={bundle} catalog={data} containers={data.containers} connection={connection} />
      )
    }
    case 'Domain': {
      const domain = named(data.domains)
      return domain && <DomainPage domain={domain} systems={data.systems} containers={data.containers} />
    }
    case 'GitProvider': {
      const provider = named(data.gitProviders)
      return provider && <GitProviderPage provider={provider} containers={data.containers} />
    }
    case 'DeployTarget': {
      const target = named(data.deployTargets)
      return target && <DeployTargetPage target={target} containers={data.containers} />
    }
    case 'Benchmark': {
      const bench = named(data.benchmarks)
      return bench && <BenchmarkPage bench={bench} tasks={data.tasks} />
    }
    case 'TestEnvironment': {
      const env = named(data.testEnvironments)
      return env && <TestEnvironmentPage env={env} />
    }
  }
}
