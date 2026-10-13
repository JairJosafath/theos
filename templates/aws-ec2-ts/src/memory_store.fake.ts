import type { ItemStore } from './item_store';
import type { Item } from './items';

/**
 * An in-memory store for tests; `failing` makes every call throw, as a
 * DynamoDB outage would.
 */
export class MemoryStore implements ItemStore {
  readonly items = new Map<string, Item>();

  constructor(private readonly failing = false) {}

  get(id: string): Promise<Item | undefined> {
    return this.failing
      ? Promise.reject(new Error('timeout'))
      : Promise.resolve(this.items.get(id));
  }

  put(item: Item): Promise<void> {
    if (this.failing) {
      return Promise.reject(new Error('timeout'));
    }
    this.items.set(item.id, item);
    return Promise.resolve();
  }
}
