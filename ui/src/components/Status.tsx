// What the page says about its connection to the cluster: the status pill,
// and a banner for what could not be read.

import type { Connection, Problem } from '../k8s/sources'
import { WarningIcon } from './icons'
import './bits.css'

export function ConnectionStatus({ connection }: { connection: Connection }) {
  const labels: Record<Connection['phase'], [string, string]> = {
    demo: ['Demo data', 'Built-in example data, not a cluster'],
    live: ['Live', 'Watching the cluster: changes show up as they happen'],
    reconnecting: ['Reconnecting…', 'Lost the connection to the cluster; retrying'],
    connecting: ['Connecting…', 'Loading the story from the cluster'],
  }
  const [label, title] = labels[connection.phase]
  return (
    <span className={`status status-${connection.phase}`} title={title} role="status">
      <span className="status-dot" aria-hidden="true" />
      {label}
    </span>
  )
}

/** The usual fix for a read that failed, by its HTTP code. */
function problemHint(p: Problem): string {
  if (p.code === 403)
    return 'The UI is not allowed to read these. Its ClusterRole (config/ui) needs get, list and watch on them.'
  if (p.code === 404) return 'This kind is not installed in the cluster. Are the C4 Factory CRDs installed?'
  if (p.code === 502 || p.code === 503 || p.code === 504) return 'The UI server cannot reach the Kubernetes API.'
  return 'Retrying automatically.'
}

/** One line per distinct failure, so nine failing watches read as one problem. */
function distinctProblems(problems: Problem[]): { problem: Problem; what: string }[] {
  const distinct = [...new Map(problems.map((p) => [`${p.code}|${p.message}`, p])).values()]
  return distinct.map((problem) => ({
    problem,
    what: [...new Set(problems.filter((q) => q.message === problem.message).map((q) => q.what))].join(', '),
  }))
}

function ProblemLine({ problem, what }: { problem: Problem; what: string }) {
  return (
    <p>
      <strong>Could not read {what}.</strong> {problemHint(problem)}{' '}
      <span className="muted">
        ({problem.code ? `${problem.code}: ` : ''}
        {problem.message})
      </span>
    </p>
  )
}

/** Explains why something couldn't be read, with a hint at the usual fix. */
export function ProblemsBanner({ problems }: { problems: Problem[] }) {
  if (!problems.length) return null
  return (
    <div className="banner banner-problem" role="alert">
      <WarningIcon size={16} />
      <div>
        {distinctProblems(problems).map((d) => (
          <ProblemLine key={`${d.problem.code}|${d.problem.message}`} {...d} />
        ))}
      </div>
    </div>
  )
}

/**
 * What the API server warned about the page's reads, such as a read at a
 * deprecated version (#1618). The reads worked, so it is a status, not an alert.
 */
export function WarningsBanner({ warnings }: { warnings: string[] }) {
  const texts = [...new Set(warnings.filter(Boolean))]
  if (!texts.length) return null
  return (
    <div className="banner banner-warning" role="status">
      <WarningIcon size={16} />
      <div>
        {texts.map((text) => (
          <p key={text}>
            <strong>The API server warns:</strong> {text}
          </p>
        ))}
      </div>
    </div>
  )
}

/** The reads behind one panel that failed, said in the panel, with the usual fix. */
export function ReadProblems({ problems }: { problems: Problem[] }) {
  if (!problems.length) return null
  return (
    <div className="read-problem" role="alert">
      {distinctProblems(problems).map((d) => (
        <ProblemLine key={`${d.problem.code}|${d.problem.message}`} {...d} />
      ))}
    </div>
  )
}
