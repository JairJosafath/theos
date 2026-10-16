/**
 * What the worker does with one message, free of the Lambda runtime: check
 * it, store the item it carries, and say how it went. The handler hands each
 * message of a batch here through Powertools' batch processing, which
 * reports the ones that throw as failed, so SQS delivers only those again.
 */
import { checkMessage } from './items';
import type { Item } from './items';
import type { ItemStore } from './item_store';

/** What the worker reads of an SQS message: its id and its body. */
export interface Message {
  readonly messageId: string;
  readonly body: string;
}

/** The work done on one message; it throws when the message failed. */
export type Work = (message: Message) => Promise<void>;

/** Where the worker says how each message went: the handler's logs and metrics. */
export interface Report {
  consumed(messageId: string): void;
  failed(messageId: string, error: unknown): void;
}

/**
 * A message no retry can make valid. It fails like any other, and after the
 * queue's last delivery it waits in the dead-letter queue for a person.
 */
export class Malformed extends Error {
  constructor(messageId: string, reason: string) {
    super(`Message ${messageId} is malformed: ${reason}`);
    this.name = 'Malformed';
  }
}

/** The item `message` carries; throws `Malformed` when it carries none. */
export function itemOf(message: Message): Item {
  const checked = checkMessage(message.body);
  if (!checked.ok) {
    throw new Malformed(message.messageId, checked.error);
  }
  return checked.value;
}

/** Stores the item `message` carries; throws `Malformed` when it carries none, and what the store throws. */
export async function consume(message: Message, store: ItemStore): Promise<void> {
  await store.put(itemOf(message));
}

/**
 * `work`, run only on a message that carries an item: a malformed one
 * throws `Malformed` before `work` (the idempotent store) records anything.
 */
export function checkingFirst(work: Work): Work {
  return async (message) => {
    itemOf(message);
    await work(message);
  };
}

/** `work`, telling `report` how each message went, and throwing on what it throws. */
export function reporting(work: Work, report: Report): Work {
  return async (message) => {
    try {
      await work(message);
    } catch (error: unknown) {
      report.failed(message.messageId, error);
      throw error;
    }
    report.consumed(message.messageId);
  };
}
