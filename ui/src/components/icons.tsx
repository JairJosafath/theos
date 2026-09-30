// Small inline icons (24px grid, drawn with currentColor), so the UI needs no
// icon library.

import type { ReactNode, SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement> & { size?: number; title?: string }

function Icon({ size = 16, title, children, ...rest }: IconProps & { children: ReactNode }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      role={title ? 'img' : undefined}
      aria-hidden={title ? undefined : true}
      {...rest}
    >
      {title ? <title>{title}</title> : null}
      {children}
    </svg>
  )
}

/** An AI agent at work. */
export function AgentIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <rect x="4" y="8" width="16" height="12" rx="3" />
      <path d="M12 4v4" />
      <circle cx="12" cy="3.5" r="1" fill="currentColor" stroke="none" />
      <circle cx="9" cy="14" r="1.25" fill="currentColor" stroke="none" />
      <circle cx="15" cy="14" r="1.25" fill="currentColor" stroke="none" />
      <path d="M2 13v3M22 13v3" />
    </Icon>
  )
}

/** A human at work. */
export function HumanIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <circle cx="12" cy="8" r="4" />
      <path d="M4 21c0-4.4 3.6-7 8-7s8 2.6 8 7" />
    </Icon>
  )
}

export function ExternalLinkIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M14 4h6v6" />
      <path d="M20 4l-9 9" />
      <path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" />
    </Icon>
  )
}

export function CheckIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M5 12.5l4.5 4.5L19 7.5" />
    </Icon>
  )
}

export function CrossIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M6 6l12 12M18 6L6 18" />
    </Icon>
  )
}

export function ClockIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </Icon>
  )
}

export function QuestionIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <circle cx="12" cy="12" r="9" />
      <path d="M9.5 9.5a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .9-1 1.6v.6" />
      <circle cx="12" cy="17" r="0.6" fill="currentColor" />
    </Icon>
  )
}

export function WarningIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M12 3.5l9.5 16.5h-19z" />
      <path d="M12 10v4.5" />
      <circle cx="12" cy="17.3" r="0.6" fill="currentColor" />
    </Icon>
  )
}

export function ArrowRightIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M5 12h14M13 6l6 6-6 6" />
    </Icon>
  )
}

export function RepoIcon(props: IconProps) {
  return (
    <Icon {...props}>
      <path d="M6 3h11a1 1 0 0 1 1 1v14H7a2 2 0 0 0-2 2V5a2 2 0 0 1 1-2z" />
      <path d="M5 20a2 2 0 0 0 2 1h11v-3" />
    </Icon>
  )
}

/** The mark in the top bar: three nested boxes, for the C4 levels. */
export function LogoMark(props: IconProps) {
  return (
    <Icon {...props} strokeWidth={1.8}>
      <rect x="2.5" y="2.5" width="19" height="19" rx="4" />
      <rect x="6.5" y="6.5" width="11" height="11" rx="2.5" />
      <rect x="10" y="10" width="4" height="4" rx="1" fill="currentColor" stroke="none" />
    </Icon>
  )
}
