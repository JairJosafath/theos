# Getting started: your first app from the release

This guide takes a Kubernetes cluster from nothing to an app the factory builds:
it installs the factory from the published release, describes a small Go
command-line program as a C4 model, and has a Crew of Claude Code Agents turn
a Gitea issue into a merged pull request. Nothing is built from this repository;
every image and the chart come from GHCR.

You need:

- a Kubernetes 1.30 or later cluster (Kind v0.23 or later) with about 4 CPUs
  and 8 GiB of memory free for the factory and two agents (a Kind cluster on
  your machine works), with `kubectl` and `helm` 3.8 or later pointing at it;
- `jq` and `curl`, for the Gitea step;
- a Claude subscription or API account and the `claude` CLI, for the agents'
  token;
- the GitHub CLI (`gh`), logged in, while this repository, the chart and the
  images on GHCR are private.

The commands use the release `0.3.0`; for a later one, change `VERSION`. Run
them from a clone of this repository at that release's tag, so the sample files
match the release (`gh` clones it while the repository is private). From the
release after `0.3.0`, the chart brings the catalog and the Agents, pinned to
its own template tags and agent image, and the samples make neither; `0.3.0`'s
samples still hold their own `catalog.yaml` and `agents.yaml`.

```bash
VERSION=0.3.0
gh repo clone JairJosafath/c4factory && cd c4factory && git checkout "v$VERSION"
```

## 1. Install the factory

**Agent Sandbox.** The operator runs its agents in Sandboxes and won't start
without the `Sandbox` kind:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.4/sandbox.yaml
```

**Namespaces.** The release goes in `c4-factory-system` and Gitea in `gitea`.
Each project lives in a namespace of its own name; this guide's is `webshop`:

```bash
for ns in c4-factory-system gitea webshop; do
  kubectl create namespace $ns --dry-run=client -o yaml | kubectl apply -f -
done
```

**Registry login and the pull secret** (skip them once the chart and the
images on GHCR are public). Your GitHub token, with `read:packages`, logs Helm
in to GHCR, so it can pull the chart, and goes in a Secret named `ghcr` in each
namespace that pulls an image: the release's, Gitea's and the project's. You
run this yourself; the factory never sees the token anywhere else.

```bash
gh auth refresh -s read:packages
gh auth token | helm registry login ghcr.io -u "$(gh api user -q .login)" --password-stdin
for ns in c4-factory-system gitea webshop; do
  kubectl -n $ns create secret docker-registry ghcr --docker-server=ghcr.io \
    --docker-username="$(gh api user -q .login)" --docker-password="$(gh auth token)"
done
```

The agents run in the project namespace, in pods the operator makes outside the
chart. They run as the namespace's `default` ServiceAccount, so they pull with
the secrets it lists:

```bash
kubectl -n webshop patch serviceaccount default -p '{"imagePullSecrets":[{"name":"ghcr"}]}'
```

**The release.** `gitea.projects` lists the project namespaces: for each one
the seed makes a Gitea organization, Secret `gitea-credentials` with its token
and GitProvider `gitea`, and the chart makes the catalog (the templates the seed
pushed, pinned to their tags: Frameworks `go-cli`, `python-fastapi` and
`vscode-extension`, Standards `go-standard` and `ts-standard`, Compute `small`,
Deployments `k8s-manifests` and `gitea-release`, Connector `postgres`) and four
Agents on the release's `agent-go` image: `planner`, `designer` and `reviewer`
on Opus, `implementer` on Sonnet. The Agents read Secret `claude-credentials`,
which you make in step 2. `catalog.enabled=false` and `agents.enabled=false`
leave them out ([the chart's README](../dist/chart/README.md#values)). On a fresh cluster Gitea's image may still be pulling
when the seed starts, so give `helm install` time:

```bash
helm install c4-factory oci://ghcr.io/jairjosafath/c4factory/charts/c4-factory --version "$VERSION" \
  -n c4-factory-system --wait --timeout 10m \
  --set gitea.createNamespace=false \
  --set 'gitea.projects={webshop}' \
  --set 'global.imagePullSecrets[0].name=ghcr' \
  --set 'manager.imagePullSecrets[0].name=ghcr'
```

Without the pull secret, leave out the two `imagePullSecrets` lines. Check
that everything runs and that the project has its GitProvider, catalog and
Agents:

```bash
kubectl -n c4-factory-system get pods
kubectl -n gitea get pods
kubectl -n webshop get gitprovider,secret,c4catalog,agents
```

The release requests about 1.1 CPU and 1.4 GiB of memory; on a two-CPU node
see [the chart's README](../dist/chart/README.md#install) for what to turn
down.

## 2. The agents' token

The agents run Claude Code, which signs in with a long-lived token. Make one
with `claude setup-token` and store it in Secret `claude-credentials` in the
project namespace; the Agents name the Secret and hold no token. Run
`claude setup-token` on its own first, then the rest, and paste the token at
the prompt (it isn't echoed, and stays out of your shell history):

```bash
claude setup-token
```

```bash
read -rs -p 'Claude token: ' CLAUDE_TOKEN; echo
[ -n "$CLAUDE_TOKEN" ] && printf '%s' "$CLAUDE_TOKEN" | kubectl -n webshop create secret generic claude-credentials \
  --from-file=CLAUDE_CODE_OAUTH_TOKEN=/dev/stdin
