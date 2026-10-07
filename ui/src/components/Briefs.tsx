// The Briefs of a project: a box to describe a new app, and a card per Brief
// with what the planner made of it and a field to steer it with a prompt.
// These are the UI's only two writes; the CRD decides what is accepted.

import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { isBriefName, MAX_IDEA, MAX_PROMPT, nameFromIdea, type BriefWriter } from '../k8s/briefs'
import type { Brief } from '../k8s/types'
import { describeBrief, sortBriefs, type BriefView } from '../model/brief'
import { plural, timeAgo } from '../model/describe'
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
      await writer.create(namespace, name, idea.trim())
      setCreated(name)
      setIdea('')
      setTypedName(undefined)
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
            {view.cost ? (
              <>
                <span className="sep">·</span>
                <span title="What planning cost over all rounds">{view.cost}</span>
              </>
            ) : null}
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

      {view.needsInput ? (
        <div className="brief-note brief-note-waiting">
          <strong>It needs your input.</strong> {view.needsInput}
        </div>
      ) : null}
      {view.failure ? (
        <div className="brief-note brief-note-error">
          <strong>The last round failed.</strong> {view.failure}
        </div>
      ) : null}

      <BriefDetails view={view} />

      <PromptForm namespace={namespace} name={view.name} writer={writer} needsInput={Boolean(view.needsInput)} />
    </article>
  )
}

function BriefDetails({ view }: { view: BriefView }) {
  return (
    <>
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
                </li>
              )
            })}
          />
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
