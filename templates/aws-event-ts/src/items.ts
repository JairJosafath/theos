/**
 * The consumer's rules, free of AWS: what an item is and which events carry
 * one. Nothing here imports the AWS SDK or the Lambda types, so the tests
 * need neither.
 */

/** An item as an event's `detail` carries it and the consumer stores it. */
export interface Item {
  readonly id: string;
  readonly name: string;
  readonly quantity: number;
}

/** An EventBridge event that carries an item: what the consumer reads of it. */
export interface ItemEvent {
  /** The event's id, which EventBridge and Lambda keep on every retry: its key. */
  readonly id: string;
  /** Who sent it, and what kind of event it is, as the rule matched them. */
  readonly source: string;
  readonly detailType: string;
  /** The item its `detail` carries. */
  readonly item: Item;
}

/** The outcome of checking untrusted input: the value, or why it was refused. */
export type Checked<T> =
  { readonly ok: true; readonly value: T } | { readonly ok: false; readonly error: string };

// Ids end up in DynamoDB keys and in what reads them: a short, URL-safe alphabet.
const ID_PATTERN = /^[A-Za-z0-9_-]{1,64}$/;
// A name is for people to read; DynamoDB items can be 400 KB, names can't.
const MAX_NAME_LENGTH = 200;

/** Checks an id. */
export function checkId(raw: unknown): Checked<string> {
  if (typeof raw !== 'string' || !ID_PATTERN.test(raw)) {
    return { ok: false, error: 'The id must be 1 to 64 letters, digits, "-" or "_".' };
  }
  return { ok: true, value: raw };
}

// EventBridge's own fields are strings; none is empty, and none this long.
const MAX_FIELD_LENGTH = 256;

/** Whether `value` is a JSON object, not an array or null. */
function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Checks an item: a JSON object with an id, a name and a quantity. */
export function checkItem(value: unknown): Checked<Item> {
  if (!isObject(value)) {
    return { ok: false, error: 'The item must be a JSON object.' };
  }
  const id = checkId(value['id']);
  if (!id.ok) {
    return { ok: false, error: `"id": ${id.error}` };
  }
  return checkFields(id.value, value);
}

/** Checks one of an EventBridge event's own fields: a string, not empty. */
function checkField(event: Readonly<Record<string, unknown>>, field: string): Checked<string> {
  const value = event[field];
  if (typeof value !== 'string' || value === '' || value.length > MAX_FIELD_LENGTH) {
    return {
      ok: false,
      error: `"${field}" must be a string of 1 to ${String(MAX_FIELD_LENGTH)} characters.`,
    };
  }
  return { ok: true, value };
}

/**
 * Checks the event the rule sends: an EventBridge event, with its `id`,
 * `source` and `detail-type`, whose `detail` is an item.
 */
export function checkEvent(event: unknown): Checked<ItemEvent> {
  if (!isObject(event)) {
    return { ok: false, error: 'The event must be a JSON object.' };
  }
  const id = checkField(event, 'id');
  if (!id.ok) {
    return id;
  }
  const source = checkField(event, 'source');
  if (!source.ok) {
    return source;
  }
  const detailType = checkField(event, 'detail-type');
  if (!detailType.ok) {
    return detailType;
  }
  const item = checkItem(event['detail']);
  if (!item.ok) {
    return { ok: false, error: `"detail": ${item.error}` };
  }
  return {
    ok: true,
    value: { id: id.value, source: source.value, detailType: detailType.value, item: item.value },
  };
}

/** Checks that `value`, a JSON object, has an item's name and quantity. */
function checkFields(id: string, value: Readonly<Record<string, unknown>>): Checked<Item> {
  const { name, quantity } = value;
  if (typeof name !== 'string' || name.trim() === '' || name.length > MAX_NAME_LENGTH) {
    return {
      ok: false,
      error: `"name" must be a non-empty string of at most ${String(MAX_NAME_LENGTH)} characters.`,
    };
  }
  if (typeof quantity !== 'number' || !Number.isSafeInteger(quantity) || quantity < 0) {
    return { ok: false, error: '"quantity" must be a whole number, 0 or more.' };
  }
  return { ok: true, value: { id, name: name.trim(), quantity } };
}