unset CLAUDE_TOKEN
kubectl -n webshop get secret claude-credentials -o go-template='{{len .data.CLAUDE_CODE_OAUTH_TOKEN}}{{"\n"}}'
```

The last line prints the stored token's length in base64, never the token;
`0` or an error means the paste didn't reach `read` (pasting both blocks at once does that): delete the
Secret and run the second block again.

With an API key instead, name the key `ANTHROPIC_API_KEY`: every key of the
Secret becomes an environment variable in the agent's sandbox.

## 3. The model and the Crew

[config/samples/first-app/](../config/samples/first-app/) holds the first app:

| File | What it is |
| --- | --- |
| `project.yaml` | Project `webshop`, named after its namespace |
| `story.yaml` | Person `staff`, SoftwareSystem `webshop-core`, Container `orders-cli` (a Go command-line program) and two Relationships |
| `crew.yaml` | Crew `orders-cli`: picks up `orders-cli`'s ready issues, implemented by Agent `implementer` and reviewed by Agent `reviewer` |

The Container picks Framework `go-cli` and Standard `go-standard`, and the Crew
names Agents `implementer` and `reviewer`: all four came with the release, so
the sample makes none of them.

Apply it and wait for the Container to be scaffolded:

```bash
kubectl apply -k config/samples/first-app
kubectl -n webshop wait container/orders-cli --for=jsonpath='{.status.phase}'=Ready --timeout=5m
kubectl -n webshop get c4
```

Gitea now holds `webshop/orders-cli`: a Go module, the Standard's `Makefile`,
linter settings, the guides agents read, and CI that runs `make check` on the
`go` runner. Open it:

```bash
kubectl -n gitea port-forward service/gitea 3000:3000 &   # http://localhost:3000/webshop/orders-cli
```

The repositories are public; to sign in, the admin's name and password are in
Secret `gitea/gitea-admin`:

```bash
kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.username}' | base64 -d; echo
kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.password}' | base64 -d; echo
```

Once the Crew has polled the repository its phase is `Idle`, and the
repository has the labels it works with, `status/ready` among them:

```bash
kubectl -n webshop wait crew/orders-cli --for=jsonpath='{.status.phase}'=Idle --timeout=5m
```

## 4. The first issue

A Crew works on open issues labelled `status/ready` whose body has an
`## Acceptance criteria` section with a checklist; the reviewer judges each
item. File one as the admin, through Gitea's API (or in its web UI, with the
same title, body and label):

```bash
GITEA=http://localhost:3000
ADMIN=$(kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.username}' | base64 -d)
PASS=$(kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.password}' | base64 -d)
READY=$(curl -fsS -u "$ADMIN:$PASS" "$GITEA/api/v1/repos/webshop/orders-cli/labels" |
  jq '.[] | select(.name == "status/ready") | .id')
jq -n --argjson ready "$READY" '{
  title: "Total an order",
  labels: [$ready],
  body: "## Goal\n\n`orders-cli total` reads an order from standard input, one line per item as `QUANTITY PRICE` (such as `2 4.50`), and prints the order'"'"'s total with two decimals.\n\n## Acceptance criteria\n\n- [ ] `printf \"2 4.50\\n1 10\\n\" | orders-cli total` prints `19.00` and exits 0.\n- [ ] Empty input prints `0.00`.\n- [ ] A line that is not two numbers prints an error naming its line number to standard error and exits 1.\n- [ ] `orders-cli` without a command prints its usage, which lists `total`, and exits 2.\n- [ ] Table tests cover each case, and `make check` passes.\n"
}' | curl -fsS -u "$ADMIN:$PASS" -H 'Content-Type: application/json' \
  -d @- "$GITEA/api/v1/repos/webshop/orders-cli/issues" | jq '{number, html_url}'
