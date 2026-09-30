// Box sizes of each kind of card. The layout needs them before anything is
// rendered, so they are fixed here and the cards' CSS (styles.css) is written to
// fit: long text is clamped, and the full text is in the details panel.

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

export function nodeSize(node: DiagramNode): Size {
  switch (node.role) {
    case 'person':
      return { width: CARD_WIDTH.person, height: 184 }
    case 'system': {
      // Header, description and footer, plus a block per non-empty list.
      const list = (items: string[]) => (items.length ? 30 + 19 * Math.min(items.length, MAX_LIST_ITEMS) : 0)
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
