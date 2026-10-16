import { FullBatchFailureError } from '@aws-lambda-powertools/batch';
import type { SQSEvent, SQSRecord } from 'aws-lambda';
import { describe, expect, it } from 'vitest';

import { consumeBatch } from './batch';
import type { Message } from './consume';

/** An SQS record carrying `body`, as the event source mapping delivers it. */
function record(messageId: string, body: string): SQSRecord {
  return {
    messageId,
    receiptHandle: `handle-${messageId}`,
    body,
    attributes: {
      ApproximateReceiveCount: '1',
      SentTimestamp: '1767225600000',
      SenderId: 'AIDAEXAMPLE',
      ApproximateFirstReceiveTimestamp: '1767225600000',
    },
    messageAttributes: {},
    md5OfBody: '',
    eventSource: 'aws:sqs',
    eventSourceARN: 'arn:aws:sqs:eu-west-1:111111111111:test',
    awsRegion: 'eu-west-1',
  };
}

const EVENT: SQSEvent = {
  Records: [record('m1', 'one'), record('m2', 'two'), record('m3', 'three')],
};

describe('consumeBatch', () => {
  it('runs the work on every message, in order, and reports no failure when all pass', async () => {
    const seen: string[] = [];

    // Each message's work ends a turn later than it starts: run at once, the
    // starts and ends would interleave.
    const response = await consumeBatch(EVENT, async (message: Message) => {
      seen.push(`start ${message.messageId}=${message.body}`);
      await Promise.resolve();
      seen.push(`end ${message.messageId}`);
    });

    expect(seen).toEqual([
      'start m1=one',
      'end m1',
      'start m2=two',
      'end m2',
      'start m3=three',
      'end m3',
    ]);
    expect(response).toEqual({ batchItemFailures: [] });
  });

  it('reports only the message that failed, so it alone is delivered again', async () => {
    const response = await consumeBatch(EVENT, (message: Message) =>
      message.messageId === 'm2' ? Promise.reject(new Error('bad')) : Promise.resolve(),
    );

    expect(response).toEqual({ batchItemFailures: [{ itemIdentifier: 'm2' }] });
  });

  it('throws when every message fails, so the invocation fails', async () => {
    await expect(consumeBatch(EVENT, () => Promise.reject(new Error('bad')))).rejects.toThrow(
      FullBatchFailureError,
    );
  });
});
