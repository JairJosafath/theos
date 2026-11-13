// Where Containers ship (docs/plans/done/deploy.md, decision 6): a DeployTarget's
// page (its member, tier, approval, Ready and the Containers that ship to it;
// of its credentials only the Secret's name: the UI never reads Secrets), the
// Project page's list of them, and the Container page's deploys, one per
// spec.deploy entry: the version running and wanted, phase, health and URL.
// A version waiting for approval shows the kubectl line that approves it, or
// with UI actions on an Approve button: one JSON patch of that entry's
// approved. The CRD and RBAC decide whether it lands; the page shows the API
// server's answer.

import { useContext, useRef, useState, type ReactNode } from 'react'
import type { ContainerWriter } from '../../k8s/containers'
import { unreadOf, type Connection, type StoryData } from '../../k8s/sources'
import { RESOURCES, type Container, type ContainerDeploy, type DeployTarget } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import {
  approveCommand,
  deploysOf,
  describeApproval,
  GITHUB_SECRET_KEYS,
  githubImage,
  githubReleasesURL,
  GITLAB_SECRET_KEYS,
  gitlabImage,
  gitlabReleasesURL,
  healthTone,
  memberOf,
  phaseTone,
  readinessOf,
  shippingTo,
  sourceOf,
  tierOf,
  type DeployRow,
} from '../../model/deploys'
import type { Tone } from '../../model/describe'
import { SignInLink } from '../Account'
import { When } from '../bits'
import { writeError, type WriteError } from '../briefs/errors'
import { FormError } from '../briefs/PromptForm'
import { ExternalLinkIcon } from '../icons'
import { CommandLine, ObjectHeader } from '../ObjectHeader'
import { pageHref, projectCrumb } from './links'
import { ReadProblem } from './ReadProblem'
import { compareText } from '../../model/order'

/** A small badge of a tone, such as a Ready condition's or a lease's state. */
export function Badge({ tone, label, title }: { tone: Tone; label: string; title?: string }) {
  return (
    <span className={`badge badge-${tone} badge-sm`} title={title}>
      <span className="badge-dot" aria-hidden="true" />
      {label}
    </span>
  )
}

/** Where the member ships to, linked where it is a page on the web. */
function Where({ target }: { target: DeployTarget }) {
  const { where } = memberOf(target)
  if (!where) return <span className="muted">not shown by this UI</span>
  const releases = githubReleasesURL(target) ?? gitlabReleasesURL(target)
  if (releases)
    return (
      <a className="mono" href={releases} target="_blank" rel="noreferrer">
        {where} <ExternalLinkIcon size={12} />
      </a>
    )
  return <span className="mono">{where}</span>
}

/** A github or gitlab target's release tags, the prefix as the operator fills it when unset. */
function TagsFact({ tagPrefix, children }: { tagPrefix: string | undefined; children?: ReactNode }) {
  return (
    <>
      <dt>Tags</dt>
      <dd>
        {tagPrefix === undefined ? (
          <>
            <span className="mono">&lt;container&gt;-v1.2.3</span>
            <span className="muted"> (tagPrefix not set: the Container’s name and -)</span>
          </>
        ) : (
          <span className="mono">{tagPrefix}v1.2.3</span>
        )}
        {children}
      </dd>
    </>
  )
}

/** Where a github or gitlab target puts each release's source, the parts the operator fills named; or that it publishes none. */
function SourceFact({ target }: { target: DeployTarget }) {
  const source = sourceOf(target)
  const path = (target.spec.github ?? target.spec.gitlab)?.source?.path
  return (
    <>
      <dt>Source</dt>
      <dd>
        {source ? (
          <>
            folder <span className="mono">{source.path}</span>, tagged{' '}
            <span className="mono">{source.tagPrefix}v1.2.3</span>
            {path === undefined ? <span className="muted"> (path not set: each Container’s name)</span> : null}
          </>
        ) : (
          <span className="muted">not published: the release only</span>
        )}
      </dd>
    </>
  )
}

/** A github target's tags, source and image, each unset part as the operator fills it. */
function GitHubFacts({ target }: { target: DeployTarget }) {
  const github = target.spec.github
  if (!github) return null
  return (
    <>
      <TagsFact tagPrefix={github.tagPrefix}>
        {github.latest === false ? <span className="muted"> · never marked Latest</span> : null}
      </TagsFact>
      <SourceFact target={target} />
      <dt>Image</dt>
      <dd>
        <span className="mono">{githubImage(target)}</span>
        <span className="muted small">
          {' '}
          · copied, for a Container whose release builds an image, when the Secret holds {GITHUB_SECRET_KEYS.packages}
        </span>
      </dd>
    </>
  )
}

