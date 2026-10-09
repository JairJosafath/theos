// The Work area's releases: each Crew's last one, newest first, with how its
// CI runs and publishing went.

import { timeAgo } from '../../model/describe'
import { crewReleases, RELEASE_RUN } from '../../model/releases'
import type { Crew } from '../../k8s/types'
import { ExternalLinkIcon } from '../icons'
import type { ObjectHref } from '../Factory'

export function ReleasesPanel({
  crews,
  loaded,
  now,
  pageHref,
}: {
  crews: Crew[]
  loaded: boolean
  now: number
  pageHref?: ObjectHref
}) {
  const rows = crewReleases(crews)
  return (
    <section className="panel" aria-labelledby="releases-title">
      <h2 className="panel-title" id="releases-title">
        Releases
      </h2>
      <p className="panel-caption">
        The release each Crew tagged last, newest first. A Crew tags one once its milestone is done, if it is set to.
      </p>
      {rows.length ? (
        <ul className="crews">
          {rows.map(({ key, namespace, crew, container, release }) => {
            const href = pageHref?.(namespace, 'Crew', crew)
            const failed = release.run === 'Failed' || release.publish?.state === 'Failed'
            return (
              <li key={key} className={`crew-card${failed ? ' tone-error' : ''}`}>
                <div className="crew-head">
                  <span className="crew-title mono">{release.tag}</span>
                </div>
                <div className="crew-sub">
                  {namespace} · Crew{' '}
                  {href ? (
                    <a href={href} aria-label={`Crew ${crew}`}>
                      {crew}
                    </a>
                  ) : (
                    <span className="mono">{crew}</span>
                  )}
                  {container !== crew ? <span className="mono"> ({container})</span> : null}
                </div>
                <dl className="facts crew-facts">
                  <dt>Tagged</dt>
                  <dd>
                    {timeAgo(release.time, now)} <span className="muted mono">at {release.commit.slice(0, 7)}</span>
                  </dd>
                  <dt>CI runs</dt>
                  <dd>
                    {release.run ? RELEASE_RUN[release.run] : 'not reported yet'}
                    {release.runURL ? (
                      <>
                        {' '}
                        <a href={release.runURL} target="_blank" rel="noreferrer">
                          Open the run <ExternalLinkIcon size={11} />
                        </a>
                      </>
                    ) : null}
                  </dd>
                  {release.publish ? (
                    <>
                      <dt>Published</dt>
                      <dd>
                        {release.publish.url ? (
                          <a href={release.publish.url} target="_blank" rel="noreferrer">
                            {release.publish.state} <ExternalLinkIcon size={11} />
                          </a>
                        ) : (
                          release.publish.state
                        )}
                      </dd>
                    </>
                  ) : null}
                </dl>
              </li>
            )
          })}
        </ul>
      ) : (
        <div className="placeholder small">
          {loaded ? 'No release yet: a Crew tags one once its milestone is done.' : 'Loading the releases…'}
        </div>
      )}
    </section>
  )
}
