---
title: Mark CEL in other YAML with a comment
labels: status/proposed, kind/feature
milestone: Later
---
## Context

Some tools keep CEL in YAML the server doesn't know (custom operators, Helm
values). Authors could mark it with a comment on the line above.

## Goal

A `# cel` comment (with optional declared variables, such as `# cel: self, oldSelf`)
makes the next scalar a CEL expression, checked with `dyn` variables.

## Acceptance criteria

- [ ] The marker works for plain, quoted and block scalars, and is documented.
- [ ] Unmarked unknown YAML still yields nothing.

## Notes

Triage: agree on the marker's syntax with users first.
