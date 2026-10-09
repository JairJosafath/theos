// Box sizes of each kind of card. The layout needs them before anything is
// rendered, so they are fixed here and the cards' CSS (components/diagram.css)
// is written to fit: long text is clamped, and the full text is in the details
// panel. A size both use is set here and read by the CSS from cardSizeVars.

import type { Size } from './layout'
import type { DiagramNode } from './story'

export const CARD_WIDTH = {
  person: 196,
  system: 288,
  externalSystem: 224,
  container: 264,
  dataStore: 224,
} as const

/** Most list items a system card shows per list (needs, KPIs). */
export const MAX_LIST_ITEMS = 3

/** Height of one list item on a system card, its line height in the CSS. */
export const LIST_ITEM_HEIGHT = 19

/** What a non-empty list adds to a system card besides its items: its margin and label. */
const LIST_HEAD = 30

/** The sizes the cards' CSS reads, as custom properties set on the diagram's canvas. */
export const cardSizeVars: Record<`--${string}`, string> = {
  '--list-item-height': `${LIST_ITEM_HEIGHT}px`,
}

export function nodeSize(node: DiagramNode): Size {
  switch (node.role) {
    case 'person':
      return { width: CARD_WIDTH.person, height: 184 }
    case 'system': {
      // Header, description and footer, plus a block per non-empty list.
      const list = (items: string[]) =>
        items.length ? LIST_HEAD + LIST_ITEM_HEIGHT * Math.min(items.length, MAX_LIST_ITEMS) : 0
      return { width: CARD_WIDTH.system, height: 164 + list(node.needs) + list(node.kpis) }
    }
    case 'external-system':
      return { width: CARD_WIDTH.externalSystem, height: 124 }
    case 'container':
      return { width: CARD_WIDTH.container, height: 228 }
    case 'datastore':
      return { width: CARD_WIDTH.dataStore, height: 156 }
  }
}
