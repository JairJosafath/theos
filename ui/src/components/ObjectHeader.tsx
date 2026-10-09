// The header every object page opens with (docs/plans/ui-lifecycle.md,
// decisions 3 and 4): what it is and what it belongs to, where it is on its
// lifecycle, what a person does now (an action the UI takes, or the exact next
// step: a link or a kubectl line to copy), then its conditions, its timeline,
// its children and its links out. Everything comes from the object's status
// and the kind's table in model/lifecycle.ts.

import { useState, type ReactNode } from 'react'
import type { Condition } from '../k8s/types'
import { hasPhases, lifecycleOf, type LifecycleKind, type NextStep, type StepView } from '../model/lifecycle'
import { ConditionList } from './bits'
import { ExternalLinkIcon } from './icons'
import { pageHref } from './objects/links'
import './objects.css'

export interface Crumb {
  label: string
  href?: string
}

export interface TimelineEntry {
  key: string
  /** What happened, in a few words. */
  title: string
  detail?: string
  at?: string
  tone?: 'ok' | 'waiting' | 'error'
}

export interface ChildRef {
  kind: string
  name: string
  /** Its object page, for kinds that have one. */
  href?: string
  note?: string
}

export interface OutLink {
  href: string
  label: string
}

export interface ObjectHeaderProps {
  kind: LifecycleKind
  name: string
  namespace: string
  /** The parents, outermost first; the object itself comes last by itself. */
  breadcrumb: Crumb[]
  /** displayName, when it differs from the name. */
  title?: string
  phase?: string
  /** False for an object of a kind with phases that has none itself (a Relationship the factory doesn't apply). */
  phased?: boolean
  /** Undefined for a kind whose status has no conditions. */
  conditions?: Condition[]
  /** The kind's history, oldest first; undefined for a kind without one. */
  timeline?: TimelineEntry[]
  children: ChildRef[]
  /** The children's heading, such as "What it made". */
  childrenTitle: string
  links: OutLink[]
  /** What the UI itself can do at this step: one of its three writes. */
  actions?: ReactNode
  /** The step the UI can't take, said exactly. */
  next?: NextStep
}

export function ObjectHeader(props: ObjectHeaderProps) {
  const {
    kind,
    name,
    namespace,
    breadcrumb,
    title,
    phase,
    phased = true,
    conditions,
    timeline,
    children,
    childrenTitle,
    links,
    actions,
    next,
  } = props
  const life = lifecycleOf(kind, phase)
  const stepper = phased && hasPhases(kind)
  return (
    <section className="panel object-header" aria-label={`${kind} ${name}`}>
      <nav className="crumbs" aria-label="Breadcrumb">
        <ol>
          {breadcrumb.map((c) => (
            <li key={`${c.label}|${c.href ?? ''}`}>{c.href ? <a href={c.href}>{c.label}</a> : c.label}</li>
          ))}
          <li aria-current="page">
            {kind} {name}
          </li>
        </ol>
      </nav>
      <div className="panel-head">
        <div>
          <div className="eyebrow">
            {kind} · {namespace}
          </div>
          <h1 className="panel-title mono">{name}</h1>
          {title && title !== name ? <p className="object-title">{title}</p> : null}
        </div>
        {stepper && life.current ? (
          <span className={`badge badge-${life.current.tone}`} title={life.current.explanation}>
            <span className="badge-dot" aria-hidden="true" />
            {life.current.label}
          </span>
        ) : null}
      </div>

      {stepper ? <Stepper path={life.path} exits={life.exits} /> : null}
      <p className="object-moves">{life.moves}</p>

      <div className={`object-next object-next-${next ? 'person' : 'none'}`}>
        <h2 className="object-section">What you do now</h2>
        <p>{next?.text ?? life.person}</p>
        {next?.command ? <CommandLine command={next.command} /> : null}
        {next?.link ? (
          <p>
            <a href={next.link.href} target="_blank" rel="noreferrer">
              {next.link.label} <ExternalLinkIcon size={12} />
            </a>
          </p>
        ) : null}
        {next?.page ? (
          <p>
            <a href={pageHref(namespace, next.page.kind, next.page.name)}>{next.page.label}</a>
          </p>
        ) : null}
        {actions ? <div className="object-actions">{actions}</div> : null}
      </div>

      {conditions ? (
        <>
          <h2 className="object-section">Conditions</h2>
          <ConditionList
            conditions={conditions}
            empty={`The factory has not reported conditions on this ${kind} yet.`}
          />
        </>
      ) : null}

      {timeline ? (
        <>
          <h2 className="object-section">Timeline</h2>
          {timeline.length ? (
            <ol className="timeline">
              {timeline.map((t) => (
                <li key={t.key} className={`timeline-${t.tone ?? 'ok'}`}>
                  <span className="timeline-title">{t.title}</span>
                  {t.at ? (
                    <time className="timeline-at" dateTime={t.at}>
                      {new Date(t.at).toLocaleString()}
                    </time>
                  ) : null}
                  {t.detail ? <span className="timeline-detail">{t.detail}</span> : null}
                </li>
              ))}
            </ol>
          ) : (
            <p className="muted small">Nothing has happened yet.</p>
          )}
        </>
      ) : null}

      <h2 className="object-section">{childrenTitle}</h2>
      {children.length ? (
        <ul className="object-children">
          {children.map((c) => (
            <li key={`${c.kind}|${c.name}`}>
              <span className="object-child-kind">{c.kind}</span>{' '}
              {c.href ? (
                <a className="mono" href={c.href}>
                  {c.name}
                </a>
              ) : (
                <span className="mono">{c.name}</span>
              )}
              {c.note ? <span className="muted small"> · {c.note}</span> : null}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted small">Nothing yet.</p>
      )}

      {links.length ? (
        <ul className="object-links">
          {links.map((l) => (
            <li key={l.href}>
              <a href={l.href} target="_blank" rel="noreferrer">
                {l.label} <ExternalLinkIcon size={12} />
              </a>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  )
}

/** The kind's path with the current step marked, and its side exits off the path. */
function Stepper({ path, exits }: { path: StepView[]; exits: StepView[] }) {
  return (
    <div className="stepper">
      <ol className="stepper-path" aria-label="Lifecycle">
        {path.map((s) => (
          <Step key={s.phase} step={s} />
        ))}
      </ol>
      <ol className="stepper-exits" aria-label="Side exits">
        {exits.map((s) => (
          <Step key={s.phase} step={s} />
        ))}
      </ol>
    </div>
  )
}

function Step({ step }: { step: StepView }) {
  return (
    <li
      className={`step step-${step.state} step-tone-${step.tone}`}
      title={step.explanation}
      aria-current={step.state === 'current' ? 'step' : undefined}
    >
      <span className="step-dot" aria-hidden="true" />
      {step.label}
    </li>
  )
}

/** A command to run, with a button that copies it. */
export function CommandLine({ command }: { command: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="command-line">
      <code>{command}</code>
      <button
        type="button"
        className="button button-secondary button-small"
        aria-label="Copy the command"
        onClick={() => {
          void navigator.clipboard?.writeText(command).then(() => setCopied(true))
        }}
      >
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  )
}
