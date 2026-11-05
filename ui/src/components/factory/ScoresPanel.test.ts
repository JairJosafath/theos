import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { demoStory } from '../../demo/fixture'
import type { Task } from '../../k8s/types'
import { ScoreTaskVerdicts, TaskScores } from '../objects/Scores'
import { describeRate, ScoresPanel } from './ScoresPanel'

const text = (html: string) =>
  html
    .replace(/<[^>]+>/g, ' ')
    .replace(/&#x27;/g, "'")
    .replace(/\s+/g, ' ')

const story = demoStory(Date.parse('2026-10-16T04:00:00Z'))
const named = (name: string) => story.tasks.find((t) => t.metadata.name === name) as Task

describe('Scores in the UI (measure-and-improve, step 5)', () => {
  // The links read the page's address.
  beforeEach(() => vi.stubGlobal('window', { location: { pathname: '/', search: '', hash: '' } }))

  it('rates each criterion of each Scorer by configuration', () => {
    const html = renderToStaticMarkup(createElement(ScoresPanel, { tasks: story.tasks, loaded: true, problems: [] }))
    expect(text(html)).toContain('Scorer tests-and-why · 2 judged')
    const rows = [...html.matchAll(/<tr><th scope="row" class="score-criterion">(.*?)<\/tr>/g)].map((m) =>
      text(m[1]).trim(),
    )
    expect(rows).toEqual([
      'The change has a test that fails without it. 100% (2 of 2)',
      'The pull request’s description says why the change is made, not only what it does. 50% (1 of 2)',
    ])
    expect(html).toContain('<th scope="col">claude-code · claude-sonnet-5-5</th>')
  })

  it('says unclear verdicts apart, and a dash when none was judged either way', () => {
    expect(describeRate({ pass: 3, fail: 1, unclear: 2, rate: 0.75 })).toBe('75% (3 of 4), 2 unclear')
    expect(describeRate({ pass: 0, fail: 0, unclear: 1 })).toBe('–, 1 unclear')
    expect(describeRate(undefined)).toBe('–')
  })

  it('says when no Scorer judged anything', () => {
    const html = renderToStaticMarkup(createElement(ScoresPanel, { tasks: [], loaded: true, problems: [] }))
    expect(text(html)).toContain('No Scorer has judged a Task yet')
  })

  it("shows a judged Task's Scores on its page, each verdict with its why and a link to the judge run", () => {
    const html = renderToStaticMarkup(createElement(TaskScores, { task: named('orders-api-11'), tasks: story.tasks }))
    expect(text(html)).toContain('Scorer tests-and-why · its judge run')
    expect(text(html)).toContain(
      'Fail The pull request’s description says why the change is made, not only what it does. · The description lists the endpoint and its fields, but not why.',
    )
    expect(html).toContain('name=tests-and-why-orders-api-11')
    expect(renderToStaticMarkup(createElement(TaskScores, { task: named('orders-api-14'), tasks: story.tasks }))).toBe(
      '',
    )
  })

  it("shows a Score Task's verdicts and what it judged on its own page", () => {
    const html = renderToStaticMarkup(createElement(ScoreTaskVerdicts, { task: named('tests-and-why-orders-api-10') }))
    expect(text(html)).toContain(
      'Scorer tests-and-why’s judge on Task orders-api-10 , whose implementer ran on claude-code · claude-sonnet-5-5.',
    )
    expect(html.match(/badge badge-ok/g)).toHaveLength(2)
    expect(renderToStaticMarkup(createElement(ScoreTaskVerdicts, { task: named('orders-api-10') }))).toBe('')
  })
})
