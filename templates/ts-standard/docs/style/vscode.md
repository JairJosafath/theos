# VS Code extension digest

Distilled from VS Code's extension guidelines, UX guidelines, activation events,
bundling and language server guides, and the Language Server Protocol 3.17 (see
`SOURCES.md`). `[lint: x]` = enforced by `make check`; `[review]` = check it
yourself.

## Activation

- Activate as late as possible, on the narrowest events that work:
  `onLanguage:yaml`, `workspaceContains:<glob>`. Never `*`; `onStartupFinished`
  only for background work that must start anyway. [review]
- Since VS Code 1.74, contributed commands, languages, views and custom
  editors activate the extension by themselves: don't list them in
  `activationEvents`. [review]
- `activate()` returns quickly: no synchronous file reads, no network, no
  waiting for work the user hasn't asked for. Start long work in the
  background and report it with progress. [review]

## Lifetime and disposal

- Everything `activate()` creates that VS Code can dispose goes into
  `context.subscriptions`: commands, providers, event listeners, file
  watchers, output channels, status bar items, the language client. [review]
- Add `deactivate()` only for asynchronous cleanup, and return its promise
  (`return client?.stop()`); VS Code waits for it. [review]
- Anything created later (per document, per request) is disposed by its owner
  when that ends. A second registration of the same command throws: that is
  how leaks show in tests. [review]

## The extension host

- Never block it: it runs every extension. No synchronous I/O on hot paths, no
  long loops on keystrokes; heavy work goes to the language server process. [review]
- Providers honour their `CancellationToken` and return quickly when it fires. [review]
- Use `vscode.workspace.fs` and `Uri`s for workspace files, so remote and
  virtual workspaces work; `node:fs` only for the extension's own files. [review]
- Support multi-root workspaces: no single "the workspace folder". [review]
- VS Code 1.100, the oldest `engines.vscode` allows, runs extensions on Node 20,
  while `@types/node` describes Node 24: use no Node API newer than Node 20. [review]
- Logs go to a `LogOutputChannel` named after the extension, never the
  console. [lint: no-console]

## The language client

- Use `vscode-languageclient` (10.x needs `engines.vscode` ^1.91, which ^1.100
  meets). It speaks LSP 3.17 to a server started over stdio:
  `{ command, args: ['serve', '--stdio'], transport: TransportKind.stdio }`. [review]
- Find the server in a fixed order, and log which one is used: the user's
  setting, then the binary shipped in the extension, then `PATH`. [review]
- The `documentSelector` is as narrow as the server's job:
  `{ scheme: 'file', language: 'yaml' }`. [review]
- `await client.start()` inside `try`. When the server is missing or crashes,
  show one actionable error ("Language server not found: set
  `<extension>.server.path` or reinstall the extension", with a button to
  the setting) and leave VS Code usable. [review]
- Contribute `<extension>.trace.server` (`off`, `messages`, `verbose`) so
  people can see the protocol in the output channel. [review]
- Don't redo the server's work in the client: diagnostics, completion and
  hover come from the server. [review]

## Language Server Protocol 3.17

- Lifecycle: `initialize` (requests before it get error -32002), `initialized`,
  then work; `shutdown`, then `exit`. [review]
- Positions are zero-based line and character. Characters count UTF-16 code
  units unless the client offered, and the server chose, another
  `positionEncoding`. Range ends are exclusive. Convert with care around
  non-ASCII text. [review]
- Text sync is Full or Incremental; every change raises the document version. [review]
- `textDocument/publishDiagnostics` replaces a document's diagnostics; an empty
  array clears them. [review]
- `$/cancelRequest` cancels; the server still answers, with RequestCancelled
  (-32800), or ContentModified (-32801) when the document changed under it. [review]
- Answer requests in roughly the order they came, unless reordering can't change
  the result. [review]
- Over stdio, stdout carries only protocol messages (`Content-Length` framed
  JSON-RPC); anything else breaks the connection. [review]

## UX

- **Notifications:** only when necessary, one at a time, never repeated, never
  for promotion or feedback on first install. Errors say what happened and
  offer the fix as a button. Recurring ones offer "Don't show again". [review]
- **Progress:** long operations show progress with a cancel action when they
  can stop, and a link to the logs; never left spinning. [review]
- **Modal dialogs** only when the user must decide now, never to show
  information. [review]
- **Status bar:** at most one item, short text, an icon only for a clear
  metaphor; contextual items on the right; no custom colours (the error and
  warning backgrounds only for blocking problems). [review]
- **Commands:** clear names in Title Case, all under one `category` (the
  extension's display name), no emojis; don't override existing keybindings. [review]
- **Settings:** prefixed with the extension's name; each has a default and a
  short `description` or `markdownDescription` (linking related settings); the
  right `scope`; listed in the README. No custom settings pages. [review]

## Bundling and packaging

- esbuild bundles `src/extension.ts` into one CommonJS file in `out/`, with
  `vscode` external. No dynamic `require`; native modules can't be bundled. [review]
- `.vscodeignore` keeps everything but the bundle and the documents out of the
  `.vsix`. [lint: make package-check packages it]
- `engines.vscode` is the oldest VS Code the extension supports;
  `@types/vscode` never exceeds it. [lint: make package-check (vsce)]
- `package.json`'s `version` is the release; each release has a CHANGELOG
  entry, and its tag matches the version. [lint: make dist refuses a mismatch; CHANGELOG review]

## Tests

- Logic lives in modules that don't import `vscode`; vitest tests them in plain
  Node. [review]
- Code that needs `vscode` is tested against `test/vscode.ts`, the stand-in.
  Extend it with only what tests need, typed with `satisfies` against the real
  API, and clear its state in `reset()`. [review]
- Integration tests in a real VS Code (`@vscode/test-electron`) only when an
  issue needs them; they download VS Code and need a display, so they are not
  part of `make check`. [review]
