// The Container page's actions, with UI actions on: retry a Failed scaffold,
// behind a dialog that says to empty the repository first, and start a Task
// from a goal with the Container's Crew. In OIDC mode with nobody signed in it
// says to sign in instead. Each write shows the API server's message when it
// refuses one; nothing here judges a value (design.md, UI actions).

import { useContext, useId, useRef, useState, type FormEvent } from 'react'
import type { ContainerWriter } from '../../k8s/containers'
import type { Container, Crew } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { SignInLink } from '../Account'
import { writeError, type WriteError } from '../briefs/errors'
import { FormError } from '../briefs/PromptForm'
import { ExternalLinkIcon } from '../icons'
import { pageHref } from './links'

export function ContainerActions({
  container,
  crew,
  writer,
}: {
  container: Container
  crew: Crew | undefined
  writer: ContainerWriter
}) {
  const session = useContext(SessionContext)
  if (!session?.actions) return null
  const failed = container.status?.phase === 'Failed'
  return (
    <section className="panel container-actions" aria-labelledby="container-actions">
      <h2 className="panel-title small-title" id="container-actions">
        Act on it
      </h2>
      {session.mode === 'oidc' && !session.user ? (
        <p className="panel-text">
          Sign in to {failed ? 'retry its scaffold or ' : ''}start a Task on it. <SignInLink />
        </p>
      ) : (
        <>
          {failed ? <RetryScaffold container={container} writer={writer} /> : null}
          <TaskFromGoal container={container} crew={crew} writer={writer} />
        </>
      )}
    </section>
  )
}

/** The retry button, and its dialog: the repository must be empty first. */
function RetryScaffold({ container, writer }: { container: Container; writer: ContainerWriter }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState<WriteError>()
  const ns = container.metadata.namespace ?? ''
  const name = container.metadata.name
  const repo = container.status?.repositoryURL

  function open() {
    setError(undefined)
    setSent(false)
    dialog.current?.showModal()
  }

  async function retry() {
    setSending(true)
    try {
      await writer.retry(ns, name)
      setSent(true)
    } catch (err) {
      setError(writeError(err))
    } finally {
      setSending(false)
      dialog.current?.close()
    }
  }

  return (
    <div className="container-action">
      <h3 className="object-section">Retry the scaffold</h3>
      <p className="panel-text">
        Once its cause is fixed, the factory scaffolds it again. It scaffolds only an empty repository.
      </p>
      <button type="button" className="button button-secondary" onClick={open} disabled={sending}>
        Retry the scaffold…
      </button>
      {sent ? (
        <p className="form-done" role="status">
          Asked the factory to retry; the phase above follows it.
        </p>
      ) : null}
      {error ? <FormError error={error} /> : null}
      <dialog ref={dialog} className="dialog" aria-labelledby={`retry-${name}`}>
        <h2 className="panel-title small-title" id={`retry-${name}`}>
          Retry the scaffold of {name}?
        </h2>
        <p className="panel-text">
          Empty the repository first: delete every branch and commit in it, or delete and recreate it empty. If it still
          has commits the factory didn’t make, the retry fails again.
        </p>
        {repo ? (
          <p className="panel-text">
            <a href={repo} target="_blank" rel="noreferrer">
              Open the repository <ExternalLinkIcon size={12} />
            </a>
          </p>
        ) : null}
        <div className="form-row dialog-buttons">
          <button type="button" className="button button-secondary" onClick={() => dialog.current?.close()}>
            Cancel
          </button>
          <button type="button" className="button" onClick={retry} disabled={sending}>
            {sending ? 'Retrying…' : 'It is empty: retry'}
          </button>
        </div>
      </dialog>
    </div>
  )
}

/** A goal box that starts a Task with the Crew's implementer and reviewer, then links to it. */
function TaskFromGoal({
  container,
  crew,
  writer,
}: {
  container: Container
  crew: Crew | undefined
  writer: ContainerWriter
}) {
  const id = useId()
  const [goal, setGoal] = useState('')
  const [sending, setSending] = useState(false)
  const [started, setStarted] = useState<string>()
  const [error, setError] = useState<WriteError>()
  const ns = container.metadata.namespace ?? ''
  const name = container.metadata.name

  if (!crew) {
    return (
      <div className="container-action">
        <h3 className="object-section">Start a Task</h3>
        <p className="panel-text">
          A Task takes its implementer and reviewer from the Container’s Crew, and this page sees no Crew for {name}{' '}
          (none yet, or one it can’t read).
        </p>
      </div>
    )
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setSending(true)
    setError(undefined)
    setStarted(undefined)
    try {
      // The goal goes as typed, trimmed: the CRD, not the UI, refuses an empty one.
      setStarted(await writer.startTask(ns, name, crew!, goal.trim()))
      setGoal('')
    } catch (err) {
      setError(writeError(err))
    } finally {
      setSending(false)
    }
  }

  return (
    <form className="container-action" onSubmit={submit}>
      <h3 className="object-section">
        <label htmlFor={`${id}-goal`}>Start a Task</label>
      </h3>
      <p className="panel-text">
        <span className="mono">{crew.spec.implementer.name}</span> implements it and{' '}
        <span className="mono">{crew.spec.reviewer.name}</span> reviews it, as in Crew{' '}
        <span className="mono">{crew.metadata.name}</span>.
      </p>
      <textarea
        id={`${id}-goal`}
        className="input"
        value={goal}
        placeholder="What the change must achieve, in plain words…"
        onChange={(e) => setGoal(e.target.value)}
        disabled={sending}
      />
      <button type="submit" className="button" disabled={sending}>
        {sending ? 'Starting…' : 'Start the Task'}
      </button>
      {started ? (
        <p className="form-done" role="status">
          Started Task <a href={pageHref(ns, 'Task', started)}>{started}</a>.
        </p>
      ) : null}
      {error ? <FormError error={error} /> : null}
    </form>
  )
}
