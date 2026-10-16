import { PutCommand } from '@aws-sdk/lib-dynamodb';
import type { PutCommandOutput } from '@aws-sdk/lib-dynamodb';
import { describe, expect, it } from 'vitest';

import { DynamoDbRunStore } from './run_store';
import type { DocumentSender } from './run_store';

/** A stand-in for DynamoDB's document client: records the commands it is sent. */
class FakeTable implements DocumentSender {
  readonly sent: PutCommand[] = [];

  send(command: PutCommand): Promise<PutCommandOutput> {
    this.sent.push(command);
    return Promise.resolve({ $metadata: {} });
  }
}

describe('DynamoDbRunStore', () => {
  it('puts the whole record', async () => {
    const table = new FakeTable();
    const store = new DynamoDbRunStore(table, 'runs');

    await store.put({
      id: '2026-10-16T03:00:00Z',
      attempt: 1,
      finishedAt: '2026-10-16T03:00:04.250Z',
    });

    expect(table.sent[0]).toBeInstanceOf(PutCommand);
    expect(table.sent[0]?.input).toEqual({
      TableName: 'runs',
      Item: { id: '2026-10-16T03:00:00Z', attempt: 1, finishedAt: '2026-10-16T03:00:04.250Z' },
    });
  });
});
