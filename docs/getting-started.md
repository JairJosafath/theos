# Getting started: your first app from the release

This guide takes a Kubernetes cluster from nothing to an app the factory builds:
it installs the factory from the published release, describes a small Go
command-line program as a C4 model, and has a Crew of Claude Code Agents turn
a Gitea issue into a merged pull request. [The short path](#the-short-path-describe-the-app)
does the same from an idea in plain words: a planner Agent writes the model
and the first issues. Nothing is built from this repository;
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

The commands use the release `0.7.0`; for a later one, change `VERSION`. Run
them from a clone of this repository at that release's tag, so the sample files
match the release (`gh` clones it while the repository is private). From the
release after `0.3.0`, the chart brings the catalog and the Agents, pinned to
its own template tags and agent image, and the samples make neither; `0.3.0`'s
samples still hold their own `catalog.yaml` and `agents.yaml`.

```bash
VERSION=0.7.0
gh repo clone JairJosafath/c4factory && cd c4factory && git checkout "v$VERSION"
```

## 1. Install the factory

**Agent Sandbox.** The operator runs its agents in Sandboxes and won't start
without the `Sandbox` kind:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.4/sandbox.yaml
```

**Namespaces.** The release goes in `c4-factory-system` and Gitea in `gitea`.
Each project lives in a namespace of its own name, which the chart makes; this
guide's is `webshop`:

```bash
for ns in c4-factory-system gitea; do
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
for ns in c4-factory-system gitea; do
  kubectl -n $ns create secret docker-registry ghcr --docker-server=ghcr.io \
    --docker-username="$(gh api user -q .login)" --docker-password="$(gh auth token)"
done
```

**The release.** `gitea.projects` lists the projects: for each one the chart
makes its namespace, labeled `c4.factory.io/shared-secrets=true` so the
operator shares the pull secret and the agents' token into it, and the seed
makes a Gitea organization, Secret `gitea-credentials` with its token
and GitProvider `gitea`, and the chart makes the catalog (the templates the seed
pushed, pinned to their tags: Frameworks `go-cli`, `node-cli`,
`python-cli`, `python-fastapi`, `react-vite` and `vscode-extension`, Standards `go-standard`,
`ts-standard` and `python-standard`, Compute `small`,
Deployments `k8s-manifests` and `gitea-release`, Connector `postgres`) and four
Agents on the release's `agent-go` image: `planner`, `designer` and `reviewer`
on Opus, `implementer` on Sonnet. The Agents read Secret `claude-credentials`,
which you make in step 2. A fifth, `stand-in`, needs no token: it plans from
the catalog and makes a scripted change, to show the loop
([without a token](#without-a-token)). `catalog.enabled=false` and `agents.enabled=false`
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
with `claude setup-token` and store it once in Secret `claude-credentials` in
the release's namespace, `c4-factory-system`; the operator copies it into
every project namespace and keeps the copies in step, so a new token reaches
every project. The Agents name the Secret and hold no token. Run
`claude setup-token` on its own first, then the rest, and paste the token at
the prompt (it isn't echoed, and stays out of your shell history):

```bash
claude setup-token
```

```bash
read -rs -p 'Claude token: ' CLAUDE_TOKEN; echo
[ -n "$CLAUDE_TOKEN" ] && printf '%s' "$CLAUDE_TOKEN" | kubectl -n c4-factory-system create secret generic claude-credentials \
  --from-file=CLAUDE_CODE_OAUTH_TOKEN=/dev/stdin
unset CLAUDE_TOKEN
kubectl -n c4-factory-system get secret claude-credentials -o go-template='{{len .data.CLAUDE_CODE_OAUTH_TOKEN}}{{"\n"}}'
```

The last line prints the stored token's length in base64, never the token;
`0` or an error means the paste didn't reach `read` (pasting both blocks at once does that): delete the
Secret and run the second block again.

With an API key instead, name the key `ANTHROPIC_API_KEY`: every key of the
Secret becomes an environment variable in the agent's sandbox.

A project on a token of its own: make `claude-credentials` in its namespace
instead. The operator never changes or deletes a Secret it didn't copy, and
each Project's condition `SecretsShared` says which one it uses.

## The short path: describe the app

From the release after `0.3.0`, steps 1 and 2 are all you do by hand: install
the factory, make the token Secret, and describe the app in plain words. A
planner Agent writes the model, a Crew per Container and the first issues, and
says what it chose; steps 3 to 5 show the same loop written by hand. For a
project of its own, say `cel-lens`, set `gitea.projects={cel-lens}` where
step 1 says `webshop`. The seed makes Project `cel-lens`, so the UI
offers the project.

Open the UI:

```bash
kubectl -n c4-factory-system port-forward svc/c4-factory-ui 8080 &   # http://localhost:8080
```

Pick project `cel-lens`, write the idea in the box under *Describe an app*,
and create the Brief. Or, the same without the UI,
[config/samples/brief/cel-lens.yaml](../config/samples/brief/cel-lens.yaml)
holds the idea from [#240](https://github.com/JairJosafath/c4factory/issues/240):

```bash
kubectl apply -f config/samples/brief/cel-lens.yaml
```

The Brief's phase goes `Planning` (the planner works in the Brief's own Gitea
repository, `cel-lens/brief-cel-lens`), `Applying`, then `Planned`, in 10 to
30 minutes. Its card in the UI, and its status, list what it chose, the
objects it created and the issues it filed; each Container's Crew then picks
up its issues as in step 5:

```bash
kubectl -n cel-lens wait brief/cel-lens --for=jsonpath='{.status.phase}'=Planned --timeout=45m
kubectl -n cel-lens get brief cel-lens -o jsonpath='{range .status.choices[*]}- {@}{"\n"}{end}'
kubectl -n cel-lens get c4
```

Steer it with a follow-up in the card's prompt field, such as "Support
ValidatingAdmissionPolicy files first.". A follow-up never scaffolds a
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
kubectl -n cel-lens patch brief cel-lens --type merge -p '{"spec":{"autopilot":"On"}}'
```

Either way the Crew's three tries hold, and each tending round is in the
Brief's history, so a prompt can overrule it.

### Without a token

To watch the loop before making the token Secret, name Agent `stand-in` for
every role. [config/samples/brief/stand-in.yaml](../config/samples/brief/stand-in.yaml)
does that in `webshop`. The stand-in reads only the catalog: it adds Container
`stand-in-demo` on the first Framework, `go-cli`, files one issue for it,
and the Container's Crew picks the issue up. It plans in a minute or so, and
every release checks it does:

```bash
kubectl apply -f config/samples/brief/stand-in.yaml
kubectl -n webshop wait brief/stand-in-demo --for=jsonpath='{.status.phase}'=Planned --timeout=10m
kubectl -n webshop get c4
```

Deleting a Brief keeps what it made; `kubectl -n webshop delete brief,container
stand-in-demo` removes the Container and its Crew too.

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
| A sandbox pod in `ImagePullBackOff` in `webshop` | Secret `c4-factory-system/ghcr` is missing, the install lacks `global.imagePullSecrets`, or namespace `webshop` lacks the label `c4.factory.io/shared-secrets=true`; `kubectl -n webshop get project webshop -o jsonpath='{.status.conditions[?(@.type=="SecretsShared")].message}'` says which. Fix it, then delete the pod. |
| `helm install` times out | Gitea's image is still pulling; `kubectl -n gitea get pods`, then `helm upgrade` with the same settings. |
| Container `orders-cli` not `Ready` | `kubectl -n webshop describe container orders-cli`: the condition's message says which template or GitProvider is missing. |
| The issue is labelled `status/needs-human` before any Task | The Crew rejected it and said why in a comment, such as a missing checklist; fix the body and label it `status/ready` again. |
| The Crew stays `Waiting` | `kubectl -n webshop get crew orders-cli -o yaml`: `status.conditions` says what it waits for, such as the Container. |
| A Brief stays `NeedsInput` | `kubectl -n cel-lens get brief cel-lens -o jsonpath='{.status.conditions[?(@.type=="NeedsInput")].message}'` (or its card in the UI) says what it waits for in one sentence, such as Secret `claude-credentials` missing (step 2); it goes on within a minute of the fix. |
| The agent says it isn't logged in | Secret `c4-factory-system/claude-credentials` (or the project's own `webshop/claude-credentials`) is missing, empty (step 2's length check prints `0`), or its key isn't `CLAUDE_CODE_OAUTH_TOKEN`. |

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
| `SecretUnreadable` | The operator couldn't read the Secret (Forbidden, a timeout); the message says why. Check the operator's role can read Secrets in the Crew's namespace. |
| `Unauthorized` | GitHub refused the token: expired, revoked, or without *Contents: Read and write*. Make a new one and replace the Secret. |
| `RepositoryNotFound` | No such repository, or the token can't see it: check `repository` and the token's *Repository access*. |
| `EmptyRepository` | The repository has no commit to tag; push one. |
| `NoReleaseFiles` | The Gitea release of the tag is missing or has no files: check the tag's release run. |
| `TagTaken` | The repository already has a release or tag of that name the factory didn't make; it leaves it alone. Delete it, or set a `tagPrefix`. |
| `GitHubError` | GitHub failed, rate-limited, or couldn't be reached; it is tried again. |

## Release a Brief's app

A Brief's app is released when its plan says so: the planner names the
Containers that ship releases in `plan/releases.yaml`, and the Crew the Brief
made for each then tags and prepares releases (`tagReleases` and
`prepareReleases`). Each round files that Container's issues in a release
milestone of its own, the next minor version (`v0.1.0`, then `v0.2.0`, ...),
due a week later, so once the round's issues are merged the Crew prepares the
release (version files and a changelog), tags it and its release run makes the
Gitea release. Nothing on the Crew needs editing by hand.

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
   ```

   ```bash
   kubectl -n cel-lens patch brief cel-lens --type merge -p \
     '{"spec":{"releases":[{"container":"linter","github":{"repository":"your-name/cel-lens","tokenSecretRef":{"name":"cel-lens-github"}}}]}}'
   ```

   An entry turns releases on for its Container by itself, and sets the
   `publishReleases` of the Crew the Brief made for it at once, without
   waiting for a round; the next round's planner sees it too and names the
   Container if the plan hasn't made it yet. Up to 10 entries, one per
   Container. The UI doesn't set them: use kubectl or GitOps, like the Secret.

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

## Next

- Your own project: add it to `gitea.projects` and `helm upgrade`.
- [docs/design.md](design.md) explains the model, the catalog, Crews and
  Standards; [the chart's README](../dist/chart/README.md) lists its values.
- A problem with the release: [report it](https://github.com/JairJosafath/c4factory/issues/new?template=user-report.yml).
