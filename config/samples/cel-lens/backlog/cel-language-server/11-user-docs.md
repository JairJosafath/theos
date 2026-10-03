---
title: Document installing and using the server
labels: status/ready, kind/docs
milestone: M4 Release
depends: 06-check-command, 07-lsp-diagnostics, 10-kubernetes-library-docs
---
## Context

A policy author arriving at the repository should be checking CEL within five
minutes, in their editor or in CI.

## Goal

A README that sells nothing and answers everything a user needs.

## Acceptance criteria

- [ ] What the server does and doesn't do, with the table of supported kinds and fields, and what is typed in each.
- [ ] How to install it: from a Gitea release (the `dist/` files `make dist` builds) and with `go install`.
- [ ] Editor setup: VS Code (the CEL Lens extension), and a working Neovim `vim.lsp.config` snippet.
- [ ] CI: the `check` command with exit codes and an Actions example.
- [ ] A short troubleshooting section: where logs go, how to report a wrong diagnostic.
- [ ] Every command and snippet in the README is run by a test or by `make check` (for example a test that runs the README's `check` example against testdata).

## Out of scope

A website.
