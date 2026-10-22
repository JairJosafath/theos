// A Container's operations (spec.operations): backups, recovery and alarms.
// What the page shows is what the operator resolved: status.operations, the
// block infra/operations.ts was last rendered with, every field filled in;
// before the scaffold records it, the spec as written, with nothing filled in.
// The defaults are the operator's (model.ResolveOperations), and this keeps
// no copy of them (#1210). OperationsApplied when False (a changed block whose
// pull request hasn't merged, or can't be made) comes along. Pure, and read
// only from the Container.

import type { Condition, Container, Operations } from '../k8s/types'
import { findCondition } from '../k8s/types'

export interface OperationRow {
  key: 'retention' | 'copy' | 'rpo' | 'rto' | 'alerts'
  label: string
  /** The words for the value; 'default' when the spec leaves it out and the operator hasn't resolved it yet. */
  value: string
  /** True when the Container leaves the field out, so its value is the operator's default. */
  isDefault: boolean
}

export interface OperationsView {
  /** Whether the rows are the operator's resolved block (status.operations) or the spec as written. */
  resolved: boolean
  rows: OperationRow[]
  /** OperationsApplied when False: a changed block not yet in the repository. */
  notApplied?: Condition
}

/** The words for a retention in days. */
export function describeRetention(days: number): string {
  if (days === 0) return 'point-in-time recovery only'
  return days === 1 ? '1 day' : `${days} days`
}

/** What the Container page shows of its operations. */
export function operationsOf(container: Container): OperationsView {
  const spec = container.spec.operations
  const status = container.status?.operations
  const applied = findCondition(container.status?.conditions, 'OperationsApplied')
  const notApplied = applied?.status === 'False' ? applied : undefined
  // A field the spec leaves out takes the operator's default; status.operations holds it once
  // resolved. While a changed block waits to be applied, status.operations is the old block, so
  // which of its values are defaults is not known.
  const leftOut = {
    retention: spec?.backups?.retentionDays === undefined,
    copy: !spec?.backups?.copyToRegion,
    rpo: !spec?.recovery?.rpo,
    rto: !spec?.recovery?.rto,
    alerts: !spec?.alerts?.email,
  }
  const source: Operations | undefined = status ?? spec
  const resolved = status !== undefined
  const row = (key: OperationRow['key'], label: string, words: string | undefined): OperationRow => ({
    key,
    label,
    value: words ?? 'default',
    isDefault: leftOut[key] && (!resolved || !notApplied),
  })
  const days = source?.backups?.retentionDays
  const region = source?.backups?.copyToRegion
  const email = source?.alerts?.email
  return {
    resolved,
    rows: [
      row('retention', 'Backup retention', days === undefined ? undefined : describeRetention(days)),
      row('copy', 'Backup copy region', resolved || region ? region || 'none' : undefined),
      row('rpo', 'RPO', source?.recovery?.rpo || undefined),
      row('rto', 'RTO', source?.recovery?.rto || undefined),
      row('alerts', 'Alert email', resolved || email ? email || 'nobody' : undefined),
    ],
    notApplied,
  }
}
