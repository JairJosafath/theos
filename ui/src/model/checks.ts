// When a Container's CI runs its full check (design.md, "The owner chooses
// when the full check runs"; docs/plans/checks-before-release.md): what the
// owner asked in spec.checks, what the repository runs (status.standard.checks)
// and, when the Standard can't, the ChecksApplied condition saying so. Pure,
// and read only from the Container.

import type { ChecksMode, Condition, Container } from '../k8s/types'
import { findCondition } from '../k8s/types'

/** The two modes in the order the page offers them, each with its words. */
export const CHECKS_MODES: { mode: ChecksMode; label: string; explanation: string }[] = [
  {
    mode: 'EveryChange',
    label: 'On every change',
    explanation: 'CI runs the full check (make check) on every pull request and push to main.',
  },
  {
    mode: 'BeforeRelease',
    label: 'Before a release',
    explanation:
      'Each change runs a light check (make check-light); the full check runs on the commit a release will be tagged at, before the tag.',
  },
]

export interface ChecksView {
  /** What the owner asked; unset reads as EveryChange, the CRD's default. */
  asked: ChecksMode
  /** What the repository's CI runs, once the Container is scaffolded with its Standard. */
  runs?: ChecksMode
  /** ChecksApplied when False: the Standard runs the full check on every change whatever is asked. */
  notApplied?: Condition
  /** True when the repository runs other checks than the setting gives: the Standard update's pull request brings them. */
  pending: boolean
}

/** The words for a mode. */
export function describeChecks(mode: ChecksMode): { label: string; explanation: string } {
  return CHECKS_MODES.find((m) => m.mode === mode) ?? CHECKS_MODES[0]
}

/** What the Container page shows of its checks. */
export function checksOf(container: Container): ChecksView {
  const asked = container.spec.checks ?? 'EveryChange'
  const standard = container.status?.standard
  const runs = standard ? (standard.checks ?? 'EveryChange') : undefined
  const applied = findCondition(container.status?.conditions, 'ChecksApplied')
  const notApplied = applied?.status === 'False' ? applied : undefined
  // A Standard that can't run them keeps the full check on every change.
  const effective = notApplied ? 'EveryChange' : asked
  return { asked, runs, notApplied, pending: runs !== undefined && runs !== effective }
}
