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

## What waits for the owner

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
3. **Disk.** The agent and Go runner images are about 6 GB each, loaded into
   Kind once more; the session has 13 GB free. They are built once 1 and 2
   are settled, slimmer if they must be.
4. **The container is reclaimed when the session is idle,** and the cluster
   with it. This plan and `cloud-session/` rebuild it.
