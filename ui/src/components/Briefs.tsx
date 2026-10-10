// The Briefs of a project: a box to describe a new app, and a card per Brief
// with what the planner made of it, a field to steer it with a prompt and a
// button to pause or resume it. These are the UI's three writes when actions are off; the CRD
// decides what is accepted.

import { useState } from 'react'
import type { BriefWriter } from '../k8s/briefs'
import type { Problem } from '../k8s/sources'
import type { Agent, Brief } from '../k8s/types'
import { describeBrief, pauseAction, sortBriefs, type BriefView } from '../model/brief'
import { UsageLine, When } from './bits'
import { ExternalLinkIcon } from './icons'
import { PanelState } from './PanelState'
import { plannerLimit } from './objects/rows'
import { NewBrief } from './briefs/NewBrief'
import { BriefDetails } from './briefs/BriefDetails'
import { FormError, PromptForm } from './briefs/PromptForm'
import { writeError, type WriteError } from './briefs/errors'
import { AnswerButtons, type AnswerContext } from './briefs/Answers'
import { RequestButtons } from './briefs/RequestButtons'
import { failedAnswers } from '../model/answers'
import './briefs.css'

export function Briefs({
  briefs,
  namespace,
  canCreate,
  writer,
  agents = [],
  pageHref,
  loaded = true,
  problems,
}: {
  briefs: Brief[]
  /** The Briefs and the Project have loaded; unset where the caller waits for them. */
  loaded?: boolean
  /** The failed reads of Briefs and the Project. */
  problems?: Problem[]
  /** Each Brief's object page. */
  pageHref: (name: string) => string
  namespace: string
  /** The namespace's Agents, for each planner's sessionTokenLimit. */
  agents?: Agent[]
  /** False when the namespace holds no Project: the server makes no Brief there. */
  canCreate: boolean
  writer: BriefWriter
}) {
  if (loaded && !problems?.length && !canCreate && !briefs.length) return null
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
      <PanelState loaded={loaded} problems={problems} loading="Loading the Briefs…">
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
              plannerLimit={plannerLimit(agents, b)}
              href={pageHref(b.metadata.name)}
            />
          ))}
        </div>
      </PanelState>
    </section>
  )
}

function BriefCard({
  brief,
  namespace,
  writer,
  plannerLimit,
  href,
}: {
  brief: Brief
  namespace: string
  writer: BriefWriter
  plannerLimit?: number
  /** The Brief's page. */
  href: string
}) {
  const view = describeBrief(brief, plannerLimit)
  const [pauseError, setPauseError] = useState<WriteError>()
  return (
    <article className={`panel brief brief-${view.phase.tone}`} aria-label={`Brief ${view.name}`}>
      <div className="panel-head">
        <div className="brief-title">
          <h3 className="panel-title small-title mono">
            <a href={href}>{view.name}</a>
          </h3>
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
                  created <When at={brief.metadata.creationTimestamp} />
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
      {pauseError ? <FormError error={pauseError} /> : null}
      <BriefBody view={view} namespace={namespace} writer={writer} />
    </article>
  )
}

/**
 * What a Brief's card and page show below its head: notes, what needs you, its
 * details and the prompt box. With `answering` (the Brief's page) each item
 * that waits for the person has its one-click answers.
 */
export function BriefBody({
  view,
  namespace,
  writer,
  answering = false,
}: {
  view: BriefView
  namespace: string
  writer: BriefWriter
  answering?: boolean
}) {
  const answers: AnswerContext | undefined = answering
    ? { namespace, name: view.name, writer, prompts: view.prompts }
    : undefined
  return (
    <>
      {view.pausedNote ? (
        <div className="brief-note brief-note-waiting brief-paused" role="status">
          {view.pausedNote}
        </div>
      ) : null}

      {view.needsYou.length ? <NeedsYouBox view={view} answers={answers} /> : null}
      {view.failure ? (
        <div className="brief-note brief-note-error">
          <strong>The last round failed.</strong> {view.failure}
        </div>
      ) : null}

      <BriefDetails view={view} answers={answers} />

      <PromptForm namespace={namespace} name={view.name} writer={writer} needsInput={Boolean(view.needsInput)} />
    </>
  )
}

/** Pauses a running Brief or resumes a paused one by patching spec.paused; the card shows the API's refusal. */
export function PauseButton({
  namespace,
  view,
  writer,
  onError,
}: {
  namespace: string
  view: BriefView
  writer: BriefWriter
  onError: (error: WriteError | undefined) => void
}) {
  const [sending, setSending] = useState(false)
  const action = pauseAction(view)

  async function click() {
    setSending(true)
    onError(undefined)
    try {
      await writer.setPaused(namespace, view.name, action.paused)
    } catch (err) {
      onError(writeError(err))
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
function NeedsYouBox({ view, answers }: { view: BriefView; answers?: AnswerContext }) {
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
                · <When at={n.since} />
              </span>
            ) : null}
            {n.recommend ? (
              <div className="field-hint">
                It would: {n.recommend}
                {n.why ? ` — ${n.why}` : ''}
              </div>
            ) : null}
            {answers && n.issue ? (
              <AnswerButtons
                context={answers}
                label={n.label}
                answers={failedAnswers(n.issue)}
                recommended={n.recommended}
              />
            ) : null}
            {answers && n.request ? (
              <RequestButtons
                namespace={answers.namespace}
                name={answers.name}
                writer={answers.writer}
                releases={view.releases}
                resourceVersion={view.resourceVersion}
                request={n.request}
                label={n.label}
                settled={view.requests.settled[n.request.repository.toLowerCase()]}
              />
            ) : null}
          </li>
        ))}
      </ul>
      {view.needsYouWaiting > view.needsYou.length ? (
        <p className="field-hint">{view.needsYouWaiting - view.needsYou.length} more wait than are listed here.</p>
      ) : null}
      <p className="field-hint">
        {view.needsYou.some((n) => !n.request)
          ? answers && view.needsYou.some((n) => n.issue)
            ? 'Answer with a button, which adds a prompt saying so, or in a prompt of your own, such as "do what you suggest".'
            : 'Answer in a prompt, such as "do what you suggest" or "close the CLI issue instead".'
          : ''}
        {view.needsYou.some((n) => n.request)
          ? `${view.needsYou.some((n) => !n.request) ? ' ' : ''}A request from GitHub is answered by accepting or declining it, not by a prompt: its words reach the planner only once you said yes.`
          : ''}
      </p>
    </div>
  )
}
