// The project's Chat, read only, on About and in Settings (docs/design.md,
// Chat): on Matrix the homeserver, the Space and each room with a matrix.to
// link; on Slack the host, each room as its channel and the people of
// spec.slack.people (#162), with no Space and no homeserver; and Ready with its
// message, and each person the host refused to invite (#251, #253), told as
// news, not a failure: Ready stays True. Nothing when the namespace has no Chat.
// IDs and names are the Chat's own text, rendered as text.

import type { Problem } from '../k8s/sources'
import type { Chat } from '../k8s/types'
import { chatSummary, type ChatSummary } from '../model/chat'
import { ExternalLinkIcon } from './icons'
import { ReadProblems } from './Status'

export function ChatPanel({ chat, problems = [] }: { chat?: Chat; problems?: Problem[] }) {
  // A cluster without the Chat kind answers 404: it has no Chat to show.
  const unread = problems.filter((p) => p.code !== 404)
  if (!chat) {
    if (!unread.length) return null
    return (
      <section className="chat-panel" aria-label="Chat">
        <h2 className="panel-title small-title">Chat</h2>
        <ReadProblems problems={unread} />
      </section>
    )
  }
  const s = chatSummary(chat)
  if (s.host === 'slack') return <SlackChat s={s} />
  return (
    <section className="chat-panel" aria-label="Chat">
      <h2 className="panel-title small-title">Chat</h2>
      <p className="muted small">
        Follow and steer this project from Matrix. The rooms are invite only: ask to be added to the Chat&apos;s people.
      </p>
      <dl className="facts chat-facts">
        <dt>Homeserver</dt>
        <dd>{s.homeserver ?? "The operator's default"}</dd>
        <dt>Space</dt>
        <dd>{s.space ? <MatrixLink href={s.space.href}>{s.space.target}</MatrixLink> : 'Not made yet'}</dd>
        <dt>Ready</dt>
        <dd className={`chat-ready chat-ready-${s.ready.state}`}>
          {s.ready.state === 'ready' ? 'Yes' : s.ready.state === 'not-ready' ? 'No' : 'Unknown'}
          {s.ready.reason && s.ready.state !== 'ready' ? ` (${s.ready.reason})` : ''}: {s.ready.message}
        </dd>
      </dl>
      <Refused refused={s.refused} where="the Chat's people" />
      <ul className="chat-rooms" aria-label="Rooms">
        {s.rooms.map((r) => (
          <li key={r.purpose}>
            {r.href ? <MatrixLink href={r.href}>{r.name}</MatrixLink> : <span>{r.name}</span>}
            {r.name !== r.purpose ? <span className="muted small"> {r.purpose}</span> : null}
            {r.href ? null : <span className="muted small"> not made yet</span>}
          </li>
        ))}
      </ul>
    </section>
  )
}

function MatrixLink({ href, children }: { href: string; children: string }) {
  return (
    <a href={href} target="_blank" rel="noreferrer">
      {children} <ExternalLinkIcon size={12} />
    </a>
  )
}

function Refused({ refused, where }: { refused: ChatSummary['refused']; where: string }) {
  if (!refused.length) return null
  return (
    <div className="chat-refused" role="note" aria-label="Refused invites">
      <p className="chat-refused-title">Refused invites</p>
      <ul>
        {refused.map((p) => (
          <li key={p.id}>
            The host refused the invite for {p.id}
            {p.code ? `: ${p.code}` : ''}
            {p.rooms.length ? ` (rooms: ${p.rooms.join(', ')})` : ''}.
          </li>
        ))}
      </ul>
      <p className="muted small">
        The Chat keeps working for everyone else. The operator asks the host again about those rooms; check the ID in{' '}
        {where}.
      </p>
    </div>
  )
}

/** A Chat on Slack (#162): its host, no Space and no homeserver, its channels and its people. */
function SlackChat({ s }: { s: ChatSummary }) {
  return (
    <section className="chat-panel" aria-label="Chat">
      <h2 className="panel-title small-title">Chat</h2>
      <p className="muted small">
        Follow and steer this project from Slack. The channels are private: ask to be added to the Chat&apos;s people.
      </p>
      <dl className="facts chat-facts">
        <dt>Host</dt>
        <dd>Slack</dd>
        <dt>Ready</dt>
        <dd className={`chat-ready chat-ready-${s.ready.state}`}>
          {s.ready.state === 'ready' ? 'Yes' : s.ready.state === 'not-ready' ? 'No' : 'Unknown'}
          {s.ready.reason && s.ready.state !== 'ready' ? ` (${s.ready.reason})` : ''}: {s.ready.message}
        </dd>
      </dl>
      <Refused refused={s.refused} where="the Chat's slack.people" />
      <p className="chat-list-title">Channels</p>
      <ul className="chat-rooms" aria-label="Channels">
        {s.rooms.map((r) => (
          <li key={r.purpose}>
            <span>{r.name}</span>
            <span className="muted small"> {r.purpose}</span>
            {r.target ? null : <span className="muted small"> not made yet</span>}
          </li>
        ))}
      </ul>
      <p className="chat-list-title">People</p>
      <ul className="chat-people" aria-label="People">
        {s.people.length ? (
          s.people.map((p) => (
            <li key={p.id}>
              <span>{p.id}</span>
            </li>
          ))
        ) : (
          <li className="muted small">No one is listed: no one may speak to the factory.</li>
        )}
      </ul>
    </section>
  )
}
