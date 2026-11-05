// The shell's areas (docs/plans/done/ui-lifecycle.md, decisions 1 and 2): their
// order in the navigation, their names, what each one is about, and the
// sidebar's group they sit in (docs/plans/done/ui-shell.md).

export type Area = 'needs' | 'loop' | 'briefs' | 'story' | 'work' | 'factory' | 'about'

/** The sidebar's groups: Needs you on its own, Build, Operate, and About at the bottom. */
export type AreaGroup = 'needs' | 'build' | 'operate' | 'bottom'

export const GROUP_LABELS: Record<AreaGroup, string | undefined> = {
  needs: undefined,
  build: 'Build',
  operate: 'Operate',
  bottom: undefined,
}

export const AREAS: { key: Area; label: string; about: string; group: AreaGroup }[] = [
  { key: 'needs', label: 'Needs you', about: 'What waits on a person, across projects', group: 'needs' },
  {
    key: 'loop',
    label: 'Loop',
    about: "The project's software-development cycle, from an idea or a bug to a monitored release",
    group: 'build',
  },
  { key: 'briefs', label: 'Briefs', about: "The project's ideas, their plans and prompts", group: 'build' },
  {
    key: 'story',
    label: 'Story',
    about: "The project's model: its systems, containers and who uses them",
    group: 'build',
  },
  { key: 'work', label: 'Work', about: 'Crews and their Tasks, across projects', group: 'build' },
  { key: 'factory', label: 'Factory', about: "The factory's health and agent slots", group: 'operate' },
  { key: 'about', label: 'About', about: 'Versions, docs and release notes', group: 'bottom' },
]

/** The label of the group an area sits in, for the breadcrumb; none for Needs you and About. */
export function groupLabel(area: Area): string | undefined {
  return GROUP_LABELS[AREAS.find((a) => a.key === area)!.group]
}

export function isArea(s: string | null | undefined): s is Area {
  return AREAS.some((a) => a.key === s)
}

export function areaLabel(area: Area): string {
  return AREAS.find((a) => a.key === area)!.label
}

/** Whether the area shows one project's namespace; the others show every namespace, or none. */
export function inNamespace(area: Area): boolean {
  return area === 'loop' || area === 'briefs' || area === 'story'
}
