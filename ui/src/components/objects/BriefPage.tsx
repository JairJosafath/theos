// A Brief's page: the object header (its lifecycle, conditions, rounds as the
// timeline, what it made, its repository and the pause button), then what its
// card shows: what needs you, with a one-click answer on each item, its
// rounds, prompts, issues and suggestions, and the prompt box; then its
// settings (autopilot, parallel, releases), which it changes with actions on.

import { useState } from 'react'
import type { BriefWriter } from '../../k8s/briefs'
import type { Agent, Brief } from '../../k8s/types'
import { useNow } from '../../lib/hooks'
import { describeBrief } from '../../model/brief'
import { BriefBody, PauseButton } from '../Briefs'
import { plannerLimit } from './rows'
import { FormError } from '../briefs/PromptForm'
import type { WriteError } from '../briefs/errors'
import { ObjectHeader } from '../ObjectHeader'
import { BriefSettings } from '../briefs/BriefSettings'
import { briefTimeline } from './rows'
import { briefsHref, pageHref, storyHref } from './links'
import '../briefs.css'

export function BriefPage({ brief, agents, writer }: { brief: Brief; agents: Agent[]; writer: BriefWriter }) {
  const now = useNow()
  const ns = brief.metadata.namespace ?? ''
  const view = describeBrief(brief, plannerLimit(agents, brief))
  const [pauseError, setPauseError] = useState<WriteError>()
  return (
    <main className="shell-page object-page">
      <ObjectHeader
        kind="Brief"
        name={brief.metadata.name}
        namespace={ns}
        breadcrumb={[
          { label: `Project ${ns}`, href: storyHref(ns) },
          { label: 'Briefs', href: briefsHref(ns) },
        ]}
        phase={brief.status?.phase}
        conditions={brief.status?.conditions ?? []}
        timeline={briefTimeline(brief)}
        childrenTitle="What it made"
        children={(brief.status?.created ?? []).map((c) => ({
          kind: c.kind,
          name: c.name,
          href: pageHref(ns, c.kind, c.name),
        }))}
        links={view.repositoryURL ? [{ href: view.repositoryURL, label: 'Its plans, in its repository' }] : []}
        actions={<PauseButton namespace={ns} view={view} writer={writer} onError={setPauseError} />}
      />
      <section
        className={`panel brief brief-${view.phase.tone}`}
        aria-label={`Brief ${view.name}, its idea and rounds`}
      >
        <h2 className="panel-title small-title">The idea</h2>
        <p className="brief-idea">{view.idea}</p>
        {pauseError ? <FormError error={pauseError} /> : null}
        <BriefBody view={view} namespace={ns} writer={writer} now={now} answering />
      </section>
      <BriefSettings brief={brief} writer={writer} />
    </main>
  )
}
