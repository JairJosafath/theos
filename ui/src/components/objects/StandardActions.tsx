// The Standard page's action, with UI actions on: bump its pinned ref, behind
// a dialog that names how many Containers will get an update pull request,
// counted from the Containers the page already reads (plan ui-actions,
// decision 11). In OIDC mode with nobody signed in it says to sign in
// instead. The ref goes as typed: that it is a tag or commit is the
// operator's rule (UnpinnedRef, on each Container), and a refused write shows the API server's
// message (design.md, UI actions).

import { useContext, useId, useRef, useState, type FormEvent } from 'react'
import type { StandardWriter } from '../../k8s/standards'
import type { Container, Standard } from '../../k8s/types'
import { choicesOf } from '../../model/choices'
import { SessionContext } from '../../lib/hooks'
import { SignInLink } from '../Account'
import { writeError, type WriteError } from '../briefs/errors'
import { FormError } from '../briefs/PromptForm'

/**
 * The Containers on a Standard, split by who gets an update pull request: the
 * Ready ones do (design.md, A Standard's fixes reach its Containers), the
 * rest don't.
 */
export function standardUpdates(name: string, containers: Container[]): { ready: string[]; notReady: string[] } {
  const on = containers.filter((c) => choicesOf(c).standard === name)
  return {
    ready: on.filter((c) => c.status?.phase === 'Ready').map((c) => c.metadata.name),
    notReady: on.filter((c) => c.status?.phase !== 'Ready').map((c) => c.metadata.name),
  }
}

const containersWord = (n: number) => (n === 1 ? '1 Container' : `${n} Containers`)

export function StandardActions({
  standard,
  containers,
  writer,
}: {
  standard: Standard
  containers: Container[]
  writer: StandardWriter
}) {
  const session = useContext(SessionContext)
  if (!session?.actions) return null
  return (
    <section className="panel container-actions" aria-labelledby="standard-actions">
      <h2 className="panel-title small-title" id="standard-actions">
        Act on it
      </h2>
      {session.mode === 'oidc' && !session.user ? (
        <p className="panel-text">
          Sign in to bump its ref. <SignInLink />
        </p>
      ) : (
        <BumpRef standard={standard} containers={containers} writer={writer} />
      )}
    </section>
  )
}

/** A ref box and its confirmation dialog, which counts the update pull requests. */
function BumpRef({
  standard,
  containers,
  writer,
}: {
  standard: Standard
  containers: Container[]
  writer: StandardWriter
}) {
  const id = useId()
  const dialog = useRef<HTMLDialogElement>(null)
  const current = standard.spec.template.ref
  const [ref, setRef] = useState(current)
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState<string>()
  const [error, setError] = useState<WriteError>()
  const ns = standard.metadata.namespace ?? ''
  const name = standard.metadata.name
  const { ready, notReady } = standardUpdates(name, containers)
  // The ref as it will be sent: trimmed, and otherwise as typed.
  const next = ref.trim()

  function open(e: FormEvent) {
    e.preventDefault()
    setError(undefined)
    setSent(undefined)
    dialog.current?.showModal()
  }

  async function bump() {
    setSending(true)
    try {
      await writer.bumpRef(ns, name, next)
      setSent(next)
    } catch (err) {
      setError(writeError(err))
    } finally {
      setSending(false)
      dialog.current?.close()
    }
  }

  return (
    <form className="container-action" onSubmit={open}>
      <h3 className="object-section">
        <label htmlFor={`${id}-ref`}>Bump its ref</label>
      </h3>
      <p className="panel-text">
        Pinned at <span className="mono">{current}</span>. A new tag or commit of{' '}
        <span className="mono">{standard.spec.template.url}</span> reaches each Ready Container on it as a pull request
        the factory opens and merges once CI passes.
      </p>
      <input
        id={`${id}-ref`}
        className="input mono"
        value={ref}
        onChange={(e) => setRef(e.target.value)}
        disabled={sending}
        spellCheck={false}
      />
      <button type="submit" className="button button-secondary" disabled={sending}>
        Bump the ref…
      </button>
      {sent !== undefined ? (
        <p className="form-done" role="status">
          Pinned {name} at <span className="mono">{sent}</span>; each Container’s Standard row follows its update.
        </p>
      ) : null}
      {error ? <FormError error={error} /> : null}
      <dialog ref={dialog} className="dialog" aria-labelledby={`bump-${name}`}>
        <h2 className="panel-title small-title" id={`bump-${name}`}>
          Bump {name} to {next || 'an empty ref'}?
        </h2>
        <p className="panel-text">
          {ready.length ? (
            <>
              <strong>{containersWord(ready.length)}</strong> on {name} will get an update pull request:{' '}
              <span className="mono">{ready.join(', ')}</span>.
            </>
          ) : (
            <>No Container on {name} is Ready, so none gets an update pull request.</>
          )}
        </p>
        {notReady.length ? (
          <p className="panel-text">
            Not Ready, so no pull request: <span className="mono">{notReady.join(', ')}</span>.
          </p>
        ) : null}
        <p className="panel-text muted">
          The factory takes only a tag or a commit: a branch stops each update with UnpinnedRef, shown on its Container.
        </p>
        <div className="form-row dialog-buttons">
          <button type="button" className="button button-secondary" onClick={() => dialog.current?.close()}>
            Cancel
          </button>
          <button type="button" className="button" onClick={bump} disabled={sending}>
            {sending ? 'Bumping…' : 'Bump the ref'}
          </button>
        </div>
      </dialog>
    </form>
  )
}
