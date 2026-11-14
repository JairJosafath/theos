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

Step 1 ships as two pull requests, each part of #268.

- **The first part ships alone:** commits 1 and 2, the embedded CRDs of 3,
  and 5 for `internal/modelfacts`. That is `modelfacts` moved out of the
  Container reconciler, with the one branch rule; `GitHost.Login`; the
  embedded CRDs (`config/crd/bases/crds.go`); and `modelfacts`' import rows
  and depguard rule. The operator's behaviour is meant not to change: the
  reconciler calls `modelfacts`, and its envtest specs are unchanged.
- **The second part holds the rest:** the model files' strict decoder with
  the CRDs' defaults (3), `internal/nocluster` and `cmd/theos` with `init`,
  `scaffold`, `protect` and `status` (4), and their import rows and depguard
  rules (5).

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
  `//go:embed *.yaml`. Checked before anything else (3a): `make manifests`
  rewrote no byte of it, its only line in `git status` being the new file,
  and controller-gen does not read it (`CONTROLLER_GEN_PATHS` is api, cmd
  and internal). `make check-generated` names it only while it is
  uncommitted, as it does any new file under `config/crd`, and is clean once
  it is committed (`make manifests` then `make check-generated`, exit 0,
  run after the commit that adds it). envtest, `hack/chart-crds.sh` and
  `test/compat` read `*.yaml` there only.
- **Defaulting:** `k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting`
  on the structural schema of each embedded CRD. `go list -deps` of it, of
  `pkg/apiserver/schema`, of `pkg/apis/apiextensions/v1` and of
  `./api/v1beta1` lists no client-go and no controller-runtime (0 lines each).
- **Settings keys:** `repository` (default `theos-<project>`) and
  `operator`, with `sessionFeature`, `sessionBinaryURL`, `ciImageGo` and
  `ciImageBase`, the operator's four flags with their defaults. `lease`,
  `grace`, `claimants` and `merge` are not reserved: the decode is strict,
  so the step that reads one adds it.
