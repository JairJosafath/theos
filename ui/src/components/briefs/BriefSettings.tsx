// A Brief's settings on its page: autopilot, parallel and the apps whose
// releases are published to GitHub. With actions on each is a write the UI
// server allows (spec.autopilot, spec.parallel, spec.releases); off, or with
// nobody signed in with OIDC, the page shows them and says why it can't
// change them. The CRD judges each value; a refusal shows the API's message.

import { useContext, useId, useState, type FormEvent } from 'react'
import type { BriefSettings as Settings, BriefWriter } from '../../k8s/briefs'
import { canWrite } from '../../k8s/session'
import type { Brief, BriefAutopilot, BriefRelease } from '../../k8s/types'
import { SessionContext } from '../../lib/hooks'
import { AUTOPILOT_MODES, DEFAULT_AUTOPILOT_HINT } from '../../model/brief'
import { SignInLink } from '../Account'
import { FormError } from './PromptForm'
import { writeError, type WriteError } from './errors'

const MODES = Object.keys(AUTOPILOT_MODES) as BriefAutopilot[]

export function BriefSettings({ brief, writer }: { brief: Brief; writer: BriefWriter }) {
  const session = useContext(SessionContext)
  const can = canWrite(session, 'setting')
  const name = brief.metadata.name
  const namespace = brief.metadata.namespace ?? ''
  const [sending, setSending] = useState<string>()
  const [error, setError] = useState<WriteError>()

  /** Sends one setting; resolves true when the API took it. */
  async function save(what: string, settings: Settings): Promise<boolean> {
    setSending(what)
    setError(undefined)
    try {
      await writer.setSettings(namespace, name, settings)
      return true
    } catch (err) {
      setError(writeError(err))
      return false
    } finally {
      setSending(undefined)
    }
  }

  const act = can === 'act'
  const busy = sending !== undefined
  return (
    <section className="panel brief-settings" aria-label={`Settings of brief ${name}`}>
      <h2 className="panel-title small-title">Settings</h2>
      {can === 'signIn' ? (
        <p className="field-hint">
          <SignInLink /> to change these settings.
        </p>
      ) : can === 'off' ? (
        <p className="field-hint">
          The UI changes these only with actions on (the chart’s <span className="mono">ui.actions.enabled</span>);
          until then, set them on the Brief with kubectl.
        </p>
      ) : null}

      <Autopilot brief={brief} act={act} busy={busy} save={save} sending={sending} />
      <Parallel brief={brief} act={act} busy={busy} save={save} />
      <Releases brief={brief} act={act} busy={busy} save={save} sending={sending} />
      {error ? <FormError error={error} /> : null}
    </section>
  )
}

interface SettingProps {
  brief: Brief
  /** Whether the page can change it. */
  act: boolean
  /** Whether a setting is being sent. */
  busy: boolean
  save: (what: string, settings: Settings) => Promise<boolean>
  sending?: string
}

/** One button per mode, the Brief's own pressed; the factory's default when it has none. */
function Autopilot({ brief, act, busy, save, sending }: SettingProps) {
  const set = brief.spec.autopilot
  const hint = set
    ? AUTOPILOT_MODES[set].explanation
    : `${DEFAULT_AUTOPILOT_HINT}${brief.status?.autopilot ? ` It runs in ${brief.status.autopilot} now.` : ''}`
  return (
    <div className="brief-setting" role="group" aria-label="Autopilot">
      <span className="field-label">Autopilot</span>
      <span className="field-hint">{hint}</span>
      <div className="brief-setting-row">
        {MODES.map((mode) => (
          <button
            key={mode}
            type="button"
            className={`button button-small${mode === set ? '' : ' button-secondary'}`}
            aria-pressed={mode === set}
            title={AUTOPILOT_MODES[mode].explanation}
            disabled={!act || busy || mode === set}
            onClick={() => save(`autopilot-${mode}`, { autopilot: mode })}
          >
            {sending === `autopilot-${mode}` ? 'Setting…' : mode}
          </button>
        ))}
      </div>
    </div>
  )
}

/** A number and a Set button; sent as typed, so the API server judges it. */
function Parallel({ brief, act, busy, save }: SettingProps) {
  const id = useId()
  const [typed, setTyped] = useState('')
  const current = brief.spec.parallel

  async function submit(e: FormEvent) {
    e.preventDefault()
    const t = typed.trim()
    if (!t) return
    if (await save('parallel', { parallel: /^-?\d+$/.test(t) ? Number(t) : t })) setTyped('')
  }

  return (
    <form className="brief-setting" onSubmit={submit}>
      <label className="field" htmlFor={act ? `${id}-parallel` : undefined}>
        <span className="field-label">Parallel</span>
        <span className="field-hint">
          How many issues each of its crews works on at once:{' '}
          {current ? <strong>{current}</strong> : 'the factory’s default'}.
        </span>
      </label>
      {act ? (
        <div className="form-row">
          <input
            id={`${id}-parallel`}
            className="input brief-setting-number"
            inputMode="numeric"
            value={typed}
            placeholder={current ? String(current) : '1 to 5'}
            onChange={(e) => setTyped(e.target.value)}
            disabled={busy}
          />
          <button type="submit" className="button button-secondary" disabled={busy || !typed.trim()}>
            Set parallel
          </button>
        </div>
      ) : null}
    </form>
  )
}

