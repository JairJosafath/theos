import { readFileSync } from 'node:fs';

import { afterEach, describe, expect, it } from 'vitest';
import type * as vscode from 'vscode';

import { commands, reset, runCommand, shownMessages } from '../test/vscode';
import { activate, SAY_HELLO_COMMAND } from './extension';

interface Manifest {
  contributes: { commands: { command: string }[] };
}

const manifest = JSON.parse(
  readFileSync(new URL('../package.json', import.meta.url), 'utf8'),
) as Manifest;

describe('activate', () => {
  afterEach(reset);

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
  });

  it('says hello from the editor', async () => {
    activate({ subscriptions: [] });

    await runCommand(SAY_HELLO_COMMAND);

    expect(shownMessages).toEqual(['Hello from Visual Studio Code!']);
  });
});
