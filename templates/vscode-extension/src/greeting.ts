/**
 * Returns the message the say-hello command shows in the editor named
 * `appName`, such as "Visual Studio Code". A blank name gets a plain hello.
 */
export function greeting(appName: string): string {
  const name = appName.trim();
  return name === '' ? 'Hello!' : `Hello from ${name}!`;
}
