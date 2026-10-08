// The Briefs of a project: a box to describe a new app, and a card per Brief
// with what the planner made of it and a field to steer it with a prompt.
// These are the UI's only two writes; the CRD decides what is accepted.

import { useId, useState, type FormEvent, type ReactNode } from 'react'
import {
  DEFAULT_PARALLEL,
  isBriefName,
  MAX_IDEA,
  MAX_PARALLEL,
  MAX_PROMPT,
  nameFromIdea,
  type BriefWriter,
} from '../k8s/briefs'
import type { Brief, BriefAutopilot } from '../k8s/types'
import {
  AUTOPILOT_MODES,
  DEFAULT_AUTOPILOT,
  describeBrief,
  sortBriefs,
  type BriefAppView,
  type BriefView,
} from '../model/brief'
import { plural, timeAgo } from '../model/describe'
import { UsageLine } from './bits'
import { ExternalLinkIcon, RepoIcon, WarningIcon } from './icons'

/** How many items of a long list show before "N more"; chips are smaller, so more of them. */
const SHOWN = 3
const SHOWN_CHIPS = 6

export function Briefs({
  briefs,
  namespace,
  canCreate,
  writer,
  now,
}: {
  briefs: Brief[]
  namespace: string
  /** False when the namespace holds no Project: the server makes no Brief there. */
  canCreate: boolean
  writer: BriefWriter
  now: number
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
          round by round; add a prompt to steer it. Creating a brief and adding a prompt are the only changes this page
          can make.
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
            now={now}
          />
        ))}
      </div>
    </section>
  )
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** The "Describe an app" box: an idea and a name for it, prefilled from the idea. */
function NewBrief({ namespace, taken, writer }: { namespace: string; taken: string[]; writer: BriefWriter }) {
  const id = useId()
  const [idea, setIdea] = useState('')
  // Until the person types a name, it follows the idea.
  const [typedName, setTypedName] = useState<string>()
  // Empty leaves spec.autopilot unset: the factory's default.
  const [autopilot, setAutopilot] = useState<BriefAutopilot | ''>('')
  // 0 leaves spec.parallel unset: the factory's default.
  const [parallel, setParallel] = useState(0)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string>()
  const [created, setCreated] = useState<string>()
  const name = typedName ?? (idea.trim() ? nameFromIdea(idea, taken) : '')
  const nameProblem =
    name && !isBriefName(name)
      ? 'Use lowercase letters, digits and dashes, at most 63, starting and ending with a letter or digit.'
      : undefined

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!idea.trim() || !name || nameProblem) return
    setSending(true)
    setError(undefined)
    try {
      await writer.create(namespace, name, idea.trim(), {
        autopilot: autopilot || undefined,
        parallel: parallel || undefined,
      })
      setCreated(name)
      setIdea('')
      setTypedName(undefined)
      setAutopilot('')
      setParallel(0)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setSending(false)
    }
  }

  return (
    <form className="panel brief-new" onSubmit={submit}>
      <h3 className="panel-title small-title">Describe an app</h3>
      <label className="field" htmlFor={`${id}-idea`}>
        <span className="field-label">What is it for, and who uses it?</span>
      </label>
      <textarea
        id={`${id}-idea`}
        className="input"
        rows={4}
        maxLength={MAX_IDEA}
        placeholder="A place where neighbours lend each other tools, with a calendar for who has what."
        value={idea}
        onChange={(e) => {
          setIdea(e.target.value)
          setCreated(undefined)
        }}
        disabled={sending}
      />
      <label className="field" htmlFor={`${id}-name`}>
        <span className="field-label">Name</span>
        <span className="field-hint">
          Its repository is <span className="mono">brief-{name || '…'}</span>
        </span>
      </label>
      <input
        id={`${id}-name`}
        className="input mono"
        value={name}
        maxLength={63}
        placeholder="from the idea"
        spellCheck={false}
        autoComplete="off"
        aria-invalid={nameProblem ? true : undefined}
        aria-describedby={nameProblem ? `${id}-name-problem` : undefined}
        onChange={(e) => setTypedName(e.target.value)}
        disabled={sending}
      />
      {nameProblem ? (
        <p className="field-problem" id={`${id}-name-problem`}>
          {nameProblem}
        </p>
      ) : null}
      <label className="field" htmlFor={`${id}-autopilot`}>
        <span className="field-label">Autopilot</span>
        <span className="field-hint">
          {(autopilot ? AUTOPILOT_MODES[autopilot] : AUTOPILOT_MODES[DEFAULT_AUTOPILOT]).explanation}
        </span>
      </label>
      <select
        id={`${id}-autopilot`}
        className="input"
        value={autopilot}
        onChange={(e) => setAutopilot(e.target.value as BriefAutopilot | '')}
        disabled={sending}
      >
        <option value="">The factory’s default ({AUTOPILOT_MODES[DEFAULT_AUTOPILOT].label.toLowerCase()})</option>
        {(Object.keys(AUTOPILOT_MODES) as BriefAutopilot[]).map((mode) => (
          <option key={mode} value={mode}>
            {AUTOPILOT_MODES[mode].label}
          </option>
        ))}
      </select>
      <label className="field" htmlFor={`${id}-parallel`}>
        <span className="field-label">Parallel</span>
        <span className="field-hint">How many issues each of its crews works on at once.</span>
      </label>
      <select
        id={`${id}-parallel`}
        className="input"
        value={parallel}
        onChange={(e) => setParallel(Number(e.target.value))}
        disabled={sending}
      >
        <option value={0}>The factory’s default ({DEFAULT_PARALLEL} at a time)</option>
        {Array.from({ length: MAX_PARALLEL }, (_, i) => i + 1).map((n) => (
          <option key={n} value={n}>
            {n} at a time
          </option>
        ))}
      </select>
      <div className="form-row">
        <button type="submit" className="button" disabled={sending || !idea.trim() || !name || Boolean(nameProblem)}>
          {sending ? 'Creating…' : 'Create brief'}
        </button>
        {created && !error ? (
          <span className="form-done" role="status">
            Created <span className="mono">{created}</span>
          </span>
        ) : null}
      </div>
      {error ? <FormError message={error} /> : null}
    </form>
  )
}

