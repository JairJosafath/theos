// A Benchmark's page's model (docs/plans/done/measure-and-improve.md, step 7):
// its runs and its rates as words.

import type { Benchmark, BenchmarkRate, Task } from '../k8s/types'
import { compareText } from './order'

/** "67% (2 of 3; 21 to 94%)", or "–" with none finished. */
export function describeBenchmarkRate(r: BenchmarkRate | undefined): string {
  if (!r?.of) return '–'
  return `${r.percent ?? 0}% (${Math.round(((r.percent ?? 0) * r.of) / 100)} of ${r.of}; ${r.low ?? 0} to ${r.high ?? 100}%)`
}

/** The Benchmark's runs, its Tasks, in the order it made them. */
export function benchmarkRuns(bench: Benchmark, tasks: Task[]): Task[] {
  return tasks
    .filter(
      (t) =>
        t.spec.benchmark?.benchmarkRef.name === bench.metadata.name &&
        (t.metadata.namespace ?? '') === (bench.metadata.namespace ?? ''),
    )
    .sort(
      (a, b) =>
        (a.spec.benchmark?.repetition ?? 0) - (b.spec.benchmark?.repetition ?? 0) ||
        (a.spec.benchmark?.goal ?? 0) - (b.spec.benchmark?.goal ?? 0) ||
        compareText(a.metadata.name, b.metadata.name),
    )
}
