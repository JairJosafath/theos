// Accept and Decline on a request waiting in Ask: each sends the Brief's
// releases list with the issue's number added to the entry's accepted or
// declined requests, behind a test of the resourceVersion, as the other
// releases settings do. It is a setting, so it needs actions on; the CRD
// judges it, and a refusal shows the API server's message.

import { useContext, useState } from 'react'
import type { BriefWriter } from '../../k8s/briefs'
import { canWrite } from '../../k8s/session'
import type { BriefRelease } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { answerRequest, requestAnswered, type RequestAnswer } from '../../model/requests'
import { SignInLink } from '../Account'
import { FormError } from './PromptForm'
import { writeError, type WriteError } from './errors'

const ANSWERS: { answer: RequestAnswer; label: string; explanation: string }[] = [
  { answer: 'accept', label: 'Accept', explanation: 'Let it through to the planner' },
  {
    answer: 'decline',
    label: 'Decline',
    explanation: 'Turn it down: the Brief says on the issue that it is not taken, and closes it',
  },
]

export function RequestButtons({
  namespace,
  name,
  writer,
  releases,
  resourceVersion,
  request,
  label,
  settled,
}: {
  namespace: string
  name: string
  writer: BriefWriter
  releases: BriefRelease[]
  resourceVersion?: string
  request: { repository: string; number: number }
  /** The request, as its buttons name it: "gray-lenovo/cel-lens #12". */
  label: string
  /** The repository's answered numbers whose issues are closed, which an answer drops. */
  settled?: number[]
}) {
  const session = useContext(SessionContext)
  const [sending, setSending] = useState<RequestAnswer>()
  const [error, setError] = useState<WriteError>()
  // The resourceVersion an answer was sent from: until the watch brings the
  // Brief it made, a second click would send a stale list.
  const [sentFrom, setSentFrom] = useState<string>()
  const waitingForWatch = sentFrom !== undefined && sentFrom === resourceVersion
  const answered = requestAnswered(releases, request.repository, request.number)
  const can = canWrite(session, 'setting')

  if (answered) {
    return (
      <div className="brief-answers" role="status">
        <span className="pill pill-waiting">
          {answered === 'accepted' ? 'Accepted; it waits for a round' : 'Declined; the Brief closes the issue'}
        </span>
      </div>
    )
  }
  if (can === 'signIn') {
    return (
      <div className="brief-answers">
        <SignInLink /> <span className="field-hint">to accept or decline it.</span>
      </div>
    )
  }
  if (can === 'off') {
    return (
      <div className="brief-answers">
        <span className="field-hint">
          The UI answers requests only with actions on; until then, add {request.number} to the entry’s{' '}
          <span className="mono">requests.accepted</span> or <span className="mono">requests.declined</span> with
          kubectl.
        </span>
      </div>
    )
  }

  const next = (answer: RequestAnswer) => answerRequest(releases, request.repository, request.number, answer, settled)

  async function send(answer: RequestAnswer) {
    const list = next(answer)
    if (!list) return
    setSending(answer)
    setError(undefined)
    try {
      await writer.setSettings(namespace, name, { releases: list, resourceVersion })
      setSentFrom(resourceVersion)
    } catch (err) {
      setError(writeError(err))
    } finally {
      setSending(undefined)
    }
  }

  return (
    <div className="brief-answers">
      {ANSWERS.map((a) => (
        <button
          key={a.answer}
          type="button"
          className={`button button-small${a.answer === 'accept' ? '' : ' button-secondary'}`}
          title={a.explanation}
          aria-label={`${a.label} ${label}`}
          // Off while one is sent and until the watch brings it, or when no entry takes requests from the repository.
          disabled={sending !== undefined || waitingForWatch || !next(a.answer)}
          onClick={() => send(a.answer)}
        >
          {sending === a.answer ? `${a.label === 'Accept' ? 'Accepting' : 'Declining'}…` : a.label}
        </button>
      ))}
      {error ? <FormError error={error} /> : null}
    </div>
  )
}
