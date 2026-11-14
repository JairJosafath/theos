// A project's Chat for About and Settings (docs/design.md, Chat): the host,
// and on Matrix the homeserver, the Space and each room with a matrix.to link;
// on Slack each room as the channel it is and the people of spec.slack.people;
// and Ready, all from the Chat's spec and status. Read only: the UI changes
// nothing of a Chat.

import { CHAT_PURPOSES, findCondition, type Chat, type ChatHost, type ChatPurpose } from '../k8s/types'
import { compareText } from './order'

export interface ChatRoomRow {
  purpose: ChatPurpose
  /** The room's display name: spec.rooms, else its purpose. */
  name: string
  /** The alias, else the ID, once the operator has made the room. */
  target?: string
  /** A matrix.to link: Matrix only. A Slack channel has no link here, which would need the workspace's address. */
  href?: string
}

/** A person of a Slack Chat, by Slack member ID, and the Kubernetes identity their commands are made as. */
export interface ChatPersonRow {
  id: string
  user: string
  groups: string[]
}

/** A person the chat host refused to invite (#251, #253): not a failure, the Chat stays Ready. */
export interface ChatRefusal {
  id: string
  /** The host's error code for the first refusal, when it gave one. */
  code?: string
  /** The rooms the host refused the invite to, named as the Chat's rooms are; the Space is "the Space". */
  rooms: string[]
}

export interface ChatSummary {
  name: string
  /** The chat app the Chat is on; a Chat with no host is on Matrix, as the CRD defaults it. */
  host: ChatHost
  /** Where the operator reached the rooms, else the address the Chat names; unset when it names none, and on Slack. */
  homeserver?: string
  /** Unset on Slack: the workspace is the Space. */
  space?: { target: string; href: string }
  /** On Slack a room's name is its channel's, #<aliasPrefix>-<purpose>, as the operator made it. */
  rooms: ChatRoomRow[]
  /** The people of spec.slack.people; empty on Matrix, whose people the panel does not list. */
  people: ChatPersonRow[]
  ready: { state: 'ready' | 'not-ready' | 'unknown'; reason?: string; message: string }
  /** Empty unless the Invited condition is False (InviteRefused): any other state shows no refusal. */
  refused: ChatRefusal[]
}

/**
 * The namespace's Chat. There is one per namespace; a second is Duplicate,
 * so a Ready one wins, else the oldest, as the operator keeps the first.
 */
export function chatOf(chats: Chat[], namespace: string | undefined): Chat | undefined {
  if (!namespace) return undefined
  const mine = chats
    .filter((c) => c.metadata.namespace === namespace)
    .sort((a, b) => compareText(a.metadata.creationTimestamp ?? '', b.metadata.creationTimestamp ?? ''))
  return mine.find((c) => findCondition(c.status?.conditions, 'Ready')?.status === 'True') ?? mine[0]
}

/** A matrix.to link to a room alias or ID, percent-encoded as the Matrix spec asks. */
export function matrixToHref(target: string): string {
  return `https://matrix.to/#/${encodeURIComponent(target)}`
}

/** The Chat's host: slack, else matrix, which is what a Chat with no host, or one this UI does not know, reads as. */
export function chatHost(chat: Chat): ChatHost {
  return chat.spec.host === 'slack' ? 'slack' : 'matrix'
}

/** The default alias prefix, which the operator takes when the Chat names none (internal/model ChatAliasPrefix). */
const aliasPrefixOf = (chat: Chat) => chat.spec.aliasPrefix || `theos-${chat.metadata.namespace ?? ''}`

export function chatSummary(chat: Chat): ChatSummary {
  const host = chatHost(chat)
  const slack = host === 'slack'
  const made = new Map((chat.status?.rooms ?? []).map((r) => [r.purpose, r]))
  const rooms = CHAT_PURPOSES.map((purpose): ChatRoomRow => {
    const room = made.get(purpose)
    const target = room ? room.alias || room.id : undefined
    if (slack) {
      // A channel has one name, its alias (#<aliasPrefix>-<purpose>): spec.rooms is not read, the CRD keeps it at the
      // purposes. Made, the name is what the operator stored; else the one it will give the channel.
      return { purpose, name: target ?? `#${aliasPrefixOf(chat)}-${purpose}`, target }
    }
    return { purpose, name: chat.spec.rooms?.[purpose] || purpose, target, href: target && matrixToHref(target) }
  })
  // A Slack Chat has no Space: the workspace is the Space.
  const space = slack ? undefined : chat.status?.space
  const spaceTarget = space ? space.alias || space.id : undefined
  const ready = findCondition(chat.status?.conditions, 'Ready')
  // A refusal shows only while the last pass reached the invites and found one:
  // Unknown (NotChecked) means the entries may be from an earlier pass.
  const refusedNow = findCondition(chat.status?.conditions, 'Invited')?.status === 'False'
  const roomName = new Map(
    rooms.flatMap((r) => (made.get(r.purpose) ? [[made.get(r.purpose)!.id, r.name] as const] : [])),
  )
  if (space) roomName.set(space.id, 'the Space')
  const refused = refusedNow
    ? (chat.status?.refused ?? []).map((p) => ({
        id: p.id,
        code: p.code || undefined,
        rooms: (p.rooms ?? []).map((id) => roomName.get(id) ?? id),
      }))
    : []
  return {
    name: chat.metadata.name,
    host,
    homeserver: slack ? undefined : chat.status?.homeserver || chat.spec.homeserver || undefined,
    space: spaceTarget ? { target: spaceTarget, href: matrixToHref(spaceTarget) } : undefined,
    rooms,
    people: slack
      ? (chat.spec.slack?.people ?? []).map((p) => ({ id: p.slackID, user: p.user, groups: p.groups ?? [] }))
      : [],
    refused,
    ready: !ready
      ? { state: 'unknown', message: 'The operator has not reported on it yet.' }
      : ready.status === 'True'
        ? {
            state: 'ready',
            reason: ready.reason,
            message: ready.message || (slack ? 'The channels are there.' : 'The Space and its rooms are there.'),
          }
        : { state: 'not-ready', reason: ready.reason, message: ready.message || ready.reason || 'Not ready.' },
  }
}
