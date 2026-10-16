// A Container's operations (spec.operations): backups, recovery and alarms.
// What the page shows is what the Container asks for: the spec with the
// operator's defaults filled in (model.ResolveOperations), and OperationsApplied
// when False (the block was changed after the scaffold). Pure, and read only from the Container.

import type { Condition, Container } from '../k8s/types'
import { findCondition } from '../k8s/types'

/** The values an unset field takes, as the operator applies them. */
export const OPERATIONS_DEFAULTS = { retentionDays: 35, copyToRegion: '', rpo: '1h', rto: '4h', email: '' }

export interface OperationRow {
  key: 'retention' | 'copy' | 'rpo' | 'rto' | 'alerts'
  label: string
  /** The words for the value. */
  value: string
  /** True when the value is the default, because the Container leaves it out. */
  isDefault: boolean
}

export interface OperationsView {
  rows: OperationRow[]
  /** OperationsApplied when False: the block changed after the scaffold. */
  notApplied?: Condition
}

/** The words for a retention in days. */
export function describeRetention(days: number): string {
  if (days === 0) return 'point-in-time recovery only'
  return days === 1 ? '1 day' : `${days} days`
}

/** What the Container page shows of its operations. */
export function operationsOf(container: Container): OperationsView {
  // A field the spec leaves out takes its default.
  const source = container.spec.operations
  const d = OPERATIONS_DEFAULTS
  const days = source?.backups?.retentionDays
  const region = source?.backups?.copyToRegion
  const rpo = source?.recovery?.rpo
  const rto = source?.recovery?.rto
  const email = source?.alerts?.email
  const isDefault = (set: boolean) => !set
  const rows: OperationRow[] = [
    {
      key: 'retention',
      label: 'Backup retention',
      value: describeRetention(days ?? d.retentionDays),
      isDefault: isDefault(days !== undefined),
    },
    {
      key: 'copy',
      label: 'Backup copy region',
      value: (region ?? d.copyToRegion) || 'none',
      isDefault: isDefault(Boolean(region)),
    },
    { key: 'rpo', label: 'RPO', value: rpo || d.rpo, isDefault: isDefault(Boolean(rpo)) },
    { key: 'rto', label: 'RTO', value: rto || d.rto, isDefault: isDefault(Boolean(rto)) },
    {
      key: 'alerts',
      label: 'Alert email',
      value: (email ?? d.email) || 'nobody',
      isDefault: isDefault(Boolean(email)),
    },
  ]
  const applied = findCondition(container.status?.conditions, 'OperationsApplied')
  return {
    rows,
    notApplied: applied?.status === 'False' ? applied : undefined,
  }
}
