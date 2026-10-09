// A write the API refused, as the Brief forms show it.

import { needsSignIn } from '../../k8s/session'

/** A refused write: its message, and whether signing in would let it through. */
export interface WriteError {
  message: string
  signIn: boolean
}

export function writeError(err: unknown): WriteError {
  return { message: err instanceof Error ? err.message : String(err), signIn: needsSignIn(err) }
}
