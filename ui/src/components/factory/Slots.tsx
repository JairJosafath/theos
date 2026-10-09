// The agent slots: the Tasks holding one now, and the queue waiting for one.

import type { FactoryHealth, PodHealth } from '../../k8s/health'
import type { Task } from '../../k8s/types'
import { currentStage, runningTasks, slotQueue, stageLabel, taskKey, type Stage } from '../../model/factory'
import { formatBytes, formatDuration } from '../../model/format'
import type { Problem } from '../../k8s/sources'
import { PanelState } from '../PanelState'

export function SlotsPanel({
  tasks,
  loaded,
  problems,
  health,
  now,
  onSelectTask,
}: {
  tasks: Task[]
  /** The Tasks have loaded. */
  loaded: boolean
  /** The failed read of Tasks. */
  problems?: Problem[]
  health?: FactoryHealth
  now: number
  onSelectTask: (key: string) => void
}) {
  const running = runningTasks(tasks)
  const queue = slotQueue(tasks)
  const sandboxes = new Map(
    (health?.agents.sandboxes ?? []).map(
      (s) => [`${s.namespace}/${s.task}`, s] as [string, PodHealth & { task: string }],
    ),
  )
  return (
    <div className="panel">
      <h2 className="panel-title small-title">Agent slots</h2>
      <p className="panel-caption">
        The operator runs at most {health?.agents.maxSandboxes ?? '…'} agent sandbox
        {health?.agents.maxSandboxes === 1 ? '' : 'es'} at a time; others wait their turn, longest waiter first.
      </p>
      <PanelState small loaded={loaded} problems={problems} loading="Loading the Tasks…" empty={!tasks.length}>
        <h3 className="section-title">Running</h3>
        {running.length ? (
          <ul className="slot-list">
            {running.map((t) => {
              const pod = sandboxes.get(taskKey(t))
              return (
                <li key={taskKey(t)}>
                  <button type="button" className="link-button" onClick={() => onSelectTask(taskKey(t))}>
                    {t.metadata.name}
                  </button>
                  <span className="muted">
                    {' '}
                    · {stageLabel(currentStage(t) as Stage).toLowerCase()}
                    {pod
                      ? ` · ${pod.phase}${pod.memoryBytes !== undefined ? `, ${formatBytes(pod.memoryBytes)}` : ''}`
                      : ''}
                  </span>
                </li>
              )
            })}
          </ul>
        ) : (
          <p className="panel-note">No sandbox is running.</p>
        )}
        <h3 className="section-title">Waiting</h3>
        {queue.length ? (
          <ol className="slot-list">
            {queue.map((t) => (
              <li key={taskKey(t)}>
                <button type="button" className="link-button" onClick={() => onSelectTask(taskKey(t))}>
                  {t.metadata.name}
                </button>
                <span className="muted"> · {formatDuration(now - Date.parse(t.status?.slotWaitSince ?? ''))}</span>
              </li>
            ))}
          </ol>
        ) : (
          <p className="panel-note">Nothing is waiting for a slot.</p>
        )}
      </PanelState>
    </div>
  )
}
