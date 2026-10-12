// What a person does to a Crew from its page (design.md, UI actions): pause or
// resume it, set how many issues it works on at once, its release settings,
// and re-run its last release's failed runs. Shown only when the UI server
// takes actions; with OIDC and nobody signed in, a "Sign in" link instead. The
// API server decides each write; a refusal shows its message.

import { useId, useState, type FormEvent, type ReactNode } from 'react'
import {
  releasePatch,
  releaseSettings,
  settledEdits,
  sourceTagPrefix,
  type CrewReleaseSettings,
  type CrewWriter,
} from '../../k8s/crews'
import type { Crew, PublishSource } from '../../k8s/types'
import { SignInLink } from '../Account'
import { writeError, type WriteError } from '../briefs/errors'
import { FormError } from '../briefs/PromptForm'
import '../briefs.css'

/** Says that acting on the Crew needs a person signed in, with the link. */
export function SignInToAct() {
  return (
    <p className="panel-text crew-sign-in">
      Sign in to pause this Crew or change its settings. <SignInLink />
    </p>
  )
}

/** One write from a button or form: whether it is on its way, and its refusal. */
function useWrite() {
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<WriteError>()
  const [done, setDone] = useState<string>()
  async function run(write: () => Promise<void>, doneText?: string) {
    setSending(true)
    setError(undefined)
    setDone(undefined)
    try {
      await write()
      setDone(doneText)
    } catch (err) {
      setError(writeError(err))
    } finally {
      setSending(false)
    }
  }
  return { sending, error, done, run }
}

/** Pauses a Crew or resumes a paused one by patching spec.paused. */
export function CrewPauseButton({ crew, writer }: { crew: Crew; writer: CrewWriter }) {
  const { sending, error, run } = useWrite()
  const paused = Boolean(crew.spec.paused)
  const ns = crew.metadata.namespace ?? ''
  const name = crew.metadata.name
  return (
    <>
      <button
        type="button"
        className="button button-secondary button-small"
        title={paused ? 'Pick up ready issues again' : 'Pick up no new issues; a Task at work runs to its end'}
        disabled={sending}
        onClick={() => run(() => writer.setPaused(ns, name, !paused))}
      >
        {sending ? (paused ? 'Resuming…' : 'Pausing…') : paused ? 'Resume crew' : 'Pause crew'}
      </button>
      {error ? <FormError error={error} /> : null}
    </>
  )
}

/** spec.parallel: how many issues the Crew works on at once. The CRD says how many it may be. */
export function ParallelForm({ crew, writer }: { crew: Crew; writer: CrewWriter }) {
  const id = useId()
  const current = crew.spec.parallel ?? 1
  // What the person typed; unset, the field follows the Crew.
  const [typed, setTyped] = useState<string>()
  const { sending, error, done, run } = useWrite()
  // Once the Crew has what was typed, the field follows it again.
  const [seen, setSeen] = useState(current)
  if (seen !== current) {
    setSeen(current)
    if (typed !== undefined && Number(typed) === current) setTyped(undefined)
  }
  const shown = typed ?? String(current)
  const value = Number(shown.trim())
  // A whole number only, so what is sent is an integer the API server judges.
  const changed = shown.trim() !== '' && Number.isInteger(value) && value !== current

  function submit(e: FormEvent) {
    e.preventDefault()
    if (!changed) return
    void run(() => writer.setParallel(crew.metadata.namespace ?? '', crew.metadata.name, value), 'Saved')
  }

  return (
    <form className="crew-form" onSubmit={submit}>
      <label className="field" htmlFor={`${id}-parallel`}>
        <span className="field-label">Issues at once</span>
        <span className="field-hint">Each in its own Task and branch.</span>
      </label>
      <div className="form-row">
        <input
          id={`${id}-parallel`}
          className="input crew-parallel"
          type="number"
          step={1}
          inputMode="numeric"
          value={shown}
          onChange={(e) => setTyped(e.target.value)}
          disabled={sending}
        />
        <button type="submit" className="button button-secondary" disabled={sending || !changed}>
          {sending ? 'Saving…' : 'Save'}
        </button>
        <Done text={done} />
      </div>
      {error ? <FormError error={error} /> : null}
    </form>
  )
}

