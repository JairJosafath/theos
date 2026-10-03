---
title: Start the language server for YAML files
labels: status/ready, kind/feature
milestone: M3 VS Code
---
## Context

The extension is how most policy authors get CEL Lens: it starts the CEL
language server and connects VS Code to it (see the Relationship with
cel-language-server: `cel-language-server serve --stdio`, LSP 3.17). The
server's repository is in `/workspace/related/cel-language-server` when you work
on this issue; it may not have the `serve` command yet, so don't depend on
running it.

## Goal

On opening a YAML file, the extension finds the server binary, starts it with
`vscode-languageclient`, and connects it to YAML documents.

## Acceptance criteria

- [ ] Activation on `onLanguage:yaml` only; nothing heavy runs before.
- [ ] The binary is found in this order: the setting `celLens.server.path`; a binary bundled in the extension at `server/<platform>-<arch>/cel-language-server[.exe]`; `cel-language-server` on `PATH`. The lookup is a pure module with unit tests for each case, on Linux, macOS and Windows paths.
- [ ] Without a binary, one error notification says what is missing and offers "Open Settings"; the extension doesn't retry in a loop.
- [ ] The client starts `<binary> serve --stdio` with document selector `{language: yaml, scheme: file}` and `{language: yaml, scheme: untitled}`, and logs to an output channel named "CEL Lens".
- [ ] A command "CEL Lens: Restart Server" restarts the client.
- [ ] Everything created is disposed through `context.subscriptions`; `deactivate` stops the client.
- [ ] Unit tests use the `vscode` stand-in from the template; `make check` passes, including the packaging check.

## Out of scope

Bundling the binaries (a later issue), syntax highlighting, settings other than
the server path.

## Notes

- Add `vscode-languageclient` (the `node` entry point) as a dependency; esbuild
  bundles it.
- Replace the template's example `sayHello` command and its greeting module;
  they were only there to show the layout.
