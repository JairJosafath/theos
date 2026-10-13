// The shell's Needs you area (docs/plans/done/ui-lifecycle.md, decision 5): what
// waits on a person across projects, each with why, in status's own words, and
// where it is done. An item that offers answers, a Standard's files left out,
// has a button for each with UI actions on, and their commands without.

import { useContext, useState, type MouseEvent } from 'react'
import type { ContainerWriter, StandardAnswer } from '../k8s/containers'
import type { Problem } from '../k8s/sources'
import type { Project } from '../k8s/types'
import { hrefFor, SessionContext, type UrlState } from '../lib/hooks'
import { byProject, type Need, type Place } from '../model/needs'
import { SignInLink } from './Account'
import { LeftOutFiles } from './bits'
import { writeError, type WriteError } from './briefs/errors'
import { FormError } from './briefs/PromptForm'
import { ExternalLinkIcon } from './icons'
import { PanelState } from './PanelState'
import './needs.css'

/** Leaves an object page and any system the URL had selected. */
const PLACE_ONLY = { ns: undefined, kind: undefined, name: undefined, system: undefined }

export function NeedsArea({
  items,
  projects,
  loaded,
  problems,
  setUrl,
  writer,
}: {
  items: Need[]
  projects: Project[]
  loaded: boolean
  /** The failed reads: any of them may hide an item. */
  problems: Problem[]
  setUrl: (next: Partial<UrlState>) => void
  /** Sends the answers items offer, with actions on. */
  writer: ContainerWriter
}) {
  const groups = byProject(items, projects)
  return (
    <main className="shell-page">
      <section className="panel needs">
        <h1 className="panel-title">Needs you</h1>
        <p className="panel-caption">
          What waits on a person, across projects: why, as the factory says it, and where it is done.
        </p>
        <PanelState
          loaded={loaded}
          problems={problems}
          empty={groups.length === 0}
          loading="Loading what waits on a person…"
          emptyText="Nothing waits on you. Briefs, Containers, Tasks and Crews will show up here when one does."
        >
          {groups.map((g) => (
            <section key={g.namespace} className="needs-project" aria-label={`Project ${g.title}`}>
              <h2 className="needs-project-title">
                {g.title}
                {g.title !== g.namespace ? <code>{g.namespace}</code> : null}
              </h2>
              <ul className="needs-list">
                {g.items.map((item) => (
                  <NeedItem key={item.key} item={item} setUrl={setUrl} writer={writer} />
                ))}
              </ul>
            </section>
          ))}
        </PanelState>
      </section>
    </main>
  )
}

function NeedItem({
  item,
  setUrl,
  writer,
}: {
  item: Need
  setUrl: (next: Partial<UrlState>) => void
  writer: ContainerWriter
}) {
  const to = { ...PLACE_ONLY, ...placeUrl(item.place.to) }
  return (
    <li className="need" aria-label={`${item.kind} ${item.name}`}>
      <div className="need-head">
        <span className="need-kind">{item.kind}</span> <strong className="need-name">{item.name}</strong> {item.title}
      </div>
      <p className="need-why">
        <code className="need-reason">{item.reason}</code>
        {item.message ? ` ${item.message}` : null}
      </p>
      <div className="need-actions">
        <a
          href={hrefFor(to)}
          onClick={(e) => {
            if (!plainClick(e)) return
            e.preventDefault()
            setUrl(to)
          }}
        >
          {item.place.label}
        </a>
        {item.links.map((l) => (
          <a key={l.url} href={l.url} target="_blank" rel="noreferrer">
            {l.label} <ExternalLinkIcon size={12} />
          </a>
        ))}
      </div>
      {item.command ? <Command text={item.command} /> : null}
      {item.standardAnswer ? <Answers item={item} answer={item.standardAnswer} writer={writer} /> : null}
    </li>
  )
}

/**
 * The Standard's files left out, each with what the Standard changed in it,
 * then the take and keep buttons, with actions on
 * and someone to make the write; otherwise the commands, and in OIDC mode with
 * nobody signed in a sign-in link.
 */
function Answers({
  item,
  answer,
  writer,
}: {
  item: Need
  answer: NonNullable<Need['standardAnswer']>
  writer: ContainerWriter
}) {
  const session = useContext(SessionContext)
  const [sending, setSending] = useState<StandardAnswer>()
  const [sent, setSent] = useState<StandardAnswer>()
  const [error, setError] = useState<WriteError>()
  const signedOut = session?.mode === 'oidc' && !session.user

  const files = answer.files.length ? <LeftOutFiles files={answer.files} className="need-why" /> : null

  if (!session?.actions || signedOut) {
    return (
      <>
        {files}
        {signedOut ? (
          <p className="need-why">
            Sign in to take or keep them. <SignInLink />
          </p>
        ) : null}
        <Command text={answer.take} />
        <Command text={answer.keep} />
      </>
    )
  }

  async function send(a: StandardAnswer) {
    setSending(a)
    setSent(undefined)
    setError(undefined)
    try {
      await writer.answerStandard(item.namespace, item.name, a, answer.ref)
      setSent(a)
    } catch (err) {
      setError(writeError(err))
    } finally {
      setSending(undefined)
    }
  }

  return (
    <>
      {files}
      <div className="need-buttons">
        <button type="button" className="button button-small" onClick={() => send('take')} disabled={!!sending}>
          {sending === 'take' ? 'Asking…' : 'Take the Standard’s version'}
        </button>
        <button
          type="button"
          className="button button-secondary button-small"
          onClick={() => send('keep')}
          disabled={!!sending}
        >
          {sending === 'keep' ? 'Asking…' : 'Keep ours'}
        </button>
      </div>
      {sent ? (
        <p className="form-done" role="status">
          Asked the factory to {sent === 'take' ? 'take the Standard’s version' : 'keep the repository’s version'}.
        </p>
      ) : null}
      {error ? <FormError error={error} /> : null}
    </>
  )
}

/** The place as URL state; a place that names no project keeps the one in view. */
function placeUrl(p: Place): Partial<UrlState> {
  if ('page' in p) return { ...p.page }
  return {
    area: p.area,
    view: p.view ?? 'context',
    system: p.system,
    ...(p.project ? { project: p.project } : {}),
  }
}

/** A command to run, with a button that copies it. */
function Command({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="need-command">
      <code>{text}</code>
      <button
        type="button"
        className="button button-secondary button-small"
        onClick={() => {
          void navigator.clipboard?.writeText(text).then(
            () => setCopied(true),
            () => setCopied(false),
          )
        }}
      >
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  )
}

function plainClick(e: MouseEvent) {
  return e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey
}
