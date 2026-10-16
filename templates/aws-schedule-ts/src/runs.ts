/**
 * The job's rules, free of AWS: what a scheduled run is and which events
 * name one. Nothing here imports the AWS SDK or the Lambda types, so the
 * tests need neither.
 */

/** One run of the job, as the schedule names it. */
export interface Run {
  /** When the schedule meant it to run, `YYYY-MM-DDTHH:MM:SSZ`: the run's key. */
  readonly scheduledTime: string;
  /** Which delivery of the run this is, from 1: EventBridge Scheduler counts its retries. */
  readonly attempt: number;
}

/** What the job keeps of a run that finished. */
export interface RunRecord {
  readonly id: string;
  readonly attempt: number;
  readonly finishedAt: string;
}

/** The outcome of checking untrusted input: the value, or why it was refused. */
export type Checked<T> =
  { readonly ok: true; readonly value: T } | { readonly ok: false; readonly error: string };

// EventBridge Scheduler's <aws.scheduler.scheduled-time>: UTC, to the second.
const SCHEDULED_TIME_PATTERN = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/;
// The schedule retries at most 185 times; one more is the first delivery.
const MAX_ATTEMPT = 186;

/** Checks the scheduled time of a run. */
export function checkScheduledTime(raw: unknown): Checked<string> {
  if (
    typeof raw !== 'string' ||
    !SCHEDULED_TIME_PATTERN.test(raw) ||
    Number.isNaN(Date.parse(raw)) ||
    new Date(raw).toISOString() !== raw.replace('Z', '.000Z')
  ) {
    return { ok: false, error: '"scheduledTime" must be a UTC time, YYYY-MM-DDTHH:MM:SSZ.' };
  }
  return { ok: true, value: raw };
}

/**
 * Checks the event the schedule sends: a JSON object with the run's
 * `scheduledTime` and its `attemptNumber`, which the schedule's input fills
 * in as a string.
 */
export function checkEvent(event: unknown): Checked<Run> {
  if (typeof event !== 'object' || event === null || Array.isArray(event)) {
    return { ok: false, error: 'The event must be a JSON object.' };
  }
  const fields = event as Record<string, unknown>;
  const scheduledTime = checkScheduledTime(fields['scheduledTime']);
  if (!scheduledTime.ok) {
    return scheduledTime;
  }
  const raw = fields['attemptNumber'];
  const attempt = typeof raw === 'string' && /^\d{1,3}$/.test(raw) ? Number(raw) : raw;
  if (
    typeof attempt !== 'number' ||
    !Number.isSafeInteger(attempt) ||
    attempt < 1 ||
    attempt > MAX_ATTEMPT
  ) {
    return {
      ok: false,
      error: `"attemptNumber" must be a whole number from 1 to ${String(MAX_ATTEMPT)}.`,
    };
  }
  return { ok: true, value: { scheduledTime: scheduledTime.value, attempt } };
}

/** What the job keeps of `run`, finished at `finishedAt`. */
export function recordOf(run: Run, finishedAt: Date): RunRecord {
  return { id: run.scheduledTime, attempt: run.attempt, finishedAt: finishedAt.toISOString() };
}
