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

/** A line written to a LogOutputChannel. */
export interface LogLine {
  level: 'trace' | 'debug' | 'info' | 'warn' | 'error';
  message: string;
  args: unknown[];
}

/** Fails a test that uses a part of the API the stand-in doesn't have yet. */
function missing(member: string): never {
  throw new Error(`the vscode stand-in has no ${member} yet: add it in test/vscode.ts`);
}

/** Keeps what is logged to it in `lines`, oldest first, instead of showing it. */
export class LogOutputChannel implements vscode.LogOutputChannel {
  readonly lines: LogLine[] = [];
  disposed = false;

  constructor(readonly name: string) {}

  get logLevel(): vscode.LogLevel {
    return missing('LogOutputChannel.logLevel');
  }
  onDidChangeLogLevel(): vscode.Disposable {
    return missing('LogOutputChannel.onDidChangeLogLevel');
  }
  trace(message: string, ...args: unknown[]): void {
    this.lines.push({ level: 'trace', message, args });
  }
  debug(message: string, ...args: unknown[]): void {
    this.lines.push({ level: 'debug', message, args });
  }
  info(message: string, ...args: unknown[]): void {
    this.lines.push({ level: 'info', message, args });
  }
  warn(message: string, ...args: unknown[]): void {
    this.lines.push({ level: 'warn', message, args });
  }
  error(error: string | Error, ...args: unknown[]): void {
    this.lines.push({ level: 'error', message: String(error), args });
  }
  append(): void {
    missing('LogOutputChannel.append');
  }
  appendLine(): void {
    missing('LogOutputChannel.appendLine');
  }
  replace(): void {
    missing('LogOutputChannel.replace');
  }
  clear(): void {
    missing('LogOutputChannel.clear');
  }
  show(): void {
    missing('LogOutputChannel.show');
  }
  hide(): void {
    missing('LogOutputChannel.hide');
  }
  dispose(): void {
    this.disposed = true;
  }
}

/** The channels made with window.createOutputChannel, oldest first. */
export const outputChannels: LogOutputChannel[] = [];

export const window = {
  showInformationMessage(message: string): Promise<undefined> {
    shownMessages.push(message);
    return Promise.resolve(undefined);
  },
  // Only the { log: true } form: an extension logs, it doesn't print.
  createOutputChannel(name: string, options?: string | { log: true }): LogOutputChannel {
    if (typeof options !== 'object') {
      missing('createOutputChannel without { log: true }');
    }
    const channel = new LogOutputChannel(name);
    outputChannels.push(channel);
    return channel;
  },
} satisfies Partial<typeof vscode.window>;

export const env = {
  appName: 'Visual Studio Code',
} satisfies Partial<typeof vscode.env>;

/** Forgets every registration, message and channel, for the next test. */
export function reset(): void {
  registry.clear();
  shownMessages.length = 0;
  outputChannels.length = 0;
}
