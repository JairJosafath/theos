/**
 * A stand-in for the `vscode` module, which exists only inside VS Code's
 * extension host. vitest.config.mts points `vscode` here, so unit tests run in
 * plain Node. Each part `satisfies` the real API's types, so a stand-in that
 * drifts from VS Code fails the type check. Add only what tests need, and
 * clear its state in reset().
 */
import type * as vscode from 'vscode';

/** Runs a callback once when disposed, like vscode.Disposable. */
export class Disposable implements vscode.Disposable {
  private disposed = false;

  constructor(private readonly onDispose: () => void) {}

  dispose(): void {
    if (!this.disposed) {
      this.disposed = true;
      this.onDispose();
    }
  }
}

const registry = new Map<string, (...args: unknown[]) => unknown>();

export const commands = {
  registerCommand(command: string, callback: (...args: unknown[]) => unknown): Disposable {
    // VS Code refuses a second registration, which is how leaks show.
    if (registry.has(command)) {
      throw new Error(`command '${command}' already exists`);
    }
    registry.set(command, callback);
    return new Disposable(() => registry.delete(command));
  },
  getCommands(): Promise<string[]> {
    return Promise.resolve([...registry.keys()]);
  },
} satisfies Partial<typeof vscode.commands>;

/** Runs a registered command, as vscode.commands.executeCommand does. */
export async function runCommand(command: string, ...args: unknown[]): Promise<unknown> {
  const callback = registry.get(command);
  if (callback === undefined) {
    throw new Error(`command '${command}' not found`);
  }
  return await callback(...args);
}

/** The messages shown with window.showInformationMessage, oldest first. */
export const shownMessages: string[] = [];

export const window = {
  showInformationMessage(message: string): Promise<undefined> {
    shownMessages.push(message);
    return Promise.resolve(undefined);
  },
} satisfies Partial<typeof vscode.window>;

export const env = {
  appName: 'Visual Studio Code',
} satisfies Partial<typeof vscode.env>;

/** Forgets every registration and message, for the next test. */
export function reset(): void {
  registry.clear();
  shownMessages.length = 0;
}
