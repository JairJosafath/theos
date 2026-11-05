// The page's keyboard shortcut for Search or jump (CommandPalette.tsx).

import { useEffect, useRef } from 'react'
import { overlayOpen } from './layer'

/** Whether a key goes to a field the person is typing in. */
function typing(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el?.closest) return false
  return Boolean(el.closest('input, textarea, select, [contenteditable=""], [contenteditable="true"]'))
}

/** Opens the palette on ⌘K or Ctrl+K, and on / outside a field while nothing else is open. */
export function usePaletteShortcut(open: () => void): void {
  const onOpen = useRef(open)
  useEffect(() => {
    onOpen.current = open
  })
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.defaultPrevented) return
      const k = e.key.toLowerCase()
      if (k === 'k' && (e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey) {
        e.preventDefault()
        onOpen.current()
      } else if (e.key === '/' && !e.metaKey && !e.ctrlKey && !e.altKey && !typing(e.target) && !overlayOpen()) {
        e.preventDefault()
        onOpen.current()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])
}
