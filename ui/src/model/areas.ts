// The shell's areas (docs/plans/done/ui-lifecycle.md, decisions 1 and 2): their
// order in the navigation, their names, and what each one is about.

export type Area = 'needs' | 'loop' | 'briefs' | 'story' | 'work' | 'factory' | 'about'

export const AREAS: { key: Area; label: string; about: string }[] = [
  { key: 'needs', label: 'Needs you', about: 'What waits on a person, across projects' },
  {
    key: 'loop',
    label: 'Loop',
    about: "The project's software-development cycle, from an idea or a bug to a monitored release",
  },
  { key: 'briefs', label: 'Briefs', about: "The project's ideas, their plans and prompts" },
  { key: 'story', label: 'Story', about: "The project's model: its systems, containers and who uses them" },
  { key: 'work', label: 'Work', about: 'Crews and their Tasks, across projects' },
  { key: 'factory', label: 'Factory', about: "The factory's health and agent slots" },
  { key: 'about', label: 'About', about: 'Versions, docs and release notes' },
]

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
