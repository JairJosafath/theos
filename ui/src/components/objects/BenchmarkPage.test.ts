import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { demoStory } from '../../demo/fixture'
import type { Task } from '../../k8s/types'
import { benchmarkRuns, describeBenchmarkRate } from '../../model/benchmark'
import { BenchmarkPage } from './BenchmarkPage'

const text = (html: string) =>
  html
    .replace(/<[^>]+>/g, ' ')
    .replace(/&#x27;/g, "'")
    .replace(/\s+/g, ' ')

const story = demoStory(Date.parse('2026-10-16T04:00:00Z'))
const bench = story.benchmarks[0]

describe('A Benchmark’s page (measure-and-improve, step 7)', () => {
  // The links read the page's address.
  beforeEach(() => vi.stubGlobal('window', { location: { pathname: '/', search: '', hash: '' } }))

  it('says a rate with its interval, and a dash with none finished', () => {
    expect(describeBenchmarkRate({ of: 6, percent: 67, low: 30, high: 90 })).toBe('67% (4 of 6; 30 to 90%)')
    expect(describeBenchmarkRate({})).toBe('–')
    expect(describeBenchmarkRate(undefined)).toBe('–')
  })

  it('lists its runs in the order it made them, and no one else’s', () => {
    const run = (name: string, variant: string, goal: number, repetition: number, of = bench.metadata.name): Task => ({
      metadata: { name, namespace: bench.metadata.namespace },
      spec: {
        containerRef: { name: 'orders-api' },
        agentRef: { name: 'claude-implementer' },
        goal: 'x',
        benchmark: { benchmarkRef: { name: of }, variant, goal, repetition, base: bench.spec.base },
      },
    })
    const runs = benchmarkRuns(bench, [
      run('b-r2', 'implementer', 0, 2),
      run('a-r1', 'reviewer-model', 1, 1),
      run('other', 'implementer', 0, 1, 'another'),
      run('c-r1', 'implementer', 0, 1),
    ])
    expect(runs.map((t) => t.metadata.name)).toEqual(['c-r1', 'a-r1', 'b-r2'])
  })

  it('shows what it found: the comparison, each variant with its intervals, and its Scorers by variant', () => {
    const html = renderToStaticMarkup(createElement(BenchmarkPage, { bench, tasks: story.tasks }))
    expect(text(html)).toContain('No clear difference at 12 finished runs')
    const rows = [...html.matchAll(/<tr><th scope="row">(.*?)<\/tr>/g)].map((m) => text(m[1]).trim())
    expect(rows).toEqual([
      'implementer 6 of 6 67% (4 of 6; 30 to 90%) 1.8 31 min $1.42',
      'reviewer-model 6 of 6 83% (5 of 6; 44 to 97%) 1.3 26 min $2.31',
    ])
    expect(text(html)).toContain(
      'The change has a test that fails without it. (tests-and-why) 83% (5 of 6; 44 to 97%) 100% (5 of 5; 57 to 100%), 1 unclear',
    )
    expect(text(html)).toContain('2 goals × 2 variants × 3 repetitions, 2 at a time')
    expect(text(html)).toContain('$21.48 of $30')
  })
})
