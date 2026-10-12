// A Container's checks on its page: when CI runs the full check, what the
// repository runs now, and ChecksApplied when False (its Standard keeps the
// full check on every change). With UI actions on, the owner picks the mode;
// the CRD's enum judges it and a refusal shows the API server's message
// (design.md, UI actions).

import { useContext, useId, useState, type FormEvent } from 'react'
import type { ContainerWriter } from '../../k8s/containers'
import type { ChecksMode, Container } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { CHECKS_MODES, checksOf, describeChecks } from '../../model/checks'
import { SignInLink } from '../Account'
import { writeError, type WriteError } from '../briefs/errors'
import { FormError } from '../briefs/PromptForm'

export function ContainerChecks({ container, writer }: { container: Container; writer: ContainerWriter }) {
  const session = useContext(SessionContext)
  const view = checksOf(container)
  const asked = describeChecks(view.asked)
  const canWrite = Boolean(session?.actions) && !(session?.mode === 'oidc' && !session.user)
  return (
    <section className="panel" aria-labelledby="container-checks">
      <h2 className="panel-title small-title" id="container-checks">
        When CI runs the full check
      </h2>
      <p className="panel-text">
        <strong>{asked.label}</strong> <span className="mono muted">({view.asked})</span>: {asked.explanation}
      </p>
      {view.notApplied ? (
        <p className="panel-text checks-not-applied">
          Not applied ({view.notApplied.reason}): {view.notApplied.message}
        </p>
      ) : null}
      {view.pending && view.runs ? (
        <p className="panel-text">
          The repository still runs the full check {describeChecks(view.runs).label.toLowerCase()}; the Standard
          update’s pull request brings the change.
        </p>
      ) : null}
      {view.asked === 'BeforeRelease' && !view.notApplied ? (
        <p className="panel-text muted">
          A person who tags a release by hand first pushes the branch <span className="mono">release-check/vX.Y.Z</span>{' '}
          at the commit and waits for the <span className="mono">full</span> job to pass.
        </p>
      ) : null}
      {canWrite ? (
        <ChecksForm container={container} writer={writer} />
      ) : session?.actions ? (
        <p className="panel-text">
          Sign in to change when the full check runs. <SignInLink />
        </p>
      ) : null}
    </section>
  )
}

/** A choice of mode and Save, which sends only a change. */
function ChecksForm({ container, writer }: { container: Container; writer: ContainerWriter }) {
  const id = useId()
  const current = container.spec.checks ?? 'EveryChange'
  // What the person picked; unset, the field follows the Container.
  const [picked, setPicked] = useState<ChecksMode>()
  const [sending, setSending] = useState(false)
  const [done, setDone] = useState<string>()
  const [error, setError] = useState<WriteError>()
  // Once the Container has what was picked, the field follows it again.
  const [seen, setSeen] = useState(current)
  if (seen !== current) {
    setSeen(current)
    if (picked === current) setPicked(undefined)
  }
  const shown = picked ?? current
  const changed = shown !== current

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!changed) return
    setSending(true)
    setError(undefined)
    setDone(undefined)
    try {
      await writer.setChecks(container.metadata.namespace ?? '', container.metadata.name, shown)
      setDone('Saved')
    } catch (err) {
      setError(writeError(err))
    } finally {
      setSending(false)
    }
  }

  return (
    <form className="crew-form" onSubmit={submit}>
      <label className="field" htmlFor={`${id}-checks`}>
        <span className="field-label">Run the full check</span>
        <span className="field-hint">A change reaches the repository through the Standard update’s pull request.</span>
      </label>
      <div className="form-row">
        <select
          id={`${id}-checks`}
          className="input"
          value={shown}
          onChange={(e) => setPicked(e.target.value as ChecksMode)}
          disabled={sending}
        >
          {CHECKS_MODES.map((m) => (
            <option key={m.mode} value={m.mode}>
              {m.label}
            </option>
          ))}
        </select>
        <button type="submit" className="button button-secondary" disabled={sending || !changed}>
          {sending ? 'Saving…' : 'Save'}
        </button>
        {done ? (
          <span className="form-done" role="status">
            {done}
          </span>
        ) : null}
      </div>
      {error ? <FormError error={error} /> : null}
    </form>
  )
}
