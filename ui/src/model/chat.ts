// A project's Chat for About and Settings (docs/design.md, Chat): the
// homeserver, each room with a matrix.to link, and Ready, all from the Chat's
// spec and status. Read only: the UI changes nothing of a Chat.

import { CHAT_PURPOSES, findCondition, type Chat, type ChatPurpose } from '../k8s/types'

export interface ChatRoomRow {
  purpose: ChatPurpose
  /** The room's display name: spec.rooms, else its purpose. */
  name: string
  /** The alias, else the ID, once the operator has made the room. */
  target?: string
  href?: string
}

export interface ChatSummary {
  name: string
  /** Where the operator reached the rooms, else the address the Chat names; unset when it names none. */
  homeserver?: string
  space?: { target: string; href: string }
  rooms: ChatRoomRow[]
  ready: { state: 'ready' | 'not-ready' | 'unknown'; reason?: string; message: string }
}

/**
 * The namespace's Chat. There is one per namespace; a second is Duplicate,
 * so a Ready one wins, else the oldest, as the operator keeps the first.
 */
export function chatOf(chats: Chat[], namespace: string | undefined): Chat | undefined {
  if (!namespace) return undefined
  const mine = chats
    .filter((c) => c.metadata.namespace === namespace)
    .sort((a, b) => (a.metadata.creationTimestamp ?? '').localeCompare(b.metadata.creationTimestamp ?? ''))
  return mine.find((c) => findCondition(c.status?.conditions, 'Ready')?.status === 'True') ?? mine[0]
}

/** A matrix.to link to a room alias or ID, percent-encoded as the Matrix spec asks. */
export function matrixToHref(target: string): string {
  return `https://matrix.to/#/${encodeURIComponent(target)}`
}

export function chatSummary(chat: Chat): ChatSummary {
  const made = new Map((chat.status?.rooms ?? []).map((r) => [r.purpose, r]))
  const rooms = CHAT_PURPOSES.map((purpose): ChatRoomRow => {
    const room = made.get(purpose)
    const target = room ? room.alias || room.id : undefined
    return { purpose, name: chat.spec.rooms?.[purpose] || purpose, target, href: target && matrixToHref(target) }
  })
  const space = chat.status?.space
  const spaceTarget = space ? space.alias || space.id : undefined
  const ready = findCondition(chat.status?.conditions, 'Ready')
  return {
    name: chat.metadata.name,
    homeserver: chat.status?.homeserver || chat.spec.homeserver || undefined,
    space: spaceTarget ? { target: spaceTarget, href: matrixToHref(spaceTarget) } : undefined,
    rooms,
    ready: !ready
      ? { state: 'unknown', message: 'The operator has not reported on it yet.' }
      : ready.status === 'True'
        ? { state: 'ready', reason: ready.reason, message: ready.message || 'The Space and its rooms are there.' }
        : { state: 'not-ready', reason: ready.reason, message: ready.message || ready.reason || 'Not ready.' },
  }
}