/** The Brief's releases list: each entry with a Remove, and a form that adds one. */
function Releases({ brief, act, busy, save, sending }: SettingProps) {
  const releases = brief.spec.releases ?? []
  // The Containers it made, and any it names already, to pick from.
  const containers = [
    ...new Set([
      ...(brief.status?.created ?? []).filter((c) => c.kind === 'Container').map((c) => c.name),
      ...(brief.status?.apps ?? []).map((a) => a.container),
    ]),
  ].filter((c) => !releases.some((r) => r.container === c))

  return (
    <div className="brief-setting" role="group" aria-label="Releases">
      <span className="field-label">Releases published to GitHub</span>
      <span className="field-hint">
        Each app here gets releases, published to its GitHub repository; removing one leaves its crew as it is.
      </span>
      {releases.length ? (
        <ul className="brief-list brief-setting-releases">
          {releases.map((r) => (
            <li key={r.container}>
              <span className="mono">{r.container}</span> to <span className="mono">{r.github.repository}</span>
              <span className="field-hint">
                {' '}
                · token Secret {r.github.tokenSecretRef.name}
                {r.tagPrefix ? ` · tags ${r.tagPrefix}v…` : ''}
              </span>
              {act ? (
                <button
                  type="button"
                  className="button button-secondary button-small"
                  aria-label={`Stop publishing ${r.container}`}
                  disabled={busy}
                  onClick={() =>
                    save(`remove-${r.container}`, { releases: releases.filter((x) => x.container !== r.container) })
                  }
                >
                  {sending === `remove-${r.container}` ? 'Removing…' : 'Remove'}
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      ) : (
        <p className="field-hint">None yet.</p>
      )}
      {act ? <AddRelease containers={containers} releases={releases} busy={busy} save={save} /> : null}
    </div>
  )
}

function AddRelease({
  containers,
  releases,
  busy,
  save,
}: {
  containers: string[]
  releases: BriefRelease[]
  busy: boolean
  save: SettingProps['save']
}) {
  const id = useId()
  const [container, setContainer] = useState('')
  const [repository, setRepository] = useState('')
  const [secret, setSecret] = useState('')
  const [tagPrefix, setTagPrefix] = useState('')
  const picked = container || containers[0] || ''

  async function submit(e: FormEvent) {
    e.preventDefault()
    const entry: BriefRelease = {
      container: picked,
      github: { repository: repository.trim(), tokenSecretRef: { name: secret.trim() } },
    }
    if (tagPrefix.trim()) entry.tagPrefix = tagPrefix.trim()
    if (await save('add-release', { releases: [...releases, entry] })) {
      setContainer('')
      setRepository('')
      setSecret('')
      setTagPrefix('')
    }
  }

  if (!containers.length) return null
  return (
    <form className="brief-setting-add" onSubmit={submit} aria-label="Publish an app's releases">
      <label className="field" htmlFor={`${id}-container`}>
        <span className="field-label">App</span>
      </label>
      <select
        id={`${id}-container`}
        className="input"
        value={picked}
        onChange={(e) => setContainer(e.target.value)}
        disabled={busy}
      >
        {containers.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </select>
      <label className="field" htmlFor={`${id}-repository`}>
        <span className="field-label">GitHub repository</span>
        <span className="field-hint">owner/name</span>
      </label>
      <input
        id={`${id}-repository`}
        className="input mono"
        value={repository}
        onChange={(e) => setRepository(e.target.value)}
        disabled={busy}
      />
      <label className="field" htmlFor={`${id}-secret`}>
        <span className="field-label">Token Secret</span>
        <span className="field-hint">A Secret in this namespace with the GitHub token; the UI never sees it.</span>
      </label>
      <input
        id={`${id}-secret`}
        className="input mono"
        value={secret}
        onChange={(e) => setSecret(e.target.value)}
        disabled={busy}
      />
      <label className="field" htmlFor={`${id}-prefix`}>
        <span className="field-label">Tag prefix</span>
        <span className="field-hint">Optional, such as cli- when two apps publish to one repository.</span>
      </label>
      <input
        id={`${id}-prefix`}
        className="input mono"
        value={tagPrefix}
        onChange={(e) => setTagPrefix(e.target.value)}
        disabled={busy}
      />
      <div className="form-row">
        <button type="submit" className="button button-secondary" disabled={busy || !picked}>
          Publish its releases
        </button>
      </div>
    </form>
  )
}
