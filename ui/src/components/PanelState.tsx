// A panel's own loading, empty and error states (docs/plans/ui-lifecycle.md,
// step 10): a read that failed is said in the panel that shows its kind, never
// only in one banner for the page, and what was read before it failed still
// shows under it.

import type { ReactNode } from 'react'
import type { Problem } from '../k8s/sources'
import { ReadProblems } from './Status'

export interface PanelStateProps {
  /** Every list the panel shows has loaded at least once. */
  loaded: boolean
  /** The failed reads of the kinds the panel shows. */
  problems?: Problem[]
  /** Loaded, with nothing to show. */
  empty?: boolean
  /** While loading, such as "Loading the Crews…". */
  loading: ReactNode
  /** Once loaded with nothing to show, such as "No Crews yet"; without it, the contents say so themselves. */
  emptyText?: ReactNode
  /** A smaller placeholder, for a panel beside others. */
  small?: boolean
  children?: ReactNode
}

export function PanelState({ loaded, problems = [], empty, loading, emptyText, small, children }: PanelStateProps) {
  const placeholder = small ? 'placeholder small' : 'placeholder'
  const failed = problems.length > 0
  // A failed read never loads, and an empty list read with a failure may not be empty.
  if (failed && (!loaded || empty)) return <ReadProblems problems={problems} />
  if (!loaded)
    return (
      <div className={placeholder} role="status">
        {loading}
      </div>
    )
  if (empty && emptyText) return <div className={placeholder}>{emptyText}</div>
  return (
    <>
      <ReadProblems problems={problems} />
      {children}
    </>
  )
}
