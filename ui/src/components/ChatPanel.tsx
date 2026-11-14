// The project's Chat, read only, on About and in Settings (docs/design.md,
// Chat): the homeserver, each room with a matrix.to link, and Ready with its
// message, and each person the host refused to invite (#251, #253), told as
// news, not a failure: Ready stays True. Nothing when the namespace has no Chat.

import type { Problem } from '../k8s/sources'
import type { Chat } from '../k8s/types'
import { chatSummary } from '../model/chat'
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
      {s.refused.length ? (
        <div className="chat-refused" role="note" aria-label="Refused invites">
          <ul>
            {s.refused.map((p) => (
              <li key={p.id}>
                The host refused the invite for {p.id}
                {p.code ? `: ${p.code}` : ''}
                {p.rooms.length ? ` (rooms: ${p.rooms.join(', ')})` : ''}.
              </li>
            ))}
          </ul>
          <p className="muted small">
            The Chat keeps working for everyone else. The operator asks the host again to those rooms; check the ID in
            the Chat&apos;s people.
          </p>
        </div>
      ) : null}
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
