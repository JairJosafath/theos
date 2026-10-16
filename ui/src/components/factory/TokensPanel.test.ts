import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { demoStory } from '../../demo/fixture'
import type { Task } from '../../k8s/types'
import { TokensPanel } from './TokensPanel'

const text = (html: string) =>
  html
    .replace(/<[^>]+>/g, ' ')
    .replace(/&#x27;/g, "'")
    .replace(/\s+/g, ' ')

const story = demoStory(Date.parse('2026-10-16T04:00:00Z'))

function panel(tasks: Task[] = story.tasks, loaded = true) {
  return renderToStaticMarkup(
    createElement(TokensPanel, {
      tasks,
      agents: story.agents,
      loaded,
      problems: [],
      pageHref: (ns: string, kind: string, name: string) => `/?ns=${ns}&kind=${kind}&name=${name}`,
    }),
  )
}

describe('What agent runs use (#1086)', () => {
  it('charts the Agent with the most runs against its limit, and offers the others', () => {
    const html = panel()
    expect(html).toContain(
      'role="group" aria-label="7 runs of Claude Code, Sonnet (implementer), largest 142k against a 130k limit"',
    )
    expect(text(html)).toContain('limit 130k')
    expect(html).toMatch(
      /<option value="webshop\/claude-implementer" selected="">Claude Code, Sonnet \(implementer\) · limit 130k<\/option>/,
    )
    expect(html).toContain('Claude Code, Opus (reviewer) · limit 130k</option>')
  })

  it('marks each run by how its Task ended, and links it to the Task', () => {
    const html = panel()
    expect(html.match(/class="tokens-mark tokens-merged"/g)).toHaveLength(3 + 2) // the legend's Merged and Handed off keys
    expect(html.match(/class="tokens-mark tokens-failed"/g)).toHaveLength(3 + 1)
    expect(html.match(/class="tokens-mark tokens-active"/g)).toHaveLength(1 + 1)
    expect(html.match(/class="tokens-halo"/g)).toHaveLength(1 + 1)
    expect(html).toContain(
      '<a href="/?ns=webshop&amp;kind=Task&amp;name=orders-api-13" class="tokens-run" aria-label="#13 Refund an order',
    )
  })

  it('names the run, what it used and how its Task ended when pointed at', () => {
    expect(text(panel())).toContain(
      '#13 Refund an order through the payment provider (orders-api-13) · round 2, implement · largest 142k, past the 130k limit · 5M tokens in all · handed off 2 times, at 131k and 138k tokens · the Task failed after 3 rounds',
    )
  })

  it('tables the same figures by outcome, for a reader who can’t see the chart', () => {
    const rows = [...panel().matchAll(/<tr><th scope="row">(.*?)<\/tr>/g)].map((m) => text(m[1]).trim())
    expect(rows).toEqual([
      'Merged 2 1.5 3 96k 1.8M 0 0',
      'Failed 1 3.0 3 121k 3.1M 1 1',
      'Still going 1 2.0 1 79k 1.2M 0 0',
    ])
  })

  it('says when no run has ended, or none reported its tokens, instead of an empty chart', () => {
    expect(text(panel([]))).toContain('No agent run has ended yet')
    const standIn = story.tasks.map((t) => ({
      ...t,
      status: { ...t.status, runs: t.status?.runs?.map((r) => ({ ...r, tokens: undefined })) },
    }))
    const html = panel(standIn)
    expect(html).not.toContain('tokens-chart')
    expect(text(html)).toContain('None of the 12 agent runs that ended reported its tokens yet')
    expect(text(panel([], false))).toContain('Loading the agent runs…')
  })
})
