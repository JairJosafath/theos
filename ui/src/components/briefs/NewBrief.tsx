// The "Describe an app" box: the form that creates a Brief.

import { useId, useState, type FormEvent } from 'react'
import { DEFAULT_PARALLEL, isBriefName, MAX_IDEA, MAX_PARALLEL, nameFromIdea, type BriefWriter } from '../../k8s/briefs'
import type { BriefAutopilot } from '../../k8s/types'
import { AUTOPILOT_MODES, DEFAULT_AUTOPILOT } from '../../model/brief'
import { errorText } from './errors'
import { FormError } from './PromptForm'

/** The "Describe an app" box: an idea and a name for it, prefilled from the idea. */
export function NewBrief({ namespace, taken, writer }: { namespace: string; taken: string[]; writer: BriefWriter }) {
  const id = useId()
  const [idea, setIdea] = useState('')
  // Until the person types a name, it follows the idea.
  const [typedName, setTypedName] = useState<string>()
  // Empty leaves spec.autopilot unset: the factory's default.
  const [autopilot, setAutopilot] = useState<BriefAutopilot | ''>('')
  // 0 leaves spec.parallel unset: the factory's default.
  const [parallel, setParallel] = useState(0)
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
      await writer.create(namespace, name, idea.trim(), {
        autopilot: autopilot || undefined,
        parallel: parallel || undefined,
      })
      setCreated(name)
      setIdea('')
      setTypedName(undefined)
      setAutopilot('')
      setParallel(0)
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
      <label className="field" htmlFor={`${id}-autopilot`}>
        <span className="field-label">Autopilot</span>
        <span className="field-hint">
          {(autopilot ? AUTOPILOT_MODES[autopilot] : AUTOPILOT_MODES[DEFAULT_AUTOPILOT]).explanation}
        </span>
      </label>
      <select
        id={`${id}-autopilot`}
        className="input"
        value={autopilot}
        onChange={(e) => setAutopilot(e.target.value as BriefAutopilot | '')}
        disabled={sending}
      >
        <option value="">The factory’s default ({AUTOPILOT_MODES[DEFAULT_AUTOPILOT].label.toLowerCase()})</option>
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
      <select
        id={`${id}-parallel`}
        className="input"
        value={parallel}
        onChange={(e) => setParallel(Number(e.target.value))}
        disabled={sending}
      >
        <option value={0}>The factory’s default ({DEFAULT_PARALLEL} at a time)</option>
        {Array.from({ length: MAX_PARALLEL }, (_, i) => i + 1).map((n) => (
          <option key={n} value={n}>
            {n} at a time
          </option>
        ))}
      </select>
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
