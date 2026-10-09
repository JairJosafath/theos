// Links between the shell's pages: an object's page, and the areas a
// breadcrumb leads back to.

import { hrefFor } from '../../lib/hooks'
import { hasPage } from '../../model/lifecycle'

const NO_OBJECT = { ns: undefined, kind: undefined, name: undefined }

/** The object's page, when its kind has one. */
export function pageHref(ns: string, kind: string, name: string): string | undefined {
  return hasPage(kind) ? hrefFor({ ns, kind, name, task: undefined }) : undefined
}

/** The project's story: its system context, or one system's containers. */
export function storyHref(project: string, system?: string): string {
  return hrefFor({
    ...NO_OBJECT,
    area: 'story',
    project,
    view: system ? 'containers' : 'context',
    system,
    task: undefined,
  })
}

/** The project's Briefs. */
export function briefsHref(project: string): string {
  return hrefFor({ ...NO_OBJECT, area: 'briefs', project, view: 'context', system: undefined, task: undefined })
}

/** A Task in the Work area. */
export function taskHref(ns: string, name: string): string {
  return hrefFor({ ...NO_OBJECT, area: 'work', project: ns, task: `${ns}/${name}` })
}

/** The breadcrumb's first step: the namespace's Project page. */
export function projectCrumb(ns: string): { label: string; href?: string } {
  return { label: `Project ${ns}`, href: pageHref(ns, 'Project', ns) }
}
