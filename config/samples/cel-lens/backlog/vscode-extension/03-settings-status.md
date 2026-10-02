---
title: Settings and a status item for the server
labels: status/ready, kind/feature
milestone: M3 VS Code
depends: 01-start-server
---
## Context

When CEL Lens doesn't work, the author must see why without opening a terminal:
is the server running, did it fail to start, where are its logs?

## Goal

Contributed settings, documented, and a status bar item showing the server's state.

## Acceptance criteria

- [ ] Settings `celLens.enable` (default true), `celLens.server.path`, and `celLens.trace.server` (`off`, `messages`, `verbose`), each with a description; changing `enable` or `server.path` restarts or stops the server without a window reload.
- [ ] A status bar item, visible only for YAML editors, shows starting, running or failed (with the reason in its tooltip); clicking it opens the "CEL Lens" output channel.
- [ ] The state logic is a pure module with unit tests for every transition.
- [ ] The README lists the settings and the commands.

## Out of scope

A webview; telemetry (there is none).
