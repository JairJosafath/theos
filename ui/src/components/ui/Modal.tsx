// A dialog over the page (docs/plans/ui-shell.md): labelled by its title, the
// keyboard kept inside, Esc, the close button or a click on the backdrop closes
// it, and the focus goes back to what opened it.

import { useId, useRef, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { CrossIcon } from '../icons'
import { useLayer } from './layer'
import './ui.css'

export interface ModalProps {
  open: boolean
  onClose: () => void
  title: ReactNode
  /** A line under the title. */
  description?: ReactNode
  children: ReactNode
  /** What takes the focus on opening; else the first control. */
  initialFocus?: RefObject<HTMLElement | null>
  size?: 'narrow' | 'regular'
  className?: string
}

export function Modal({
  open,
  onClose,
  title,
  description,
  children,
  initialFocus,
  size = 'regular',
  className,
}: ModalProps) {
  const ref = useRef<HTMLDivElement>(null)
  const id = useId()
  useLayer(open, ref, onClose, { initialFocus })
  if (!open) return null
  return createPortal(
    <div
      className="overlay overlay-center"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div
        ref={ref}
        className={`modal modal-${size}${className ? ` ${className}` : ''}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={`${id}-title`}
        aria-describedby={description ? `${id}-desc` : undefined}
        tabIndex={-1}
      >
        <div className="modal-head">
          <div className="modal-heading">
            <h2 className="modal-title" id={`${id}-title`}>
              {title}
            </h2>
            {description ? (
              <p className="modal-desc" id={`${id}-desc`}>
                {description}
              </p>
            ) : null}
          </div>
          <button type="button" className="icon-button" aria-label="Close" onClick={onClose}>
            <CrossIcon size={18} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>,
    document.body,
  )
}
