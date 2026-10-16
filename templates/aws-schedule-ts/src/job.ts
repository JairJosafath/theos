/**
 * What the job does on one run, free of the Lambda runtime: check the event,
 * do the work, and say how it went. The handler runs it once per scheduled
 * time through Powertools' Idempotency, so a run delivered again changes
 * nothing twice.
 */
import { checkEvent, recordOf } from './runs';
import type { Run } from './runs';
import type { RunStore } from './run_store';

/** The work done on one event; it throws when the run failed. */
export type Work = (event: unknown) => Promise<void>;

/** Where the job says how each run went: the handler's logs and metrics. */
export interface Report {
  completed(run: Run): void;
  failed(event: unknown, error: unknown): void;
}

/**
 * An event no retry can make valid: the schedule's input was changed, or
 * someone invoked the function by hand. It fails like any other run, and
 * after Lambda's last retry it waits in the dead-letter queue for a person.
 */
export class Malformed extends Error {
  constructor(reason: string) {
    super(`The event names no run: ${reason}`);
    this.name = 'Malformed';
  }
}

/** The run `event` names; throws `Malformed` when it names none. */
export function runOf(event: unknown): Run {
  const checked = checkEvent(event);
  if (!checked.ok) {
    throw new Malformed(checked.error);
  }
  return checked.value;
}

/**
 * The job: here it keeps a record of each run, which is where a Container's
 * own work goes. Throws `Malformed` for an event that names no run, and what
 * the store throws.
 */
export async function runJob(event: unknown, store: RunStore, now: () => Date): Promise<Run> {
  const run = runOf(event);
  await store.put(recordOf(run, now()));
  return run;
}

/**
 * `work`, run only on an event that names a run: a malformed one throws
 * `Malformed` before `work` (the idempotent job) records anything.
 */
export function checkingFirst(work: Work): Work {
  return async (event) => {
    runOf(event);
    await work(event);
  };
}

/** `work`, telling `report` how each run went, and throwing on what it throws. */
export function reporting(work: Work, report: Report): Work {
  return async (event) => {
    try {
      await work(event);
    } catch (error: unknown) {
      report.failed(event, error);
      throw error;
    }
    report.completed(runOf(event));
  };
}
