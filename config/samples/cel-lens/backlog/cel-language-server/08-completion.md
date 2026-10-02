---
title: Complete variables, fields and functions
labels: status/ready, kind/feature
milestone: M2 Completion and hover
depends: 07-lsp-diagnostics
---
## Context

Policy authors don't remember that it's `object.spec.template.spec.containers`
or which variables a matchCondition may use. The server knows the type of every
receiver from the place the expression is in (see the system's needs).

## Goal

`textDocument/completion` inside CEL expressions, with items typed by the place's
environment.

## Acceptance criteria

- [ ] After `object.spec.` in a policy matching `apps/v1` deployments, the items are Deployment's spec fields, each with its CEL type as detail and its schema description as documentation.
- [ ] At the start of an expression, the items are the variables in scope for the place (for a policy: `object`, `oldObject`, `request`, `params`, `namespaceObject`, `authorizer`, `variables`; for a CRD rule: `self`, and `oldSelf` where allowed), and the functions and macros.
- [ ] After `variables.`, the policy's variables; inside a macro (`object.spec.template.spec.containers.all(c, c.`), the iteration variable's fields.
- [ ] After `self.` in a CRD rule, the fields of the schema node the rule is on.
- [ ] Items use the LSP kinds Field, Variable, Function and Keyword (macros), and macros insert a snippet (`all(${1:x}, ${2})`).
- [ ] Outside CEL the server returns no items, so other YAML extensions' completion is untouched.
- [ ] Tests cover each case through the LSP integration harness; a completion answers within 50 ms on the 1,000-line test file.

## Out of scope

Hover (next issue); signature help; completing YAML keys.

## Notes

- From the spike: compile the receiver (`object.spec.template.spec.containers`)
  in the place's environment, read the output type, and list the fields of the
  matching `DeclType`; the environment's `Functions()` and `Macros()` list the
  rest (158 functions in Kubernetes' base environment).
- The text before the cursor often doesn't parse; take the receiver chain with a
  small scanner rather than the CEL parser.
