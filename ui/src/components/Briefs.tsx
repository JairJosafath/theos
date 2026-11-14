// The Briefs of a project (docs/plans/done/ui-shell.md, step 2): a card that opens
// the New brief modal to describe a new app, and a compact card per Brief (its
// name, phase, what it waits for, cost and latest round) that opens its page
// in the drawer. The page shows what the planner made of it, a field to steer
// it with a prompt and a button to pause or resume it. These are the UI's three
// writes when actions are off; the CRD decides what is accepted.

import { useState } from 'react'
import type { BriefWriter } from '../k8s/briefs'
import type { Problem } from '../k8s/sources'
import type { Agent, Brief } from '../k8s/types'
import { describeBrief, pauseAction, pauseRunsAction, sortBriefs, type BriefView } from '../model/brief'
import { When } from './bits'
import { ExternalLinkIcon, PlusIcon } from './icons'
import { PanelState } from './PanelState'
import { plannerLimit } from './objects/rows'
import { BriefDetails } from './briefs/BriefDetails'
import { PromptForm } from './briefs/PromptForm'
import { writeError, type WriteError } from './briefs/errors'
import { AnswerButtons, type AnswerContext } from './briefs/Answers'
import { RequestButtons } from './briefs/RequestButtons'
import { failedAnswers } from '../model/answers'
import { pageHref as objectHref } from './objects/links'
import './briefs.css'

export function Briefs({
  briefs,
  canCreate,
  onNewBrief,
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
  /** The namespace's Agents, for each planner's sessionTokenLimit. */
  agents?: Agent[]
  /** False when the namespace holds no Project: the server makes no Brief there. */
  canCreate: boolean
  /** Opens the New brief modal (the form in briefs/NewBrief.tsx). */
  onNewBrief?: () => void
}) {
  if (loaded && !problems?.length && !canCreate && !briefs.length) return null
  const sorted = sortBriefs(briefs)
  return (
    <section className="shell-page briefs" aria-labelledby="briefs-title">
      <header className="area-head">
        <div>
          <h1 className="area-title" id="briefs-title">
            Briefs
          </h1>
          <p className="area-caption">
            Describe an app in plain words. A planner agent turns it into software systems, containers, crews and
            issues, round by round; open a brief to steer it with a prompt, or to pause or resume it. Those, and
            creating a brief, are the only changes this page can make.
          </p>
        </div>
      </header>
      <PanelState loaded={loaded} problems={problems} loading="Loading the Briefs…">
        <ul className="briefs-grid">
          {canCreate ? (
            <li className="panel brief-new">
              <h2 className="panel-title small-title">Describe an app</h2>
              <p className="panel-caption">
                What is it for, and who uses it? A planner agent turns it into software systems, containers, crews and
                issues.
              </p>
              <button type="button" className="button brief-new-open" onClick={onNewBrief} disabled={!onNewBrief}>
                <PlusIcon size={16} /> New brief
              </button>
            </li>
          ) : null}
          {sorted.map((b) => (
            <BriefCard
              key={b.metadata.uid ?? b.metadata.name}
              brief={b}
              plannerLimit={plannerLimit(agents, b)}
              href={pageHref(b.metadata.name)}
            />
          ))}
        </ul>
      </PanelState>
    </section>
  )
}

/**
 * A Brief in a few lines: its name and phase, its idea, what it waits for,
 * its round and cost, and its latest round's summary cut short. The whole
 * card is a link to its page, which opens in the drawer.
 */
