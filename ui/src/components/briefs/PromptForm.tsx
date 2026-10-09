// A Brief card's prompt field, and the error line the Brief forms share.

import { useContext, useEffect, useId, useState, type FormEvent } from 'react'
import type { BriefWriter } from '../../k8s/briefs'
import { RefreshSession } from '../../lib/hooks'
import { SignInLink } from '../Account'
import { WarningIcon } from '../icons'
import { writeError, type WriteError } from './errors'

/** A refused write's message; with a "Sign in" link when nobody signed in was the reason. */
export function FormError({ error }: { error: WriteError }) {
  // A 401 means the session the page knew of is gone: ask again, so the top bar agrees.
  const refresh = useContext(RefreshSession)
  useEffect(() => {
    if (error.signIn) refresh()
  }, [error, refresh])
  return (
    <p className="form-error" role="alert">
      <WarningIcon size={14} />
      <span>{error.message}</span>
      {error.signIn ? <SignInLink /> : null}
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
  const [error, setError] = useState<WriteError>()

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!prompt.trim()) return
    setSending(true)
    setError(undefined)
    try {
      await writer.addPrompt(namespace, name, prompt.trim())
      setPrompt('')
    } catch (err) {
      setError(writeError(err))
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
      {error ? <FormError error={error} /> : null}
    </form>
  )
}
