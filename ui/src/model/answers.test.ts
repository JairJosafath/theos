import { describe, expect, it } from 'vitest'
import { answeredWith, failedAnswers, shownAnswer, suggestionAnswers } from './answers'

describe('the prompt worded for each answer', () => {
  it('takes or skips a reviewer suggestion, naming it and its title', () => {
    const target = { container: 'orders-api', number: 17, title: 'Reject orders with a negative quantity' }
    expect(suggestionAnswers(target).map((a) => [a.kind, a.label, a.prompt])).toEqual([
      ['take', 'Take', 'Take suggestion #17 in orders-api: Reject orders with a negative quantity.'],
      ['skip', 'Skip', 'Skip suggestion #17 in orders-api: Reject orders with a negative quantity.'],
    ])
  })

  it('retries, closes or waits on a failed issue', () => {
    const target = { container: 'storefront', number: 3, title: 'Reorder button on past orders' }
    expect(failedAnswers(target).map((a) => [a.kind, a.label, a.prompt])).toEqual([
      ['retry', 'Retry', 'Retry issue #3 in storefront: Reorder button on past orders.'],
      ['close', 'Close', 'Close issue #3 in storefront: Reorder button on past orders.'],
      ['wait', 'Wait', 'Wait on issue #3 in storefront: Reorder button on past orders.'],
    ])
  })

  it('names the item alone when it has no title, and ends a title with one full stop', () => {
    expect(failedAnswers({ container: 'cli', number: 7 })[0].prompt).toBe('Retry issue #7 in cli.')
    expect(failedAnswers({ container: 'cli', number: 7, title: '  ' })[0].prompt).toBe('Retry issue #7 in cli.')
    expect(suggestionAnswers({ container: 'cli', number: 2, title: 'Is it safe? ' })[0].prompt).toBe(
      'Take suggestion #2 in cli: Is it safe.',
    )
  })

  it('says each answer asks the next round, not that it acts now', () => {
    for (const a of [
      ...suggestionAnswers({ container: 'a', number: 1 }),
      ...failedAnswers({ container: 'a', number: 1 }),
    ])
      expect(a.explanation).toMatch(/^Ask the next round to /)
  })
})

describe('answeredWith', () => {
  const answers = failedAnswers({ container: 'cli', number: 7 })

  it('finds the answer whose prompt waits for the next round', () => {
    const prompts = [
      { text: 'Add a CLI.', planned: true },
      { text: 'Close issue #7 in cli.', planned: false },
    ]
    expect(answeredWith(answers, prompts)?.kind).toBe('close')
  })

  it('ignores a prompt a round took already, so the item can be answered again', () => {
    expect(answeredWith(answers, [{ text: 'Wait on issue #7 in cli.', planned: true }])).toBe(undefined)
  })

  it('ignores a prompt that only resembles one', () => {
    expect(answeredWith(answers, [{ text: 'Retry issue #7 in cli. Now.', planned: false }])).toBe(undefined)
  })
})

describe('shownAnswer', () => {
  const answers = failedAnswers({ container: 'cli', number: 7 })
  const close = answers.find((a) => a.kind === 'close')!
  const older = (n: number) => Array.from({ length: n }, (_, i) => ({ text: `Prompt ${i}`, planned: true }))

  it('shows the answer just sent until the watch brings its prompt', () => {
    expect(shownAnswer(answers, older(30), 30, { answer: close, written: 30 })).toBe(close)
  })

  it('shows the waiting prompt once the watch brings it', () => {
    const prompts = [...older(30), { text: close.prompt, planned: false }]
    expect(shownAnswer(answers, prompts, 31, { answer: close, written: 30 })).toBe(close)
  })

  it('stops showing it once a round plans the prompt, though retiring shrank the list', () => {
    // 30 prompts, the answer lands as the 31st, a round plans it and
    // retires the five oldest: 26 listed, 31 written.
    const prompts = [...older(25), { text: close.prompt, planned: true }]
    expect(shownAnswer(answers, prompts, 31, { answer: close, written: 30 })).toBe(undefined)
  })
})
