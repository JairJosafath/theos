/**
 * What the consumer does with one event, free of the Lambda runtime: check
 * it, store the item it carries, and say how it went. The handler runs it
 * once per event id through Powertools' Idempotency, so an event delivered
 * again changes nothing twice.
 */
import { checkEvent } from './items';
import type { Item, ItemEvent } from './items';
import type { ItemStore } from './item_store';

/** The work done on one event; it throws when the event failed. */
export type Work = (event: unknown) => Promise<void>;

/** Where the consumer says how each event went: the handler's logs and metrics. */
export interface Report {
  consumed(event: ItemEvent): void;
  failed(event: unknown, error: unknown): void;
}

/**
 * An event no retry can make valid: a producer sent a detail that is no
 * item, or someone invoked the function by hand with something that is no
 * EventBridge event. It fails like any other event, and after Lambda's last
 * retry it waits in the dead-letter queue for a person.
 */
export class Malformed extends Error {
  constructor(reason: string) {
    super(`The event carries no item: ${reason}`);
    this.name = 'Malformed';
  }
}

/** The event `event` is, checked; throws `Malformed` when it carries no item. */
export function eventOf(event: unknown): ItemEvent {
  const checked = checkEvent(event);
  if (!checked.ok) {
    throw new Malformed(checked.error);
  }
  return checked.value;
}

/** The item `event` carries; throws `Malformed` when it carries none. */
export function itemOf(event: unknown): Item {
  return eventOf(event).item;
}

/**
 * Stores the item `event` carries, which is where a Container's own work
 * goes. Throws `Malformed` when it carries none, and what the store throws.
 */
export async function consume(event: unknown, store: ItemStore): Promise<void> {
  await store.put(itemOf(event));
}

/**
 * `work`, run only on an event that carries an item: a malformed one throws
 * `Malformed` before `work` (the idempotent store) records anything.
 */
export function checkingFirst(work: Work): Work {
  return async (event) => {
    eventOf(event);
    await work(event);
  };
}

/**
 * `work`, telling `report` of each event it consumed. The handler runs it
 * inside the idempotent wrapper, so an event delivered again, which its
 * record answers without running the work, is not counted twice.
 */
export function counting(work: Work, report: Pick<Report, 'consumed'>): Work {
  return async (event) => {
    await work(event);
    report.consumed(eventOf(event));
  };
}

/** `work`, telling `report` of each event that failed, and throwing on what it throws. */
export function reporting(work: Work, report: Pick<Report, 'failed'>): Work {
  return async (event) => {
    try {
      await work(event);
    } catch (error: unknown) {
      report.failed(event, error);
      throw error;
    }
  };
}
