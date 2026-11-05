// A popover's way out: a click outside it, or Esc, which gives the focus back
// to the button that opened it. Menu and the demo chip use it.

import { useEffect, useRef, type RefObject } from 'react'

/** Closes an open popover on a click outside `wrapper`, or on Esc (focus back to `button`). */
export function usePopover(
  open: boolean,
  close: () => void,
  wrapper: RefObject<HTMLElement | null>,
  button: RefObject<HTMLElement | null>,
): void {
  const onClose = useRef(close)
  useEffect(() => {
    onClose.current = close
  })
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (!wrapper.current?.contains(e.target as Node)) onClose.current()
    }
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== 'Escape' || !wrapper.current?.contains(document.activeElement)) return
      e.preventDefault()
      e.stopPropagation()
      onClose.current()
      button.current?.focus()
    }
    document.addEventListener('mousedown', onDown)
    // Capture, so an overlay under it doesn't close first.
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey, true)
    }
  }, [open, wrapper, button])
}