/**
 * A gitlab target's tags, source and image, each unset part as the operator
 * fills it (docs/plans/gitlab-deploy.md, decisions 1, 4 and 5). The image's
 * registry is the project's own, which the operator reads from the project:
 * named for gitlab.com, a placeholder for a self-hosted instance.
 */
function GitLabFacts({ target }: { target: DeployTarget }) {
  const gitlab = target.spec.gitlab
  if (!gitlab) return null
  return (
    <>
      <TagsFact tagPrefix={gitlab.tagPrefix} />
      <SourceFact target={target} />
      <dt>Image</dt>
      <dd>
        <span className="mono">{gitlabImage(target)}</span>
        <span className="muted small">
          {' '}
          · copied to the project’s container registry, for a Container whose release builds an image, when the registry
          is on and the token has api, or read_registry and write_registry
          {gitlab.url ? '; the instance names its registry' : ''}
        </span>
      </dd>
    </>
  )
}

/** The key the operator reads from a gitlab target's Secret, by name only: never its value. */
function GitLabKeys() {
  return (
    <p className="muted small">
      The operator reads its key <span className="mono">{GITLAB_SECRET_KEYS.token}</span>, a project access token with
      the Maintainer role and the api scope, which covers the container registry (a group or personal access token works
      the same); a deploy token can’t call the API. The same token logs in to the project’s container registry.
    </p>
  )
}

/** The keys the operator reads from a github target's Secret, by name only: never their values. */
function GitHubKeys() {
  const [appId, installationId, privateKey] = GITHUB_SECRET_KEYS.app
  return (
    <p className="muted small">
      The operator reads its keys <span className="mono">{GITHUB_SECRET_KEYS.token}</span> (a fine-grained token), or{' '}
      <span className="mono">{appId}</span>, <span className="mono">{installationId}</span> and{' '}
      <span className="mono">{privateKey}</span> (a GitHub App); and{' '}
      <span className="mono">{GITHUB_SECRET_KEYS.packages}</span>, when there, for images on its registry.
    </p>
  )
}

/**
 * A DeployTarget's page. It shows the fields it names one by one, never the
 * object whole, and of its credentials only the Secret's name.
 */
