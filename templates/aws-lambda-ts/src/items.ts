/**
 * The API's rules, free of AWS: what an item is, which ids and bodies are
 * valid, and the responses the handler returns. Nothing here imports the AWS
 * SDK or the Lambda types, so the tests need neither.
 */

/** An item as the API stores and returns it. */
export interface Item {
  readonly id: string;
  readonly name: string;
  readonly quantity: number;
}

/** The outcome of checking untrusted input: the value, or why it was refused. */
export type Checked<T> =
  { readonly ok: true; readonly value: T } | { readonly ok: false; readonly error: string };

/** An HTTP response, in the shape API Gateway's HTTP API reads from a Lambda. */
export interface HttpResponse {
  readonly statusCode: number;
  readonly headers: Readonly<Record<string, string>>;
  readonly body: string;
}

// Ids end up in URLs and DynamoDB keys: a short, URL-safe alphabet.
const ID_PATTERN = /^[A-Za-z0-9_-]{1,64}$/;
// A name is for people to read; DynamoDB items can be 400 KB, names can't.
const MAX_NAME_LENGTH = 200;

/** Checks an id from the path. */
export function checkId(raw: string | undefined): Checked<string> {
  if (raw === undefined || !ID_PATTERN.test(raw)) {
    return { ok: false, error: 'The id must be 1 to 64 letters, digits, "-" or "_".' };
  }
  return { ok: true, value: raw };
}

/** Checks a request body: a JSON object with a name and a quantity. */
export function checkItem(id: string, body: string | undefined): Checked<Item> {
  let parsed: unknown;
  try {
    parsed = JSON.parse(body ?? '');
  } catch {
    return { ok: false, error: 'The body must be JSON.' };
  }
  return checkFields(id, parsed);
}

/**
 * Checks that `value` has an item's fields. The store calls it too, on what
 * DynamoDB returns, so a row written by something else can't reach a client
 * unchecked.
 */
export function checkFields(id: string, value: unknown): Checked<Item> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return { ok: false, error: 'The body must be a JSON object.' };
  }
  const { name, quantity } = value as Record<string, unknown>;
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

/** A JSON response. */
export function json(statusCode: number, value: unknown): HttpResponse {
  return {
    statusCode,
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(value),
  };
}

/** An error response a client can read: the status and one message, never a stack trace. */
export function problem(statusCode: number, message: string): HttpResponse {
  return json(statusCode, { message });
}
