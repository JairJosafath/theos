import { PutCommand } from '@aws-sdk/lib-dynamodb';
import type { PutCommandOutput } from '@aws-sdk/lib-dynamodb';
import { describe, expect, it } from 'vitest';

import { DynamoDbItemStore } from './item_store';
import type { DocumentSender } from './item_store';

/** A stand-in for DynamoDB's document client: records the commands it is sent. */
class FakeTable implements DocumentSender {
  readonly sent: PutCommand[] = [];

  send(command: PutCommand): Promise<PutCommandOutput> {
    this.sent.push(command);
    return Promise.resolve({ $metadata: {} });
  }
}

describe('DynamoDbItemStore', () => {
  it('puts the whole item', async () => {
    const table = new FakeTable();
    const store = new DynamoDbItemStore(table, 'items');

    await store.put({ id: 'i1', name: 'Lamp', quantity: 3 });

    expect(table.sent[0]).toBeInstanceOf(PutCommand);
    expect(table.sent[0]?.input).toEqual({
      TableName: 'items',
      Item: { id: 'i1', name: 'Lamp', quantity: 3 },
    });
  });
});
