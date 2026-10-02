---
title: Document the extension for users
labels: status/ready, kind/docs
milestone: M4 Release
depends: 02-highlight-cel, 03-settings-status
---
## Context

The README is the extension's page in the marketplace and in VS Code.

## Goal

A README and CHANGELOG a user can act on.

## Acceptance criteria

- [ ] What the extension does, which Kubernetes kinds and keys it understands, and that it works offline.
- [ ] Installing: the `.vsix` from a Gitea release (`code --install-extension`), and where the server binary comes from.
- [ ] Settings, commands and the status item, matching `package.json` (a test compares the README's settings table with the contributed settings).
- [ ] Troubleshooting: the output channel, trace setting, and how to report a problem.
- [ ] CHANGELOG lists what each milestone added.

## Out of scope

Screenshots (agents can't run VS Code); a marketplace listing.
