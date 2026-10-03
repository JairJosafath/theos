---
name: Task
about: One piece of work an agent can finish in one pull request, with acceptance criteria
title: ""
labels: ["status/proposed"]
---

<!--
The factory picks up an issue only when it is labelled status/ready, its
dependencies are closed, and its "Acceptance criteria" section holds at least
one unchecked checklist item. Without one, it labels the issue
status/needs-human and comments, before any agent runs.

Size it for one pull request: a few files, one concern.
-->

## Context

<!-- Why this matters: the need of the system it serves (from the C4 story) and what exists today. -->

## Goal

<!-- One paragraph: what is true when this is done. -->

## Acceptance criteria

<!--
Observable, testable outcomes, one unchecked checklist item each. Each becomes
a test, and the reviewer reports each one as met or not met. For example:
`check policy.yaml` exits 1 and prints `policy.yaml:12:7: undefined field 'replica'`.
-->

## Out of scope

<!-- What this issue doesn't do, so it stays one pull request. -->

## Notes

<!-- Pointers: files, packages, docs, libraries to use, known pitfalls. -->

## Dependencies

<!-- Issues that must be closed first. Add them as Gitea dependencies in the sidebar too: the factory reads those, not this text. -->
