---
title: Serve diagnostics over LSP
labels: status/ready, kind/feature
milestone: M1 Diagnostics
depends: 04-check-policies
---
## Context

Editors run the server as `cel-language-server serve --stdio` and speak LSP 3.17
with it (see the Relationship with the VS Code extension). Diagnostics must
appear while the author types, within 200 ms (see the system's needs).

## Goal

The `serve --stdio` command: a language server that keeps open YAML documents in
memory and publishes CEL diagnostics for them.

## Acceptance criteria

- [ ] JSON-RPC 2.0 over stdin and stdout with `Content-Length` framing; logs go to stderr only.
- [ ] `initialize` answers with full text sync, and the server info with its version; `initialized`, `shutdown` and `exit` follow the spec (exit code 0 after shutdown, 1 without).
- [ ] `textDocument/didOpen`, `didChange` (full) and `didClose` keep the documents; diagnostics are published for `languageId` `yaml` documents, debounced per document (about 100 ms), and cleared on close.
- [ ] Unknown requests get `MethodNotFound`; unknown notifications are ignored; a malformed message is logged and skipped without stopping the server; `$/cancelRequest` is accepted.
- [ ] An integration test drives the server through an in-memory pipe: open a policy with an error, receive its diagnostic with the right range, fix it with `didChange`, receive an empty list, shut down.
- [ ] A test shows diagnostics for a 1,000-line file with 50 expressions arrive within 200 ms of the change on CI.

## Out of scope

Completion and hover (next milestone); pull diagnostics; workspace-wide checks.

## Notes

- No LSP framework: a small JSON-RPC package and the protocol types for the
  methods used, following https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/.
  It is a few hundred lines and fully testable, and avoids unmaintained Go LSP
  libraries.
- Keep the protocol package free of CEL; the server wires `analysis.Diagnose` in.