/**
 * The release settings: tag, prepare, and where to publish with the name of
 * the Secret that holds the token, and the folder the app's source goes to.
 * The browser never reads that Secret; the person makes it in the Crew's
 * namespace. Save sends only what changed.
 */
export function ReleaseForm({ crew, writer }: { crew: Crew; writer: CrewWriter }) {
  const id = useId()
  // What the person changed; the rest follows the Crew, so a save never writes back a value read earlier.
  const [edits, setEdits] = useState<Partial<CrewReleaseSettings>>({})
  const { sending, error, done, run } = useWrite()
  // Once the Crew has an edited value, that field follows it again.
  const rv = crew.metadata.resourceVersion
  // The publish fields are sent as one object, so the save says which Crew
  // they were worked out from: one changed since refuses it rather than
  // write back a field someone else changed meanwhile.
  const [publishFrom, setPublishFrom] = useState<string>()
  const [seen, setSeen] = useState(rv)
  if (seen !== rv) {
    setSeen(rv)
    const settled = settledEdits(crew, edits)
    setEdits(settled)
    if (!('publish' in settled)) setPublishFrom(undefined)
  }
  const form = { ...releaseSettings(crew), ...edits }
  const setForm = (next: Partial<CrewReleaseSettings>) => {
    if ('publish' in next && !('publish' in edits)) setPublishFrom(rv)
    setEdits({ ...edits, ...next })
  }
  const ops = releasePatch(crew, form, publishFrom)
  const publish: NonNullable<CrewReleaseSettings['publish']> = form.publish ?? {
    repository: '',
    tokenSecret: '',
    tagPrefix: '',
    latest: true,
  }
  const setPublish = (patch: Partial<typeof publish>) => setForm({ publish: { ...publish, ...patch } })

  function submit(e: FormEvent) {
    e.preventDefault()
    if (!ops.length) return
    void run(() => writer.setReleases(crew.metadata.namespace ?? '', crew.metadata.name, ops), 'Saved')
  }

  return (
    <form className="crew-form" onSubmit={submit} aria-label={`Release settings of crew ${crew.metadata.name}`}>
      <Check
        label="Tag releases"
        hint="Tag a release once its milestone is done."
        checked={form.tagReleases}
        disabled={sending}
        onChange={(tagReleases) => setForm({ tagReleases })}
      />
      <Check
        label="Prepare releases"
        hint="File each release’s prepare issue: the version and the changelog."
        checked={form.prepareReleases}
        disabled={sending}
        onChange={(prepareReleases) => setForm({ prepareReleases })}
      />
      <Check
        label="Publish releases to GitHub"
        hint="Copy each tagged release to a GitHub repository once its runs succeed."
        checked={Boolean(form.publish)}
        disabled={sending}
        onChange={(on) => setForm({ publish: on ? (releaseSettings(crew).publish ?? publish) : undefined })}
      />
      {form.publish ? (
        <div className="crew-publish">
          <Text id={`${id}-repo`} label="Repository" hint="owner/name on github.com">
            <input
              id={`${id}-repo`}
              className="input mono"
              value={publish.repository}
              spellCheck={false}
              onChange={(e) => setPublish({ repository: e.target.value })}
              disabled={sending}
            />
          </Text>
          <Text
            id={`${id}-secret`}
            label="Token Secret"
            hint={`The name of a Secret in ${crew.metadata.namespace ?? 'its namespace'} with key token; the UI never reads it.`}
          >
            <input
              id={`${id}-secret`}
              className="input mono"
              value={publish.tokenSecret}
              spellCheck={false}
              onChange={(e) => setPublish({ tokenSecret: e.target.value })}
              disabled={sending}
            />
          </Text>
          <Text id={`${id}-prefix`} label="Tag prefix" hint="Optional, such as cli- for cli-v0.2.0.">
            <input
              id={`${id}-prefix`}
              className="input mono"
              value={publish.tagPrefix}
              spellCheck={false}
              onChange={(e) => setPublish({ tagPrefix: e.target.value })}
              disabled={sending}
            />
          </Text>
          <Check
            label="Latest on GitHub"
            hint="Each release becomes the repository’s Latest. Turn it off for every app but the one people install, when several publish to one repository."
            checked={publish.latest}
            disabled={sending}
            onChange={(latest) => setPublish({ latest })}
          />
          <Check
            label="Publish the source"
            hint="Put the app’s source, as of each release’s tag, into a folder of the repository, so go install finds it. The factory’s own files stay out."
            checked={Boolean(publish.source)}
            disabled={sending}
            onChange={(on) =>
              setPublish({ source: on ? (releaseSettings(crew).publish?.source ?? { path: '' }) : undefined })
            }
          />
          {publish.source ? (
            <SourceFields
              id={id}
              source={publish.source}
              sending={sending}
              onChange={(source) => setPublish({ source })}
            />
          ) : null}
        </div>
      ) : null}
      <div className="form-row">
        <button type="submit" className="button button-secondary" disabled={sending || !ops.length}>
          {sending ? 'Saving…' : 'Save release settings'}
        </button>
        <Done text={done} />
      </div>
      {error ? <FormError error={error} /> : null}
    </form>
  )
}

