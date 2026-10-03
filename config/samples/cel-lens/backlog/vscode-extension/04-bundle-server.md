---
title: Bundle the server in platform-specific packages
labels: status/proposed, kind/feature
milestone: M4 Release
depends: cel-language-server#11-user-docs
---
## Context

Installing a separate binary is the biggest hurdle for users. VS Code supports
platform-specific extension packages, each with its own binary.

## Goal

`make dist` builds one `.vsix` per platform (linux-x64, linux-arm64, darwin-x64,
darwin-arm64, win32-x64), each with the matching `cel-language-server` from a
pinned release of the server, checked against its `SHA256SUMS`.

## Acceptance criteria

- [ ] The server version is pinned in one place; the download is verified and cached.
- [ ] Each package finds its bundled binary (see "Start the language server").

## Notes

Triage: ready once cel-language-server has a release (a person tags it; only the
platform may push `v*` tags).
