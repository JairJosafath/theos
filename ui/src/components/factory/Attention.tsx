// The Work area's "needs a look" strip (docs/plans/done/ui-shell.md, step 2): one
// line with how many items there are and the first of them, errors first,
// that opens to the whole list; each item links to its Task.

import { useId, useState } from 'react'
import { ChevronDownIcon, WarningIcon } from '../icons'
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
  const [open, setOpen] = useState(false)
  const id = useId()
  const errors = items.some((i) => i.level === 'error')
  const first = items[0]
  return (
    <section className={`attention${errors ? ' attention-error' : ''}`} aria-label="Needs a look">
      <h2 className="attention-bar">
        <button
          type="button"
          className="fold-button attention-toggle"
          aria-expanded={open}
          aria-controls={id}
          onClick={() => setOpen(!open)}
        >
          <WarningIcon size={16} className="attention-icon" />
          <span className="attention-label">Needs a look</span>
          <span className="chip-count">{items.length}</span>
          {first ? (
            <span className="attention-first">
              {first.title}
              {items.length > 1 ? <span className="muted"> and {items.length - 1} more</span> : null}
            </span>
          ) : (
            <span className="attention-first" />
          )}
          <span className="attention-more">{open ? 'Hide' : 'Show all'}</span>
          <ChevronDownIcon size={16} className="fold-chevron" />
        </button>
      </h2>
      {/* A failed read may hide items: it shows whether the list is open or not. */}
      <ReadProblems problems={problems} />
      <ul className="attention-list" id={id} hidden={!open}>
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
    </section>
  )
}
