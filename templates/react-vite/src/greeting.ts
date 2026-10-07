/**
 * Returns the greeting the page shows for `name`, as typed by the visitor.
 * A blank name gets a plain hello.
 */
export function greeting(name: string): string {
  const trimmed = name.trim();
  return trimmed === '' ? 'Hello!' : `Hello, ${trimmed}!`;
}
