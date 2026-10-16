import { describe, expect, it } from 'vitest';

import { checkEvent, checkScheduledTime, recordOf } from './runs';

describe('checkScheduledTime', () => {
  it.each(['2026-10-16T03:00:00Z', '2024-02-29T23:59:59Z'])('accepts %s', (raw) => {
    expect(checkScheduledTime(raw)).toEqual({ ok: true, value: raw });
  });

  it.each([
    ['a number', 1760583600],
    ['no zone', '2026-10-16T03:00:00'],
    ['an offset', '2026-10-16T03:00:00+02:00'],
    ['milliseconds', '2026-10-16T03:00:00.000Z'],
    ['a day that is not', '2026-02-30T03:00:00Z'],
    ['an hour that is not', '2026-10-16T24:00:00Z'],
    ['the placeholder', '<aws.scheduler.scheduled-time>'],
  ])('refuses %s', (_, raw) => {
    expect(checkScheduledTime(raw)).toEqual({
      ok: false,
      error: '"scheduledTime" must be a UTC time, YYYY-MM-DDTHH:MM:SSZ.',
    });
  });
});

describe('checkEvent', () => {
  it('reads the scheduled time and the attempt the schedule fills in', () => {
    expect(checkEvent({ scheduledTime: '2026-10-16T03:00:00Z', attemptNumber: '2' })).toEqual({
      ok: true,
      value: { scheduledTime: '2026-10-16T03:00:00Z', attempt: 2 },
    });
  });

  it('takes an attempt given as a number', () => {
    expect(checkEvent({ scheduledTime: '2026-10-16T03:00:00Z', attemptNumber: 1 })).toMatchObject({
      ok: true,
      value: { attempt: 1 },
    });
  });

  it.each([
    ['null', null],
    ['an array', []],
    ['a string', '2026-10-16T03:00:00Z'],
  ])('refuses %s', (_, event) => {
    expect(checkEvent(event)).toEqual({ ok: false, error: 'The event must be a JSON object.' });
  });

  it('refuses an event without a scheduled time', () => {
    expect(checkEvent({ attemptNumber: '1' })).toMatchObject({
      ok: false,
      error: expect.stringContaining('"scheduledTime"') as unknown,
    });
  });

  it.each([
    ['missing', undefined],
    ['0', '0'],
    ['187', '187'],
    ['a fraction', 1.5],
    ['words', 'one'],
  ])('refuses an attempt that is %s', (_, attemptNumber) => {
    expect(checkEvent({ scheduledTime: '2026-10-16T03:00:00Z', attemptNumber })).toEqual({
      ok: false,
      error: '"attemptNumber" must be a whole number from 1 to 186.',
    });
  });
});

describe('recordOf', () => {
  it('keys the record by the scheduled time', () => {
    expect(
      recordOf(
        { scheduledTime: '2026-10-16T03:00:00Z', attempt: 1 },
        new Date('2026-10-16T03:00:04.250Z'),
      ),
    ).toEqual({ id: '2026-10-16T03:00:00Z', attempt: 1, finishedAt: '2026-10-16T03:00:04.250Z' });
  });
});
