// The Briefs of a project: a box to describe a new app, and a card per Brief
// with what the planner made of it, a field to steer it with a prompt and a
// button to pause or resume it. These are the UI's only three writes; the CRD
// decides what is accepted.

import { useState } from 'react'
import type { BriefWriter } from '../k8s/briefs'
import type { Agent, Brief } from '../k8s/types'
import { describeBrief, pauseAction, sortBriefs, type BriefView } from '../model/brief'
import { useNow } from '../lib/hooks'
import { timeAgo } from '../model/describe'
import { UsageLine } from './bits'
import { ExternalLinkIcon } from './icons'
import { NewBrief } from './briefs/NewBrief'
import { BriefDetails } from './briefs/BriefDetails'
import { FormError, PromptForm } from './briefs/PromptForm'
import { errorText } from './briefs/errors'
import './briefs.css'

export function Briefs({
  briefs,
  namespace,
  canCreate,
  writer,
  agents = [],
}: {
  briefs: Brief[]
  namespace: string
  /** The namespace's Agents, for each planner's sessionTokenLimit. */
  agents?: Agent[]
  /** False when the namespace holds no Project: the server makes no Brief there. */
  canCreate: boolean
  writer: BriefWriter
}) {
  if (!canCreate && !briefs.length) return null
  const sorted = sortBriefs(briefs)
  return (
    <section className="briefs" aria-labelledby="briefs-title">
      <div className="view-head">
        <h2 className="view-title" id="briefs-title">
          Briefs
        </h2>
        <p className="view-caption">
          Describe an app in plain words. A planner agent turns it into software systems, containers, crews and issues,
          round by round; add a prompt to steer it. Creating a brief, adding a prompt and pausing or resuming a brief
          are the only changes this page can make.
        </p>
      </div>
      <div className="briefs-grid">
        {canCreate ? (
          <NewBrief namespace={namespace} taken={briefs.map((b) => b.metadata.name)} writer={writer} />
        ) : null}
        {sorted.map((b) => (
          <BriefCard
            key={b.metadata.uid ?? b.metadata.name}
            brief={b}
            namespace={namespace}
            writer={writer}
            plannerLimit={agents.find((a) => a.metadata.name === b.spec.agents?.planner?.name)?.spec.sessionTokenLimit}
          />
        ))}
      </div>
    </section>
  )
}

function BriefCard({
  brief,
  namespace,
  writer,
  plannerLimit,
}: {
  brief: Brief
  namespace: string
  writer: BriefWriter
  plannerLimit?: number
}) {
  const now = useNow()
  const view = describeBrief(brief, plannerLimit)
  const [pauseError, setPauseError] = useState<string>()
  return (
    <article className={`panel brief brief-${view.phase.tone}`} aria-label={`Brief ${view.name}`}>
      <div className="panel-head">
        <div className="brief-title">
          <h3 className="panel-title small-title mono">{view.name}</h3>
          <span className="brief-meta">
            {view.round ? `Round ${view.round}` : 'No round yet'}
            {view.usage ? (
              <>
                <span className="sep">·</span>
                <span title="What planning used over all rounds">
                  <UsageLine usage={view.usage} />
                </span>
              </>
            ) : null}
            <span className="sep">·</span>
            <span title={view.autopilot.explanation}>{view.autopilot.label}</span>
            {brief.metadata.creationTimestamp ? (
              <>
                <span className="sep">·</span>
                <span title={brief.metadata.creationTimestamp}>
                  created {timeAgo(brief.metadata.creationTimestamp, now)}
                </span>
              </>
            ) : null}
          </span>
        </div>
        <div className="brief-head-actions">
          <span className={`badge badge-${view.phase.tone} badge-sm`} title={view.phase.explanation}>
            <span className="badge-dot" aria-hidden="true" />
            {view.phase.label}
          </span>
          <PauseButton namespace={namespace} view={view} writer={writer} onError={setPauseError} />
        </div>
      </div>
      <p className="brief-idea" title={view.idea}>
        {view.idea}
      </p>
      {pauseError ? <FormError message={pauseError} /> : null}
      {view.pausedNote ? (
        <div className="brief-note brief-note-waiting brief-paused" role="status">
          {view.pausedNote}
        </div>
      ) : null}

      {view.needsYou.length ? <NeedsYouBox view={view} now={now} /> : null}
      {view.failure ? (
        <div className="brief-note brief-note-error">
          <strong>The last round failed.</strong> {view.failure}
        </div>
      ) : null}

      <BriefDetails view={view} now={now} />

      <PromptForm namespace={namespace} name={view.name} writer={writer} needsInput={Boolean(view.needsInput)} />
    </article>
  )
}

/** Pauses a running Brief or resumes a paused one by patching spec.paused; the card shows the API's refusal. */
function PauseButton({
  namespace,
  view,
  writer,
  onError,
}: {
  namespace: string
  view: BriefView
  writer: BriefWriter
  onError: (message: string | undefined) => void
}) {
  const [sending, setSending] = useState(false)
  const action = pauseAction(view)

  async function click() {
    setSending(true)
    onError(undefined)
    try {
      await writer.setPaused(namespace, view.name, action.paused)
    } catch (err) {
      onError(errorText(err))
    } finally {
      setSending(false)
    }
  }

  return (
    <button
      type="button"
      className="button button-secondary button-small"
      title={action.explanation}
      aria-label={`${action.label} brief ${view.name}`}
      disabled={sending}
      onClick={click}
    >
      {sending ? (action.paused ? 'Pausing…' : 'Resuming…') : action.label}
    </button>
  )
}

/** What only the person can decide: what the Brief waits for, then each item with what it would do and why. */
function NeedsYouBox({ view, now }: { view: BriefView; now: number }) {
  return (
    <div className="brief-note brief-note-waiting brief-needs-you">
      <strong>Needs you ({view.needsYouWaiting})</strong>
      <ul className="brief-list brief-issues">
        {view.needsYou.map((n) => (
          <li key={`${n.kind}|${n.label}`}>
            {/* The operator's question names the item already. */}
            <span>{n.question}</span>
            {n.url ? (
              <>
                {' '}
                <a href={n.url} target="_blank" rel="noreferrer" title={n.label}>
                  open <ExternalLinkIcon size={11} />
                </a>
              </>
            ) : null}
            {n.since ? (
              <span className="section-hint" title={n.since}>
                {' '}
                · {timeAgo(n.since, now)}
              </span>
            ) : null}
            {n.recommend ? (
              <div className="field-hint">
                It would: {n.recommend}
                {n.why ? ` — ${n.why}` : ''}
              </div>
            ) : null}
          </li>
        ))}
      </ul>
      {view.needsYouWaiting > view.needsYou.length ? (
        <p className="field-hint">{view.needsYouWaiting - view.needsYou.length} more wait than are listed here.</p>
      ) : null}
      <p className="field-hint">Answer in a prompt, such as "do what you suggest" or "close the CLI issue instead".</p>
    </div>
  )
}
