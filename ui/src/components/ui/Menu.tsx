// A button that opens a list of choices (docs/plans/done/ui-shell.md), and the
// popover under it: the arrow keys, Home and End move through the items,
// Enter or Space picks one, Esc closes it and gives the focus back to the
// button, and a click elsewhere or Tab closes it.

import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { usePopover } from './popover'
import './ui.css'

function itemsIn(list: HTMLElement | null): HTMLElement[] {
  return [...(list?.querySelectorAll<HTMLElement>('[role^="menuitem"]') ?? [])]
}

export interface MenuItem {
  id: string
  label: ReactNode
  /** A second line, such as a namespace. */
  hint?: ReactNode
  /** For a choice of one: whether this is the one chosen. */
  checked?: boolean
  onSelect: () => void
}

export interface MenuProps {
  /** What the button shows. */
  button: ReactNode
  /** The button's accessible name, when what it shows doesn't say it. */
  buttonLabel?: string
  buttonClassName?: string
  /** The menu's accessible name. */
  label: string
  items: MenuItem[]
  /** Which edge of the button the menu lines up with. */
  align?: 'start' | 'end'
  className?: string
}

export function Menu({ button, buttonLabel, buttonClassName, label, items, align = 'start', className }: MenuProps) {
  const id = useId()
  const [open, setOpen] = useState(false)
  const wrapper = useRef<HTMLDivElement>(null)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const list = useRef<HTMLDivElement>(null)
  usePopover(open, () => setOpen(false), wrapper, buttonRef)

  // Opening puts the focus on the chosen item, else the first (or the last, from ArrowUp).
  const [focusLast, setFocusLast] = useState(false)
  useEffect(() => {
    if (!open) return
    const all = itemsIn(list.current)
    const chosen = all.find((el) => el.getAttribute('aria-checked') === 'true')
    ;(focusLast ? all[all.length - 1] : (chosen ?? all[0]))?.focus()
  }, [open, focusLast])

  function show(last = false) {
    setFocusLast(last)
    setOpen(true)
  }

  function onButtonKey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      show(e.key === 'ArrowUp')
    }
  }

  function onListKey(e: KeyboardEvent) {
    const all = itemsIn(list.current)
    const at = all.indexOf(document.activeElement as HTMLElement)
    const to =
      e.key === 'ArrowDown'
        ? all[(at + 1) % all.length]
        : e.key === 'ArrowUp'
          ? all[(at - 1 + all.length) % all.length]
          : e.key === 'Home'
            ? all[0]
            : e.key === 'End'
              ? all[all.length - 1]
              : undefined
    if (to) {
      e.preventDefault()
      to.focus()
    } else if (e.key === 'Tab') {
      setOpen(false)
    }
  }

  return (
    <div className={`menu${className ? ` ${className}` : ''}`} ref={wrapper}>
      <button
        ref={buttonRef}
        type="button"
        className={buttonClassName}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? `${id}-menu` : undefined}
        aria-label={buttonLabel}
        onClick={() => (open ? setOpen(false) : show())}
        onKeyDown={onButtonKey}
      >
        {button}
      </button>
      {open ? (
        <div
          ref={list}
          id={`${id}-menu`}
          className={`menu-list menu-${align}`}
          role="menu"
          aria-label={label}
          onKeyDown={onListKey}
        >
          {items.map((item) => (
            <button
              key={item.id}
              type="button"
              className="menu-item"
              role={item.checked === undefined ? 'menuitem' : 'menuitemradio'}
              aria-checked={item.checked}
              tabIndex={-1}
              onClick={() => {
                setOpen(false)
                buttonRef.current?.focus()
                item.onSelect()
              }}
            >
              <span className="menu-item-label">{item.label}</span>
              {item.hint ? <span className="menu-item-hint">{item.hint}</span> : null}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  )
}
