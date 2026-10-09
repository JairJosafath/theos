// What the page says about its connection to the cluster: the status pill,
// and a banner for what could not be read.

import type { Connection, Problem } from '../k8s/sources'
import { WarningIcon } from './icons'

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

/** Explains why something couldn't be read, with a hint at the usual fix. */
export function ProblemsBanner({ problems }: { problems: Problem[] }) {
  if (!problems.length) return null
  const hint = (p: Problem) => {
    if (p.code === 403)
      return 'The UI is not allowed to read these. Its ClusterRole (config/ui) needs get, list and watch on them.'
    if (p.code === 404) return 'This kind is not installed in the cluster. Are the C4 Factory CRDs installed?'
    if (p.code === 502 || p.code === 503 || p.code === 504) return 'The UI server cannot reach the Kubernetes API.'
    return 'Retrying automatically.'
  }
  // One line per distinct message, so nine failing watches read as one problem.
  const distinct = [...new Map(problems.map((p) => [`${p.code}|${p.message}`, p])).values()]
  return (
    <div className="banner banner-problem" role="alert">
      <WarningIcon size={16} />
      <div>
        {distinct.map((p) => (
          <p key={`${p.code}|${p.message}`}>
            <strong>
              Could not read{' '}
              {problems
                .filter((q) => q.message === p.message)
                .map((q) => q.what)
                .join(', ')}
              .
            </strong>{' '}
            {hint(p)}{' '}
            <span className="muted">
              ({p.code ? `${p.code}: ` : ''}
              {p.message})
            </span>
          </p>
        ))}
      </div>
    </div>
  )
}
