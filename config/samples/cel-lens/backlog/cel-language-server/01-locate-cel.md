---
title: Find the CEL expressions in Kubernetes YAML
labels: status/ready, kind/feature
milestone: M1 Diagnostics
---
## Context

Everything the server does starts by knowing where CEL is. Policy authors don't
mark CEL: a document's `apiVersion` and `kind` say which fields hold it (see the
system's needs). This issue builds that map; checking the expressions comes later.

## Goal

A package (suggested: `internal/locate`) that takes the bytes of a YAML file and
returns every CEL expression in it: its text (the decoded scalar value), the kind
of place it is in, the index of its YAML document, its YAML path, and the
position of the scalar in the file. For each document it also returns what later
checks need: the decoded document (as a `yaml.Node` or raw bytes) and its
`apiVersion` and `kind`.

Places to find:

| apiVersion | kind | fields |
| --- | --- | --- |
| `admissionregistration.k8s.io/v1`, `/v1beta1` | ValidatingAdmissionPolicy | `spec.matchConditions[].expression`, `spec.variables[].expression`, `spec.validations[].expression`, `spec.validations[].messageExpression`, `spec.auditAnnotations[].valueExpression` |
| `admissionregistration.k8s.io/v1` | ValidatingWebhookConfiguration, MutatingWebhookConfiguration | `webhooks[].matchConditions[].expression` |
| `apiextensions.k8s.io/v1` | CustomResourceDefinition | `x-kubernetes-validations[].rule` and `.messageExpression` at any depth of `spec.versions[].schema.openAPIV3Schema` (under `properties`, `items`, `additionalProperties`, `allOf`/`anyOf`/`oneOf`/`not`) |

## Acceptance criteria

- [ ] Every field in the table is found, with its place kind, document index, YAML path (such as `spec.validations[1].messageExpression` or `spec.versions[0].schema.openAPIV3Schema.properties.spec.x-kubernetes-validations[0].rule`) and the scalar's start line and column.
- [ ] Multi-document files (`---`) work; documents of other kinds, empty documents and comments yield nothing and don't fail.
- [ ] A YAML syntax error is returned as an error with line and column; the function never panics. A fuzz test (`go test -fuzz`) seeded with the testdata files runs clean for 30 seconds.
- [ ] Golden tests: `testdata/locate/*.yaml` with expected output in `*.golden.json`; `go test ./internal/locate -update` rewrites them. Cover each row of the table, nested CRD schemas, and a file mixing kinds.
- [ ] A benchmark locates a 1,000-line file with 50 expressions in under 5 ms.

## Out of scope

Compiling or checking CEL; positions inside a scalar (issue "Map positions inside
YAML scalars"); MutatingAdmissionPolicy and Kyverno (later issues).

## Notes

- Use `go.yaml.in/yaml/v3`, the YAML organisation's maintained continuation of
  `gopkg.in/yaml.v3` (which is archived); Kubernetes' own `sigs.k8s.io/yaml`
  already depends on it. Its `yaml.Node` keeps line, column and scalar style.
- Keep the place kinds a small exported enum; later issues switch on it to build
  the right CEL environment.
