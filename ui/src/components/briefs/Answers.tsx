// One-click answers on a Brief's page: a button per answer to an item that
// waits for the person, each appending the prompt model/answers.ts words from
// the item. Once sent, the item says so until the next round takes the prompt.

import { useContext, useState } from 'react'
import type { BriefWriter } from '../../k8s/briefs'
import { canWrite } from '../../k8s/session'
import { SessionContext } from '../../lib/hooks'
import { answeredWith, type Answer, type AnswerKind } from '../../model/answers'
import type { BriefPrompt } from '../../model/brief'
import { SignInLink } from '../Account'
import { FormError } from './PromptForm'
import { writeError, type WriteError } from './errors'

/** What every answer on one Brief shares: where it writes and the prompts it has. */
export interface AnswerContext {
  namespace: string
  name: string
  writer: BriefWriter
  prompts: BriefPrompt[]
}

export function AnswerButtons({
  context,
  label,
  answers,
  recommended,
}: {
  context: AnswerContext
  /** The item, as its buttons name it: "storefront #3". */
  label: string
  answers: Answer[]
  /** The answer the Brief would give, shown first among equals. */
  recommended?: AnswerKind
}) {
  const session = useContext(SessionContext)
  const [sending, setSending] = useState<Answer>()
  // The answer sent, and how many prompts the Brief had then: it shows as
  // answered until the watch brings the prompt, then the prompts say.
  const [sent, setSent] = useState<{ answer: Answer; prompts: number }>()
  const [error, setError] = useState<WriteError>()
  const answered =
    answeredWith(answers, context.prompts) ?? (sent && context.prompts.length <= sent.prompts ? sent.answer : undefined)
  const can = canWrite(session, 'prompt')

  if (answered) {
    return (
      <div className="brief-answers" role="status">
        <span className="pill pill-waiting" title={answered.prompt}>
          Answered: {answered.label}, for the next round
        </span>
      </div>
    )
  }
  if (can === 'signIn') {
    return (
      <div className="brief-answers">
        <SignInLink /> <span className="field-hint">to answer here.</span>
      </div>
    )
  }

  async function answer(a: Answer) {
    setSending(a)
    setError(undefined)
    try {
      const prompts = context.prompts.length
      await context.writer.addPrompt(context.namespace, context.name, a.prompt)
      setSent({ answer: a, prompts })
    } catch (err) {
      setError(writeError(err))
    } finally {
      setSending(undefined)
    }
  }

  return (
    <div className="brief-answers">
      {answers.map((a) => (
        <button
          key={a.kind}
          type="button"
          className={`button button-small${a.kind === recommended ? '' : ' button-secondary'}`}
          title={`${a.explanation}: adds the prompt "${a.prompt}"`}
          aria-label={`${a.label} ${label}${a.kind === recommended ? ', as it would' : ''}`}
          disabled={sending !== undefined}
          onClick={() => answer(a)}
        >
          {sending === a ? 'Answering…' : a.label}
        </button>
      ))}
      {error ? <FormError error={error} /> : null}
    </div>
  )
}
