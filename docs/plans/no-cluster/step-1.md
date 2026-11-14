# Step 1 of no-cluster: the build

The build note for step 1 of [the plan](../no-cluster.md) (#268): `theos
init`, `scaffold`, `protect` and `status`. The design is the plan's
decisions 1 to 4, 9 and 12 and [to-file.md](to-file.md)'s "Step 1"; this
note holds the commits and the choices the plan leaves open.

## Commits

1. `internal/modelfacts`: what the Container reconciler's `resolve`,
   `scaffoldInput`, `fetchLayers` and `resolveLicense` work out, as pure
   functions on the objects a caller read. The reconciler calls them.
2. The GitHost port's `Login`: the credential's own login, which Gitea's
   branch rule names.
3. The model files: `model/model.yaml`, `model/setup.yaml` and
   `model/settings.yaml`, defaulted from the embedded CRDs and decoded
   strictly.
4. `internal/nocluster` and `cmd/theos`: the four commands.
5. The import rows in docs/architecture.md and docs/dev/structure.md, and
   depguard's rules for the new packages.

## Two parts

Step 1 ships as two pull requests of #268: the first (#316) holds commits 1,
2, the embedded CRDs of 3 and 5 for `modelfacts`; the second the rest.

## Choices

- **`modelfacts` takes lists.** `Objects` holds a namespace's objects of
  each kind the scaffold reads, as a List returns them or a model file holds
  them, and the credentials of the Secrets the caller read, by name. The
  reconciler lists each kind from its cache where it got each one by name:
  the same informer answers both, and the role already grants `list`. So the
  reconciler and the tool hand `Resolve` the same thing.
- **The pieces, not one call.** The reconciler gives a failed template fetch,
  a host with no CI and a failed render different reasons, in that order, so
  `modelfacts` gives `FetchLayers` and `Build` (input, render and commit
  message) as two calls; `theos scaffold` calls the same two.
- **`Login` on GitHost**, not on `Credentials`: a token alone does not say
  whose it is, and only the host can. When the Secret names no user,
  Gitea's adapter asks `/api/v1/user` on each `ProtectBranch` and
  `ProtectTags`, so protecting a repository asks twice; GitHub's already
  asks `/user`.
- **One branch rule.** `modelfacts.Rule(ci, standardPaths)` builds the rule
  a Container's main is protected with; `internal/controller/protection.go`
  stays as its caller (it reads the GitProvider and the Standard) and puts
  what `Rule` returns, and `modelfacts.Protection` returns it for the tool.
  `TestProtectRepositoryPutsModelfactsRule` compares the rule the
  reconciler puts on the fake host with `Protection`'s for the same
  objects.
- **The schemas' place: `config/crd/bases/crds.go`**, package `bases`,
  `//go:embed *.yaml`, which controller-gen does not read; envtest,
  `hack/chart-crds.sh` and `test/compat` read `*.yaml` there only.
- **Defaulting:** `k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting`
  on the structural schema of each embedded CRD. `go list -deps` of it, of
  `pkg/apiserver/schema`, of `pkg/apis/apiextensions/v1` and of
  `./api/v1beta1` lists no client-go and no controller-runtime (0 lines each).
- **The CRDs' create-time rules run in the decoder,** by the API server's
  own validators on the embedded schemas, so a model file the API server
  would refuse at create is refused by the same rule: OpenAPI patterns,
  enums, formats and bounds, list types, CEL rules with no old object, and
  the metadata checks of a custom resource. An object is read in the API
  server's order for a create: a whole number is an integer, a field
  written as null that is not nullable and has no default reads as left
  out (a required one is refused as missing), defaults are set, and a kind
  with a status subresource reads `status` strictly (an unknown field in
  it is refused) and drops it. A name is used once per kind,
  and `metadata.resourceVersion` is the API server's, so a model leaves it
  out. A refusal names the field and the rule, never the value. The rules
  add one package to `go list -deps` of `internal/nocluster`,
  `schema/listtype`, and no client-go or controller-runtime. The provider
  enum alone is left to a cluster, being its word on where it can host
  (the plan's decision 2). A template's ref is a tag or a full commit, which no CRD rule can show;
  the template fetcher, the operator's, refuses a branch.
- **One host for the credential:** the credential is sent only to the
  host it was given for. `THEOS_GIT_TOKEN` goes only to `THEOS_GIT_HOST`,
  which is set beside it; without `THEOS_GIT_TOKEN`, `git credential fill`
  is asked for the origin remote's host. The token needs the stated host
  because it names no host itself, while `git credential fill` keeps one
  credential per host. The credential goes over https unless the
  person's own `THEOS_GIT_HOST`, or the origin remote, says http, or the
  host is the machine itself. A GitProvider on another host or scheme,
  or whose url holds a user or password, is refused before any request,
  naming both hosts, where the credential's host came from, and the
  `THEOS_GIT_HOST` that a model on another host sets; a template on
  another host is fetched without the credential, and `init`'s catalog
  fetch never carries it. A redirect to another host is not followed
  with the credential: `cmd/theos` gives the host adapters an HTTP client
  that follows a redirect only on the scheme, host and port the request
  began on. Hosts compare lower case, without a trailing dot or the
  scheme's default port; a host outside ASCII is refused, and a person
  writes its `xn--` form. The host is read from outside the repository
  because whoever can change the model files must not choose where the
  person's credential goes.
- **`model/setup.yaml` is what a person makes:** theos scaffolds from the
  template url and ref it names, fetched without the credential unless
  the template is on the credential's host. theos fetches a template over
  https or http only, as the CRD's pattern says. Read every change to
  `model/setup.yaml` before merging it; step 2's protected paths name
  `model/`.
- **Settings keys:** `repository` (default `theos-<project>`) and
  `operator`, with `sessionFeature`, `sessionBinaryURL`, `ciImageGo` and
  `ciImageBase`, the operator's four flags with their defaults. `lease`,
  `grace`, `claimants` and `merge` are not reserved: the decode is strict,
  so the step that reads one adds it.

## What the second part built

- **The decoder is in `internal/nocluster`** (`modelfiles.go`), as
  to-file's row puts it. `model/model.yaml` holds the Project, Person,
  SoftwareSystem, Container, DataStore and Relationship;
  `model/setup.yaml` the GitProvider, the six catalog kinds and Bundles;
  another kind in a file is refused, as is what the CRDs' create-time
  rules refuse (Choices). A provider `model.HostCI` knows no CI of is
  refused, since the CRD's provider enum does not run here. `go list -deps`
  of it and of `cmd/theos` names no client-go and no controller-runtime
  (their `TestImports`).
- **`scaffold` is the reconciler's sequence:** resolve and check, then
  `FetchLayers`, `CI`, `License`, `Build`, `PushInitialCommit` with the
  operator's author, and the operator's rule and release tags. A
  repository with theos's commit gets only its rule again; foreign
  commits and `adopt` fail the command, adoption being an operator's.
  The golden test compares, for the four webshop Containers, the tree and
  message pushed on the ports' fake with `modelfacts`' render of the same
  objects and templates.
- **`init` asks, never guesses:** the project's name is its namespace's,
  a DNS label, checked before anything is fetched or asked; github.com is
  read from the remote;
  any other host's provider comes from `-provider` or a question on
  stdin, and without an answer `init` stops. The catalog is the chart's
  `catalog.entries` and `catalog.bundles`, read from `dist/chart` of the
  theos repository at the release's tag through the template fetcher, so
  it has one source; each entry names that repository, the tag and
  `templates/<name>`. The tag is the build's, `go install`'s module
  version, or `-ref`.
- **`status` reads `AddedAfterScaffold`** as the Relationship reconciler
  does: `model.RelationshipApplied` on the first commit's
  `Applied-Relationship` trailers. The loss table's reason words are
  `model.NoClusterLosses`, so step 2's playbook can render the same words;
  `AgentHoldsOwnerCredential` always applies until a setting names a
  second account. The account is the host's `Login`.
- **Host-neutral guard:** `cmd/theos/main.go` joins its allowlist at one
  hit, the adapter's import path, as `cmd/main.go` is on it.
