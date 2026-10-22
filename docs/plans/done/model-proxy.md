# The model proxy: the Claude token stays out of the sandbox

Parent issue: #1075. Written 2026-10-15 by otter-1075. ROADMAP.md's 1.0
needs "the agent fence closes its known gap": today the Claude token is
readable inside the agent sandbox ([docs/design.md](../../design.md#the-fence)).

Done 2026-10-22: every step landed (#1134 to #1137); the token never enters
a sandbox or a project namespace, and the docs say so.

## Where we stand

- The token's one source is the Secret `agents.credentialsSecret`
  (`claude-credentials`, key `CLAUDE_CODE_OAUTH_TOKEN`) in the release
  namespace. The ProjectReconciler copies it into every namespace labelled
  `c4.factory.io/shared-secrets=true` (`project_shared_secrets.go`, the
  `--shared-secret` flag).
- An Agent names a Secret in its own namespace with `credentialsSecretRef`;
  `buildSandbox` (`task_sandbox.go`) puts every key of it into the agent
  container with `envFrom`. Every role goes through `buildSandbox`, so that is
  the one place the token enters a pod.
- The fence, `c4-agent-sandbox` (`project_agent_network.go`), lets sandbox
  pods reach DNS, the Git hosts' namespaces and public HTTPS on 443. Nothing
  sits between the agent and `api.anthropic.com`.
- Stand-in Agents (`harness: standin`) have no Secret and must keep working
  with no proxy at all.

## Decisions

1. **Our own small proxy, not Agent Substrate.** The 2026-09-30 spike found
   Substrate too young to install as a dependency
   ([plan](2026-09-30-agent-factory-design.md)). What we need is one
   reverse proxy to one upstream that swaps one header, about a hundred lines
   of Go on `net/http/httputil`. It lives in `internal/modelproxy`, its
   binary in `cmd/model-proxy`, and ships in the operator image as a second
   binary, so a release builds and signs no new image.
2. **One proxy per release, in the release namespace,** next to the one
   Secret it reads: a Deployment and a ClusterIP Service `c4-model-proxy`,
   port 8080, from the chart (and `config/model-proxy/` for kustomize). It
   mounts the Secret as a volume, never as env and never with `subPath`, so
   the kubelet refreshes the file when the Secret changes.
3. **The proxy reads the token on every request** (the file, cached by its
   modification time). A rotated token reaches the next request within the
   kubelet's sync period; neither the proxy nor any sandbox restarts. Until
   the file holds a token the proxy answers 503 and says so in its log; its
   readiness probe passes all the same and the Secret volume is `optional`,
   because the guide makes the Secret after `helm install --wait`, which a
   pod waiting on the Secret would time out (#1627).
4. **The sandbox gets the proxy's address and a placeholder.** For an Agent
   on the shared token, `buildSandbox` sets no `envFrom` and instead
   `ANTHROPIC_BASE_URL=http://c4-model-proxy.<release namespace>.svc:8080` (under
   kustomize, `config/default`'s `namePrefix: c4-factory-` names the Service
   `c4-factory-model-proxy`, so its `--model-proxy-url` says that) and
   `ANTHROPIC_AUTH_TOKEN=c4-model-proxy` (not a secret; Claude Code needs some
   credential to start and sends it as `Authorization: Bearer`). The proxy
   drops whatever `Authorization` and `x-api-key` the client sent and adds the
   real one: for key `CLAUDE_CODE_OAUTH_TOKEN`, `Authorization: Bearer` and
   the `oauth-2025-04-20` value in `anthropic-beta`; for key
   `ANTHROPIC_API_KEY`, `x-api-key`. Claude Code then runs as if on an API
   key, and none of its OAuth login paths run in the sandbox.
5. **"On the shared token" means `credentialsSecretRef` names the
   `--shared-secret` Secret** and the operator runs with `--model-proxy-url`.
   An Agent naming any other Secret is the project's own business: its keys
   still go in as env, and the design says so. A project that brings its own
   token takes on its own exposure; the factory's token never enters a
   sandbox.
6. **The fence routes the token's path through the proxy.** The sandbox
   policy gets one more egress rule: the proxy's pods (podSelector
   `app.kubernetes.io/name: c4-model-proxy`) in the release namespace, TCP
   8080. Public HTTPS stays open, because agents fetch packages; a sandbox
   reaching `api.anthropic.com` directly has no token to use there, so the
   only way to the Claude API with a credential is the proxy. A
   NetworkPolicy can't filter by host name, and needn't.
7. **The proxy takes traffic only from sandboxes.** A NetworkPolicy on its
   pods admits ingress only from pods labelled
   `c4.factory.io/agent-sandbox=true` in namespaces labelled
   `c4.factory.io/shared-secrets=true`, the same label that today decides
   who gets a copy of the token, and that a namespace's own users can't set.
   The proxy forwards to its one upstream (`--upstream`, default
   `https://api.anthropic.com`) and nowhere else, streams responses
   unbuffered (`FlushInterval: -1`, the API streams server-sent events) and
   never logs a header.
8. **The token stays in the release namespace.** Once sandboxes use the
   proxy, nothing in a project namespace needs the token: the operator stops
   copying it and deletes the copies it made (they carry
   `c4.factory.io/shared-from`). The pull secret is still copied. The
   pre-flight check (`CredentialsNotFound`) reads the release namespace's
   Secret for a shared-token Agent.

## Steps

| Step | Issue | How we know |
| --- | --- | --- |
| This plan | #1075 (`Refs`) | it is merged |
| The model proxy: `internal/modelproxy`, `cmd/model-proxy`, in the operator image | #1134 | `make check`: httptest unit tests |
| The chart and kustomize run the proxy, with its ingress policy | #1135 | `make check`: `make validate-yaml`, the chart tests |
| Sandboxes on the shared token use the proxy; the fence routes to it | #1136 | `make check`: `buildSandbox` and fence unit tests, envtest |
| The token stays in the release namespace; the design and ROADMAP say the gap is closed | #1137 | `make check`: envtest of the shared secrets; the docs |

## Done when

- A sandbox pod for an Agent on the shared token holds no Claude token: no
  env var, no file, no mounted Secret (unit test on `buildSandbox`).
- The proxy adds the credential from the one Secret and picks up a rotated
  token without a restart (httptest).
- The fence lets the sandbox reach the proxy, and the proxy admits only
  sandboxes (unit test, envtest, `make validate-yaml`).
- No project namespace holds a copy of the token (envtest).
- docs/design.md and ROADMAP.md say the gap is closed.

## What only a human's Kind run shows

That Claude Code 2.1.x runs a whole Task through the proxy on an OAuth token
(`claude setup-token`) with `ANTHROPIC_AUTH_TOKEN` as its only credential,
streaming included, and that a rotated Secret reaches the proxy within the
kubelet's sync period. No step waits for it; each step's pull request says
what a human would check.
