// The project's story in one line, in the Story area: Briefs, plan,
// model, repositories, Tasks and releases, each a link to where they are.

import type { StoryData } from '../k8s/sources'
import { hrefFor } from '../lib/hooks'
import { storyline, type StoryTarget } from '../model/storyline'
import { briefsHref, pageHref } from './objects/links'
import './storyline.css'

const NO_OBJECT = { ns: undefined, kind: undefined, name: undefined }

function hrefOf(to: StoryTarget, ns: string): string | undefined {
  if ('kind' in to) return pageHref(ns, to.kind, to.name)
  if (to.area === 'briefs') return briefsHref(ns)
  if (to.area === 'story') return hrefFor({ ...NO_OBJECT, area: 'story', project: ns, view: to.view })
  return hrefFor({ ...NO_OBJECT, area: 'work' })
}

export function StoryLine({ data }: { data: StoryData }) {
  const steps = storyline(data)
  return (
    <nav className="storyline" aria-label="The project’s story">
      <ol>
        {steps.map((step) => (
          <li key={step.key} className={`storyline-step tone-${step.tone}`}>
            <a href={hrefOf(step.to, data.namespace)} title={[step.value, step.detail].filter(Boolean).join(': ')}>
              <span className="storyline-label">{step.label}</span>
              <span className="storyline-value">{step.value}</span>
              {step.detail ? <span className="storyline-detail">{step.detail}</span> : null}
            </a>
          </li>
        ))}
      </ol>
    </nav>
  )
}
