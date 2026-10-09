// A Brief card's prompt field, and the error line the Brief forms share.

import { useId, useState, type FormEvent } from 'react'
import type { BriefWriter } from '../../k8s/briefs'
import { WarningIcon } from '../icons'
import { errorText } from './errors'

export function FormError({ message }: { message: string }) {
  return (
    <p className="form-error" role="alert">
      <WarningIcon size={14} />
      <span>{message}</span>
    </p>
  )
}

/** A follow-up prompt for one Brief; the planner takes it in its next round. A refused prompt shows the API server's message. */
export function PromptForm({
  namespace,
  name,
  writer,
  needsInput,
}: {
  namespace: string
  name: string
  writer: BriefWriter
  needsInput: boolean
}) {
  const id = useId()
  const [prompt, setPrompt] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string>()

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!prompt.trim()) return
    setSending(true)
    setError(undefined)
    try {
      await writer.addPrompt(namespace, name, prompt.trim())
      setPrompt('')
    } catch (err) {
      setError(errorText(err))
    } finally {
      setSending(false)
    }
  }

  return (
    <form className="brief-prompt-form" onSubmit={submit}>
      <label className="visually-hidden" htmlFor={`${id}-prompt`}>
        {needsInput ? `Answer brief ${name}` : `Add a prompt to brief ${name}`}
      </label>
      <div className="form-row">
        <input
          id={`${id}-prompt`}
          className="input"
          value={prompt}
          placeholder={needsInput ? 'Answer in a prompt…' : 'Add a prompt to steer it…'}
          onChange={(e) => setPrompt(e.target.value)}
          disabled={sending}
        />
        <button type="submit" className="button button-secondary" disabled={sending || !prompt.trim()}>
          {sending ? 'Adding…' : 'Add'}
        </button>
      </div>
      {error ? <FormError message={error} /> : null}
    </form>
  )
}
