# Plan: theos runs cel-lens in a Kind cluster of a cloud session

The owner, 9 November 2026: "You will run Kind locally. You will run theos
locally", and the work on cel-lens happens in theos's Gitea, with GitHub only
its mirror. The factory on the owner's PC had published nothing since 4
November. This plan brings a factory up inside a Claude Code cloud session's
container, moves cel-lens into its Gitea, and files the owner's core feature
there. The files it uses are in [cloud-session/](cloud-session/).

## Where it stands (9 November, 06:00 UTC)

Done:

1. **Kind** cluster `theos` (Kubernetes v1.37.0), with `cloud-session/kind.yaml`.
   The container is cgroup v1 and lacks `CAP_SYS_RESOURCE`, so the kubelet
   runs with `failCgroupV1: false` and containerd with
   `restrict_oom_score_adj = true`. The node pulls through the session's
   HTTPS proxy, relayed onto the kind network's gateway
   (`cloud-session/proxy-relay.py 172.18.0.1 3128 38377`), and trusts the
   proxy's CA (mounted over the node's bundle).
2. **theos from this branch:** Gitea, Agent Sandbox, the operator and the
   model proxy (`config/default`), the seed for project `cel-lens`, and the
   chart's catalog and Agents for it. Images are built from copies of the
   Dockerfiles that `cloud-session/ca-dockerfile.py` makes (each stage trusts
   the proxy's CA; apt over HTTPS), with `--network host` and the proxy as a
   build argument. GHCR's images are private, so nothing is pulled from there.
3. **A bug found and fixed:** `config/default`'s metrics patch gave the model
   proxy `--metrics-bind-address`, and it crash-looped (commit "fix:
   config/default's metrics patch hits only the manager").
4. **cel-lens in Gitea.** `engine/` and `vscode/` of JairJosafath/cel-lens,
   split with their history (`git subtree split`), are
   `cel-lens/cel-lens-core` and `cel-lens/cel-lens-vscode`, tagged `vX.Y.Z` at
   each release commit (core v0.27.0, vscode v0.35.0). An adoption commit puts
   back the factory's own files the mirror leaves out (`.theos.yaml`,
   `AGENTS.md`, `docs/agents/`, `.gitea/`), rendered as the operator renders
   them (`cloud-session/adoptrender.go.txt`, run from the module root).
5. **Adopted with their own Standards.** The repositories' Makefiles carry
   targets earlier Standard changes added (per-platform `.vsix` with the
   engine, `e2e`, `dyn-report`, `vet-windows`). Adoption requires the
   Standard's files exactly, and agents work only on a repository with a
   Standard, so each Container adopts with a Standard of its own in Gitea's
   `templates` org: `cel-lens-ts-standard` (ts-standard v1.8.0, the UX bar)
   and `cel-lens-go-standard` (go-standard v1.4.0), each with that
   repository's Makefile (`cloud-session/cel-lens-standards.yaml`). Both
   Containers are `Ready`, `main` protected, and Crews `cel-lens-core` and
   `cel-lens-vscode` run (`cloud-session/cel-lens-model.yaml`).
6. **The core feature filed in Gitea**, `status/ready`, with Gitea
   dependencies (`cloud-session/issues/`):
   - cel-lens-core #1 The `.cellens` file: types cel-lens assumes where Kubernetes gives dyn
   - cel-lens-core #2 The engine checks, hovers and completes with them (after #1)
   - cel-lens-core #3 Inline info: a quiet line above each CEL rule, with Give it a type (after #2)
   - cel-lens-core #4 Engine commands that list, check, set and remove `.cellens` types (after #2)
   - cel-lens-vscode #1 Give it a type: the quick pick and input box (after core #4)
   - cel-lens-vscode #2 Toggle Inline Info (after core #3)

   Task `cel-lens-core-1` took core #1 and waits for main's checks.

## Work in Gitea (9 November, 06:00 to 06:50 UTC)

The owner: "My PC is not running. Start from what you have on GitHub. Set up
theos locally and work." The agents can't run without a token, so this
session works the issues itself, through Gitea, as user `claude` (write on
the `cel-lens` org); the Crews are paused so no Task races it.

- **CI** runs on a Gitea Actions runner on the session's host
  (`cloud-session/runner-config.yaml`, labels `go`, `go-release`, `base`, user
  `ci`, Node 24.21.0, golangci-lint v2.14.0), which reaches the internet
  through the session's proxy. `gitea.gitea.svc.cluster.local` resolves to a
  port-forward, so the jobs' clone URLs work.
- **The factory's own files came back.** The mirror leaves out `.gitea/`, and
  the repositories' tests read it, so the check and release workflows were
  rebuilt to what those tests ask (`cloud-session/workflows/`). The Standards
  became `cel-lens-go-standard` v1.0.1 and `cel-lens-ts-standard` v1.0.2
  (git bundles in `cloud-session/standards/`); core checks before release,
  the extension checks every change.
- **core #1 (`.cellens` reader): PR cel-lens-core#5.** `make check` passes; CI
  green. An independent reviewer agent requested changes (duplicate keys
  lost entries, unquoted struct types, wrong columns for multi-line types);
  the fix commit addressed them and the re-review approved. Five
  non-blocking findings are issue #6 (`status/proposed`). The source is also
  on JairJosafath/cel-lens branch `claude/zen-cori-vxzrxq`, under `engine/`.
- **core #2 (types used by checks, hover, completion)** is being built on top
  of #5.

## Pull requests in Gitea (9 November, to 12:30 UTC)

Each is built by an implementer agent, reviewed by a separate reviewer agent
that sees only the code and the issue (never the implementer's reasoning),
fixed until approved, and checked by CI on the host runner. All are
approved; none is merged: merging waits for the owner, in this order.

| Order | Gitea PR | Issue | Review rounds |
| --- | --- | --- | --- |
| 1 | cel-lens-core#5 | core #1 the `.cellens` reader | 2 (duplicate keys, quoting, columns) |
| 2 | cel-lens-core#7 | core #2 checks, hover, completion use `.cellens` types | 2 (publish ordering, symlinks, CRD semantics) |
| 3 | cel-lens-core#9 | core #3 inline info, the line above each rule | 2 (policy lines 4 s → under 110 ms; every part clickable) |
| 4 | cel-lens-core#11 | core #4 commands that list, check, set, remove types | 3 (concurrent edits, comments, cancel) |
| 5 | cel-lens-vscode#4 | vscode #2 Toggle Inline Info | 2 (changelog test) |
| 6 | cel-lens-vscode#5 | vscode #3 summary opens problems, `+N more` | 2 |
| 7 | cel-lens-vscode#6 | vscode #1 Give it a type picker | 2 (`.cellens` sync, Escape after Enter) |

Each core PR stacks on the one before it, and so does each VS Code PR;
merging in order leaves nothing to rebase. CI ran green on the core PRs;
the extension's CI stops at its e2e step (GitHub release downloads, below).

Follow-ups filed from the reviews: core #6, #8, #10, #12 (the engine's
commands under `cel-lens.engine.*`), vscode #7. The source of all seven is
also on JairJosafath/cel-lens branch `claude/zen-cori-vxzrxq` under
`engine/` and `vscode/`.

After the merges, a core release (the Crew tags it; the release workflow on
the host runner builds the binaries into the Gitea release) lets the
extension pin an engine that draws the lines: `core-version.json` with a
`baseUrl` of that Gitea release. Until then the bundled v0.25.1 engine draws
none.

The reviews settled one design point: in a CustomResourceDefinition the API
server types no CEL value kept open with `x-kubernetes-preserve-unknown-fields`
(it ignores the flag), so only `x-kubernetes-int-or-string` is `dyn` to a
rule, and takes only `int` or `string`; `.cellens` structs are for a policy's
`params`, `object`, `oldObject` and variables.

## What waits for the owner

0. **Merging.** Auto mode refuses a merge to `main` without the owner, even
   reviewed and green. Each pull request waits, reviewed and green, for the
   owner to merge (Gitea, as the operator, which alone may merge), in order.

1. **The agents' Claude token.** The model proxy reaches Anthropic through
   the session's proxy (Anthropic answers 401 to the placeholder token), but
   the session refuses to store a credential for Anthropic's hosts, and a
   token never goes in chat or in a plain environment variable. The owner
   decides how it gets into Secret `theos-system/claude-credentials`.
2. **Egress for agents' and CI pods.** The fence lets them reach public
   HTTPS only, so they can't reach the relay on a private address. Redirecting
   the kind network's outbound 443 into a forwarder that opens each
   connection through the session's proxy (`CONNECT` by the TLS server
   name) keeps all traffic going through that proxy and leaves the fence as
   it is. Auto mode refused it as weakening security; the owner allows it or
   says no.
3. **GitHub release downloads** answer 504 through the session's proxy, so
   the extension's `make check` stops at its e2e step, which fetches the
   pinned engine from JairJosafath/cel-lens's releases. A rebuild of core
   v0.25.1 here doesn't match the pinned checksum, so Gitea can't mirror it.
4. **Disk.** The agent and Go runner images are about 6 GB each, loaded into
   Kind once more; the session has 13 GB free. They are built once 1 and 2
   are settled, slimmer if they must be.
5. **The container is reclaimed when the session is idle,** and the cluster
   with it. This plan and `cloud-session/` rebuild it.
