// A panel from the right over the page (docs/plans/ui-shell.md), for an
// object's page over the area it was opened from: a dialog like Modal, with
// room for a page.

import { useEffect, useRef, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { CrossIcon } from '../icons'
import { useLayer } from './layer'
import './ui.css'

export interface DrawerProps {
  open: boolean
  onClose: () => void
  /** The dialog's accessible name, such as "Task orders-api-21". */
  label: string
  /** Shown in the drawer's bar, beside the close button: a link or two. */
  actions?: ReactNode
  /** A new key starts the drawer's scroll at the top, as another object opens in it. */
  contentKey?: string
  children: ReactNode
}

export function Drawer({ open, onClose, label, actions, contentKey, children }: DrawerProps) {
  const ref = useRef<HTMLDivElement>(null)
  const body = useRef<HTMLDivElement>(null)
  // The drawer itself takes the focus, so its name is read before its contents.
  useLayer(open, ref, onClose, { initialFocus: ref })
  useEffect(() => {
    body.current?.scrollTo?.(0, 0)
    // Another object opened from a link in it: the link is gone, so the drawer takes the focus again.
    if (ref.current && !ref.current.contains(document.activeElement)) ref.current.focus({ preventScroll: true })
  }, [contentKey])
  if (!open) return null
  return createPortal(
    <div
      className="overlay overlay-right"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div ref={ref} className="drawer" role="dialog" aria-modal="true" aria-label={label} tabIndex={-1}>
        <div className="drawer-bar">
          <span className="drawer-label">{label}</span>
          <div className="drawer-actions">
            {actions}
            <button type="button" className="icon-button" aria-label="Close" onClick={onClose}>
              <CrossIcon size={18} />
            </button>
          </div>
        </div>
        <div className="drawer-body" ref={body}>
          {children}
        </div>
      </div>
    </div>,
    document.body,
  )
}
