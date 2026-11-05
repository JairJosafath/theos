// What every overlay shares (Modal, Drawer, the command palette and the phone's
// navigation): while open it keeps the keyboard inside, Esc closes it, the page
// behind does not scroll, and on closing the focus goes back to what opened it.
// Overlays stack: only the top one answers a key.

import { useEffect, useRef, type RefObject } from 'react'

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'summary',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

/** The elements inside `root` the Tab key reaches, in order. */
export function focusables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
    (el) => !el.closest('[inert], [hidden], [aria-hidden="true"]') && !el.closest('dialog:not([open])'),
  )
}

/** The open overlays, the top one last. */
const stack: symbol[] = []
let scrollLocks = 0
let savedOverflow = ''

function lockScroll() {
  if (scrollLocks++ === 0) {
    savedOverflow = document.documentElement.style.overflow
    document.documentElement.style.overflow = 'hidden'
  }
}

function unlockScroll() {
  if (--scrollLocks === 0) document.documentElement.style.overflow = savedOverflow
}

/** Whether an overlay is open: the page's own shortcuts wait while one is. */
export function overlayOpen(): boolean {
  return stack.length > 0
}

export interface LayerOptions {
  /** What takes the focus on opening; else an element marked data-autofocus, the first control, or the overlay itself. */
  initialFocus?: RefObject<HTMLElement | null>
  /** Keep the page behind from scrolling (default true). */
  lockScroll?: boolean
}

/**
 * Makes `ref` an overlay while `open`: Tab and Shift+Tab cycle inside it, Esc
 * calls onClose, and the focus returns to the element that had it when it
 * opened. A native <dialog> open inside it (a confirmation) keeps its own keys.
 */
export function useLayer(
  open: boolean,
  ref: RefObject<HTMLElement | null>,
  onClose: () => void,
  options: LayerOptions = {},
): void {
  const close = useRef(onClose)
  useEffect(() => {
    close.current = onClose
  })
  const { initialFocus, lockScroll: lock = true } = options
  useEffect(() => {
    if (!open) return
    const id = Symbol('layer')
    stack.push(id)
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
    if (lock) lockScroll()
    const root = ref.current
    if (root && !root.contains(document.activeElement)) {
      const first =
        initialFocus?.current ?? root.querySelector<HTMLElement>('[data-autofocus]') ?? focusables(root)[0] ?? root
      first.focus({ preventScroll: true })
    }
    const onKey = (e: KeyboardEvent) => {
      if (stack[stack.length - 1] !== id || e.defaultPrevented) return
      const el = ref.current
      if (!el) return
      // A confirmation's own <dialog> handles its keys.
      if ((e.target as Element | null)?.closest?.('dialog[open]')) return
      if (e.key === 'Escape') {
        e.preventDefault()
        close.current()
        return
      }
      if (e.key !== 'Tab') return
      const items = focusables(el)
      if (!items.length) {
        e.preventDefault()
        el.focus()
        return
      }
      const first = items[0]
      const last = items[items.length - 1]
      const active = document.activeElement
      if (!el.contains(active)) {
        e.preventDefault()
        ;(e.shiftKey ? last : first).focus()
      } else if (e.shiftKey && (active === first || active === el)) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && active === last) {
        e.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      const at = stack.indexOf(id)
      if (at >= 0) stack.splice(at, 1)
      if (lock) unlockScroll()
      // Back to what opened it, if it is still on the page.
      if (opener?.isConnected) opener.focus({ preventScroll: true })
    }
  }, [open, ref, initialFocus, lock])
}