/** The source's folder and tag prefix, with the tag the prefix makes. */
function SourceFields({
  id,
  source,
  sending,
  onChange,
}: {
  id: string
  source: PublishSource
  sending: boolean
  onChange: (source: PublishSource) => void
}) {
  // Unset, the prefix follows the folder: what go install looks for.
  const fallback = sourceTagPrefix({ path: source.path })
  return (
    <>
      <Text
        id={`${id}-source-path`}
        label="Source folder"
        hint="Such as engine; . is the whole repository. Each release replaces what the folder holds."
      >
        <input
          id={`${id}-source-path`}
          className="input mono"
          value={source.path}
          spellCheck={false}
          onChange={(e) => onChange({ ...source, path: e.target.value })}
          disabled={sending}
        />
      </Text>
      <Text
        id={`${id}-source-prefix`}
        label="Source tag prefix"
        hint={`Optional; empty, it is ${fallback ? fallback : 'nothing'}. The source is tagged ${sourceTagPrefix(source)}vX.Y.Z.`}
      >
        <input
          id={`${id}-source-prefix`}
          className="input mono"
          value={source.tagPrefix ?? ''}
          placeholder={fallback}
          spellCheck={false}
          onChange={(e) => onChange({ path: source.path, ...(e.target.value ? { tagPrefix: e.target.value } : {}) })}
          disabled={sending}
        />
      </Text>
    </>
  )
}

/**
 * Re-runs the last release's failed runs: sets c4.factory.io/rerun-release to
 * a new value, which the operator answers once (design.md, UI actions).
 */
export function RerunButton({ crew, writer }: { crew: Crew; writer: CrewWriter }) {
  const { sending, error, done, run } = useWrite()
  const tag = crew.status?.lastRelease?.tag
  return (
    <div className="crew-rerun">
      <div className="form-row">
        <button
          type="button"
          className="button button-secondary"
          // Asked once; CrewPage gives a new button when the release's re-run time changes.
          disabled={sending || Boolean(done)}
          onClick={() =>
            run(
              () => writer.rerunRelease(crew.metadata.namespace ?? '', crew.metadata.name, new Date().toISOString()),
              'Asked the operator to re-run them',
            )
          }
        >
          {sending ? 'Asking…' : `Re-run ${tag ?? 'the release'}’s failed runs`}
        </button>
        <Done text={done} />
      </div>
      {error ? <FormError error={error} /> : null}
    </div>
  )
}

function Done({ text }: { text: string | undefined }) {
  return text ? (
    <span className="form-done" role="status">
      {text}
    </span>
  ) : null
}

function Check({
  label,
  hint,
  checked,
  disabled,
  onChange,
}: {
  label: string
  hint: string
  checked: boolean
  disabled: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label className="crew-check">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span>
        <span className="field-label">{label}</span> <span className="field-hint">{hint}</span>
      </span>
    </label>
  )
}

function Text({ id, label, hint, children }: { id: string; label: string; hint: string; children: ReactNode }) {
  return (
    <>
      <label className="field" htmlFor={id}>
        <span className="field-label">{label}</span>
        <span className="field-hint">{hint}</span>
      </label>
      {children}
    </>
  )
}
