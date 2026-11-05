// Tabs (docs/plans/ui-shell.md): a tablist with roving focus, so Tab enters the
// list once and the arrow keys, Home and End move between tabs and select the
// one they land on. With `syncUrl`, the selected tab is the address's `tab=`, so
// a link opens it and the page keeps it on reload.

import { useContext, useId, useState, type KeyboardEvent, type ReactNode } from 'react'
import { UrlContext } from '../../lib/hooks'
import './ui.css'

export interface TabItem {
  id: string
  label: ReactNode
  panel: ReactNode
  disabled?: boolean
}

export interface TabsProps {
  tabs: TabItem[]
  /** The tablist's accessible name, such as "Work". */
  label: string
  /** The selected tab, when the caller keeps it; else the Tabs keep their own, or the address's. */
  value?: string
  onChange?: (id: string) => void
  /** Keep the selected tab in the address as tab=. */
  syncUrl?: boolean
  className?: string
}

export function Tabs({ tabs, label, value, onChange, syncUrl, className }: TabsProps) {
  const id = useId()
  const url = useContext(UrlContext)
  const [own, setOwn] = useState<string>()
  const enabled = tabs.filter((t) => !t.disabled)
  const wanted = value ?? (syncUrl ? url?.[0].tab : own)
  const selected = enabled.find((t) => t.id === wanted) ?? enabled[0]

  function select(tabId: string) {
    if (tabId === selected?.id) return
    if (value === undefined) {
      // The address keeps the tab without a history entry of its own, so Back leaves the page.
      if (syncUrl && url) url[1]({ tab: tabId }, true)
      else setOwn(tabId)
    }
    onChange?.(tabId)
  }

  function onKeyDown(e: KeyboardEvent) {
    if (!selected || !enabled.length) return
    const at = enabled.findIndex((t) => t.id === selected.id)
    const to =
      e.key === 'ArrowRight' || e.key === 'ArrowDown'
        ? enabled[(at + 1) % enabled.length]
        : e.key === 'ArrowLeft' || e.key === 'ArrowUp'
          ? enabled[(at - 1 + enabled.length) % enabled.length]
          : e.key === 'Home'
            ? enabled[0]
            : e.key === 'End'
              ? enabled[enabled.length - 1]
              : undefined
    if (!to) return
    e.preventDefault()
    select(to.id)
    document.getElementById(`${id}-tab-${to.id}`)?.focus()
  }

  return (
    <div className={`tabs-ui${className ? ` ${className}` : ''}`}>
      <div className="tabs-ui-list" role="tablist" aria-label={label} onKeyDown={onKeyDown}>
        {tabs.map((t) => {
          const on = t.id === selected?.id
          return (
            <button
              key={t.id}
              type="button"
              role="tab"
              id={`${id}-tab-${t.id}`}
              className="tabs-ui-tab"
              aria-selected={on}
              // Only the selected tab's panel is on the page.
              aria-controls={on ? `${id}-panel-${t.id}` : undefined}
              tabIndex={on ? 0 : -1}
              disabled={t.disabled}
              onClick={() => select(t.id)}
            >
              {t.label}
            </button>
          )
        })}
      </div>
      {selected ? (
        <div
          className="tabs-ui-panel"
          role="tabpanel"
          id={`${id}-panel-${selected.id}`}
          aria-labelledby={`${id}-tab-${selected.id}`}
          tabIndex={0}
        >
          {selected.panel}
        </div>
      ) : null}
    </div>
  )
}
