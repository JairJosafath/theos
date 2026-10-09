// The side panels: details of the selected box, who is working right now, and
// the legend.

import type { StoryData } from '../k8s/sources'
import type { Condition, ContainerPhase } from '../k8s/types'
import { useNow } from '../lib/hooks'
import { describeContainerPhase, KIND_LABEL, plural, splitTemplateRef } from '../model/describe'
import { crewDoing, issueList, type ActiveCrewTask, type CrewSummary } from '../model/crews'
import { linksOf, type ContainerNode, type DiagramNode, type Link } from '../model/story'
import { AgentName, ConditionList, CrewPhaseBadge, PhaseBadge, SessionRow } from './bits'
import { AgentIcon, ArrowRightIcon, CrossIcon, ExternalLinkIcon, HumanIcon, WarningIcon } from './icons'
import './panels.css'

export function DetailsPanel({
  node,
  data,
  onClose,
  onOpenSystem,
}: {
  node: DiagramNode
  data: StoryData
  onClose: () => void
  onOpenSystem: (name: string) => void
}) {
  const now = useNow()
  const links = linksOf(data, node.kind, node.name)
  return (
    <section className="panel details" aria-label={`Details of ${node.title}`}>
      <div className="panel-head">
        <div>
          <div className="eyebrow">{KIND_LABEL[node.role]}</div>
          <h2 className="panel-title">{node.title}</h2>
          <div className="object-name" title="The stable name of this resource in the cluster">
            {node.name}
          </div>
        </div>
        <button type="button" className="icon-button" onClick={onClose} aria-label="Close details">
          <CrossIcon size={16} />
        </button>
      </div>

      {node.description ? <p className="panel-text">{node.description}</p> : null}

      {node.role === 'system' || node.role === 'external-system' ? (
        <>
          <p className="summary-line">{node.summary}</p>
          {node.needs.length ? (
            <DetailList title="Needs" hint="What it exists for, in its users' words" items={node.needs} />
          ) : null}
          {node.kpis.length ? <DetailList title="KPIs" hint="How you know it delivers" items={node.kpis} /> : null}
          <button type="button" className="button" onClick={() => onOpenSystem(node.name)}>
            Show its containers <ArrowRightIcon size={14} />
          </button>
        </>
      ) : null}

      {node.role === 'container' ? <ContainerDetails node={node} now={now} /> : null}

      {node.role === 'datastore' ? (
        <>
          <dl className="facts">
            <dt>Connector</dt>
            <dd>
              {node.connector}
              {node.connector !== node.connectorName ? <span className="muted"> ({node.connectorName})</span> : null}
            </dd>
          </dl>
          <p className="panel-note">
            A container linked to this data store gets the connector&apos;s settings in its repository when it is
            scaffolded.
          </p>
          <h3 className="section-title">Checks</h3>
          <ConditionList conditions={node.conditions} empty="The factory has not checked this data store yet." />
        </>
      ) : null}

      <h3 className="section-title">Links</h3>
      {links.length ? (
        <ul className="links">
          {links.map((link) => (
            <LinkRow key={`${link.direction}-${link.name}`} link={link} />
          ))}
        </ul>
      ) : (
        <p className="muted small">No links to or from this {KIND_LABEL[node.role].toLowerCase()}.</p>
      )}
    </section>
  )
}

function DetailList({ title, hint, items }: { title: string; hint: string; items: string[] }) {
  return (
    <>
      <h3 className="section-title">
        {title} <span className="section-hint">{hint}</span>
      </h3>
      <ul className="bullets">
        {items.map((item, i) => (
          <li key={i}>{item}</li>
        ))}
      </ul>
    </>
  )
}

function ContainerDetails({ node, now }: { node: ContainerNode; now: number }) {
  const phase = describeContainerPhase(node.phase)
  return (
    <>
      <div className={`phase-callout tone-${phase.tone}`}>
        <PhaseBadge phase={node.phase} />
        <span>{phase.explanation}</span>
      </div>
      <dl className="facts">
        <dt>Type</dt>
        <dd>{node.typeLabel}</dd>
        <dt>Framework</dt>
        <dd>{node.framework ?? 'none'}</dd>
        <dt>Repository</dt>
        <dd>
          {node.repositoryURL ? (
            <a href={node.repositoryURL} target="_blank" rel="noreferrer" className="break">
              {node.repositoryURL} <ExternalLinkIcon size={12} />
            </a>
          ) : (
            <span className="muted">Not created yet</span>
          )}
        </dd>
        <dt>Started from</dt>
        <dd>
          {node.scaffoldedFrom.length ? (
            <ul className="templates">
              {node.scaffoldedFrom.map((ref) => {
                const { name, version } = splitTemplateRef(ref)
                return (
                  <li key={ref}>
                    <span className="mono">{name}</span>
                    {version ? <span className="version">{version}</span> : null}
                  </li>
                )
              })}
            </ul>
          ) : (
            <span className="muted">Not scaffolded yet</span>
          )}
        </dd>
      </dl>
      {node.repositoryURL ? (
        <p className="panel-note">
          Repository addresses point inside the cluster. From a laptop, Gitea is usually reached through a port-forward.
        </p>
      ) : null}

      <h3 className="section-title">Checks</h3>
      <ConditionList conditions={node.conditions} empty="The factory has not checked this container yet." />

      {node.crew ? <CrewDetails crew={node.crew} /> : null}

      <h3 className="section-title">
        Sessions <span className="section-hint">{plural(node.activeSessions, 'active session')}</span>
      </h3>
      {node.sessions.length ? (
        <ul className="sessions">
          {node.sessions.map((s) => (
            <SessionRow key={s.metadata.name} session={s} now={now} />
          ))}
        </ul>
      ) : (
        <p className="muted small">Nobody is working on it right now.</p>
      )}
    </>
  )
}