function FormError({ message }: { message: string }) {
  return (
    <p className="form-error" role="alert">
      <WarningIcon size={14} />
      <span>{message}</span>
    </p>
  )
}

function BriefCard({
  brief,
  namespace,
  writer,
  now,
}: {
  brief: Brief
  namespace: string
  writer: BriefWriter
  now: number
}) {
  const view = describeBrief(brief)
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
            <span title={view.autopilot.explanation}>
              {view.autopilot.label}
              {view.autopilot.isDefault ? ' (default)' : ''}
            </span>
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
        <span className={`badge badge-${view.phase.tone} badge-sm`} title={view.phase.explanation}>
          <span className="badge-dot" aria-hidden="true" />
          {view.phase.label}
        </span>
      </div>
      <p className="brief-idea" title={view.idea}>
        {view.idea}
      </p>

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

function BriefDetails({ view, now }: { view: BriefView; now: number }) {
  return (
    <>
      {view.tended.length ? (
        <>
          <h4 className="section-title">
            What it did by itself <span className="section-hint">{plural(view.tended.length, 'tending round')}</span>
          </h4>
          <Clipped
            className="brief-list brief-issues brief-tended"
            items={view.tended.map((t) => (
              <li key={t.round}>
                <span className="mono">Round {t.round}</span>
                {t.summary ? <span className="brief-issue-title"> {t.summary}</span> : null}
                {t.answers.map((a) => (
                  <div key={a.label} className="field-hint">
                    {a.url ? (
                      <a href={a.url} target="_blank" rel="noreferrer">
                        {a.label}
                      </a>
                    ) : (
                      <span className="mono">{a.label}</span>
                    )}{' '}
                    {a.text}
                  </div>
                ))}
              </li>
            ))}
          />
        </>
      ) : null}
      {view.rounds.length ? (
        <>
          <h4 className="section-title">
            Rounds <span className="section-hint">newest first, with what each round’s planner used</span>
          </h4>
          <Clipped
            className="brief-list brief-issues brief-rounds"
            items={view.rounds.map((r) => (
              <li key={r.round}>
                <span className="mono">Round {r.round}</span>
                {r.outcome ? <span className="field-hint"> · {r.outcome}</span> : null}
                {r.summary ? <span className="brief-issue-title"> {r.summary}</span> : null}
                {r.usage ? (
                  <div className="field-hint">
                    <UsageLine usage={r.usage} />
                  </div>
                ) : null}
              </li>
            ))}
          />
        </>
      ) : null}
      {view.choices.length ? (
        <>
          <h4 className="section-title">Choices in the last round</h4>
          <Clipped
            items={view.choices.map((c) => (
              <li key={c}>{c}</li>
            ))}
            className="bullets brief-list"
          />
        </>
      ) : null}
      {view.created.length ? (
        <>
          <h4 className="section-title">
            Made <span className="section-hint">{view.created.length}</span>
          </h4>
          <Clipped
            className="brief-chips"
            shown={SHOWN_CHIPS}
            items={view.created.map((o) => (
              <li key={`${o.kind}/${o.name}`} className="brief-chip" title={`${o.kind} ${o.name}`}>
                <span className="brief-chip-kind">{o.kind}</span> {o.name}
              </li>
            ))}
          />
        </>
      ) : null}
      {view.issues.length ? (
        <>
          <h4 className="section-title">
            Issues <span className="section-hint">{view.issues.length}</span>
          </h4>
          <Clipped
            className="brief-list brief-issues"
            items={view.issues.map((i) => {
              const label = `${i.repository}#${i.number}`
              return (
                <li key={label}>
                  {i.url ? (
                    <a href={i.url} target="_blank" rel="noreferrer">
                      {label}
                    </a>
                  ) : (
                    <span className="mono">{label}</span>
                  )}
                  {i.title ? <span className="brief-issue-title"> {i.title}</span> : null}
                  {i.failed ? (
                    <span className="badge badge-error badge-sm brief-issue-failed" title="Handed back to a person">
                      failed
                    </span>
                  ) : null}
                </li>
              )
            })}
          />
        </>
      ) : null}
      {view.failedWaiting ? (
        <>
          <h4 className="section-title">Failed issues ({view.failedWaiting})</h4>
          <Clipped
            className="brief-list brief-issues brief-failed"
            items={view.failed.map((f) => {
              const label = `${f.container} #${f.number}`
              return (
                <li key={label}>
                  {f.url ? (
                    <a href={f.url} target="_blank" rel="noreferrer">
                      {label}
                    </a>
                  ) : (
                    <span className="mono">{label}</span>
                  )}
                  {f.title ? <span className="brief-issue-title"> {f.title}</span> : null}
                  <div className="brief-failed-reason">{f.reason}</div>
                  {f.note ? <div className="field-hint">{f.note}</div> : null}
                </li>
              )
            })}
          />
          {view.failedWaiting > view.failed.length ? (
            <p className="field-hint">{view.failedWaiting - view.failed.length} more wait than are listed here.</p>
          ) : null}
          <p className="field-hint">{view.failedHint}</p>
        </>
      ) : null}
      {view.suggestionsWaiting ? (
        <>
          <h4 className="section-title">Reviewer suggestions ({view.suggestionsWaiting})</h4>
          <Clipped
            className="brief-list brief-issues"
            items={view.suggestions.map((g) => {
              const label = `${g.container} #${g.number}`
              return (
                <li key={label}>
                  {g.url ? (
                    <a href={g.url} target="_blank" rel="noreferrer">
                      {label}
                    </a>
                  ) : (
                    <span className="mono">{label}</span>
                  )}
                  {g.title ? <span className="brief-issue-title"> {g.title}</span> : null}
                </li>
              )
            })}
          />
          {view.suggestionsWaiting > view.suggestions.length ? (
            <p className="field-hint">
              {view.suggestionsWaiting - view.suggestions.length} more wait than are listed here.
            </p>
          ) : null}
          <p className="field-hint">Answer in a prompt, such as "take the input-safety ones, skip the rest".</p>
        </>
      ) : null}
      {view.apps.length ? (
        <>
          <h4 className="section-title">
            Releases <span className="section-hint">{view.apps.length}</span>
          </h4>
          <Clipped
            className="brief-list brief-apps"
            items={view.apps.map((a) => (
              <AppLine key={a.container} app={a} now={now} />
            ))}
          />
        </>
      ) : null}
      {view.prompts.length ? (
        <>
          <h4 className="section-title">
            Prompts{' '}
            {view.waitingPrompts ? (
              <span className="section-hint">{plural(view.waitingPrompts, 'waits', 'wait')} for the next round</span>
            ) : null}
          </h4>
          <Clipped
            className="brief-list brief-prompts"
            items={view.prompts.map((p, i) => (
              <li key={i} className={p.planned ? undefined : 'brief-prompt-waiting'}>
                <span>{p.text}</span>
                {p.planned ? null : <span className="pill pill-waiting">next round</span>}
              </li>
            ))}
          />
        </>
      ) : null}
      {view.repositoryURL ? (
        <a className="brief-repo" href={view.repositoryURL} target="_blank" rel="noreferrer">
          <RepoIcon size={14} /> Its plans, round by round <ExternalLinkIcon size={12} />
        </a>
      ) : null}
    </>
  )
}

/** An app of the Brief: releases on or off, its latest release and where it was published. */
function AppLine({ app, now }: { app: BriefAppView; now: number }) {
  return (
    <li className="brief-app">
      <div className="brief-app-head">
        <span className="mono">{app.container}</span>
        <span className={`badge badge-${app.tone} badge-sm`}>
          <span className="badge-dot" aria-hidden="true" />
          {app.label}
        </span>
        {app.release ? (
          <span className="brief-app-release">
            {app.release.url ? (
              <a href={app.release.url} target="_blank" rel="noreferrer" title={`Release ${app.release.tag} on Gitea`}>
                {app.release.tag}
              </a>
            ) : (
              <span className="mono">{app.release.tag}</span>
            )}
            {app.release.time ? <span className="brief-issue-title"> {timeAgo(app.release.time, now)}</span> : null}
          </span>
        ) : null}
        {app.publishedURL ? (
          <a
            className="brief-app-published"
            href={app.publishedURL}
            target="_blank"
            rel="noreferrer"
            title={`Release ${app.release?.tag ?? ''} on GitHub`}
          >
            {app.publishTo ?? 'GitHub'} <ExternalLinkIcon size={11} />
          </a>
        ) : app.publishTo ? (
          <span className="brief-app-published" title="Where its releases are published">
            to {app.publishTo}
          </span>
        ) : null}
        {app.parallel ? (
          <span className="brief-app-parallel" title="How many issues its crew works on at once">
            {app.parallel}
          </span>
        ) : null}
      </div>
      <p className="brief-app-message">{app.message}</p>
    </li>
  )
}

/** A list that shows its first few items and folds the rest behind "N more". */
function Clipped({ items, className, shown = SHOWN }: { items: ReactNode[]; className: string; shown?: number }) {
  // Folding away a single item saves nothing.
  if (items.length <= shown + 1) return <ul className={className}>{items}</ul>
  return (
    <>
      <ul className={className}>{items.slice(0, shown)}</ul>
      <details className="brief-more">
        <summary>{items.length - shown} more</summary>
        <ul className={className}>{items.slice(shown)}</ul>
      </details>
    </>
  )
}

/** A follow-up prompt for one Brief; the planner takes it in its next round. */
function PromptForm({
  namespace,
  name,
  writer,
  needsInput,
}: {
  namespace: string
  name: string
  writer: BriefWriter
  needsInput: boolean
}) {
  const id = useId()
  const [prompt, setPrompt] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string>()

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!prompt.trim()) return
    setSending(true)
    setError(undefined)
    try {
      await writer.addPrompt(namespace, name, prompt.trim())
      setPrompt('')
    } catch (err) {
      setError(errorText(err))
    } finally {
      setSending(false)
    }
  }

  return (
    <form className="brief-prompt-form" onSubmit={submit}>
      <label className="visually-hidden" htmlFor={`${id}-prompt`}>
        {needsInput ? `Answer brief ${name}` : `Add a prompt to brief ${name}`}
      </label>
      <div className="form-row">
        <input
          id={`${id}-prompt`}
          className="input"
          value={prompt}
          maxLength={MAX_PROMPT}
          placeholder={needsInput ? 'Answer in a prompt…' : 'Add a prompt to steer it…'}
          onChange={(e) => setPrompt(e.target.value)}
          disabled={sending}
        />
        <button type="submit" className="button button-secondary" disabled={sending || !prompt.trim()}>
          {sending ? 'Adding…' : 'Add'}
        </button>
      </div>
      {error ? <FormError message={error} /> : null}
    </form>
  )
}
