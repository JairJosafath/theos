import { describe, expect, it } from 'vitest';

import { checkingFirst, Malformed, reporting, runJob, runOf } from './job';
import type { Report, Work } from './job';
import type { Run, RunRecord } from './runs';
import type { RunStore } from './run_store';

/** A stand-in for the table: keeps what it is given, or fails with `failure`. */
class FakeStore implements RunStore {
  readonly records: RunRecord[] = [];

  constructor(private readonly failure?: Error) {}

  put(record: RunRecord): Promise<void> {
    if (this.failure !== undefined) {
      return Promise.reject(this.failure);
    }
    this.records.push(record);
    return Promise.resolve();
  }
}

/** A report that records what it is told. */
class Recorder implements Report {
  readonly lines: string[] = [];

  completed(run: Run): void {
    this.lines.push(`completed ${run.scheduledTime}`);
  }

  failed(_: unknown, error: unknown): void {
    this.lines.push(`failed: ${error instanceof Error ? error.message : 'unknown'}`);
  }
}

const NIGHTLY = { scheduledTime: '2026-10-16T03:00:00Z', attemptNumber: '1' };
const NOW = (): Date => new Date('2026-10-16T03:00:04.250Z');

describe('runJob', () => {
  it('keeps a record of the run, keyed by its scheduled time', async () => {
    const store = new FakeStore();

    await expect(runJob(NIGHTLY, store, NOW)).resolves.toEqual({
      scheduledTime: '2026-10-16T03:00:00Z',
      attempt: 1,
    });

    expect(store.records).toEqual([
      { id: '2026-10-16T03:00:00Z', attempt: 1, finishedAt: '2026-10-16T03:00:04.250Z' },
    ]);
  });

  it('throws Malformed for an event that names no run, and keeps nothing', async () => {
    const store = new FakeStore();

    const ran = runJob({ attemptNumber: '1' }, store, NOW);

    await expect(ran).rejects.toBeInstanceOf(Malformed);
    await expect(ran).rejects.toThrow('The event names no run: "scheduledTime" must be');
    expect(store.records).toEqual([]);
  });

  it('throws what the store throws', async () => {
    await expect(runJob(NIGHTLY, new FakeStore(new Error('throttled')), NOW)).rejects.toThrow(
      'throttled',
    );
  });
});

describe('runOf', () => {
  it('names the run of a valid event', () => {
    expect(runOf(NIGHTLY)).toEqual({ scheduledTime: '2026-10-16T03:00:00Z', attempt: 1 });
  });
});

describe('checkingFirst', () => {
  it('runs the work on an event that names a run', async () => {
    const seen: unknown[] = [];

    await checkingFirst((event) => {
      seen.push(event);
      return Promise.resolve();
    })(NIGHTLY);

    expect(seen).toEqual([NIGHTLY]);
  });

  it('refuses a malformed event before the work records it', async () => {
    const seen: unknown[] = [];
    const work: Work = (event) => {
      seen.push(event);
      return Promise.resolve();
    };

    await expect(checkingFirst(work)({ scheduledTime: 'tonight' })).rejects.toBeInstanceOf(
      Malformed,
    );
    expect(seen).toEqual([]);
  });
});

describe('reporting', () => {
  it('reports a run that finished', async () => {
    const recorder = new Recorder();

    await reporting(() => Promise.resolve(), recorder)(NIGHTLY);

    expect(recorder.lines).toEqual(['completed 2026-10-16T03:00:00Z']);
  });

  it('reports a run that failed and throws again, so Lambda retries it', async () => {
    const recorder = new Recorder();

    await expect(
      reporting(() => Promise.reject(new Error('throttled')), recorder)(NIGHTLY),
    ).rejects.toThrow('throttled');

    expect(recorder.lines).toEqual(['failed: throttled']);
  });
});
