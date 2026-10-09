import { versionSkew, type Versions } from '../model/versions'
import { WarningIcon } from './icons'

const UNKNOWN = 'unknown'

/**
 * The footer's version line: the UI's, the operator's, the chart's and the CRD
 * API's versions, and a warning when the UI and the operator differ.
 */
export function VersionLine({ versions }: { versions: Versions }) {
  const skew = versionSkew(versions)
  return (
    <div className="versions">
      <p className="versions-line">
        UI {versions.ui} · operator {versions.operator ?? UNKNOWN} · chart {versions.chart ?? UNKNOWN} · API{' '}
        {versions.api}
      </p>
      {skew && (
        <p className="versions-skew" role="alert">
          <WarningIcon />
          {skew}
        </p>
      )}
    </div>
  )
}
