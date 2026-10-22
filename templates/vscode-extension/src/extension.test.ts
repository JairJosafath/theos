import { readFileSync } from 'node:fs';

import { afterEach, describe, expect, it, vi } from 'vitest';
import type * as vscode from 'vscode';

import { commands, outputChannels, reset, runCommand, shownMessages, window } from '../test/vscode';
import { activate, LOG_NAME, SAY_HELLO_COMMAND } from './extension';

interface Manifest {
  contributes: { commands: { command: string }[] };
}

const manifest = JSON.parse(
  readFileSync(new URL('../package.json', import.meta.url), 'utf8'),
) as Manifest;

describe('activate', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    reset();
  });

  it('registers every command package.json contributes', async () => {
    activate({ subscriptions: [] });

    const registered = await commands.getCommands();
    for (const { command } of manifest.contributes.commands) {
      expect(registered).toContain(command);
    }
  });

  it('hands every registration to VS Code to dispose', async () => {
    const subscriptions: vscode.Disposable[] = [];
    activate({ subscriptions });

    for (const subscription of subscriptions) {
      subscription.dispose();
    }

    expect(await commands.getCommands()).toEqual([]);
    expect(outputChannels.map((channel) => channel.disposed)).toEqual([true]);
  });

  it('makes one log, named after the extension', () => {
    activate({ subscriptions: [] });

    expect(outputChannels.map((channel) => channel.name)).toEqual([LOG_NAME]);
  });

  it('says hello from the editor', async () => {
    activate({ subscriptions: [] });

    await runCommand(SAY_HELLO_COMMAND);

    expect(shownMessages).toEqual(['Hello from Visual Studio Code!']);
  });

  it('logs the command when it runs', async () => {
    activate({ subscriptions: [] });

    await runCommand(SAY_HELLO_COMMAND);

    expect(outputChannels[0]?.lines).toEqual([
      { level: 'info', message: `${SAY_HELLO_COMMAND} runs`, args: [] },
    ]);
  });

  it('logs the error when the command fails', async () => {
    const failure = new Error('no window');
    vi.spyOn(window, 'showInformationMessage').mockRejectedValueOnce(failure);
    activate({ subscriptions: [] });

    await expect(runCommand(SAY_HELLO_COMMAND)).rejects.toBe(failure);

    expect(outputChannels[0]?.lines.at(-1)).toEqual({
      level: 'error',
      message: `${SAY_HELLO_COMMAND} failed`,
      args: [failure],
    });
  });
});
