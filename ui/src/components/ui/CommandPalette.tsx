// Search or jump (docs/plans/ui-shell.md): ⌘K or Ctrl+K anywhere, or / when
// not typing, opens a box that finds an area, a project or any object the page
// watches (model/search.ts). The arrow keys move through what it found, Enter
// goes there and Esc closes it.

import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
import { matchPalette, type PaletteItem } from '../../model/search'
import { SearchIcon } from '../icons'
import { useLayer } from './layer'
import './ui.css'

export interface CommandPaletteProps {
  open: boolean
  onClose: () => void
  items: PaletteItem[]
  onPick: (item: PaletteItem) => void
}

export function CommandPalette({ open, onClose, items, onPick }: CommandPaletteProps) {
  if (!open) return null
  return <PaletteBox onClose={onClose} items={items} onPick={onPick} />
}

/** The open palette; mounted fresh each time, so it opens empty. */
function PaletteBox({ onClose, items, onPick }: Omit<CommandPaletteProps, 'open'>) {
  const id = useId()
  const ref = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  useLayer(true, ref, onClose, { initialFocus: input })
  const found = useMemo(() => matchPalette(query, items), [query, items])
  const current = Math.min(active, Math.max(found.length - 1, 0))

  useEffect(() => {
    document.getElementById(`${id}-opt-${current}`)?.scrollIntoView?.({ block: 'nearest' })
  }, [id, current])

  function pick(item: PaletteItem | undefined) {
    if (!item) return
    onClose()
    onPick(item)
  }

  function onKeyDown(e: KeyboardEvent) {
    if (!found.length) return
    if (e.key === 'ArrowDown') setActive((current + 1) % found.length)
    else if (e.key === 'ArrowUp') setActive((current - 1 + found.length) % found.length)
    else if (e.key === 'Home' && e.ctrlKey) setActive(0)
    else if (e.key === 'End' && e.ctrlKey) setActive(found.length - 1)
    else if (e.key === 'Enter') pick(found[current])
    else return
    e.preventDefault()
  }

  return createPortal(
    <div
      className="overlay overlay-top"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div ref={ref} className="palette" role="dialog" aria-modal="true" aria-label="Search or jump">
        <div className="palette-search">
          <SearchIcon size={18} />
          <input
            ref={input}
            className="palette-input"
            type="text"
            role="combobox"
            aria-expanded="true"
            aria-controls={`${id}-list`}
            aria-autocomplete="list"
            aria-activedescendant={found.length ? `${id}-opt-${current}` : undefined}
            aria-label="Search areas, projects and objects"
            placeholder="Search areas, projects and objects…"
            spellCheck={false}
            autoComplete="off"
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setActive(0)
            }}
            onKeyDown={onKeyDown}
          />
          <kbd className="kbd">Esc</kbd>
        </div>
        <ul className="palette-list" id={`${id}-list`} role="listbox" aria-label="Results">
          {found.map((item, i) => (
            <li
              key={item.key}
              id={`${id}-opt-${i}`}
              role="option"
              aria-selected={i === current}
              className="palette-option"
              onMouseMove={() => i !== current && setActive(i)}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => pick(item)}
            >
              <span className={`palette-type palette-type-${item.type}`}>
                {item.type === 'object' ? item.kind : item.type === 'area' ? 'Area' : 'Project'}
              </span>
              <span className="palette-label">
                {item.type === 'object' ? item.label.slice((item.kind?.length ?? -1) + 1) : item.label}
                {item.extra && item.type === 'object' ? <span className="palette-extra"> · {item.extra}</span> : null}
              </span>
              <span className="palette-hint">{item.hint}</span>
            </li>
          ))}
        </ul>
        {found.length ? null : (
          <p className="palette-empty" role="status">
            Nothing matches “{query}”.
          </p>
        )}
        <p className="palette-foot" aria-hidden="true">
          <kbd className="kbd">↑</kbd>
          <kbd className="kbd">↓</kbd> to move · <kbd className="kbd">Enter</kbd> to open
        </p>
      </div>
    </div>,
    document.body,
  )
}