function BriefCard({
  brief,
  plannerLimit,
  href,
}: {
  brief: Brief
  plannerLimit?: number
  /** The Brief's page. */
  href: string
}) {
  const view = describeBrief(brief, plannerLimit)
  const latest = view.rounds[0]
  const waits = waitsFor(view)
  return (
    <li className={`brief-card tone-${view.phase.tone}`}>
      {/* The card's text names the link (WCAG 2.5.3, label in name); what isn't shown, that it is a Brief, is
          said for a screen reader, so the name starts "Brief <name> <phase>". */}
      <a className="panel brief-card-link" href={href}>
        <span className="brief-card-head">
          <span className="visually-hidden">Brief </span>
          <span className="brief-card-name mono">{view.name}</span>
          <span className={`badge badge-${view.phase.tone} badge-sm`} title={view.phase.explanation}>
            <span className="badge-dot" aria-hidden="true" />
            {view.phase.label}
          </span>
        </span>
        <span className="brief-card-idea" title={view.idea}>
          {view.idea}
        </span>
        {waits ? <span className={`brief-card-waits brief-card-waits-${waits.tone}`}>{waits.text}</span> : null}
        <span className="brief-card-meta">
          {view.round ? `Round ${view.round}` : 'No round yet'}
          {view.usage?.cost ? (
            <>
              <span className="sep">·</span>
              <span title="What planning cost over all rounds">{view.usage.cost}</span>
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
        {latest?.summary ? (
          <span className="brief-card-round" title={latest.summary}>
            <strong>
              Round {latest.round}
              {latest.outcome ? ` · ${latest.outcome}` : ''}:
            </strong>{' '}
            {latest.summary}
          </span>
        ) : null}
      </a>
    </li>
  )
}

/** What a Brief waits for, in a line: a person, its pause, or a failed round; nothing while it gets on. */
function waitsFor(view: BriefView): { text: string; tone: 'waiting' | 'error' } | undefined {
  if (view.paused) return { text: view.pausedNote ?? 'Paused', tone: 'waiting' }
  if (view.needsInput) return { text: `Waits for your answer: ${view.needsInput}`, tone: 'waiting' }
  if (view.needsYouWaiting) return { text: `${view.needsYouWaiting} waiting for you`, tone: 'waiting' }
  if (view.failure) return { text: `The last round failed: ${view.failure}`, tone: 'error' }
  return undefined
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
    ? { namespace, name: view.name, writer, prompts: view.prompts, written: view.promptsWritten }
    : undefined
  return (
    <>
      {view.pausedNote ? (
        <div className="brief-note brief-note-waiting brief-paused" role="status">
          {view.pausedNote}
        </div>
      ) : null}

      {view.needsYou.length ? <NeedsYouBox view={view} namespace={namespace} answers={answers} /> : null}
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

/**
 * Pauses a running Brief or resumes a paused one by patching spec.paused, and
 * spec.pauseRuns when its runs are paused too; the card shows the API's refusal.
 */
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
      await writer.setPaused(namespace, view.name, action.paused, view.pauseRuns)
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

/**
 * Pauses the running Tasks of a Brief and its Crews at their next tool
 * boundary, or resumes them, by patching spec.pauseRuns (with spec.paused when
 * pausing); the card shows the API's refusal.
 */
export function PauseRunsButton({
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
  const action = pauseRunsAction(view.pauseRuns, 'brief')

  async function click() {
    setSending(true)
    onError(undefined)
    try {
      await writer.setPauseRuns(namespace, view.name, action.pauseRuns)
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
      aria-label={`${action.label} of brief ${view.name}`}
      aria-pressed={view.pauseRuns}
      disabled={sending}
      onClick={click}
    >
      {sending ? (action.pauseRuns ? 'Pausing runs…' : 'Resuming runs…') : action.label}
    </button>
  )
}

/** What only the person can decide: what the Brief waits for, then each item with what it would do and why. */
function NeedsYouBox({ view, namespace, answers }: { view: BriefView; namespace: string; answers?: AnswerContext }) {
  const prompted = view.needsYou.some((n) => !n.request && !n.container)
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
            {n.container ? (
              <>
                {' '}
                <a href={objectHref(namespace, 'Container', n.container)} title={n.label}>
                  open the Container
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
                requestSources={view.requestSources}
                resourceVersion={view.resourceVersion}
                request={n.request}
                label={n.label}
                settled={
                  'source' in n.request
                    ? view.requests.settledSources[n.request.source]
                    : view.requests.settled[n.request.repository.toLowerCase()]
                }
              />
            ) : null}
          </li>
        ))}
      </ul>
      {view.needsYouWaiting > view.needsYou.length ? (
        <p className="field-hint">{view.needsYouWaiting - view.needsYou.length} more wait than are listed here.</p>
      ) : null}
      <p className="field-hint">
        {[
          prompted
            ? answers && view.needsYou.some((n) => n.issue)
              ? 'Answer with a button, which adds a prompt saying so, or in a prompt of your own, such as "do what you suggest".'
              : 'Answer in a prompt, such as "do what you suggest" or "close the CLI issue instead".'
            : '',
          view.needsYou.some((n) => n.container) ? 'A Container is settled on its page, which says what to do.' : '',
          view.needsYou.some((n) => n.request)
            ? 'A request from GitHub or a tracker is answered by accepting or declining it, not by a prompt: its words reach the planner only once you said yes.'
            : '',
        ]
          .filter(Boolean)
          .join(' ')}
      </p>
    </div>
  )
}
