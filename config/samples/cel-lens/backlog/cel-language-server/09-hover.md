---
title: Explain variables, fields and functions on hover
labels: status/ready, kind/feature
milestone: M2 Completion and hover
depends: 08-completion
---
## Context

Reading a policy means knowing what `request.userInfo` or `object.spec.minReadySeconds`
is. Hover answers that in place (see the system's needs).

## Goal

`textDocument/hover` inside CEL expressions: what the thing under the cursor is,
its type, and its documentation.

## Acceptance criteria

- [ ] On a variable: its type and what it holds (`object`: "the object from the incoming request; null for DELETE", and so on for each variable of each place).
- [ ] On a field: its CEL type and the schema's description.
- [ ] On a function or macro: its overloads' signatures and its documentation, where cel-go has some.
- [ ] Hover ranges cover exactly the identifier under the cursor; outside CEL there is no hover.
- [ ] Tests through the LSP integration harness for each kind of hover, in a policy and in a CRD rule.

## Out of scope

Documentation for the Kubernetes library functions that cel-go doesn't document
(next issue).

## Notes

- Share the "what is at this position" logic with completion; don't duplicate it.
- The variables' meanings are on https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/
  and https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules.
