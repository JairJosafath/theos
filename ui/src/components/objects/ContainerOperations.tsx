// A Container's operations on its page: backup retention and copy region,
// RPO and RTO, and who the alarms tell, as the scaffold rendered them (or the
// spec with the defaults filled in), and OperationsApplied when False (the
// block changed after the scaffold). Read only.

import { Fragment } from 'react'
import type { Container } from '../../k8s/types'
import { operationsOf } from '../../model/operations'

export function ContainerOperations({ container }: { container: Container }) {
  const view = operationsOf(container)
  return (
    <section className="panel" aria-labelledby="container-operations">
      <h2 className="panel-title small-title" id="container-operations">
        Operations
      </h2>
      <dl className="facts">
        {view.rows.map((row) => (
          <Fragment key={row.key}>
            <dt>{row.label}</dt>
            <dd>
              {row.value}
              {row.isDefault ? <span className="muted"> (default)</span> : null}
            </dd>
          </Fragment>
        ))}
      </dl>
      {view.notApplied ? (
        <p className="panel-text checks-not-applied">
          Not applied ({view.notApplied.reason}): {view.notApplied.message}
        </p>
      ) : null}
    </section>
  )
}