```

## 5. Watch it build

Within a minute the Crew's phase turns `Working`: it starts Task
`orders-cli-1` and labels the issue
`status/in-progress`. The implementer works in an Agent Sandbox pod, pushes a
branch and opens a pull request; CI runs `make check`; the reviewer approves or
asks for changes, up to the Agent's `maxRounds`; an approved, green pull request
is merged and the issue closed. With Claude Code that takes 10 to 30 minutes.

```bash
kubectl -n webshop get crew,tasks
kubectl -n webshop get task orders-cli-1 -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
kubectl -n webshop get sandboxes,pods
```

The Task's phase ends `Merged`. The pull request, its review and CI run are on
<http://localhost:3000/webshop/orders-cli/pulls>. A Task that fails labels its
issue `status/needs-human` with the reason in a comment; label it
`status/ready` again to try once more.

## When it doesn't work

| What you see | Why, and what to do |
| --- | --- |
| A pod in `ImagePullBackOff` in `c4-factory-system` or `gitea` | The images are private and that namespace has no `ghcr` Secret, or the install lacks the `imagePullSecrets` settings. |
| A sandbox pod in `ImagePullBackOff` in `webshop` | ServiceAccount `webshop/default` doesn't list `ghcr`, or Secret `webshop/ghcr` is missing; patch it, then delete the pod. |
| `helm install` times out | Gitea's image is still pulling; `kubectl -n gitea get pods`, then `helm upgrade` with the same settings. |
| Container `orders-cli` not `Ready` | `kubectl -n webshop describe container orders-cli`: the condition's message says which template or GitProvider is missing. |
| The issue is labelled `status/needs-human` before any Task | The Crew rejected it and said why in a comment, such as a missing checklist; fix the body and label it `status/ready` again. |
| The Crew stays `Waiting` | `kubectl -n webshop get crew orders-cli -o yaml`: `status.conditions` says what it waits for, such as the Container. |
| The agent says it isn't logged in | Secret `webshop/claude-credentials` is missing, empty (step 2's length check prints `0`), or its key isn't `CLAUDE_CODE_OAUTH_TOKEN`. |

## Publish releases to GitHub

A Crew with `spec.tagReleases` tags each release (a Gitea milestone titled
`vMAJOR.MINOR.PATCH` with a due date) once its issues are merged, and the tag's
release run attaches the files to a Gitea release. Those live in the cluster's
Gitea. To have every release also show up on a GitHub repository, with the
same tag, notes and files, give the Crew that repository and a token for it.
The factory only adds releases there: it never pushes a commit or changes a
file.

1. **The token.** On GitHub, *Settings → Developer settings → Fine-grained
   tokens → Generate new token*: *Repository access: Only select repositories*,
   the one repository; *Repository permissions: Contents: Read and write*
   (*Metadata: Read* comes with it). Nothing else. The repository needs a first
   commit: a release's tag goes at its default branch's head.
2. **The Secret**, in the Crew's namespace, with the key `token`:

   ```bash
   read -rsp 'GitHub token: ' GH_TOKEN; echo
   kubectl -n webshop create secret generic orders-cli-github --from-literal=token="$GH_TOKEN"
   unset GH_TOKEN
   ```

3. **The Crew field.** [config/samples/first-app/crew-publish.yaml](../config/samples/first-app/crew-publish.yaml)
   is the Crew of step 3 with it; set your repository and apply it:

   ```yaml
   spec:
     tagReleases: true          # publishReleases needs it
     publishReleases:
       github:
         repository: your-name/orders-cli   # owner/name on github.com
         tokenSecretRef: {name: orders-cli-github}
       tagPrefix: ""            # optional: "cli-" publishes v0.2.0 as cli-v0.2.0
   ```

Once a release's runs succeed, the Crew copies it: the notes are the Gitea
release's, or, when nobody wrote any, the titles of its merged issues, which it
writes on the Gitea release too. It builds a draft, uploads each file, then
publishes it, so a release is never seen half done. Only the last release is
published; one tagged before the previous was published leaves that one to
you, with a Warning Event `ReleaseNotPublished`. Removing `publishReleases`
stops publishing; what is on GitHub stays.

**Reading how it went:**

```bash
kubectl -n webshop get crew orders-cli -o jsonpath='{.status.lastRelease.publish}{"\n"}'
kubectl -n webshop get crew orders-cli -o jsonpath='{.status.conditions[?(@.type=="Published")]}{"\n"}'
kubectl -n webshop events --for crew/orders-cli
```

`publish.state` is `Pending` until the release runs succeed, `Publishing`,
then `Published` with the release's `url`, or `Failed`. A failure is a Warning
Event `ReleasePublishFailed`, once per reason, and is tried again every
minute, so fixing the cause is enough. The `Published` condition's reason
says what to fix:

| Reason | What to do |
| --- | --- |
| `Waiting` | The release runs haven't succeeded yet; a failed run is published once a re-run succeeds. |
| `SecretMissing` | Create the Secret of step 2, in the Crew's namespace, with the key `token`. |
| `Unauthorized` | GitHub refused the token: expired, revoked, or without *Contents: Read and write*. Make a new one and replace the Secret. |
| `RepositoryNotFound` | No such repository, or the token can't see it: check `repository` and the token's *Repository access*. |
| `EmptyRepository` | The repository has no commit to tag; push one. |
| `NoReleaseFiles` | The Gitea release of the tag is missing or has no files: check the tag's release run. |
| `TagTaken` | The repository already has a release or tag of that name the factory didn't make; it leaves it alone. Delete it, or set a `tagPrefix`. |
| `GitHubError` | GitHub failed, rate-limited, or couldn't be reached; it is tried again. |

## Next

- Your own project: create its namespace (with the `ghcr` Secret and the
  `default` ServiceAccount patch), add it to `gitea.projects`, `helm upgrade`
  with the same settings, and copy `claude-credentials` into it.
- [docs/design.md](design.md) explains the model, the catalog, Crews and
  Standards; [the chart's README](../dist/chart/README.md) lists its values.
- A problem with the release: [report it](https://github.com/JairJosafath/c4factory/issues/new?template=user-report.yml).
