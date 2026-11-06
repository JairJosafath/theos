# Getting started: your first app from the release

This guide takes a Kubernetes cluster from nothing to an app the factory builds:
it installs the factory from the published release, describes a small Go
command-line program as a C4 model, and has a Crew of Claude Code Agents turn
a Gitea issue into a merged pull request. [The short path](#the-short-path-describe-the-app)
does the same from a Brief, plain words about the app: its example is the
webshop, the application the factory is judged by, whose planner Agent makes
a Crew for each of its four Containers and files their first issues. Nothing
is built from this repository;
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

The commands use the release `0.30.0`; for a later one, change `VERSION`. Run
them from a clone of this repository at that release's tag, so the sample files
match the release (`gh` clones it while the repository is private). From the
release after `0.3.0`, the chart brings the catalog and the Agents, pinned to
its own template tags and agent image, and the samples make neither; `0.3.0`'s
samples still hold their own `catalog.yaml` and `agents.yaml`.

```bash
VERSION=0.30.0
gh repo clone JairJosafath/theos && cd theos && git checkout "v$VERSION"
```

## What the factory trusts

One install is one trust domain: run it on a device you own, for people you
would hand the Claude token to, and never on a shared or production cluster.
Anyone who can create a Project or a Brief in the cluster, the UI's users
included, can have the factory run code: in an agent's sandbox, and in a CI
job, on a runner pod of the project's own. The sandbox holds no Claude token:
the model proxy in the release namespace adds it to the agent's requests, so
the agent's code never sees it
([#1075](https://github.com/JairJosafath/c4factory/issues/1075) closed that
gap). What the runners still share:

- **The Gitea CI runners share the node.** A project's checks run on its own
  pool of single-job runner pods in its namespace, each registered to the
  project's organization for one job and replaced after it, under a
  NetworkPolicy that keeps a job to DNS, Gitea and public HTTPS (on a network
  plugin that enforces them, which the agents' fence needs too), with the
  registration token out of a job's reach and a Go build cache of the
  project's own; the shared runners Helm installs serve the templates'
  checks only. The chart's GitProviders carry that pool (a `base` and a
  `go` runner, the labels the templates' checks run on), so a fresh install
  needs nothing added for a project's checks to run. What the pods still
  share is the node: its
  kernel, and the cache directories on its disk, which only the kubelet's
  mounts keep apart.

## 1. Install the factory

**Agent Sandbox.** The operator runs its agents in Sandboxes and won't start
without the `Sandbox` kind. Its controller runs one replica, which needs no
leader election: with it on, the controller exits with "leader election lost"
whenever a busy host slows the API server past its 10 s lease renewal, and
v1.0.4 has no flag for the lease's length, so the patch turns it off. On an
install made without the patch, run the patch alone; run it again after any
`kubectl apply` of `sandbox.yaml`.

```bash
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.4/sandbox.yaml
kubectl -n agent-sandbox-system patch deployment agent-sandbox-controller --type=json \
  -p '[{"op":"replace","path":"/spec/template/spec/containers/0/args/0","value":"--leader-elect=false"}]'
```

**Namespaces.** The release goes in `theos-system` and Gitea in `gitea`.
Each project lives in a namespace of its own name, which the chart makes; this
guide's is `webshop`:

```bash
for ns in theos-system gitea; do
  kubectl create namespace $ns --dry-run=client -o yaml | kubectl apply -f -
done
```

**Registry login and the pull secret** (skip them once the chart and the
images on GHCR are public). Your GitHub token, with `read:packages`, logs Helm
in to GHCR, so it can pull the chart, and goes in a Secret named `ghcr` in the
release's namespace and Gitea's. The operator copies it into each project
namespace and names it in the agents' pods, so a project needs no pull secret
or ServiceAccount patch of its own. You run this yourself; the factory never
sees the token anywhere else.

```bash
gh auth refresh -s read:packages
gh auth token | helm registry login ghcr.io -u "$(gh api user -q .login)" --password-stdin
for ns in theos-system gitea; do
  kubectl -n $ns create secret docker-registry ghcr --docker-server=ghcr.io \
    --docker-username="$(gh api user -q .login)" --docker-password="$(gh auth token)"
done
```

**The release.** `gitea.projects` lists the projects: for each one the chart
makes its namespace, labeled `theos.factory.io/shared-secrets=true` so the
operator shares the pull secret into it and its agents reach the model proxy,
and the seed
makes a Gitea organization, Secret `gitea-credentials` with its token
and GitProvider `gitea` with its pool of `base` and `go` runners, and the chart makes the catalog (the templates the seed
pushed, pinned to their tags: Frameworks `aws-ec2-ts`, `aws-event-ts`, `aws-fargate-ts`, `aws-lambda-go`, `aws-lambda-py`, `aws-lambda-ts`, `aws-queue-ts`, `aws-schedule-ts`, `aws-static-site-ts`, `go-cli`, `go-kubebuilder`, `node-cli`,
`python-cli`, `python-fastapi`, `react-vite` and `vscode-extension`, Standards `aws-standard`, `go-standard`,
`go-operator-standard`, `ts-standard` and `python-standard`, Compute `small`,
Deployments `k8s-manifests` and `gitea-release`, Connector `postgres`) and four
Agents on the release's `agent-go` image: `planner`, `designer` and `reviewer`
on Opus, `implementer` on Sonnet. The Agents read Secret `claude-credentials`,
which you make in step 2; the model proxy that hands them its token starts
without the Secret and answers 503 to every request until it exists, so the
install doesn't wait for it. A fifth, `stand-in`, needs no token: it plans from
the catalog and makes a scripted change, to show the loop
([without a token](#without-a-token)). `catalog.enabled=false` and `agents.enabled=false`
leave them out ([the chart's README](../dist/chart/README.md#values)). On a fresh cluster Gitea's image may still be pulling
when the seed starts, so give `helm install` time:

```bash
helm install theos oci://ghcr.io/jairjosafath/theos/charts/theos --version "$VERSION" \
  -n theos-system --wait --timeout 10m \
  --set gitea.createNamespace=false \
  --set 'gitea.projects={webshop}' \
  --set 'global.imagePullSecrets[0].name=ghcr' \
  --set 'manager.imagePullSecrets[0].name=ghcr'
```

Without the pull secret, leave out the two `imagePullSecrets` lines. Check
that everything runs and that the project has its GitProvider, catalog and
Agents:

```bash
kubectl -n theos-system get pods
kubectl -n gitea get pods
kubectl -n webshop get gitprovider,secret,c4catalog,agents
```

The release requests about 2.1 CPU and 3.4 GiB of memory; on a two-CPU node
see [the chart's README](../dist/chart/README.md#install) for what to turn
down.

## 2. The agents' token

The agents run Claude Code, which signs in with a long-lived token. Make one
with `claude setup-token` and store it once in Secret `claude-credentials` in
the release's namespace, `theos-system`; the model proxy in that
namespace reads it and adds it to the agents' requests, so a new token reaches
every project and never enters a sandbox. The Agents name the Secret and hold
no token. Run
`claude setup-token` on its own first, then the rest, and paste the token at
the prompt (it isn't echoed, and stays out of your shell history):

```bash
claude setup-token
```

```bash
read -rs -p 'Claude token: ' CLAUDE_TOKEN; echo
[ -n "$CLAUDE_TOKEN" ] && printf '%s' "$CLAUDE_TOKEN" | kubectl -n theos-system create secret generic claude-credentials \
  --from-file=CLAUDE_CODE_OAUTH_TOKEN=/dev/stdin
unset CLAUDE_TOKEN
kubectl -n theos-system get secret claude-credentials -o go-template='{{len .data.CLAUDE_CODE_OAUTH_TOKEN}}{{"\n"}}'
```

The last line prints the stored token's length in base64, never the token;
`0` or an error means the paste didn't reach `read` (pasting both blocks at once does that): delete the
Secret and run the second block again.

With an API key instead, name the key `ANTHROPIC_API_KEY`; the proxy reads
`CLAUDE_CODE_OAUTH_TOKEN` first, then that key.

A project on a token of its own: make a Secret of another name in its
namespace and name it in its Agents' `credentialsSecretRef`. Every key of that
Secret becomes an environment variable in the agent's sandbox, readable
inside it, and the proxy plays no part. A `claude-credentials` made in a
project namespace is unused: an Agent naming the shared Secret's name goes
through the proxy. The operator never changes or deletes a Secret it didn't
copy, and each Project's condition `SecretsShared` says which pull secrets it
copies.

## The short path: describe the app

From the release after `0.3.0`, steps 1 and 2 are all you do by hand: install
the factory, make the token Secret, and describe the app in plain words. A
planner Agent writes the model, a Crew per Container and the first issues, and
says what it chose; steps 3 to 5 show the same loop written by hand.

The example is the webshop, the application the factory is judged by, from
the release after `0.28.1`.
[config/samples/webshop/brief.yaml](../config/samples/webshop/brief.yaml)
says in a customer's words what the shop does: browse, order, pay through an
external payment provider and reorder.
[story.yaml](../config/samples/webshop/story.yaml) next to it is the model it
is built from: a `storefront` web app (`react-vite`), the `catalog-api` and
`orders-api` services (`python-fastapi`), a `payments-worker`, the `shop-db`
Postgres DataStore, and the payment provider as an External system. Each
Container has `k8s-manifests`, so its repository holds a Dockerfile and its
Kubernetes manifests. Apply the model, then the Brief; the planner keeps the
model, makes a Crew per Container and files their first issues:

```bash
kubectl apply -n webshop -f config/samples/webshop/story.yaml
kubectl apply -n webshop -f config/samples/webshop/brief.yaml
```

Two parts of the webshop wait on the factory: `payments-worker` fails with
reason `UnsupportedType` until a Framework runs a worker on Kubernetes
([#1468](https://github.com/JairJosafath/c4factory/issues/1468)), and no
DeployTarget ships to a cluster yet, so the manifests stay in the
repositories ([#1072](https://github.com/JairJosafath/c4factory/issues/1072)).

For an app of your own, write only the idea, and the planner writes the model
too; on `0.28.1`, whose tag has no `brief.yaml`, take this way. For a project
of its own, say `cel-lens`, set
`gitea.projects={webshop,cel-lens}` where step 1 says `webshop`. The seed
makes Project `cel-lens`, so the UI offers the project.

Open the UI:

```bash
kubectl -n theos-system port-forward svc/theos-ui 8080 &   # http://localhost:8080
```

The UI also serves the Session client, `theos-session`, under `/downloads/`: a
scaffolded devcontainer's build fetches it from there, by default
`http://localhost:8080/downloads/theos-session-linux-${arch}` (the port-forward
above) or `https://<ui.allowedHosts[0]>/downloads/...` behind an Ingress,
with no GitHub login, and checks it against the `SHA256SUMS` served next to
it. The GitHub release's `SHA256SUMS` is also signed, keyless with cosign
(`SHA256SUMS.sigstore.json`), which the feature verifies when `cosign` is on
the devcontainer's `PATH`; the UI's `/downloads/` serves no signature, so
from there the checksum alone decides (#1706). The build must reach that address: a port-forward's localhost is the
laptop's, not a container's, so a laptop devcontainer needs the chart's
`sessionBinaryURL` set to an address it reaches, or it starts without
becoming a Session.

Open *Briefs* in the top bar, pick project `cel-lens`, write the idea in the
box under *Describe an app*, and create the Brief. Or, the same without the UI,
[config/samples/brief/cel-lens.yaml](../config/samples/brief/cel-lens.yaml)
holds the idea from [#240](https://github.com/JairJosafath/c4factory/issues/240):

```bash
kubectl apply -f config/samples/brief/cel-lens.yaml
```

The Brief's phase goes `Planning` (the planner works in the Brief's own Gitea
repository, `webshop/brief-webshop` for the webshop), `Applying`, then
`Planned`, in 10 to 30 minutes. Its card in the UI, and its status, list what
it chose, the objects it created and the issues it filed; each Container's
Crew then picks up its issues as in step 5. The commands here and below name
the webshop's Brief; for cel-lens's, write `-n cel-lens` and `cel-lens`:

```bash
kubectl -n webshop wait brief/webshop --for=jsonpath='{.status.phase}'=Planned --timeout=45m
kubectl -n webshop get brief webshop -o jsonpath='{range .status.choices[*]}- {@}{"\n"}{end}'
kubectl -n webshop get theos
```

Steer it with a follow-up in the card's prompt field, such as "Show the
delivery cost before checkout.". A follow-up never scaffolds a
Container again: it adds new Containers, and turns a change to an existing
one into an issue in its repository. When the planner can't go on without
you, such as an Agent it needs is missing, the phase is `NeedsInput` and the
card says what in one sentence.

While the Crew builds, its reviewer may suggest more work. The card lists the
suggestions waiting under *Reviewer suggestions* (`kubectl get brief` shows
how many), and nothing is built from one until you say so: answer them in a
prompt, such as "Take the reviewer's suggestions about input safety, skip the
rest." or "Yes to linter #5, no to the others.". The factory readies the ones
you take for the Crew and closes the others with your words quoted.

When an issue fails, its agents having given up on it or the Crew having
refused it, the card lists it under *Failed issues*, above the suggestions,
with why in a sentence, and marks it in the issue list (`kubectl get brief`
shows how many under `Failed`). Say in a prompt whether to try it again, such
as "Try the CLI issue again now that the engine's checker is merged.": the
factory adds what you said to the issue and readies it for the Crew. One the
agents already tried three times is filed again, reworded or split, in place
of being tried as it is.

You don't have to answer each one yourself. The Brief's autopilot, `Ask`
unless you set it, looks after what it handed back: what heals by itself (a
usage limit, an agent slot, a Standard change in review) it leaves alone for
a while, and for a failed issue it runs a tending round, a planner round with
no new prompt, that decides to retry it, close or split it, or wait. In `Ask`
it applies none of that: each answer goes under *Needs you* on the card
(`kubectl get brief` shows how many) with what the Brief would do and why,
and a prompt such as "Do what you suggest." or "Close the CLI issue instead."
answers them. *Needs you* also holds what only you can decide, such as a Crew
whose Agent is missing. Set `On` to have the Brief apply its answers itself,
or `Off` for nothing without a prompt, in the create form or with:

```bash
kubectl -n webshop patch brief webshop --type merge -p '{"spec":{"autopilot":"On"}}'
```

Either way the Crew's three tries hold, and each tending round is in the
Brief's history, so a prompt can overrule it.

### Without a token

To watch the loop before making the token Secret, name Agent `stand-in` for
every role; the install runs without the Secret, and the model proxy's pods
are ready without it. [config/samples/brief/stand-in.yaml](../config/samples/brief/stand-in.yaml)
does that in `webshop`. The stand-in reads only the catalog: it adds Container
`stand-in-demo` on the first Framework a Deployment ships, `go-cli` with
`gitea-release`, releases it and files one issue for it, and the Container's
Crew picks the issue up. It plans in a minute or so:

```bash
kubectl apply -f config/samples/brief/stand-in.yaml
kubectl -n webshop wait brief/stand-in-demo --for=jsonpath='{.status.phase}'=Planned --timeout=15m
kubectl -n webshop get theos
```

The stand-in implements and reviews the issue, and once it is merged the
Crew releases the app as in *Release a Brief's app*: it files "Prepare
v0.1.0", the stand-in does that too, the Crew tags `v0.1.0`, and the release
run puts the binaries and `SHA256SUMS` on the Gitea release of
`webshop/stand-in-demo`. That takes about half an hour. The factory's
*Verify Release* run checks it does for every minor release on its own, and
for a patch when a human dispatches it:

```bash
kubectl -n webshop wait crew/stand-in-demo --for=jsonpath='{.status.lastRelease.run}'=Succeeded --timeout=60m
kubectl -n webshop get brief stand-in-demo
kubectl -n webshop get brief stand-in-demo -o jsonpath='{range .status.apps[*]}{.container}: {.message}{"\n"}{end}'
```

To have its releases on GitHub too, name it in the Brief's `spec.releases`, as
step 2 of *Release a Brief's app* says, with a repository and token of yours.
Deleting a Brief keeps what it made; `kubectl -n webshop delete brief,container
stand-in-demo` removes the Container and its Crew too.

## 3. The model and the Crew

[config/samples/first-app/](../config/samples/first-app/) holds the first app:

| File | What it is |
| --- | --- |
| `project.yaml` | Project `webshop`, named after its namespace |
| `story.yaml` | Person `staff`, SoftwareSystem `webshop-core`, Container `orders-cli` (a Go command-line program, released on Gitea) and two Relationships |
| `crew.yaml` | Crew `orders-cli`: picks up `orders-cli`'s ready issues, implemented by Agent `implementer` and reviewed by Agent `reviewer` |

The Container picks Framework `go-cli`, Standard `go-standard` and Deployment
`gitea-release`, and the Crew names Agents `implementer` and `reviewer`: all
five came with the release, so the sample makes none of them. The Container is
scaffolded once, so it names `gitea-release` from the start: a Crew that tags
releases, as in *Publish releases to GitHub*, waits on the release run its
workflow starts, and without it a tag starts none. A Crew can also send the issues with a label to
other Agents, such as `kind/docs` to a cheaper model, in `spec.routes`
(`kubectl explain crew.spec.routes`).

Apply it and wait for the Container to be scaffolded:

```bash
kubectl apply -k config/samples/first-app
kubectl -n webshop wait container/orders-cli --for=jsonpath='{.status.phase}'=Ready --timeout=5m
kubectl -n webshop get theos
```

Gitea now holds `webshop/orders-cli`: a Go module, the Standard's `Makefile`,
linter settings, the guides agents read, CI that runs `make check` on the
`go` runner, and a release workflow that puts the binaries on the Gitea
release of each version tag. Open it:

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
| A pod in `ImagePullBackOff` in `theos-system` or `gitea` | The images are private and that namespace has no `ghcr` Secret, or the install lacks the `imagePullSecrets` settings. |
| A sandbox pod in `ImagePullBackOff` in `webshop` | Secret `theos-system/ghcr` is missing, the install lacks `global.imagePullSecrets`, or namespace `webshop` lacks the label `theos.factory.io/shared-secrets=true`; `kubectl -n webshop get project webshop -o jsonpath='{.status.conditions[?(@.type=="SecretsShared")].message}'` says which. Fix it, then delete the pod. |
| `helm install` times out | Gitea's image is still pulling, or the seed failed and the runners wait for its Secret `gitea-runner`; `kubectl -n gitea get pods` and `kubectl -n gitea logs -l app.kubernetes.io/name=seed` say which. Then `helm upgrade` with the same settings. |
| GitProvider `gitea`'s `Events` condition stays `Registered`, not `Receiving`, after a Task's first push | Gitea's deliveries don't reach the operator, which keeps polling, so the factory works, only slower to react. With the release's Gitea, `kubectl -n gitea get configmap gitea-env -o jsonpath='{.data.GITEA__webhook__ALLOWED_HOST_LIST}'` must print the host of the manager's `--git-events-url`. A Gitea of your own needs `gitEvents.url`, its own allowed host list and `gitEvents.from` ([the chart's README](../dist/chart/README.md#git-events)). |
| Container `orders-cli` not `Ready` | `kubectl -n webshop describe container orders-cli`: the condition's message says which template or GitProvider is missing. |
| The issue is labelled `status/needs-human` before any Task | The Crew rejected it and said why in a comment, such as a missing checklist; fix the body and label it `status/ready` again. |
| The Crew stays `Waiting` | `kubectl -n webshop get crew orders-cli -o yaml`: `status.conditions` says what it waits for, such as the Container. |
| A Brief stays `NeedsInput` | `kubectl -n webshop get brief webshop -o jsonpath='{.status.conditions[?(@.type=="NeedsInput")].message}'` (or its card in the UI) says what it waits for in one sentence, such as Secret `claude-credentials` missing (step 2); it goes on within a minute of the fix. |
| The agent says it isn't logged in | Secret `theos-system/claude-credentials` (or the project's own `webshop/claude-credentials`) is missing, empty (step 2's length check prints `0`), or its key isn't `CLAUDE_CODE_OAUTH_TOKEN`. |

## Publish releases to GitHub

A Crew with `spec.tagReleases` tags each release (a Gitea milestone titled
`vMAJOR.MINOR.PATCH` with a due date) once its issues are merged, and the tag's
release run attaches the files to a Gitea release. The run is the workflow of
the Container's Deployment, `gitea-release` for `orders-cli`; a Container
scaffolded without one gets no run on a tag, and the Crew never publishes its
releases. Those live in the cluster's Gitea. To have every release also show up on a GitHub repository, with the
same tag, notes and files, give the Crew that repository and a token for it.

> The Crew's `publishReleases` is deprecated: a `github` DeployTarget does the
> same and more (*Publish a service on GitHub* below). It keeps working in
> `v1beta1`; [docs/design.md](design.md#publishing-a-release) says how to
> move over, and the Crew's page in the UI links it.
The factory only adds releases there, and, when you ask for the app's source
too, one commit per release inside the folder you name: it never touches
another file.

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

   **Or a GitHub App,** for a credential that belongs to an organisation,
   expires every hour and is revoked by uninstalling it. On GitHub, *Settings
   → Developer settings → GitHub Apps → New GitHub App* (an organisation's
   own settings for one it owns): no webhook; *Repository permissions:
   Contents: Read and write*, plus *Issues: Read and write* if a Brief takes
   requests from the repository (*Take requests from its GitHub issues*).
   Generate a private key, then *Install App* on the one repository. The App's
   page shows its *App ID*; the installation's URL ends in its *installation
   ID*. The Secret holds the three instead of `token`:

   ```bash
   kubectl -n webshop create secret generic orders-cli-github \
     --from-literal=appId=123456 --from-literal=installationId=7890123 \
     --from-file=privateKey=orders-cli.private-key.pem
   ```

   The operator mints an installation token from them and renews it before
   it expires; a Secret with both `token` and the App's keys uses `token`.
   Copying the image to GHCR (`packagesToken` on a DeployTarget) still needs a
   classic token: GHCR takes no installation token.

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
       latest: true             # optional: false never makes its releases GitHub's Latest
       source:                  # optional: the app's source goes out with each release
         path: cli              # the folder in the repository; "." is the whole repository
         tagPrefix: cli/        # optional; defaults to path + "/", "" for "."
   ```

Once a release's runs succeed, the Crew copies it: the notes are the Gitea
release's, or, when nobody wrote any, the titles of its merged issues, which it
writes on the Gitea release too. It builds a draft, uploads each file, then
publishes it, so a release is never seen half done. Only the last release is
published; one tagged before the previous was published leaves that one to
you, with a Warning Event `ReleaseNotPublished`. Removing `publishReleases`
stops publishing; what is on GitHub stays.

Each release is named after the app and its version, the Container's
`displayName` (else its name) then the tag, such as `Orders CLI v0.2.0`, so a
repository several apps publish to (each with its own `tagPrefix`) says whose
each release is. GitHub's Latest goes to the release published last; set
`latest: false` on every app but the one people install, and only its
releases take Latest.

**The source.** With `source`, each release also puts the app's source, as of
its tag, into that folder of the repository: everything the Container's
repository holds at the tag but the factory's own files (`AGENTS.md`,
`docs/agents/`, `.theos.yaml`, `.devcontainer/`, `.gitea/`). It is one
commit on the default branch that replaces only the folder, so files removed
from the app disappear from it too, and whatever was in it before (a copy made
by hand) is replaced on the first publish; every other file stays. It is
tagged `source.tagPrefix` and the Crew's tag, by default the folder, a slash
and the tag (`cli/v0.2.0`), which is the tag Go looks for, so

```bash
go install github.com/your-name/orders-cli/cli@v0.2.0
```

works once the app's `go.mod` declares `module github.com/your-name/orders-cli/cli`.
A `go.mod` declaring another path is published anyway, with a Warning Event
`SourceNotGoInstallable` that says the path and tag Go expects. The source goes
out first and the release's tag names the same commit, so the release's
*Source code* archive is that app's source. The token needs nothing more than
*Contents: Read and write*; the default branch must take its pushes (no branch
protection the token's user can't pass). Apps sharing a repository each take
their own folder, neither inside the other's; turning `source` on applies from
the next release, and removing it stops publishing the source, leaving what
is on GitHub. With UI actions on, the Crew's release settings (and a Brief's
*Releases published to GitHub*) set the folder and its tag prefix, showing the
tag they make, and the Crew's page links the source at its tag and says each
`Failed` reason in words.

**Reading how it went:**

```bash
kubectl -n webshop get crew orders-cli -o jsonpath='{.status.lastRelease.publish}{"\n"}'
kubectl -n webshop get crew orders-cli -o jsonpath='{.status.conditions[?(@.type=="Published")]}{"\n"}'
kubectl -n webshop events --for crew/orders-cli
```

`publish.state` is `Pending` until the release runs succeed, `Publishing`,
then `Published` with the release's `url` (and `source`, its `commit`, `tag` and
the folder's `url`, once the source is out), or `Failed`. A failure is a Warning
Event `ReleasePublishFailed`, once per reason, and is tried again every
minute, so fixing the cause is enough. The `Published` condition's reason
says what to fix:

| Reason | What to do |
| --- | --- |
| `Waiting` | The release runs haven't succeeded yet; a failed run is published once a re-run succeeds. |
| `SecretMissing` | Create the Secret of step 2, in the Crew's namespace, with the key `token`, or the App's `appId`, `installationId` and `privateKey`; the message names a key that is missing. |
| `SecretUnreadable` | The operator couldn't read the Secret (Forbidden, a timeout); the message says why. Check the operator's role can read Secrets in the Crew's namespace. |
| `Unauthorized` | GitHub refused the token: expired, revoked, or without *Contents: Read and write*. Make a new one and replace the Secret. For an App, GitHub refused to mint its installation token (the message says why): check `appId`, `installationId`, that the private key is the App's current one and that the App is still installed. |
| `RepositoryNotFound` | No such repository, or the token can't see it: check `repository` and the token's *Repository access*. |
| `EmptyRepository` | The repository has no commit to tag; push one. |
| `NoReleaseFiles` | The Gitea release of the tag is missing or has no files: check the tag's release run. |
| `TagTaken` | The repository already has a release or tag of that name the factory didn't make; it leaves it alone. Delete it, or set a `tagPrefix`. |
| `SourceTagTaken` | The repository already has the source tag on another folder tree; it leaves it alone. Delete the tag, or set `source.tagPrefix`. |
| `FolderOverlaps` | Another Crew publishes source to the same repository in this folder, or one inside or around it; the message names it. Both wait until one moves. |
| `SourceTooLarge` | A file of the source is over GitHub's 100 MiB; take it out of the repository, or mark it `export-ignore` in `.gitattributes`. |
| `BranchProtected` | GitHub refused the source commit on the protected default branch: let the token's user push to it, or drop the protection. |
| `GitHubError` | GitHub failed, rate-limited, or couldn't be reached; it is tried again. |

## Ship each release to its targets

A Container names the places its releases ship to in `spec.deploy`, in
order; each is a DeployTarget, a place with its own credential and tier.
`orders-cli` in [story.yaml](../config/samples/first-app/story.yaml) names
`orders-cli-releases`, a GitHub repository whose releases are the deploys:

1. **The token and Secret,** as in steps 1 and 2 above (a token or a GitHub
   App), in a Secret named `orders-cli-deploy`.
2. **The target.** Set your repository in
   [deploytarget.yaml](../config/samples/first-app/deploytarget.yaml) and
   apply it with `kubectl apply -n webshop -f deploytarget.yaml`. Its `Ready`
   condition says whether GitHub takes the token: `SecretIncomplete` names
   the keys missing, and `CredentialRefused` carries GitHub's message.

Once a release's runs succeed, the operator ships it to each entry: the
entry's pinned `version`, else the newest release. An entry with `after`
waits until that earlier entry runs the version, and an entry whose target
is `tier: production` waits for a person, who lets the version in by setting
the entry's `approved`:

```yaml
spec:
  deploy:
    - target: staging
    - target: prod
      after: staging
      approved: v0.3.0   # the version a person let into prod
    - target: oss
      version: v0.2.0    # pinned: how you roll back
```

The list may change at any time; it never scaffolds the Container again,
and removing an entry leaves what runs there alone. A rollout the target's
tool reports failed isn't tried again until a newer release or a pin.

```bash
kubectl -n webshop get container orders-cli -o jsonpath='{.status.deploys}{"\n"}'
kubectl -n webshop get container orders-cli -o jsonpath='{.status.conditions[?(@.type=="Deployed")]}{"\n"}'
kubectl -n webshop events --for container/orders-cli
```

Each of `status.deploys` says the `version` running, the `wantedVersion`,
the `phase` (`Waiting`, `Deploying`, `Deployed`, `Failed`), the `health` the
tool reports, the `url`, and why in `reason` and `message`. `Deployed` is
True once every entry runs its wanted version, else False with the first of
`Incompatible`, `TargetNotReady`, `DeployFailed`, `WaitingForApproval`,
`WaitingForPrevious`, `Deploying` and `NoRelease`. Events: `Deployed` when an
entry turns Deployed, `ApprovalNeeded` once per version, and a Warning
`DeployFailed` once per version and reason.

## Publish a service on GitHub

A `github` DeployTarget does more than copy the release. With `source` it
commits the app's own files at the release's tag into a folder of the
repository, tagged, and makes the GitHub release of that commit; with a
`packagesToken` it copies the release's image to GHCR (a `k8s-manifests`
Container's, or an `operator`'s with `gitea-release`). The
overlay [config/samples/webshop-github](../config/samples/webshop-github/)
publishes the webshop's `orders-api` that way, after it ships to the Argo
CD in the cluster:

1. **The repository and Secret.** Make the repository on GitHub, then the
   Secret `webshop-github` with the key `token` (a fine-grained token for
   that repository alone, *Contents: Read and write*) or the App's `appId`,
   `installationId` and `privateKey`, as in *Publish releases to GitHub*;
   and, for the image, `packagesToken`, a classic token with
   `write:packages` and `read:packages`:

   ```bash
   read -rsp 'GitHub token: ' GH_TOKEN; echo
   read -rsp 'GHCR token: ' GHCR_TOKEN; echo
   kubectl -n webshop create secret generic webshop-github \
     --from-literal=token="$GH_TOKEN" --from-literal=packagesToken="$GHCR_TOKEN"
   unset GH_TOKEN GHCR_TOKEN
   ```

2. **The target and the entry.** Set your repository in
   [deploytarget.yaml](../config/samples/webshop-github/deploytarget.yaml)
   and apply the overlay, which adds `webshop-oss` to `orders-api`'s
   `spec.deploy`, after `kind-cluster`:

   ```bash
   kubectl apply -k config/samples/webshop-github
   ```

   ```yaml
   spec:
     github:
       repository: your-name/webshop
       source: {}                 # folder orders-api/, tags orders-api/v0.1.0
       # source: {path: ., tagPrefix: v}  # the whole repository, for one app
     credentialsSecretRef: {name: webshop-github}
   ```

   Unset, the folder is the Container's name and its tag prefix the folder
   and `/`, so any number of Containers share the target; a `cli` Container
   publishes its files to the release as well, and a service with no
   `packagesToken` publishes its source alone.

3. **Watch it.** Once a release's runs succeed, the entry turns `Deployed`,
   and `status.deploys[].image` names the copy on GHCR
   (`ghcr.io/your-name/webshop/orders-api@sha256:...`):

   ```bash
   kubectl -n webshop get container orders-api -o jsonpath='{.status.deploys}{"\n"}'
   ```

In the UI, the DeployTarget's page shows the repository, the tags, the
source's folder and tag prefix, the image's name and the keys the Secret
takes (never a value: the UI never reads it); the Container's page shows
each entry's image.

## Deploy to a cluster with Argo CD

A Container built with the `k8s-manifests` Deployment (a `service`, `worker`,
`static-site` or `job`) ships to a Kubernetes cluster through Argo CD. The
webshop's four services in [story.yaml](../config/samples/webshop/story.yaml)
name `kind-cluster`, an `argoCD` DeployTarget that deploys into the cluster
the factory runs on:

1. **Argo CD.** The factory never installs it. Its core install (no API
   server, UI or dex) is enough:

   ```bash
   kubectl create namespace argocd
   kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.6/manifests/core-install.yaml
   ```

2. **The target.** [deploytarget-argocd.yaml](../config/samples/webshop/deploytarget-argocd.yaml)
   names Argo CD's namespace and project (`argocd` and `default`, the
   defaults) and the destination namespace `webshop-apps`; the webshop's
   kustomization applies it. It needs no Secret: the operator writes
   Applications with its own ServiceAccount, and `Ready` says whether Argo
   CD and the project are there.
3. **The entry.** Each Container names the target in `spec.deploy`, as
   above. A Container whose Deployment is not `k8s-manifests` (a `cli`, an
   `operator`) gets `Incompatible`.

Once a release's runs succeed, the operator writes one Argo CD Application
per Container, named after the Container's namespace and name: the
repository at the release tag, path `deploy/k8s`, the image CI pushed, by
digest, and automated sync. Argo CD rolls it into `webshop-apps`, and the
operator reads its sync and health back:

```bash
kubectl -n argocd get application webshop-orders-api
kubectl -n webshop get container orders-api -o jsonpath='{.status.deploys}{"\n"}'
kubectl -n webshop-apps get deployment,service
```

The entry's `phase` goes `Deploying` while Argo CD syncs, then `Deployed`,
with `health` as Argo CD reports it (`Healthy`, `Progressing`, `Degraded`);
a sync Argo CD gives up on is `Failed` with reason `SyncFailed` and Argo CD's
message. Nothing is written to the repository at deploy; a rollback pins
`version` in the entry.

The cluster's nodes pull the image from the registry the Container's Git
provider serves. In the Kind test environment that is the Gitea in the
cluster, which the node can't resolve by its Service name, so `make cluster`
maps it for containerd (`hack/kind.sh registry-hosts`) to the Service's fixed
ClusterIP, over plain HTTP. That is the test environment's shortcut: a real
cluster pulls from a registry with a real name and TLS, and the organization
is public, so the pull and the clone need no credential.

## Release a Brief's app

A Brief's app is released when its plan says so: the planner names the
Containers that ship releases in `plan/releases.yaml`, and the Crew the Brief
made for each then tags and prepares releases (`tagReleases` and
`prepareReleases`), once a day by default (*How often a Brief's apps release*,
below). With `every: Release`, each round files that Container's issues in a release
milestone of its own, the next minor version (`v0.1.0`, then `v0.2.0`, ...),
due a week later, so once the round's issues are merged the Crew prepares the
release (version files and a changelog), tags it and its release run makes the
Gitea release. Nothing on the Crew needs editing by hand.

With `every: Release`, the prepare step waits for the Tasks at work to end, so the changelog lists
what the tag carries. Until they do, the Crew's `Released` condition is
`False`, reason `PrepareWaitsForTasks`, and the Crew's message adds "N ready
issues wait for a release to be prepared": it starts no new ready issue but an
urgent one or a Standard change a Blocked Task waits on until the release is
prepared, then picks them up again.

1. **Ask for it.** If the Brief's choices don't say a Container ships
   releases, add a prompt, such as "Release the CLI.". The next round turns
   releases on and files at least one issue for it: a release with no merged
   work is never made.
2. **Say where they go**, for releases on GitHub as well. Make the token and
   the Secret as in steps 1 and 2 of *Publish releases to GitHub*, in the
   Brief's namespace, then name the Container in the Brief's `spec.releases`:

   ```yaml
   spec:
     releases:
     - container: linter                    # the Container, as the plan names it
       github:
         repository: your-name/cel-lens     # owner/name on github.com
         tokenSecretRef: {name: cel-lens-github}
       tagPrefix: ""                        # optional, as on a Crew
       latest: true                         # optional, as on a Crew
       source: {path: linter}               # optional, as on a Crew; entries of one repository need folders apart
   ```

   ```bash
   kubectl -n cel-lens patch brief cel-lens --type merge -p \
     '{"spec":{"releases":[{"container":"linter","github":{"repository":"your-name/cel-lens","tokenSecretRef":{"name":"cel-lens-github"}}}]}}'
   ```

   An entry turns releases on for its Container by itself, and sets the
   `publishReleases` of the Crew the Brief made for it at once, without
   waiting for a round; the next round's planner sees it too and names the
   Container if the plan hasn't made it yet. Up to 10 entries, one per
   Container. With UI actions on, the Brief's page sets them too (*Settings →
   Releases published to GitHub*); the Secret is always yours to make.

Releases only turn on: a later plan that leaves a Container out, or an entry
removed from `spec.releases`, changes nothing on its Crew, and what is on
GitHub stays. To stop releasing, edit the Crew. A Crew the Brief didn't make,
such as one you made for a Container of your own, is never changed: its
releases are yours.

The Brief shows each app's releases in `status.apps`: whether they are on,
where they are published, the latest tag with links to its Gitea and GitHub
releases, and a sentence on how they go, such as how to turn them on.
`kubectl get brief` prints the latest tags:

```bash
kubectl -n cel-lens get brief cel-lens
kubectl -n cel-lens get brief cel-lens -o jsonpath='{range .status.apps[*]}{.container}: {.message}{"\n"}{end}'
```

Why a release run or a publish failed is as in *Publish releases to GitHub*,
on the Crew named after the Container.

### How often a Brief's apps release

A Brief's apps release once a day by default, at 18:00 UTC: every round,
fixes and features alike, files a released app's issues in the release in
progress, and at the cut the Crew ships what is merged by then and moves the
rest to the next release. A day of fixes alone goes out as the next patch
(`v0.14.1` after `v0.14.0`). Agents keep starting and merging Tasks all day;
only while the cut release's prepare issue runs does a Task ready to merge
wait (`WaitingForRelease`), so the changelog holds all the tag carries.
Choose your own in `spec.releaseCadence`, which the Brief writes onto the
Crews it made:

```yaml
spec:
  releaseCadence:
    every: Day                 # Day, Week, or Release: each release as soon as it is done
    at: "09:00"                # HH:MM, 24-hour; default 18:00
    timeZone: Europe/Amsterdam # an IANA name; default UTC
    # weekday: Monday          # only with every: Week; default Friday
```

`every: Release` keeps a release per round, as before this setting: fixes in
the patch release right after the release in progress, other work in the
next minor. A Brief made before it reads `every: Day`, so set `Release` to
keep that. Something that can't wait, such as a fix of a crash users hit in
the latest release, ships at once: ask for it in a prompt ("ship the login
fix today") and the planner marks it, or add the `release/now` label to the
issue in Gitea yourself; once merged, its release goes out with the day's
work so far. The Brief's app line and `status.apps[].nextRelease` say what
the next release holds and when it goes out; a time zone the operator can't
load stops the Crews (`Ready=False`, `InvalidTimeZone`).

### Take requests from its GitHub issues

A Brief can take requests from the issues of a repository its app publishes
to: an open issue by a person you name becomes input to planning, like a
prompt. It is off unless you turn it on, per entry of `spec.releases`, and an
issue by anyone else is left alone: no comment, no label, and its text never
reaches an agent.

1. **The token's permissions.** Edit the entry's fine-grained token on GitHub
   (*Settings → Developer settings → Fine-grained tokens*) and tick, beside
   *Contents: Read and write*: *Repository permissions → Issues: Read and
   write*, and only if you name `teams`, *Organization permissions → Members:
   Read*. No new Secret: the entry's is used. For a GitHub App, give the App
   *Issues: Read and write* (and *Members: Read* for `teams`) and accept the
   new permissions on its installation.
2. **Turn it on** in the entry:

   ```yaml
   spec:
     releases:
     - container: linter
       github:
         repository: your-name/cel-lens
         tokenSecretRef: {name: cel-lens-github}
       requests:
         mode: Ask                  # Off (the default), Ask or On
         authors: [your-name]       # GitHub logins whose issues are requests
         teams: [your-org/maintainers]   # optional: org/slug
   ```

   With UI actions on, the Brief's page sets the same in *Settings*: *Requests
   of linter* on an entry, or the requests fields when publishing an app.
   `authors` or `teams` must name someone unless `mode` is `Off`, and only
   one entry per repository may take requests.

In `Ask`, each new request waits under the Brief's *Needs you* until you
accept or decline it: the *Accept* and *Decline* buttons on its page, or its
issue number added to `requests.accepted` or `requests.declined` with kubectl.
`On` lets every request through when first seen. A request declined before a
round takes it gets a comment saying it isn't taken and is closed. The Brief reads the issues every
ten minutes and at once when the entry changes. A request let through starts
a round, like a new prompt; the round takes it or skips it, and the Brief says
on the issue what it became (the issues and their release) or why it was
skipped, and closes a skipped one. The comment when its release ships is step
5 of [the plan](plans/done/brief-requests.md).

`requests.accepted` and `requests.declined` hold at most 200 numbers each,
and the operator never changes them. Once an issue is closed and done with,
its number does nothing more: the Brief lists those numbers per repository in
`status.requestsSettled`, and *Accept* and *Decline* drop them from both
lists with each answer. With kubectl, remove them yourself when a list nears
200; the API server refuses the 201st. A dropped number whose issue is
reopened is a new request, asked again in `Ask`:

```sh
kubectl get brief app -o jsonpath='{.status.requestsSettled}'
```

The Brief's page lists the open requests with how far each got
(`status.requests`), and the condition `Requests` says whether the issues can
be read:

```bash
kubectl -n cel-lens get brief cel-lens -o jsonpath='{range .status.requests[*]}{.repository}#{.number} {.stage}: {.title}{"\n"}{end}'
kubectl -n cel-lens get brief cel-lens -o jsonpath='{.status.conditions[?(@.type=="Requests")]}{"\n"}'
```

| Reason | What to do |
| --- | --- |
| `TokenLacksIssues` | Tick *Issues: Read and write* on the token. |
| `TokenLacksMembers` | Tick *Organization permissions → Members: Read*, or remove `teams`. |
| `SecretMissing`, `SecretUnreadable`, `Unauthorized`, `RepositoryNotFound` | As for publishing, above. |
| `IssuesDisabled` | Turn issues on in the repository's settings. |
| `GitHubUnavailable`, `GitHubError` | A rate limit or GitHub unreachable; it reads again on the next ten minutes. |

Releases still publish while requests can't be read.

## When CI runs the full check

Each Container chooses when its CI runs the app's full `make check`, in
`spec.checks`:

- `EveryChange`, the default: every pull request and every push to `main` runs
  `make check`.
- `BeforeRelease`: each change runs a light check, `make check-light` (lint
  and the type or vet checks, no tests), trusting the agent's own
  `make check` in its workspace; the full `make check` runs on the commit a
  release will be tagged at, before the tag. Choose it when the full check is
  slow and changes come often.

```bash
kubectl -n webshop patch container orders-api --type merge -p '{"spec":{"checks":"BeforeRelease"}}'
```

With UI actions on, the Container's page sets it too (*When CI runs the full
check*). A new Container is scaffolded with its checks. On a Container already
scaffolded, earlier releases included, patching `spec.checks` is enough, with
no need to recreate it: the change reaches the repository through the Standard
update's pull request, which the factory merges once its checks pass; the page
says when the repository still runs the other mode, and so does
`ChecksApplied`: False, reason `Pending`, until the pull request merges. It takes effect only when the Container's
Standard can do it (`checksBeforeRelease: true` on the Standard: go-standard,
go-operator-standard, ts-standard and python-standard): otherwise CI keeps the
full check on every change, and the Container's `ChecksApplied` condition is
False, reason `StandardRunsFullChecks`, which the page shows.

The light check is a target of the app's `Makefile`. A repository that still
has its Standard's Makefile has it; an app with a Makefile of its own, such as
one a Standard update left out because the app changed the same lines
(`StandardUpdated` False, `FilesBehind`), needs a `check-light` target before
it switches: lint and the type or vet checks, the fast part of its `check`.
Until it has one, the factory holds the switch rather than let CI fail every
change: `ChecksApplied` and `StandardUpdated` are False, reason
`NoLightCheck`, and CI keeps running the full check on every change. Adding
the target, through a normal issue for the app, lets the switch through on
its own.

With `BeforeRelease`, a Crew that tags releases (`tagReleases`) doesn't tag at
once: it pushes the branch `release-check/vX.Y.Z` at the release's commit, waits
for its `full` job, tags once it passes and deletes the branch. If you tag by
hand, do the same, since nothing else runs the full check:

```bash
git push origin <commit>:refs/heads/release-check/v1.4.0
# wait for "check / full (push)" on that commit to pass in the repository's Actions
git tag v1.4.0 <commit> && git push origin v1.4.0
git push origin --delete release-check/v1.4.0
```

A failed `full` job means the commit isn't fit to release: fix it on `main`
and run the check again on the new commit. The Crew tags nothing then (its
`Released` condition says `FullCheckFailed`) and files `Full check fails for
vX.Y.Z` in the release's milestone, whose merge brings the release round again.

One case doesn't reach the repository: a change of checks whose files were
changed in the repository since in a way that doesn't merge with it, such as a
hand-edited `.gitea/workflows/check.yaml`. No pull request opens; the
Container's `StandardUpdated` and `ChecksApplied` are False, reason
`ChecksLeftOut`, naming the file, and CI keeps its mode until the Container's
checks or the Standard's `ref` change again.

## Give agents a browser

An agent's sandbox has the repository's toolchain and nothing else: no
browser's system libraries, no display, no emulator. A test or a picture
script that needs one runs in a **test environment**, a place with those
tools that the factory leases to the run that needs it
([plans/done/test-environments.md](plans/done/test-environments.md)). The chart makes
one in each project, `browser`: Playwright's Chromium, Firefox and WebKit,
and `xvfb-run` for headed programs such as VS Code or an Electron app.

```bash
kubectl -n webshop get testenvironments
# NAME      MAX   READY   REASON   AGE
# browser   2     True    Ready    3m
```

An issue asks for it with the label `env/browser`. The planner and the
triager set it where an issue's criteria need a browser, instead of handing
the work to a person as `kind/by-hand`; you can set it yourself on the Git
host. Each implementer's and verifier's run of that issue leases a pod of
its own, fenced like the sandbox, and runs commands there:

```bash
theos-env run browser -- npx playwright test
theos-env run browser -- xvfb-run -a node scripts/pictures.mjs
```

`theos-env` sends the working tree, runs the command in the same folder
there, shows its output and exits with its code, and writes the files the
command made, such as the pictures, back into the sandbox, where the agent
commits them. An issue that needs an environment the project lacks waits,
`WaitingForEnvironment`, at no cost, until you add one. A Standard or a
Framework can list environments in `spec.testEnvironments` for all its
Containers' runs; one the project lacks shows on the Container as
`TestEnvironments` False.

CI can run the same checks on it. Give the TestEnvironment a pool and have
the workflow name its label:

```bash
kubectl -n webshop patch testenvironment browser --type merge -p '{"spec":{"ci":{"size":1}}}'
```

```yaml
jobs:
  pictures:
    runs-on: env-browser
```

`limits.maxLeases` (2) caps the leases at once, and `limits.maxLeaseMinutes`
(180) how long one lasts. Turn the chart's off with
`testEnvironments.browser.enabled=false`; another environment is a
TestEnvironment of your own, on an image with its tools
(`config/samples/webshop/testenvironment.yaml`). A device on a node comes
through the cluster's device plugin: the release's `env-android` image runs
an Android emulator on KVM (`devices.kubevirt.io/kvm`, in
`testenvironment-android.yaml`; a run boots it with `theos-env run android
-- android-emulator start`), and `env-godot` runs Godot on an NVIDIA GPU
(`nvidia.com/gpu` under RuntimeClass `nvidia`, in
`testenvironment-godot.yaml`). `local.devices`, `runtimeClassName`,
`nodeSelector` and `tolerations` are the cluster's own names. `kind/by-hand`
stays for what no environment has: an account, a purchase, a person's own
device.

### Lease a GPU machine on AWS

A tool no cluster node has, such as a GPU for a game engine, comes from an
EC2 machine in your AWS account: a TestEnvironment with the `aws` member
(`config/samples/webshop/testenvironment-gpu.yaml`) leases one machine per
run, from an AMI with the tools, in a subnet that routes to the internet.
The machine gets no instance role and no SSH key, a security group the
operator makes in the subnet's VPC (`theos-env-<namespace>-<name>-<uid>`:
HTTPS in, where `theos-env` serves, and only DNS and public HTTPS out), and
its metadata service turned off before a run gets it. The run reaches it
over HTTPS with its certificate pinned, as it reaches a local one, and the
operator terminates it when the run ends; it shuts itself down, which
terminates it, at `limits.maxLeaseMinutes`.

Its credential is a Secret only the operator reads, with the keys a
`cloudFormation` DeployTarget's has:

```bash
kubectl -n webshop create secret generic gpu-aws \
  --from-literal=accessKeyId=... --from-literal=secretAccessKey=...
kubectl -n webshop apply -f config/samples/webshop/testenvironment-gpu.yaml
kubectl -n webshop get testenvironment gpu \
  -o jsonpath='{.status.conditions[?(@.type=="CredentialsValid")]}{"\n"}'
```

The key needs `ec2:RunInstances`, `ec2:CreateTags`, `ec2:DescribeInstances`,
`ec2:TerminateInstances`, `ec2:ModifyInstanceMetadataOptions`,
`ec2:DescribeSubnets`, `ec2:DescribeSecurityGroups`,
`ec2:CreateSecurityGroup`, `ec2:DeleteSecurityGroup`,
`ec2:AuthorizeSecurityGroupIngress`, `ec2:RevokeSecurityGroupIngress`,
`ec2:AuthorizeSecurityGroupEgress`, `ec2:RevokeSecurityGroupEgress` and
`pricing:GetProducts`, and no `iam:PassRole`: the machines get no role.
`CredentialsValid` is False, and a run that needs the environment waits
(`WaitingForEnvironment`, `CredentialRefused`), while the Secret is missing,
lacks a key, or holds a key AWS refuses or of another account than
`aws.account`.

Each lease costs the instance type's on-demand Linux price, which the
operator reads from the AWS Price List, for the time it is held (a minute
at least); the TestEnvironment's status shows what today's leases cost.
`limits.budgetUSDPerDay` is required: past it no lease starts that UTC day
(`OverBudget`, a Needs-you item) and the leases held finish their run.

The machine downloads `theos-env` from the chart's
`testEnvironments.toolsURL`, the binaries attached to the release on GitHub,
and checks it against the `SHA256SUMS` next to it; a subnet that can't reach
GitHub needs an address of your own there. The AMI needs a Linux with
`curl`, `sha256sum` and cloud-init. Deleting the TestEnvironment terminates
its machines and deletes its security group.

### Lease a Mac on AWS

macOS, for an iOS app's tests in the Simulator, comes from an EC2 Mac: the
same `aws` member with a Mac instance type, such as `mac2.metal`, and an EC2
macOS AMI with Xcode (`config/samples/webshop/testenvironment-ios.yaml`). A
Mac runs on a dedicated host, which EC2 bills for 24 hours at least from its
allocation and scrubs after each machine, so the operator keeps its hosts:
it allocates one in the subnet's zone when every host it keeps is busy,
places each lease's machine on a free one, and keeps each for
`aws.mac.keepHostHours` from its allocation (24 by default, at least 24),
then releases it once no machine is on it. A lease waits while the host it
would take is scrubbed (about an hour) rather than allocating another for a
day more.

The lease whose machine is the first on a host counts the host's on-demand
price for `keepHostHours` against `limits.budgetUSDPerDay` at once (a `mac2`
host's 24 hours are about 16 US dollars), so the budget must cover them; a
lease on a host kept costs nothing until the host's kept time is over. The
key needs `ec2:AllocateHosts`, `ec2:DescribeHosts` and `ec2:ReleaseHosts`
beside the calls above. The machine downloads `theos-env`'s darwin build,
so `testEnvironments.toolsURL` must name `${os}`, as its default does.
Deleting the TestEnvironment releases its hosts, which EC2 refuses within a
host's 24 hours: the TestEnvironment stays, terminating, until they are
released, a day at most.

### Run an app's tests on real phones

An Android or iOS app's tests on real phones come from AWS Device Farm: a
TestEnvironment with the `deviceFarm` member names a Device Farm project and
a device pool in `us-west-2`, Device Farm's one region
(`config/samples/webshop/testenvironment-devices.yaml`). It runs test
packages rather than commands: Espresso or UI Automator instrumentation
tests (`--type instrumentation`, an `.apk` and its test `.apk`), XCTest
(`xctest`, an `.ipa` and a `.zip`) or XCUITest (`xctest-ui`, two `.ipa`s).
A run whose issue is labelled `env/devices` builds the app and its tests,
commits what they were built from, and runs:

```bash
theos-env test devices --type instrumentation \
  --app app/build/outputs/apk/debug/app-debug.apk \
  --tests app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk
```

The run never holds the credential and can't reach the operator, so the
request goes over the Git host: `theos-env test` pushes it, signed with the
lease's token, to the lease's branch `theos-env/<lease>`. The operator reads
it on its poll, runs it on the pool's devices, pushes the report, each
device's result, the tests that didn't pass and their screenshots and logs
back to the branch, which `theos-env test` writes into `test-runs/devices/`
for the run to commit, and comments the outcome on the Task's pull request.
The TestEnvironment's status lists each run (`status.testRuns`). The branch
is deleted once the lease is returned; no CI runs on it.

A run costs its metered device minutes, at Device Farm's list price of 0.17
US dollars a minute, against the required `limits.budgetUSDPerDay`; past it
a request is answered with nothing run. The credential is a Secret with an
`aws` member's keys, read by the operator alone; its key needs
`devicefarm:GetDevicePool`, `devicefarm:ListTagsForResource`,
`devicefarm:TagResource`, `devicefarm:UntagResource`,
`devicefarm:CreateUpload`, `devicefarm:GetUpload`,
`devicefarm:ListUploads`, `devicefarm:DeleteUpload`,
`devicefarm:ScheduleRun`, `devicefarm:ListRuns`, `devicefarm:StopRun`,
`devicefarm:ListJobs`, `devicefarm:ListUniqueProblems` and
`devicefarm:ListArtifacts`. A lease is a tag on the project, so a project
takes 50 leases at most, of every environment that names it.

#### Run CI jobs on it

With `ci: {}` (as the sample has it), the environment runs the project's CI
jobs that name `runs-on: env-gpu` too: for each such job that waits, the
operator registers a Gitea runner for that one job to the GitProvider's
organization and leases a machine that runs it, within `limits.maxLeases`
and the day's budget, which it shares with the agents' runs. The machine
shuts down, which terminates it, when its job ends, and the operator deletes
its runner. The organization's registration token never leaves the
operator: the machine gets the runner's own credential, which takes that
one job.

The machines reach Gitea from the internet, so the GitProvider's
`ci.giteaActions.publicURL` gives Gitea's public address, `https://` on port
443, the one port their fence lets out; the chart's in-cluster Gitea has
none, so remote CI is off until you set it:

```bash
kubectl -n webshop patch gitprovider gitea --type merge \
  -p '{"spec":{"ci":{"giteaActions":{"publicURL":"https://git.example.com"}}}}'
kubectl -n webshop get testenvironment gpu \
  -o jsonpath='{.status.conditions[?(@.type=="CI")]}{"\n"}'
```

Gitea's own `ROOT_URL` must reach it from there too: the jobs clone from
it. The machine downloads the runner from the chart's
`testEnvironments.runnerURL`, the build of the runner image's version
attached to the release, checked against the same `SHA256SUMS`; the AMI
needs what the jobs' steps run on the machine, such as `git` and `node` for
`actions/checkout`. The `CI` condition says `Serving` once jobs run there,
and why not otherwise: `NoPublicAddress`, `LabelClash` (the GitProvider's
`ci` block names `env-gpu` itself), `Unsupported` (no `runnerURL`), or
`OverBudget`.

## What an app promises: operations

A Container's optional `spec.operations` says what the app promises wherever
it runs: how long its backups are kept (`backups.retentionDays`, 0 for
point-in-time recovery only) and the second AWS region they are copied to
(`backups.copyToRegion`), its recovery objectives (`recovery.rpo` and
`recovery.rto`, such as `15m` or `4h`), and who its alarms tell
(`alerts.email`). Only a Framework that says `readsOperations: true` renders
the block: today the nine AWS Frameworks (`aws-lambda-ts`, `aws-lambda-go`,
`aws-lambda-py`, `aws-ec2-ts`, `aws-fargate-ts`, `aws-static-site-ts`,
`aws-queue-ts`, `aws-schedule-ts` and `aws-event-ts`), none of the
Kubernetes ones. The webshop's Containers all run on Kubernetes Frameworks,
so patching the block onto `orders-api` (`python-fastapi`) shows what the
refusal looks like:

```bash
kubectl -n webshop patch container orders-api --type merge \
  -p '{"spec":{"operations":{"recovery":{"rpo":"15m"},"alerts":{"email":"ops@webshop.example"}}}}'
kubectl -n webshop get container orders-api \
  -o jsonpath='{.status.conditions[?(@.type=="OperationsApplied")]}{"\n"}'
```

The Container's page shows the block, and its `OperationsApplied` condition
is False, reason `NotReadByFramework`, naming `python-fastapi`: the
repository holds none of it, so file an issue in the app that changes it, or
leave the block out. A Container on such a Framework shows no
`OperationsApplied` condition until the block is set, and a Brief's planner
refuses `operations` on it. Take the block off again and the condition goes
with it:

```bash
kubectl -n webshop patch container orders-api --type json \
  -p '[{"op":"remove","path":"/spec/operations"}]'
```

Leave out what you don't need to say: the factory fills in 35 days, `1h`,
`4h`, no copy and nobody told, without writing them into the Container. There
is no tier: whether the app runs as `production` or `development` is the
place it is deployed to, and a deploy that says nothing is `development`. A
Brief's planner writes the block on a Container it makes, from what your idea
states, and never a tier.

On a Framework that reads it, the block renders into the repository when the
Container is scaffolded, and its `OperationsApplied` condition is True.
Change it afterwards and the
operator renders `infra/operations.ts` again from the new block, opens a
pull request with that file alone, and merges it once CI passes:
`OperationsApplied` is False, reason `Updating`, with the pull request's
address while CI runs, then True, reason `UpdateMerged`, naming the commit.
Failed CI leaves it False, reason `ChecksFailed`, with the pull request;
re-run CI and it merges. A Framework that says it reads the block but
renders no `infra/operations.ts` leaves it False, reason
`ChangedAfterScaffold`: file an issue in the app that changes the file.
So does a change on a Container that adopted its repository
(`spec.adopt`), reason `AdoptedRepository`: the factory never scaffolded
it, so it can't know whether the file there is one it can render again.

## Upgrading

Upgrade whenever a release is out, while Tasks run; there is no need to
wait for a quiet moment. Use the new `VERSION` and the same values as the
install (`--reuse-values` keeps them):

```bash
VERSION=0.30.0
helm upgrade theos oci://ghcr.io/jairjosafath/theos/charts/theos --version "$VERSION" \
  -n theos-system --reuse-values --wait --timeout 10m
```

**Running Tasks carry on**, in the same sandbox, at the same round, with the
same token:

- A Task keeps its whole state in its status, so the restarted operator finds
  its sandbox again and reads the result of a run that ended while it was
  away. A sandbox is never changed under a running agent.
- The agents' Git token is read once, when the sandbox starts; the upgrade
  doesn't touch it.
- The seed applies the Agents, the catalog and the GitProviders in place, so
  they keep their UIDs and no Task loses its Agent for a moment.
- A sandbox waits up to about five minutes for a Gitea that restarts on a new
  image. A run that still couldn't reach Gitea is rerun for free, without
  costing the Task a round.

**What changes at once:**

- A new Agent image, model or other Agent value applies from each Task's next
  run; a run already going finishes on the old one.
- A lower `maxRounds` applies to running Tasks too: one already at or past it
  fails if its current round isn't accepted, instead of starting another.
- A new template tag moves each Standard's `ref`, and the operator opens and
  merges a Standard update pull request in each of its Containers. A running
  Task's pull request goes behind `main` and takes a merge round, as after any
  other merge.

**Hand edits** of the chart's Agents, catalog and GitProviders stay only
where the chart doesn't set the field: an `env` you added to an Agent stays,
an `image` or `model` you changed goes back to the chart's. To change what
the chart sets, set its values (`agents.*`, `catalog.*`, [the chart's
README](../dist/chart/README.md#values)), or turn `agents.enabled` or
`catalog.enabled` off and keep your own. An object the chart stops rendering
stays until you delete it.

**Your objects keep working.** Write them at `theos.factory.io/v1beta1`, the
one version served. Within a served version a release never removes, renames
or retypes a field, never adds a required one and never tightens a rule so
that an object you applied before is refused after; a change that must break
waits for a new version that converts your objects. See [Compatibility](design.md#compatibility).

**A quiet moment anyway.** Pause a Brief, and with it every Crew it made, in
one step: the Pause button on its card in the UI sets its `spec.paused` to
`true`, and Resume sets it back to `false`. Pause a Crew of your own with its
`spec.paused` through kubectl, or, with the UI's actions on (*UI actions behind
an Ingress* below), with the Pause crew button on its page. A paused Crew starts no issue and a paused
Brief no round, but a Task already started runs to its end. The Brief's
`Paused` condition, which its card shows while it is paused, counts them down
until it says `Paused; nothing is running`; for a Crew, `kubectl get tasks`
shows which are still running. With kubectl:

```bash
kubectl -n cel-lens patch brief cel-lens --type merge -p '{"spec":{"paused":true}}'
kubectl -n cel-lens get brief cel-lens -o jsonpath='{.status.conditions[?(@.type=="Paused")].message}'; echo
kubectl -n webshop patch crew orders-cli --type merge -p '{"spec":{"paused":true}}'
kubectl -n webshop get tasks
```

Resume with `false`, or a Brief with its card's Resume button. Resuming a
Brief unpauses only the Crews it paused (the annotation
`theos.factory.io/paused-by: brief` marks them), so a Crew you paused by hand
stays paused.

## UI actions behind an Ingress

Off by default, the UI's actions (a Brief's and a Crew's settings, a release
re-run, a scaffold retry, a Container's checks and its answer to a Standard's
bump, a Task from a goal, a Standard's `ref`) are made as
the UI's ServiceAccount over port-forward, with `ui.actions.enabled: true`
alone. Behind an Ingress a person signs in with OIDC, and every write is made
as them, so your cluster's RBAC decides who does what.

1. Register the UI as a client at your issuer (Dex, Keycloak, Google, ...),
   with the redirect URL `https://factory.example.com/ui/callback` for your
   Ingress host, and keep its client secret in a Secret:

   ```bash
   kubectl -n theos-system create secret generic theos-ui-oidc \
     --from-file=client-secret=./client-secret   # a file holding the secret, mode 0600
   ```

2. Set the values and upgrade:

   ```yaml
   ui:
     allowedHosts: [factory.example.com]
     actions:
       enabled: true
     auth:
       oidc:
         issuerURL: https://dex.example.com   # exactly as the ID tokens name it
         clientID: theos-ui
         existingSecret: theos-ui-oidc
         scopes: [openid, email, profile, groups]   # groups only if the issuer has it
         actionGroups: [factory-actors]            # may take every action
   ```

   Then put your Ingress in front of Service `theos-ui`, port 8080, with
   TLS: the session cookie is `Secure`, so sign-in needs `https`.

3. Grant the people. The UI acts as `oidc:` + their verified email and as
   `oidc:` + each of their groups (`usernamePrefix` and `groupsPrefix` change
   the prefixes), so no one signed in becomes a cluster user or a `system:`
   group. `actionGroups` binds ClusterRole `theos-ui-actions` (exactly
   the UI's writes) to those groups; grant anyone else with your own binding,
   in one namespace if you like:

   ```bash
   kubectl -n webshop create rolebinding ui-actions-alice \
     --clusterrole=theos-ui-actions --user=oidc:alice@example.com
   ```

4. Sign in. With OIDC on, the UI's top bar shows **Sign in** (it opens
   `https://factory.example.com/ui/login`, which sends you to the issuer and
   back to the page you left); a write made while signed out is refused with
   "Sign in to act" and the same link. Signed in, the top bar shows who the UI
   acts as, and **Sign out**.

The UI's ServiceAccount gets `impersonate` on users and groups, which RBAC
can't narrow to the prefixed names: the UI pod can act as anyone, so keep
who can exec into it or read its token as short as for the operator. Reads
stay the ServiceAccount's, the same for everyone who reaches the page. A
restart of the UI, or the ID token's expiry, signs everyone out.

## Next

- Your own project: add it to `gitea.projects` and `helm upgrade` with the
  same values ([Upgrading](#upgrading)).
- [docs/design.md](design.md) explains the model, the catalog, Crews and
  Standards; [the chart's README](../dist/chart/README.md) lists its values.
- A problem with the release: [report it](https://github.com/JairJosafath/theos/issues/new?template=user-report.yml).
