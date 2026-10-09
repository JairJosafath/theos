// What the planner made of a Brief: its plan, apps, releases and items, folded
// to a few of each.

import type { ReactNode } from 'react'
import type { BriefAppView, BriefView } from '../../model/brief'
import { plural } from '../../model/describe'
import { UsageLine, When } from '../bits'
import { ExternalLinkIcon, RepoIcon } from '../icons'
import { failedAnswers, suggestionAnswers } from '../../model/answers'
import { AnswerButtons, type AnswerContext } from './Answers'

/** How many items of a long list show before "N more"; chips are smaller, so more of them. */
const SHOWN = 3
const SHOWN_CHIPS = 6

/** With `answers`, each failed issue Needs you doesn't list and each suggestion has its one-click answers. */
export function BriefDetails({ view, answers }: { view: BriefView; answers?: AnswerContext }) {
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
                  {answers && !f.asked ? (
                    <AnswerButtons context={answers} label={label} answers={failedAnswers(f)} />
                  ) : null}
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
                  {answers ? <AnswerButtons context={answers} label={label} answers={suggestionAnswers(g)} /> : null}
                </li>
              )
            })}
          />
          {view.suggestionsWaiting > view.suggestions.length ? (
            <p className="field-hint">
              {view.suggestionsWaiting - view.suggestions.length} more wait than are listed here.
            </p>
          ) : null}
          <p className="field-hint">
            {answers ? 'Take or skip each with a button, or answer' : 'Answer'} in a prompt, such as "take the
            input-safety ones, skip the rest".
          </p>
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
              <AppLine key={a.container} app={a} />
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
function AppLine({ app }: { app: BriefAppView }) {
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
            {app.release.time ? (
              <span className="brief-issue-title">
                {' '}
                <When at={app.release.time} />
              </span>
            ) : null}
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
