// The shell's Needs you area (docs/plans/done/ui-lifecycle.md, decision 5): what
// waits on a person across projects, each with why, in status's own words, and
// where it is done. An item that offers answers, a Standard's files left out,
// has a button for each with UI actions on, and their commands without.
//
// The groups per project fold, remembered in this browser; chips filter the
// items by kind, with a count each; on a wide screen the items sit in two
// columns (docs/plans/ui-shell.md, step 2).

import { useContext, useId, useState, type MouseEvent } from 'react'
import type { ContainerWriter, StandardAnswer } from '../k8s/containers'
import type { Problem } from '../k8s/sources'
import type { Project } from '../k8s/types'
import { hrefFor, SessionContext, type UrlState } from '../lib/hooks'
import { STORED_KEYS, useStoredSet } from '../lib/prefs'
import { byProject, kindCounts, type Need, type NeedKind, type Place } from '../model/needs'
import { SignInLink } from './Account'
import { LeftOutFiles } from './bits'
import { writeError, type WriteError } from './briefs/errors'
import { FormError } from './briefs/PromptForm'
import { ChevronDownIcon, ExternalLinkIcon } from './icons'
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
  const [kind, setKind] = useState<NeedKind>()
  const [folded, fold] = useStoredSet(STORED_KEYS.needsFolded)
  const counts = kindCounts(items)
  // A kind whose items are gone shows them all again.
  const filter = counts.some((c) => c.kind === kind) ? kind : undefined
  const groups = byProject(filter ? items.filter((i) => i.kind === filter) : items, projects)
  return (
    <section className="shell-page needs" aria-labelledby="needs-title">
      <header className="area-head">
        <div>
          <h1 className="area-title" id="needs-title">
            Needs you
          </h1>
          <p className="area-caption">
            What waits on a person, across projects: why, as the factory says it, and where it is done.
          </p>
        </div>
        {items.length ? (
          <div className="chips" role="group" aria-label="Show the items of a kind">
            <Chip label="All" count={items.length} on={!filter} onClick={() => setKind(undefined)} />
            {counts.map((c) => (
              <Chip
                key={c.kind}
                label={c.kind}
                count={c.count}
                on={filter === c.kind}
                onClick={() => setKind(filter === c.kind ? undefined : c.kind)}
              />
            ))}
          </div>
        ) : null}
      </header>
      <PanelState
        loaded={loaded}
        problems={problems}
        empty={items.length === 0}
        loading="Loading what waits on a person…"
        emptyText="Nothing waits on you. Briefs, Containers, Tasks and Crews will show up here when one does."
      >
        <div className="needs-groups">
          {groups.map((g) => (
            <NeedsGroup
              key={g.namespace}
              group={g}
              folded={folded.has(g.namespace)}
              onFold={(on) => fold(g.namespace, on)}
              setUrl={setUrl}
              writer={writer}
            />
          ))}
        </div>
      </PanelState>
    </section>
  )
}

/** A filter chip: its kind and how many items it has, pressed while it filters. */
function Chip({ label, count, on, onClick }: { label: string; count: number; on: boolean; onClick: () => void }) {
  return (
    <button type="button" className="chip" aria-pressed={on} onClick={onClick}>
      {label}
      <span className="chip-count">{count}</span>
    </button>
  )
}

/** A project's items under a heading that folds them, with their count. */
function NeedsGroup({
  group,
  folded,
  onFold,
  setUrl,
  writer,
}: {
  group: ReturnType<typeof byProject>[number]
  folded: boolean
  onFold: (folded: boolean) => void
  setUrl: (next: Partial<UrlState>) => void
  writer: ContainerWriter
}) {
  const id = useId()
  return (
    <section className="panel needs-project" aria-label={`Project ${group.title}`}>
      <h2 className="needs-project-title">
        <button
          type="button"
          className="fold-button"
          aria-expanded={!folded}
          aria-controls={id}
          onClick={() => onFold(!folded)}
        >
          <ChevronDownIcon size={16} className="fold-chevron" />
          {group.title}
          {group.title !== group.namespace ? <code>{group.namespace}</code> : null}
          <span className="chip-count">{group.items.length}</span>
        </button>
      </h2>
      <ul className="needs-list" id={id} hidden={folded}>
        {group.items.map((item) => (
          <NeedItem key={item.key} item={item} setUrl={setUrl} writer={writer} />
        ))}
      </ul>
    </section>
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
      {/* A long line scrolls; the keyboard reaches it too. */}
      <code tabIndex={0}>{text}</code>
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
