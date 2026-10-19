// The Container page's writes, with actions on (design.md, UI actions): retry
// a Failed scaffold through the c4.factory.io/retry annotation, answer the
// Standard's files left out through c4.factory.io/standard-take or
// c4.factory.io/standard-keep, set when CI runs the full check (spec.checks),
// approve the version waiting at one of its deploy targets (spec.deploy[i].approved),
// and start a Task from a goal with the Container's Crew. server.mjs passes on
// exactly these requests; the CRDs decide the rest, so a refused write shows
// the API server's message and nothing here copies their rules.

import { approvePatch } from '../model/deploys'
import { GROUP, VERSION, resourcePath, RESOURCES, type ChecksMode, type Crew, type Task } from './types'
import { errorFromResponse } from './watch'

/** The annotation whose new value makes the operator try a Failed Container's scaffold again. */
export const RETRY_ANNOTATION = 'c4.factory.io/retry'

/** The merge patch that retries a scaffold: the annotation set to `at`, a value it never had. */
export function retryPatch(at: Date): { metadata: { annotations: Record<string, string> } } {
  return { metadata: { annotations: { [RETRY_ANNOTATION]: at.toISOString() } } }
}

/** The annotation that makes the operator take the Standard's version, at its value's ref, of the files left out. */
export const STANDARD_TAKE_ANNOTATION = 'c4.factory.io/standard-take'

/** The annotation that makes the operator keep the repository's version, at its value's ref, of the files left out. */
export const STANDARD_KEEP_ANNOTATION = 'c4.factory.io/standard-keep'

/** A person's answer to the Standard's files left out: take the Standard's version, or keep the repository's. */
export type StandardAnswer = 'take' | 'keep'

/**
 * The merge patch that answers the files left out: the answer's annotation set
 * to `ref`, the Standard's ref in the Container's status, which the operator
 * checks it against.
 */
export function standardAnswerPatch(
  answer: StandardAnswer,
  ref: string,
): { metadata: { annotations: Record<string, string> } } {
  const key = answer === 'take' ? STANDARD_TAKE_ANNOTATION : STANDARD_KEEP_ANNOTATION
  return { metadata: { annotations: { [key]: ref } } }
}

/** A Task before the API server names it. */
export type NewTask = Omit<Task, 'metadata'> & { metadata: { generateName: string; namespace: string } }

/**
 * The Task to POST for a goal: kind Implement, the Container, and the
 * implementer and reviewer of its Crew; named by the API server after the
 * Container. The goal goes as given, so the CRD judges an empty one.
 */
export function taskManifest(namespace: string, container: string, crew: Crew, goal: string): NewTask {
  return {
    apiVersion: `${GROUP}/${VERSION}`,
    kind: 'Task',
    metadata: { generateName: `${container}-`, namespace },
    spec: {
      kind: 'Implement',
      containerRef: { name: container },
      agentRef: { name: crew.spec.implementer.name },
      reviewerRef: { name: crew.spec.reviewer.name },
      goal,
    },
  }
}

/** The JSON patch that sets spec.checks; the CRD's enum judges the value. */
export function checksPatch(checks: ChecksMode): { op: 'add'; path: '/spec/checks'; value: ChecksMode }[] {
  return [{ op: 'add', path: '/spec/checks', value: checks }]
}

/**
 * Retries a Container's scaffold, answers its Standard's files left out, sets
 * its checks and starts Tasks on it; rejects with the API's message when it
 * refuses.
 */
export interface ContainerWriter {
  retry(namespace: string, name: string): Promise<void>
  /** Takes or keeps the files left out at `ref`, the Standard's ref in the Container's status. */
  answerStandard(namespace: string, name: string, answer: StandardAnswer, ref: string): Promise<void>
  setChecks(namespace: string, name: string, checks: ChecksMode): Promise<void>
  /**
   * Approves `version` at spec.deploy's entry `index`, which must still name
   * `target` and, when given, still have `approved`, the approval the page showed.
   */
  approveDeploy(
    namespace: string,
    name: string,
    index: number,
    target: string,
    version: string,
    approved?: string,
  ): Promise<void>
  /** Resolves to the new Task's name. */
  startTask(namespace: string, container: string, crew: Crew, goal: string): Promise<string>
}

/** Writes through the /k8s proxy, relative to the page like the watches. */
export class LiveContainerWriter implements ContainerWriter {
  private readonly baseUrl: string
  private readonly fetchImpl: typeof fetch
  private readonly now: () => Date

  constructor(
    baseUrl: string,
    fetchImpl: typeof fetch = (input, init) => fetch(input, init),
    now: () => Date = () => new Date(),
  ) {
    this.baseUrl = baseUrl
    this.fetchImpl = fetchImpl
    this.now = now
  }

  async retry(namespace: string, name: string): Promise<void> {
    const path = `${resourcePath(RESOURCES.containers, namespace)}/${encodeURIComponent(name)}`
    await this.send(`${this.baseUrl}${path}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json', Accept: 'application/json' },
      body: JSON.stringify(retryPatch(this.now())),
    })
  }

  async answerStandard(namespace: string, name: string, answer: StandardAnswer, ref: string): Promise<void> {
    const path = `${resourcePath(RESOURCES.containers, namespace)}/${encodeURIComponent(name)}`
    await this.send(`${this.baseUrl}${path}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json', Accept: 'application/json' },
      body: JSON.stringify(standardAnswerPatch(answer, ref)),
    })
  }

  async setChecks(namespace: string, name: string, checks: ChecksMode): Promise<void> {
    const path = `${resourcePath(RESOURCES.containers, namespace)}/${encodeURIComponent(name)}`
    await this.send(`${this.baseUrl}${path}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json-patch+json', Accept: 'application/json' },
      body: JSON.stringify(checksPatch(checks)),
    })
  }

  async approveDeploy(
    namespace: string,
    name: string,
    index: number,
    target: string,
    version: string,
    approved?: string,
  ): Promise<void> {
    const path = `${resourcePath(RESOURCES.containers, namespace)}/${encodeURIComponent(name)}`
    await this.send(`${this.baseUrl}${path}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json-patch+json', Accept: 'application/json' },
      body: JSON.stringify(approvePatch(index, target, version, approved)),
    })
  }

  async startTask(namespace: string, container: string, crew: Crew, goal: string): Promise<string> {
    const res = await this.send(`${this.baseUrl}${resourcePath(RESOURCES.tasks, namespace)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(taskManifest(namespace, container, crew, goal)),
    })
    const task = (await res.json()) as Task
    return task.metadata.name
  }

  private async send(url: string, init: RequestInit): Promise<Response> {
    const res = await this.fetchImpl(url, init)
    if (!res.ok) throw await errorFromResponse(res)
    return res
  }
}
