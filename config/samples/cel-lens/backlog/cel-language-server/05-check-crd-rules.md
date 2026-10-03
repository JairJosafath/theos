---
title: Check CRD validation rules like the API server
labels: status/ready, kind/feature
milestone: M1 Diagnostics
depends: 01-locate-cel, 02-scalar-positions
---
## Context

CRD authors write `x-kubernetes-validations` rules where `self` is the schema
node the rule sits on. The API server rejects a CRD whose rules don't compile or
cost too much; the policy author should see that in the editor, at the rule
(see the system's needs).

## Goal

Extend `Diagnose` to CustomResourceDefinition documents: every `rule` and
`messageExpression` is compiled against the structural schema of its node, with
`self` (and `oldSelf` for transition rules) typed as the API server types them.

## Acceptance criteria

- [ ] `self.replicas > 0` on an object without `replicas` gives `undefined field 'replicas'` on `replicas`; on one with an integer `replicas` it gives nothing.
- [ ] Rules on nested properties, array items and map values (`additionalProperties`) type `self` as that node.
- [ ] Rules over the cost budget give the API server's cost message (such as one with "estimated rule cost exceeds budget"), as an error on the rule.
- [ ] `messageExpression` must give a string; `oldSelf` works in transition rules.
- [ ] Each version of a multi-version CRD is checked against its own schema.
- [ ] Golden tests under `testdata/crds/`, including a CRD that the API server accepts unchanged (no diagnostics).

## Out of scope

Checking the rest of the schema (other YAML tools do that); the CLI and LSP.

## Notes

- `k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel.Compile` compiles all
  rules of one structural schema node; build the structural schema with
  `schema.NewStructural` after converting `apiextensionsv1.JSONSchemaProps` to the
  internal type, as the API server does in its CRD validation.
- Take cel-go and its libraries through the Kubernetes modules at v0.37.0, as in
  the schemas issue.