export function DeployTargetPage({ target, containers }: { target: DeployTarget; containers: Container[] }) {
  const ns = target.metadata.namespace ?? ''
  const name = target.metadata.name
  const member = memberOf(target)
  const ready = readinessOf(target)
  const parameters = Object.entries(target.spec.parameters ?? {}).sort(([a], [b]) => compareText(a, b))
  return (
    <div className="shell-page object-page">
      <ObjectHeader
        kind="DeployTarget"
        name={name}
        namespace={ns}
        breadcrumb={[projectCrumb(ns)]}
        conditions={target.status?.conditions ?? []}
        childrenTitle="Containers that ship to it"
        children={shippingTo(name, containers).map((c) => {
          const deploy = c.status?.deploys?.find((d) => d.target === name)
          return {
            kind: 'Container',
            name: c.metadata.name,
            href: pageHref(ns, 'Container', c.metadata.name),
            note: deploy
              ? `${deploy.phase}${deploy.version ? `, runs ${deploy.version}` : ''}`
              : 'no deploy reported yet',
          }
        })}
        links={[]}
        sections={{
          overview: (
            <>
              <section className="panel" aria-labelledby="deploytarget-where">
                <h2 className="panel-title small-title" id="deploytarget-where">
                  Where it ships
                </h2>
                <dl className="facts">
                  <dt>Tool</dt>
                  <dd>
                    {member.label} <span className="mono muted">({member.member})</span>
                  </dd>
                  <dt>Where</dt>
                  <dd>
                    <Where target={target} />
                  </dd>
                  {target.spec.github ? <GitHubFacts target={target} /> : null}
                  {target.spec.gitlab ? <GitLabFacts target={target} /> : null}
                  <dt>Tier</dt>
                  <dd>
                    {tierOf(target)}
                    {target.spec.tier ? null : <span className="muted"> (not set)</span>}
                  </dd>
                  <dt>Approval</dt>
                  <dd>{describeApproval(target)}</dd>
                  <dt>Ready</dt>
                  <dd>
                    <Badge tone={ready.tone} label={ready.label} />
                    {ready.condition?.message ? <span className="panel-text"> {ready.condition.message}</span> : null}
                    {target.status?.checkedTime ? (
                      <span className="muted small">
                        {' '}
                        · checked <When at={target.status.checkedTime} />
                      </span>
                    ) : null}
                  </dd>
                  <dt>Credentials</dt>
                  <dd>
                    {target.spec.credentialsSecretRef ? (
                      <>
                        Secret <span className="mono">{target.spec.credentialsSecretRef.name}</span>
                        <span className="muted small"> · its name only: the UI never reads Secrets</span>
                        {target.spec.github ? <GitHubKeys /> : null}
                        {target.spec.gitlab ? <GitLabKeys /> : null}
                      </>
                    ) : (
                      <span className="muted">none: the operator’s own ServiceAccount, in its cluster</span>
                    )}
                  </dd>
                  <dt>Parameters</dt>
                  <dd>
                    {parameters.length ? (
                      <ul className="object-list">
                        {parameters.map(([k, v]) => (
                          <li key={k} className="mono">
                            {k}: {v}
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <span className="muted">None.</span>
                    )}
                  </dd>
                </dl>
              </section>
            </>
          ),
        }}
      />
    </div>
  )
}

/** The Project page's deploy targets, each with its member, tier, approval and Ready; a refused read says so. */
export function DeployTargetList({ ns, data, connection }: { ns: string; data: StoryData; connection: Connection }) {
  const unread = unreadOf(connection, RESOURCES.deployTargets)
  const targets = data.deployTargets
  return (
    <section className="panel" aria-labelledby="project-deploy-targets">
      <h2 className="panel-title small-title" id="project-deploy-targets">
        Where its Containers ship
      </h2>
      {/* A failed re-watch keeps what it had loaded: say the failure, above the list. */}
      {unread ? <ReadProblem problem={unread} /> : null}
      {targets.length ? (
        <ul className="deploy-targets">
          {targets.map((t) => {
            const member = memberOf(t)
            const ready = readinessOf(t)
            return (
              <li key={t.metadata.name} className="deploy-target">
                <a className="mono" href={pageHref(ns, 'DeployTarget', t.metadata.name)}>
                  {t.metadata.name}
                </a>{' '}
                <Badge tone={ready.tone} label={ready.label} title={ready.condition?.message} />
                <span className="muted small">
                  {' '}
                  · {member.label}
                  {member.where ? ` ${member.where}` : ''} · {tierOf(t)} · {describeApproval(t)}
                </span>
              </li>
            )
          })}
        </ul>
      ) : unread ? null : (
        <p className="panel-text muted">None: no DeployTarget in this project.</p>
      )}
    </section>
  )
}

/** A Container's deploys, one per spec.deploy entry, with the approval of a version waiting for one. */
export function ContainerDeploys({
  container,
  targets,
  writer,
}: {
  container: Container
  targets: DeployTarget[]
  writer: ContainerWriter
}) {
  const rows = deploysOf(container)
  if (!rows.length) return null
  const ns = container.metadata.namespace ?? ''
  const newest = container.status?.newestRelease
  return (
    <section className="panel" aria-labelledby="container-deploys">
      <h2 className="panel-title small-title" id="container-deploys">
        Where it ships
      </h2>
      <p className="panel-text">
        {newest ? (
          <>
            The newest release whose runs succeeded is <span className="mono">{newest}</span>; an entry without a pin
            follows it.
          </>
        ) : (
          <span className="muted">No release whose runs succeeded yet.</span>
        )}
      </p>
      <ul className="deploys">
        {rows.map((row) => (
          <DeployItem
            key={row.target}
            ns={ns}
            container={container}
            row={row}
            known={targets.some((t) => t.metadata.name === row.target)}
            writer={writer}
          />
        ))}
      </ul>
    </section>
  )
}

function DeployItem({
  ns,
  container,
  row,
  known,
  writer,
}: {
  ns: string
  container: Container
  row: DeployRow
  known: boolean
  writer: ContainerWriter
}) {
  const s = row.status
  return (
    <li className="deploy">
      <div className="deploy-head">
        {known ? (
          <a className="mono" href={pageHref(ns, 'DeployTarget', row.target)}>
            {row.target}
          </a>
        ) : (
          <span className="mono">{row.target}</span>
        )}{' '}
        <Badge tone={phaseTone(s?.phase)} label={s?.phase ?? 'Not reported yet'} />{' '}
        {s?.health ? (
          <Badge tone={healthTone(s.health)} label={s.health} title="As the target’s tool reports it" />
        ) : null}
        {row.index < 0 ? <span className="muted small"> · no longer in spec.deploy</span> : null}
      </div>
      <dl className="facts">
        <dt>Runs</dt>
        <dd className="mono">{s?.version || <span className="muted">nothing the factory shipped</span>}</dd>
        <dt>Wanted</dt>
        <dd className="mono">
          {s?.wantedVersion || <span className="muted">none yet</span>}
          {row.pinned ? <span className="muted"> (pinned)</span> : null}
        </dd>
        {row.after ? (
          <>
            <dt>After</dt>
            <dd className="mono">{row.after}</dd>
          </>
        ) : null}
        {row.approved ? (
          <>
            <dt>Approved</dt>
            <dd className="mono">{row.approved}</dd>
          </>
        ) : null}
        {s?.image ? (
          <>
            <dt>Image</dt>
            <dd className="mono">{s.image}</dd>
          </>
        ) : null}
        {s?.url ? (
          <>
            <dt>Address</dt>
            <dd>
              <a href={s.url} target="_blank" rel="noreferrer">
                {s.url} <ExternalLinkIcon size={12} />
              </a>
            </dd>
          </>
        ) : null}
        {s?.reason || s?.message ? (
          <>
            <dt>Why</dt>
            <dd>
              {s.message}
              {s.reason ? <span className="mono muted small"> ({s.reason})</span> : null}
              {s.since ? (
                <span className="muted small">
                  {' '}
                  · since <When at={s.since} />
                </span>
              ) : null}
            </dd>
          </>
        ) : null}
      </dl>
      {row.awaiting && row.entry ? (
        // Keyed by the version, so a newer one waiting starts afresh, not as approved.
        <Approve
          key={row.awaiting}
          container={container}
          index={row.index}
          entry={row.entry}
          version={row.awaiting}
          writer={writer}
        />
      ) : null}
    </li>
  )
}

/** The approval of the version waiting: a button with actions on, else the kubectl line. */
function Approve({
  container,
  index,
  entry,
  version,
  writer,
}: {
  container: Container
  index: number
  entry: ContainerDeploy
  version: string
  writer: ContainerWriter
}) {
  const session = useContext(SessionContext)
  const dialog = useRef<HTMLDialogElement>(null)
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState<WriteError>()
  const ns = container.metadata.namespace ?? ''
  const name = container.metadata.name
  const canWrite = Boolean(session?.actions) && !(session?.mode === 'oidc' && !session.user)

  if (!canWrite)
    return (
      <div className="container-action">
        <p className="panel-text">
          {version} waits for a person’s approval to ship to {entry.target}.{' '}
          {session?.actions ? (
            <>
              <SignInLink /> to approve it here, or run:
            </>
          ) : (
            'To approve it, run:'
          )}
        </p>
        <CommandLine command={approveCommand(container, index, entry, version)} />
      </div>
    )

  function open() {
    setError(undefined)
    setSent(false)
    dialog.current?.showModal()
  }

  async function approve() {
    setSending(true)
    try {
      await writer.approveDeploy(ns, name, index, entry, version)
      setSent(true)
    } catch (err) {
      setError(writeError(err))
    } finally {
      setSending(false)
      dialog.current?.close()
    }
  }

  const id = `approve-${name}-${entry.target}`
  return (
    <div className="container-action">
      <p className="panel-text">
        {version} waits for a person’s approval to ship to {entry.target}.
      </p>
      <button type="button" className="button" onClick={open} disabled={sending}>
        Approve {version}…
      </button>
      {sent ? (
        <p className="form-done" role="status">
          Approved; the deploy above follows it.
        </p>
      ) : null}
      {error ? <FormError error={error} /> : null}
      <dialog ref={dialog} className="dialog" aria-labelledby={id}>
        <h2 className="panel-title small-title" id={id}>
          Ship {version} of {name} to {entry.target}?
        </h2>
        <p className="panel-text">
          The factory ships it through the target’s own tool once the Container’s spec says it is approved.
        </p>
        <div className="form-row dialog-buttons">
          <button type="button" className="button button-secondary" onClick={() => dialog.current?.close()}>
            Cancel
          </button>
          <button type="button" className="button" onClick={approve} disabled={sending}>
            {sending ? 'Approving…' : `Approve ${version}`}
          </button>
        </div>
      </dialog>
    </div>
  )
}
