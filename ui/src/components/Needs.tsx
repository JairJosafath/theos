// The shell's Needs you area (docs/plans/ui-lifecycle.md, decision 5): what
// waits on a person across projects, each with why, in status's own words, and
// where it is done.

import { useState, type MouseEvent } from 'react'
import type { Project } from '../k8s/types'
import { hrefFor, type UrlState } from '../lib/hooks'
import { byProject, type Need, type Place } from '../model/needs'
import { ExternalLinkIcon } from './icons'
import './needs.css'

/** Leaves an object page and any Task or system the URL had selected. */
const PLACE_ONLY = { ns: undefined, kind: undefined, name: undefined, task: undefined, system: undefined }

export function NeedsArea({
  items,
  projects,
  loaded,
  setUrl,
}: {
  items: Need[]
  projects: Project[]
  loaded: boolean
  setUrl: (next: Partial<UrlState>) => void
}) {
  const groups = byProject(items, projects)
  return (
    <main className="shell-page">
      <section className="panel needs">
        <h1 className="panel-title">Needs you</h1>
        <p className="panel-caption">
          What waits on a person, across projects: why, as the factory says it, and where it is done.
        </p>
        {!loaded ? (
          <p className="panel-text">Loading…</p>
        ) : groups.length === 0 ? (
          <p className="panel-text">
            Nothing waits on you. Briefs, Containers, Tasks and Crews will show up here when one does.
          </p>
        ) : (
          groups.map((g) => (
            <section key={g.namespace} className="needs-project" aria-label={`Project ${g.title}`}>
              <h2 className="needs-project-title">
                {g.title}
                {g.title !== g.namespace ? <code>{g.namespace}</code> : null}
              </h2>
              <ul className="needs-list">
                {g.items.map((item) => (
                  <NeedItem key={item.key} item={item} setUrl={setUrl} />
                ))}
              </ul>
            </section>
          ))
        )}
      </section>
    </main>
  )
}

function NeedItem({ item, setUrl }: { item: Need; setUrl: (next: Partial<UrlState>) => void }) {
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
    </li>
  )
}

/** The place as URL state; a place that names no project keeps the one in view. */
function placeUrl(p: Place): Partial<UrlState> {
  if ('page' in p) return { ...p.page }
  return {
    area: p.area,
    view: p.view ?? 'context',
    system: p.system,
    task: p.task,
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
