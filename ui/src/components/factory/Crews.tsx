// The factory view's Crews: who implements and reviews, and what each Crew is doing.

import { timeAgo } from '../../model/describe'
import { AgentName, CrewPhaseBadge } from '../bits'
import { WarningIcon } from '../icons'
import { crewDoing, issueList, type CrewSummary } from '../../model/crews'
import type { ObjectHref } from '../Factory'

export function CrewsPanel({
  crews,
  loaded,
  now,
  onSelectTask,
  pageHref,
}: {
  crews: CrewSummary[]
  loaded: boolean
  now: number
  onSelectTask: (key: string) => void
  pageHref?: ObjectHref
}) {
  return (
    <div className="panel">
      <h2 className="panel-title">Crews</h2>
      <p className="panel-caption">
        A Crew picks up its Container’s ready issues one at a time, lowest number first, once their dependencies are
        closed. One Agent implements, another reviews.
      </p>
      {crews.length ? (
        <ul className="crews">
          {crews.map((crew) => (
            <CrewCard
              key={crew.key}
              crew={crew}
              now={now}
              onSelectTask={onSelectTask}
              href={pageHref?.(crew.namespace, 'Crew', crew.name)}
            />
          ))}
        </ul>
      ) : (
        <div className="placeholder small">
          {loaded ? 'No Crews yet: Tasks are only started by hand.' : 'Loading the Crews…'}
        </div>
      )}
    </div>
  )
}

function CrewCard({
  crew,
  now,
  onSelectTask,
  href,
}: {
  crew: CrewSummary
  now: number
  onSelectTask: (key: string) => void
  /** The Crew's page, in the shell. */
  href?: string
}) {
  const tone = crew.notReady ? 'error' : crew.paused ? 'waiting' : undefined
  return (
    <li className={`crew-card${tone ? ` tone-${tone}` : ''}`}>
      <div className="crew-head">
        {href ? (
          <a className="crew-title" href={href}>
            {crew.title}
          </a>
        ) : (
          <span className="crew-title">{crew.title}</span>
        )}
        <CrewPhaseBadge phase={crew.phase} />
        {crew.paused && crew.phase !== 'Paused' ? (
          <span className="badge badge-sm badge-waiting" title="spec.paused is set">
            Paused
          </span>
        ) : null}
      </div>
      <div className="crew-sub">
        {crew.namespace} · <span className="mono">{crew.container}</span>
      </div>
      {crew.notReady ? (
        <p className="crew-note tone-error">
          <WarningIcon size={13} /> {crew.notReady.message ?? 'It can’t pick up issues.'}
        </p>
      ) : null}
      {crew.paused ? (
        <p className="crew-note tone-waiting">
          Paused: picks up no new issues
          {crew.active.length > 1
            ? '; the current Tasks run to their end'
            : crew.active.length
              ? '; the current Task runs to its end'
              : ''}
          .
        </p>
      ) : null}
      <dl className="facts crew-facts">
        <dt>Implements</dt>
        <dd>
          <AgentName role={crew.implementer} />
        </dd>
        <dt>Reviews</dt>
        <dd>
          <AgentName role={crew.reviewer} />
        </dd>
        <dt>Now</dt>
        <dd>
          {crew.active.length ? (
            <>
              {crew.active.map((a) => (
                <div key={a.key}>
                  <button
                    type="button"
                    className="link-button crew-active"
                    onClick={() => onSelectTask(a.key)}
                    title={a.name}
                  >
                    {a.headline ?? a.name}
                  </button>
                </div>
              ))}
              {crew.parallel > 1 ? (
                <span className="muted small">
                  {crew.active.length} of {crew.parallel} places busy
                </span>
              ) : null}
            </>
          ) : (
            <span className="muted">{crewDoing(crew)}</span>
          )}
        </dd>
        <dt>Issues</dt>
        <dd>
          <span title={crew.ready.length ? `Ready: ${issueList(crew.ready)}` : undefined}>
            {crew.ready.length} ready
          </span>
          <span className="muted"> · </span>
          <span title={crew.waiting.length ? `Waiting: ${issueList(crew.waiting)}` : undefined}>
            {crew.waiting.length} waiting
          </span>
        </dd>
      </dl>
      <div className="crew-time">
        {crew.lastPolled ? `Read the issues ${timeAgo(crew.lastPolled, now)}` : 'Has not read the issues yet'}
      </div>
    </li>
  )
}
