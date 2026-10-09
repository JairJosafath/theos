// The "Describe an app" box: the form that creates a Brief.

import { useId, useState, type FormEvent } from 'react'
import { nameFromIdea, type BriefWriter } from '../../k8s/briefs'
import type { BriefAutopilot } from '../../k8s/types'
import { AUTOPILOT_MODES, DEFAULT_AUTOPILOT_HINT } from '../../model/brief'
import { errorText } from './errors'
import { FormError } from './PromptForm'

/**
 * The "Describe an app" box: an idea and a name for it, prefilled from the idea.
 * It checks none of the CRD's rules; a create the API server refuses shows its message.
 */
export function NewBrief({ namespace, taken, writer }: { namespace: string; taken: string[]; writer: BriefWriter }) {
  const id = useId()
  const [idea, setIdea] = useState('')
  // Until the person types a name, it follows the idea.
  const [typedName, setTypedName] = useState<string>()
  // Empty leaves spec.autopilot unset: the factory's default.
  const [autopilot, setAutopilot] = useState<BriefAutopilot | ''>('')
  // Empty leaves spec.parallel unset: the factory's default.
  const [parallel, setParallel] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string>()
  const [created, setCreated] = useState<string>()
  const name = typedName ?? (idea.trim() ? nameFromIdea(idea, taken) : '')

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!idea.trim() || !name) return
    setSending(true)
    setError(undefined)
    try {
      await writer.create(namespace, name, idea.trim(), {
        autopilot: autopilot || undefined,
        parallel: parallel.trim() ? Number(parallel) : undefined,
      })
      setCreated(name)
      setIdea('')
      setTypedName(undefined)
      setAutopilot('')
      setParallel('')
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
        placeholder="from the idea"
        spellCheck={false}
        autoComplete="off"
        onChange={(e) => setTypedName(e.target.value)}
        disabled={sending}
      />
      <label className="field" htmlFor={`${id}-autopilot`}>
        <span className="field-label">Autopilot</span>
        <span className="field-hint">
          {autopilot ? AUTOPILOT_MODES[autopilot].explanation : DEFAULT_AUTOPILOT_HINT}
        </span>
      </label>
      <select
        id={`${id}-autopilot`}
        className="input"
        value={autopilot}
        onChange={(e) => setAutopilot(e.target.value as BriefAutopilot | '')}
        disabled={sending}
      >
        <option value="">The factory’s default</option>
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
      <input
        id={`${id}-parallel`}
        className="input"
        type="number"
        inputMode="numeric"
        value={parallel}
        placeholder="the factory’s default"
        onChange={(e) => setParallel(e.target.value)}
        disabled={sending}
      />
      <div className="form-row">
        <button type="submit" className="button" disabled={sending || !idea.trim() || !name}>
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
