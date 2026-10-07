/**
 * Returns the greeting the program prints for `name`, as given on the command
 * line. A blank name gets a plain hello.
 */
export function greeting(name: string): string {
  const trimmed = name.trim();
  return trimmed === '' ? 'Hello!' : `Hello, ${trimmed}!`;
}