/** Who picks up the Container's issues: "<implementer> implements, <reviewer> reviews; working on #4". */
function CrewDetails({ crew }: { crew: CrewSummary }) {
  return (
    <>
      <h3 className="section-title">
        Crew <span className="section-hint">picks up its ready issues</span>
      </h3>
      <div className="crew-line">
        <p>
          <AgentName role={crew.implementer} /> implements, <AgentName role={crew.reviewer} /> reviews;{' '}
          {crew.active.length
            ? `working on ${crew.active.map((a) => a.headline ?? a.name).join('; ')}`
            : crewDoing(crew)}
          .
        </p>
        <p className="crew-line-meta">
          <CrewPhaseBadge phase={crew.phase} />
          {crew.paused && crew.phase !== 'Paused' ? (
            <span className="badge badge-sm badge-waiting" title="Picks up no new issues">
              Paused
            </span>
          ) : null}
          <span className="muted">
            <span title={crew.ready.length ? issueList(crew.ready) : undefined}>{crew.ready.length} ready</span>
            {' · '}
            <span title={crew.waiting.length ? issueList(crew.waiting) : undefined}>{crew.waiting.length} waiting</span>
          </span>
        </p>
        {crew.notReady ? (
          <p className="crew-note tone-error">
            <WarningIcon size={13} /> {crew.notReady.message ?? 'It can’t pick up issues.'}
          </p>
        ) : null}
      </div>
    </>
  )
}

function appliedBadge(applied: Condition | undefined) {
  if (!applied) return null
  if (applied.status === 'True') return <span className="pill pill-ok">Built in</span>
  if (applied.reason === 'AddedAfterScaffold')
    return (
      <span className="pill pill-warn" title={applied.message}>
        <WarningIcon size={11} /> Added after scaffolding, not applied
      </span>
    )
  if (applied.reason === 'WaitingForScaffold') return <span className="pill pill-waiting">Applied at scaffold</span>
  return (
    <span className="pill pill-muted" title={applied.message}>
      Not applied
    </span>
  )
}

function LinkRow({ link }: { link: Link }) {
  const other = link.other.exists ? link.other.title : `${link.other.name} (missing)`
  return (
    <li className="link">
      <span className="link-dir" aria-hidden="true">
        {link.direction === 'out' ? '→' : '←'}
      </span>
      <span className="link-body">
        <span>
          {link.direction === 'out' ? 'To ' : 'From '}
          <strong>{other}</strong>
          {link.via ? (
            <span className="muted">
              {' '}
              ({link.direction === 'out' ? 'from' : 'to'} {link.via})
            </span>
          ) : null}
          {link.description ? <span className="muted">: {link.description}</span> : null}
        </span>
        {appliedBadge(link.applied)}
      </span>
    </li>
  )
}

