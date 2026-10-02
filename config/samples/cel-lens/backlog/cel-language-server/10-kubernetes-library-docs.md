---
title: Document the Kubernetes CEL library functions
labels: status/ready, kind/feature
milestone: M2 Completion and hover
depends: 09-hover
---
## Context

A spike found that 56 of the 158 functions in Kubernetes' CEL environment carry
documentation; the Kubernetes libraries (`quantity`, `url`, `ip`, `cidr`,
`isSorted`, `format`, `authorizer` checks and others) carry none, and they are the
ones authors know least.

## Goal

Documentation for every function of Kubernetes' CEL environment, shown in hover
and in completion items.

## Acceptance criteria

- [ ] Every function in the base environment of Kubernetes v1.37 has a one-paragraph description and at least one example, taken from or checked against https://kubernetes.io/docs/reference/using-api/cel/ and the library sources in `k8s.io/apiserver/pkg/cel/library`.
- [ ] A test lists the environment's functions and fails when one has no documentation, so a Kubernetes upgrade can't silently leave gaps.
- [ ] Each example compiles in the environment (a test compiles them all).
- [ ] Hover and completion show the documentation.

## Out of scope

Kyverno's libraries.
