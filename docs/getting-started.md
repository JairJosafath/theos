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

The commands use the release `0.23.0`; for a later one, change `VERSION`. Run
them from a clone of this repository at that release's tag, so the sample files
match the release (`gh` clones it while the repository is private). From the
release after `0.3.0`, the chart brings the catalog and the Agents, pinned to
its own template tags and agent image, and the samples make neither; `0.3.0`'s
samples still hold their own `catalog.yaml` and `agents.yaml`.

```bash
VERSION=0.23.0
gh repo clone JairJosafath/c4factory && cd c4factory && git checkout "v$VERSION"
```

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
pushed, pinned to their tags: Frameworks `aws-ec2-ts`, `aws-fargate-ts`, `aws-lambda-go`, `aws-lambda-ts`, `go-cli`, `go-kubebuilder`, `node-cli`,
`python-cli`, `python-fastapi`, `react-vite` and `vscode-extension`, Standards `aws-standard`, `go-standard`,
`go-operator-standard`, `ts-standard` and `python-standard`, Compute `small`,
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

Open *Briefs* in the top bar, pick project `cel-lens`, write the idea in the
box under *Describe an app*, and create the Brief. Or, the same without the UI,
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
`docs/agents/`, `.c4factory.yaml`, `.devcontainer/`, `.gitea/`). It is one
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
| `SecretMissing` | Create the Secret of step 2, in the Crew's namespace, with the key `token`. |
| `SecretUnreadable` | The operator couldn't read the Secret (Forbidden, a timeout); the message says why. Check the operator's role can read Secrets in the Crew's namespace. |
| `Unauthorized` | GitHub refused the token: expired, revoked, or without *Contents: Read and write*. Make a new one and replace the Secret. |
| `RepositoryNotFound` | No such repository, or the token can't see it: check `repository` and the token's *Repository access*. |
| `EmptyRepository` | The repository has no commit to tag; push one. |
| `NoReleaseFiles` | The Gitea release of the tag is missing or has no files: check the tag's release run. |
| `TagTaken` | The repository already has a release or tag of that name the factory didn't make; it leaves it alone. Delete it, or set a `tagPrefix`. |
| `SourceTagTaken` | The repository already has the source tag on another folder tree; it leaves it alone. Delete the tag, or set `source.tagPrefix`. |
| `FolderOverlaps` | Another Crew publishes source to the same repository in this folder, or one inside or around it; the message names it. Both wait until one moves. |
| `SourceTooLarge` | A file of the source is over GitHub's 100 MiB; take it out of the repository, or mark it `export-ignore` in `.gitattributes`. |
| `BranchProtected` | GitHub refused the source commit on the protected default branch: let the token's user push to it, or drop the protection. |
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

The prepare step waits for the Tasks at work to end, so the changelog lists
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
   Read*. No new Secret: the entry's is used.
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
5 of [the plan](plans/brief-requests.md).

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

## Upgrading

Upgrade whenever a release is out, while Tasks run; there is no need to
wait for a quiet moment. Use the new `VERSION` and the same values as the
install (`--reuse-values` keeps them):

```bash
VERSION=0.23.0
helm upgrade c4-factory oci://ghcr.io/jairjosafath/c4factory/charts/c4-factory --version "$VERSION" \
  -n c4-factory-system --reuse-values --wait --timeout 10m
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

**A quiet moment anyway.** Pause a Brief, and with it every Crew it made, in
one step: the Pause button on its card in the UI sets its `spec.paused` to
`true`, and Resume sets it back to `false`. Pause a Crew of your own with its
`spec.paused` through kubectl; the UI has no Crew button. A paused Crew starts no issue and a paused
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
`c4.factory.io/paused-by: brief` marks them), so a Crew you paused by hand
stays paused.

## UI actions behind an Ingress

Off by default, the UI's actions (a Brief's and a Crew's settings, a release
re-run, a scaffold retry, a Task from a goal, a Standard's `ref`) are made as
the UI's ServiceAccount over port-forward, with `ui.actions.enabled: true`
alone. Behind an Ingress a person signs in with OIDC, and every write is made
as them, so your cluster's RBAC decides who does what.

1. Register the UI as a client at your issuer (Dex, Keycloak, Google, ...),
   with the redirect URL `https://factory.example.com/ui/callback` for your
   Ingress host, and keep its client secret in a Secret:

   ```bash
   kubectl -n c4-factory-system create secret generic c4-factory-ui-oidc \
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
         clientID: c4-factory-ui
         existingSecret: c4-factory-ui-oidc
         scopes: [openid, email, profile, groups]   # groups only if the issuer has it
         actionGroups: [factory-actors]            # may take every action
   ```

   Then put your Ingress in front of Service `c4-factory-ui`, port 8080, with
   TLS: the session cookie is `Secure`, so sign-in needs `https`.

3. Grant the people. The UI acts as `oidc:` + their verified email and as
   `oidc:` + each of their groups (`usernamePrefix` and `groupsPrefix` change
   the prefixes), so no one signed in becomes a cluster user or a `system:`
   group. `actionGroups` binds ClusterRole `c4-factory-ui-actions` (exactly
   the UI's writes) to those groups; grant anyone else with your own binding,
   in one namespace if you like:

   ```bash
   kubectl -n webshop create rolebinding ui-actions-alice \
     --clusterrole=c4-factory-ui-actions --user=oidc:alice@example.com
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
- A problem with the release: [report it](https://github.com/JairJosafath/c4factory/issues/new?template=user-report.yml).
