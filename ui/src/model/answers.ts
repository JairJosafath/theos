// One-click answers to what waits for the person on a Brief: take or skip a
// reviewer suggestion; retry, close or wait on a failed issue. Each answer is a
// prompt appended to the Brief, worded from the item (design.md, UI actions),
// so the planner reads it as it reads any prompt and the Brief's prompts show
// what was said.

import type { BriefPrompt } from './brief'

export type AnswerKind = 'take' | 'skip' | 'retry' | 'close' | 'wait'

/** One button: what it says, the prompt it appends and what that asks for. */
export interface Answer {
  kind: AnswerKind
  label: string
  prompt: string
  explanation: string
}

/** The item an answer is about: an issue in the repository of a Container the Brief made. */
export interface AnswerTarget {
  container: string
  number: number
  title?: string
}

/** "#17 in orders-api: Reject orders with a negative quantity." */
function about({ container, number, title }: AnswerTarget): string {
  const words = title?.trim().replace(/[.!?:;,\s]+$/, '')
  return `#${number} in ${container}${words ? `: ${words}` : ''}.`
}

/** Take or skip a reviewer suggestion. */
export function suggestionAnswers(target: AnswerTarget): Answer[] {
  return [
    {
      kind: 'take',
      label: 'Take',
      prompt: `Take suggestion ${about(target)}`,
      explanation: 'Ask the next round to make this suggestion an issue for its Crew',
    },
    {
      kind: 'skip',
      label: 'Skip',
      prompt: `Skip suggestion ${about(target)}`,
      explanation: 'Ask the next round to close this suggestion unworked',
    },
  ]
}

/** Retry, close or wait on a failed issue. */
export function failedAnswers(target: AnswerTarget): Answer[] {
  return [
    {
      kind: 'retry',
      label: 'Retry',
      prompt: `Retry issue ${about(target)}`,
      explanation: 'Ask the next round to send this issue back to its Crew',
    },
    {
      kind: 'close',
      label: 'Close',
      prompt: `Close issue ${about(target)}`,
      explanation: 'Ask the next round to close this issue',
    },
    {
      kind: 'wait',
      label: 'Wait',
      prompt: `Wait on issue ${about(target)}`,
      explanation: 'Ask the next round to leave this issue as it is for now',
    },
  ]
}

/**
 * The answer already sent: one whose prompt waits for the next round. A
 * prompt a round took doesn't count, so an issue that still waits after a
 * "wait" can be answered again.
 */
export function answeredWith(answers: Answer[], prompts: BriefPrompt[]): Answer | undefined {
  const waiting = new Set(prompts.filter((p) => !p.planned).map((p) => p.text))
  return answers.find((a) => waiting.has(a.prompt))
}
