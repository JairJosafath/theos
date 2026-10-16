/**
 * The worker's rules, free of AWS: what an item is and which messages are
 * valid. Nothing here imports the AWS SDK or the Lambda types, so the tests
 * need neither.
 */

/** An item as a message carries it and the worker stores it. */
export interface Item {
  readonly id: string;
  readonly name: string;
  readonly quantity: number;
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

/** Checks a message's body: a JSON object with an id, a name and a quantity. */
export function checkMessage(body: string): Checked<Item> {
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    return { ok: false, error: 'The body must be JSON.' };
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return { ok: false, error: 'The body must be a JSON object.' };
  }
  const fields = parsed as Record<string, unknown>;
  const id = checkId(fields['id']);
  if (!id.ok) {
    return { ok: false, error: `"id": ${id.error}` };
  }
  return checkFields(id.value, fields);
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
