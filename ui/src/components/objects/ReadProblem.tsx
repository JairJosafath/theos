// A read the API server refused or failed, said in the panel that would show
// the kind, so an optional kind's failure is never hidden.

import type { Problem } from '../../k8s/sources'

export function ReadProblem({ problem }: { problem: Problem }) {
  return (
    <p className="read-problem" role="status">
      Could not read {problem.what}
      {problem.code ? ` (${problem.code})` : ''}: {problem.message}
    </p>
  )
}