/** Who is working on what right now, grouped by container. */
export function SessionsPanel({
  containers,
  tasks,
  scope,
}: {
  containers: ContainerNode[]
  tasks: ActiveCrewTask[]
  scope: string
}) {
  const now = useNow()
  const withSessions = containers.filter((c) => c.sessions.length)
  const all = withSessions.flatMap((c) => c.sessions)
  const active = all.filter((s) => s.status?.phase === 'Active').length
  const stale = all.filter((s) => s.status?.phase === 'Stale').length
  return (
    <section className="panel" aria-label="Who is working right now">
      <h2 className="panel-title small-title">Who&apos;s working right now</h2>
      <p className="panel-caption">
        People and AI agents working in a devcontainer on {scope}, each a Session that checks in while it works; the
        factory marks one stale when it stops checking in. Crews&apos; Tasks that run now are listed too.
      </p>
      {tasks.length ? (
        <div className="session-group">
          <div className="session-group-head">
            <span className="session-group-title">Crew Tasks</span>
          </div>
          <ul className="sessions">
            {tasks.map((t) => (
              <li key={t.key} className="session session-compact">
                <span className="session-avatar session-avatar-agent">
                  <AgentIcon size={14} title="Agent" />
                </span>
                <span className="session-body">
                  <span className="session-summary">
                    {t.issue ? `#${t.issue}` : t.name}
                    {t.issueTitle ? ` ${t.issueTitle}` : ''}
                  </span>
                  <span className="session-meta">
                    <AgentName role={t.agent} />
                    {t.phase ? (
                      <>
                        <span className="sep">·</span>
                        {t.phase}
                      </>
                    ) : null}
                  </span>
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {all.length ? (
        <>
          <p className="counts">
            <span className="count-active">{active} active</span>
            {stale ? <span className="count-stale">{stale} stale</span> : null}
          </p>
          {withSessions.map((c) => (
            <div key={c.id} className="session-group">
              <div className="session-group-head">
                <span className="session-group-title">{c.title}</span>
                <PhaseBadge phase={c.phase as ContainerPhase | undefined} size="sm" />
              </div>
              <ul className="sessions">
                {c.sessions.map((s) => (
                  <SessionRow key={s.metadata.name} session={s} now={now} compact />
                ))}
              </ul>
            </div>
          ))}
        </>
      ) : (
        !tasks.length && (
          <p className="muted small">
            Nobody is working right now. When someone opens a devcontainer, they show up here.
          </p>
        )
      )}
    </section>
  )
}

/** What the shapes, colours and badges mean. */
export function Legend({ view }: { view: 'context' | 'containers' }) {
  return (
    <section className="panel legend" aria-label="Legend">
      <h2 className="panel-title small-title">How to read this</h2>
      <ul className="legend-items">
        <li>
          <svg width="30" height="30" viewBox="0 0 30 30" aria-hidden="true" className="legend-shape">
            <circle cx="15" cy="8" r="6" className="fill-person" />
            <rect x="4" y="13" width="22" height="15" rx="7" className="fill-person" />
          </svg>
          <span>
            <strong>Person</strong> someone who uses the software
          </span>
        </li>
        {view === 'context' ? (
          <li>
            <span className="legend-box fill-system" aria-hidden="true" />
            <span>
              <strong>Software system</strong> delivers value to people, has needs and KPIs
            </span>
          </li>
        ) : (
          <>
            <li>
              <span className="legend-box fill-container" aria-hidden="true" />
              <span>
                <strong>Container</strong> an application with its own Git repository
              </span>
            </li>
            <li>
              <svg width="30" height="30" viewBox="0 0 30 30" aria-hidden="true" className="legend-shape">
                <path d="M3 7 A12 4 0 0 1 27 7 V23 A12 4 0 0 1 3 23 Z" className="fill-container" />
                <ellipse cx="15" cy="7" rx="12" ry="4" className="fill-container rim" />
              </svg>
              <span>
                <strong>Data store</strong> a database or storage
              </span>
            </li>
            <li>
              <span className="legend-box fill-external" aria-hidden="true" />
              <span>
                <strong>Other system</strong> outside the one shown
              </span>
            </li>
            <li>
              <span className="legend-box legend-boundary" aria-hidden="true" />
              <span>
                <strong>Dashed box</strong> the system these belong to
              </span>
            </li>
          </>
        )}
        <li>
          <svg width="30" height="12" viewBox="0 0 30 12" aria-hidden="true">
            <path d="M1 6 H24" className="edge edge-default" />
            <path d="M22 2 L29 6 L22 10 z" className="arrowhead arrowhead-default" />
          </svg>
          <span>
            <strong>Arrow</strong> uses, with what for
          </span>
        </li>
        {view === 'containers' ? (
          <li>
            <svg width="30" height="12" viewBox="0 0 30 12" aria-hidden="true">
              <path d="M1 6 H24" className="edge edge-warn" />
              <path d="M22 2 L29 6 L22 10 z" className="arrowhead arrowhead-warn" />
            </svg>
            <span>
              <strong>Dashed amber arrow</strong> added after the container was scaffolded, so its repository does not
              include it
            </span>
          </li>
        ) : null}
      </ul>
      {view === 'containers' ? (
        <>
          <h3 className="section-title">Container phases</h3>
          <ul className="legend-phases">
            {(['Pending', 'Scaffolding', 'Ready', 'Failed'] as const).map((p) => (
              <li key={p}>
                <PhaseBadge phase={p} size="sm" />
                <span>{describeContainerPhase(p).explanation}</span>
              </li>
            ))}
          </ul>
          <h3 className="section-title">Sessions</h3>
          <ul className="legend-items">
            <li>
              <span className="avatar avatar-agent" aria-hidden="true">
                <AgentIcon size={12} />
              </span>
              <span>An AI agent at work</span>
            </li>
            <li>
              <span className="avatar avatar-human" aria-hidden="true">
                <HumanIcon size={12} />
              </span>
              <span>A person at work</span>
            </li>
            <li>
              <span className="avatar avatar-human avatar-stale" aria-hidden="true">
                <HumanIcon size={12} />
              </span>
              <span>Faded: stale, its heartbeats stopped</span>
            </li>
          </ul>
        </>
      ) : null}
    </section>
  )
}
