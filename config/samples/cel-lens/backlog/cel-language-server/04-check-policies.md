---
title: Check ValidatingAdmissionPolicy expressions like the API server
labels: status/ready, kind/feature
milestone: M1 Diagnostics
depends: 01-locate-cel, 02-scalar-positions, 03-kubernetes-schemas
---
## Context

The first real value for a policy author: a typo in a policy's CEL shows up in
the file, with the message the API server would give, before `kubectl apply`
(see the system's needs and KPIs).

## Goal

A package (suggested: `internal/analysis`) with `Diagnose(path string, src
[]byte) []Diagnostic`, where a `Diagnostic` has an LSP range (from the issue
"Map positions inside YAML scalars"), a severity, a message and the source
`cel`. This issue covers ValidatingAdmissionPolicy documents and the
`matchConditions` of webhook configurations; the CRD issue adds rules.

## Acceptance criteria

- [ ] In a policy matching `apps/v1` `deployments`, `object.spec.replica <= 5` gives `undefined field 'replica'` with a range on `replica` in the file, for plain, quoted and block scalars.
- [ ] Syntax errors, type errors (`found no matching overload for '_<=_' applied to '(int, string)'`) and wrong result types (a validation must be a bool, a messageExpression a string) are reported with the API server's message text.
- [ ] `variables` are typed from their expressions in order, `variables.<name>` works in later expressions, and an unknown variable is an error at its position.
- [ ] `matchConditions`, `validations` (expression and messageExpression) and `auditAnnotations` are checked; `params` is typed from `paramKind` when it names a built-in kind and is `dyn` otherwise.
- [ ] A policy matching several kinds is checked against each, and the message names the kind it failed for.
- [ ] Webhook `matchConditions` are checked with `object`, `oldObject`, `request` and `authorizer`.
- [ ] A document that doesn't decode as its kind (a wrong field type) gives one diagnostic on that document, not a crash; a YAML syntax error gives one diagnostic at its position.
- [ ] Golden tests: `testdata/policies/*.yaml` with expected diagnostics as `line:col-line:col: message`.

## Out of scope

CRD rules; the CLI and LSP (later issues); quick fixes.

## Notes

- `k8s.io/apiserver/pkg/admission/plugin/policy/validating.TypeChecker` does the
  checking: give it the schema resolver and RESTMapper from the schemas issue,
  call `Check(policy)`, and map each warning's `FieldRef` (such as
  `spec.validations[1].expression`) back to the located expression. A spike got
  `undefined field 'replica'` with `<input>:1:12` this way.
- Warning text carries `<input>:LINE:COL` and a caret drawing; parse the
  position, keep the message line. Check with a test whether cel-go's columns
  count runes or bytes, with non-ASCII text before an error.
- Decode a document into the typed policy with `sigs.k8s.io/yaml`.
