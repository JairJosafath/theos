---
title: Support MutatingAdmissionPolicy
labels: status/proposed, kind/feature
milestone: Later
depends: 04-check-policies
---
## Context

MutatingAdmissionPolicy uses CEL in `matchConditions`, `variables`, and in
`mutations[].applyConfiguration.expression` and `mutations[].jsonPatch.expression`,
with `Object` construction syntax.

## Goal

Find, check, complete and explain MutatingAdmissionPolicy expressions as for
ValidatingAdmissionPolicy.

## Acceptance criteria

- [ ] The fields above are found for every served version of the kind in Kubernetes v1.37.
- [ ] They are checked with the API server's mutating environment (`k8s.io/apiserver/pkg/admission/plugin/policy/mutating`), with golden tests.

## Notes

Triage: confirm which versions v1.37 serves before marking this ready.
