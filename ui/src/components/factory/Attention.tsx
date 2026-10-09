// The factory view's "needs a look" list: errors first, each linking to its Task.

import { WarningIcon } from '../icons'
import { attentionKey, type Attention } from '../../model/attention'
import type { Problem } from '../../k8s/sources'
import { ReadProblems } from '../Status'

export function AttentionPanel({
  items,
  problems = [],
  onSelectTask,
}: {
  items: Attention[]
  /** The failed reads that may hide items. */
  problems?: Problem[]
  onSelectTask: (key: string) => void
}) {
  return (
    <div className="panel attention">
      <h2 className="panel-title small-title">
        <WarningIcon size={16} /> Needs a look
      </h2>
      <ReadProblems problems={problems} />
      <ul className="attention-list">
        {items.map((item) => (
          <li
            key={attentionKey(item)}
            className={`attention-item tone-${item.level === 'error' ? 'error' : 'waiting'}`}
          >
            <span className="badge-dot" aria-hidden="true" />
            <div>
              {item.task ? (
                <button type="button" className="link-button" onClick={() => onSelectTask(item.task!)}>
                  {item.title}
                </button>
              ) : (
                <strong>{item.title}</strong>
              )}
              {item.detail ? <p className="attention-detail">{item.detail}</p> : null}
              {item.brief ? <p className="attention-detail attention-brief">{item.brief.note}</p> : null}
            </div>
          </li>
        ))}
      </ul>
    </div>
  )
}
