---
title: Embed Kubernetes' OpenAPI schemas and resolve kinds offline
labels: status/ready, kind/feature
milestone: M1 Diagnostics
---
## Context

To type `object` in a ValidatingAdmissionPolicy that matches `apps/v1`
`deployments`, the server needs Deployment's schema, and it must work offline
(see the system's needs). Kubernetes publishes OpenAPI v3 schemas per group and
version in its repository, in the same format the API server's discovery serves.

## Goal

A package (suggested: `internal/k8sschema`) with Kubernetes v1.37.0's OpenAPI v3
schemas embedded, offering:

- a `resolver.SchemaResolver` (`k8s.io/apiserver/pkg/cel/openapi/resolver`) that
  returns a kind's schema with every `$ref` populated;
- a `meta.RESTMapper` that maps resources to kinds and scopes (`deployments` in
  `apps/v1` to `Deployment`, namespaced), built from the same files.

## Acceptance criteria

- [ ] A `go generate` command downloads the v3 files of the groups Kubernetes v1.37.0 serves from `https://raw.githubusercontent.com/kubernetes/kubernetes/v1.37.0/api/openapi-spec/v3/`, checks them against SHA-256 sums kept in the repository, and stores them compressed for `go:embed`. Running it twice changes nothing.
- [ ] `ResolveSchema` returns populated schemas for at least `v1` Pod, ConfigMap, Namespace and Service, `apps/v1` Deployment and StatefulSet, `batch/v1` Job and `networking.k8s.io/v1` Ingress; an unknown kind returns an error wrapping `resolver.ErrSchemaNotFound`.
- [ ] The RESTMapper maps plural resources to kinds with the right scope, and `*` resources to every kind of the group and version.
- [ ] The embedded data adds less than 10 MB to the binary (state the number in the summary) and loading a schema the first time takes under 50 ms.
- [ ] The package README says how to move to a new Kubernetes version.

## Out of scope

Checking expressions; CRDs found in the workspace (a later issue may add them).

## Notes

- `k8s.io/apiserver/pkg/cel/openapi/resolver/discovery.go` shows how the API
  server resolves a kind from these files: find the component with the matching
  `x-kubernetes-group-version-kind`, then `resolver.PopulateRefs`.
- A spike confirmed `validating.TypeChecker` type-checks a policy against a
  schema resolved this way, offline.
- Take cel-go and the Kubernetes libraries through `k8s.io/apiserver` v0.37.0;
  don't add `github.com/google/cel-go` directly. cel-go moved to `cel.dev/cel-go`
  in v0.32, while Kubernetes 0.37 still uses `github.com/google/cel-go` v0.29.
- The toolchain image has these Kubernetes modules in its module and build
  caches; staying on v0.37.0 keeps builds fast.
