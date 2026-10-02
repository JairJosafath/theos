---
title: Support Kyverno CEL policies
labels: status/proposed, kind/feature
milestone: Later
---
## Context

Kyverno's `ValidatingPolicy` (policies.kyverno.io) mirrors ValidatingAdmissionPolicy
and adds libraries (HTTP, resource lookups, image data, global context).

## Goal

Find and check Kyverno policy expressions, with Kyverno's library functions
declared for type checking (not executed).

## Acceptance criteria

- [ ] Expressions of Kyverno's ValidatingPolicy are found and checked.
- [ ] Kyverno's library functions are declared with their signatures and documented in hover.

## Notes

Triage: pick the Kyverno version, decide whether to declare the libraries by hand
or import them, and split the issue before marking it ready.
