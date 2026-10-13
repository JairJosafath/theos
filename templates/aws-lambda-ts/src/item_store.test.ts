import { GetCommand, PutCommand } from '@aws-sdk/lib-dynamodb';
import type { GetCommandOutput, PutCommandOutput } from '@aws-sdk/lib-dynamodb';
import { describe, expect, it } from 'vitest';

import { DynamoDbItemStore } from './item_store';
import type { DocumentSender } from './item_store';

/** A stand-in for DynamoDB's document client: records commands, answers from a map. */
class FakeTable implements DocumentSender {
  readonly sent: (GetCommand | PutCommand)[] = [];

  constructor(private readonly rows: ReadonlyMap<string, Record<string, unknown>>) {}

  send(command: GetCommand | PutCommand): Promise<GetCommandOutput | PutCommandOutput> {
    this.sent.push(command);
    if (command instanceof GetCommand) {
      const id: unknown = command.input.Key?.['id'];
      const row = typeof id === 'string' ? this.rows.get(id) : undefined;
      return Promise.resolve({ $metadata: {}, Item: row });
    }
    return Promise.resolve({ $metadata: {} });
  }
}

describe('DynamoDbItemStore', () => {
  it('gets an item by id from its table', async () => {
    const table = new FakeTable(new Map([['i1', { id: 'i1', name: 'Lamp', quantity: 3 }]]));
    const store = new DynamoDbItemStore(table, 'items');

    expect(await store.get('i1')).toEqual({ id: 'i1', name: 'Lamp', quantity: 3 });
    expect(table.sent[0]?.input).toEqual({ TableName: 'items', Key: { id: 'i1' } });
  });

  it('answers undefined for a missing item', async () => {
    const store = new DynamoDbItemStore(new FakeTable(new Map()), 'items');

    expect(await store.get('i1')).toBeUndefined();
  });

  it('refuses a malformed row', async () => {
    const table = new FakeTable(new Map([['i1', { id: 'i1', name: 'Lamp' }]]));
    const store = new DynamoDbItemStore(table, 'items');

    await expect(store.get('i1')).rejects.toThrow('Item i1 in table items is malformed');
  });

  it('puts the whole item', async () => {
    const table = new FakeTable(new Map());
    const store = new DynamoDbItemStore(table, 'items');

    await store.put({ id: 'i1', name: 'Lamp', quantity: 3 });

    expect(table.sent[0]).toBeInstanceOf(PutCommand);
    expect(table.sent[0]?.input).toEqual({
      TableName: 'items',
      Item: { id: 'i1', name: 'Lamp', quantity: 3 },
    });
  });
});
