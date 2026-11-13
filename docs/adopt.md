# Bring a repository you already have

You have a repository that works. This page puts a Crew on it: agents take
its issues, its own CI checks their changes, and you approve what merges.
Nothing is scaffolded and nothing in the repository is rewritten.

It follows [getting started](getting-started.md), steps 1 and 2: a factory
that runs, a project (`webshop` there) with its GitProvider and Agents, and
the agents' token. It takes a clone of the repository and Go, to run one
command from a clone of theos at the release's tag.

What an adopted repository gets, and what it doesn't:

- A Crew that works its issues like any other: a Task per issue, in a
  sandbox, checked by **your** command and **your** CI, reviewed by an Agent.
- No Framework, and no Standard until you name one. The factory writes no
  file of its own to `main`: a Standard you name arrives as a pull request
  that you merge ([step 6](#6-name-a-standard-when-you-want-one)), and
  nothing is wired from the model's Relationships.
- A fence that is the same as everywhere: agents work only on a repository
  whose `main` requires its checks, and they can't change the CI workflows,
  `.theos.yaml` or `.devcontainer/`.

Today the repository lives on the factory's own Git host. Working on GitHub
in place is [planned](plans/github-host.md).

## 1. Ask the factory what it sees

In a clone of theos, with the repository's clone at `~/src/invoices`:

```bash
go run ./cmd/theos-session adopt --dir ~/src/invoices \
  --project webshop --git-provider gitea --write-identity > model.yaml
```

It talks to no cluster and applies nothing. It reads the build files at the
repository's root and its CI workflows, and prints two things.

**The model**, in `model.yaml`: a SoftwareSystem, the Container that adopts
the repository, and a Crew.

```yaml
apiVersion: theos.factory.io/v1beta1
kind: Container
metadata:
  name: invoices
  namespace: webshop
spec:
  softwareSystemRef:
    name: webshop
  displayName: invoices
  type: service
  gitProviderRef:
    name: gitea
  adopt: true
  checkCommand: cargo test
  checkContexts:
  - "ci / test*"
```

- `adopt: true` takes the repository as it is.
- `checkCommand` is what checks a change in the agent's workspace. It is
  left out when the repository's Makefile has a `check` target, which is the
  factory's default (`make check`).
- `checkContexts` are the checks `main` will require, as your CI reports
  them: `ci / test*` is the job `test` of the workflow `ci`, on every event.

**What to do next**, on your terminal, with what it could not tell. It never
guesses: a repository with no workflow that runs on a pull request, or no
test command it can read, gets a line that says so, and the same line is a
comment at the top of `model.yaml`. Read the model and correct it; `type`
is `service` when nothing in the repository says what it is.

[config/samples/adopt/proposed/](../config/samples/adopt/proposed/) has what
it proposes for eight repositories: Go, Node and TypeScript, Python, with
and without a Makefile, and one it can tell nothing about.

## 2. Say that the factory may work here

`--write-identity` wrote `.theos.yaml` into the clone:

```yaml
project: webshop
softwareSystem: webshop
container: invoices
type: service
scaffoldedFrom: []
```

Commit it. The factory adopts a repository only when this file on `main`
names the project and the Container: it is how the repository's owner says
yes, and the operator never writes it for you.

```bash
cd ~/src/invoices
git add .theos.yaml && git commit -m "Let project webshop work here"
```

## 3. Apply the model, then push the history

```bash
kubectl apply -f model.yaml
kubectl -n webshop get container invoices \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
```

The Container fails, `NothingToAdopt`: the operator made the repository
`webshop/invoices` on the Git host and it is empty. Push your history to it,
`main` first, as the Git host's admin (the port-forward and the admin's
Secret are in [getting started](getting-started.md#3-the-model-and-the-crew)):

```bash
ADMIN=$(kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.username}' | base64 -d)
PASS=$(kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.password}' | base64 -d)
git push "http://$ADMIN:$PASS@localhost:3000/webshop/invoices.git" HEAD:main
```

Then have the Container look again:

```bash
kubectl -n webshop annotate container invoices theos.factory.io/retry=$(date +%s) --overwrite
```

It goes `Ready`, "Adopted repository". If it says `IdentityMismatch`,
`main` has no `.theos.yaml` that names this project and Container; if
`NoDefaultBranch`, the repository's default branch isn't `main`: rename it.

## 4. Get main protected by your own checks

No agent works on a repository whose `main` doesn't require its checks. The
operator reads what `main`'s head reports and says where it stands:

```bash
kubectl -n webshop get container invoices \
  -o jsonpath='{.status.conditions[?(@.type=="Protected")].message}{"\n"}'
```

- **"main requires the checks matching ci / test\*, which its head reports"**:
  done. The operator protected `main` with those checks; the workflows,
  `.theos.yaml` and `.devcontainer/` are out of an agent's reach.
- **"main's head reports no check matching … ; it reports ci / build (push)
  and …"**: your CI calls its checks something else. Put a pattern for
  each check a change must pass in `spec.checkContexts` and apply the model
  again. Until `main` reports them the operator protects nothing: a pattern
  nothing reports would block every merge, yours too.
- **"… it reports no check at all"**: nothing ran on `main`. The
  repository's workflows have to run on **this** Git host. It runs the
  workflows under `.gitea/workflows/`, or the ones under
  `.github/workflows/` when there are none there, on the project's own
  runners, and a job runs only when its `runs-on` is a label one of them
  has:

  ```bash
  kubectl -n webshop get gitprovider gitea -o jsonpath='{.spec.ci}{"\n"}'
  ```

  A workflow written for another host's runners (`runs-on: ubuntu-latest`)
  waits forever here. Give the job a label from that list, in an image that
  has what the job needs, or add a pool with your label to the
  GitProvider's `ci`. The plainest thing that works is one workflow with
  one job that runs your check command.

Meanwhile the Crew waits and says why (`Ready=False`,
`ContainerNotProtected`); it starts nothing on an unprotected repository.

## 5. Start small

The Crew in `model.yaml` holds every change for you:

```yaml
  approval:
  - require: Person
```

An agent implements an issue, your CI and the reviewer Agent pass it, and
then the pull request waits for your approval; it is under *Needs you* in
the UI. Loosen it rule by rule as you gain trust, documentation first:
[Start small](getting-started.md#start-small-let-the-factory-merge-only-what-you-are-sure-of)
has the rules.

The Crew's first issue is its own. Once `main` is protected it files
"Write AGENTS.md for this repository, and say what you found" and builds
it: the pull request adds the guide every later agent reads first (how to
build, test and run the repository, where things are), and its description
lists what the agent found that you should know, such as a test that fails
on `main`. It is the first thing that waits for your approval, and it
changes none of your code.

Then [file an issue](getting-started.md#4-the-first-issue) of your own in
`webshop/invoices`, labelled `status/ready`, with an *Acceptance criteria*
checklist, and [watch it build](getting-started.md#5-watch-it-build).

## 6. Name a Standard when you want one

A Standard is the factory's rules for a kind of repository: an agent guide,
a `make check` and the CI workflow that runs it. An adopted repository
works without one. To take one, set `spec.standardRef` on the Container,
and remove `checkCommand` and `checkContexts`: a Standard brings its own
check, and the API refuses both together. A Standard fits only the
Frameworks it lists (`kubectl -n webshop get standards`), so the Container
names one of those as its `frameworkRef` too; nothing is scaffolded from
it.

The Container stays `Ready`, and nothing is written to `main`. The operator
opens **one pull request**, "Bring in the Standard *name* at *ref*", from
the branch `standard-adopt/<name>-<ref>`, and says where it is:

```bash
kubectl -n webshop get container invoices \
  -o jsonpath='{.status.conditions[?(@.type=="StandardUpdated")].message}{"\n"}'
```

It holds the Standard's files that `main` lacks or has with other content,
and its description lists them, the ones the Standard protects marked
`(protected)`. It is yours to merge; the operator never merges it.

- **Read it like any change.** A file that was there is replaced: a
  Makefile, a workflow. Push to the branch what the repository must keep.
  The protected files have to stay as the Standard has them; any other file
  you change in the pull request is the repository's own from then on.
- **Merge it,** and the operator records the Standard, protects `main` with
  the Standard's checks, and the Crew goes on. If a protected file differs
  on `main` after the merge, the condition says `StandardFilesDiffer` and
  names it; bring it to what the Standard renders and the operator finds
  it, with no pull request needed.
- **Close it** to stay without the Standard's files. The condition says
  `UpdateClosed` and which files are missing; the Container stays `Ready`.
  A new pull request comes when the Standard's ref or the Container's
  `checks` change, and files you bring to `main` by hand are found.
- **Change your mind** by naming another Standard, or none: the operator
  closes the pull request it opened and, for another Standard, opens that
  one's.

**While the pull request is open, or closed without the files, no agent
works on the repository.** The Crew starts no Task and a Task made by hand
waits, `ContainerNotProtected`, with the condition's words: `main` doesn't
require the Standard's checks yet. The pull request stays at the ref it
was opened at; a newer ref of the Standard follows after the merge, as an
ordinary update the operator merges once CI passes.

If `main` was already protected by your own checks (step 4), that rule
stays on the Git host while the pull request is open: it requires the old
checks and keeps the workflow paths and the merge to the operator, so
merging the pull request takes an account the host lets merge past the
rule, an administrator of the repository.

## What is and isn't tested

Each step above is held by a test that runs with every change to theos, on
a stand-in Git host: the command and its proposals
(`cmd/theos-session/adopt_test.go`, `internal/model/adopt_test.go`, and
`make validate-yaml` on the proposals); the empty repository, the push and
the retry, and the adoption on the identity alone
(`internal/controller/container_adopt_test.go`); the protection by your
checks, the waiting Crew and a Task that runs your check command to a merge
(`internal/controller/container_adopt_checks_test.go`); the Crew's first
issue, filed once (`internal/controller/crew_onboarding_test.go`); the
Standard's pull request, merged, closed, changed and renamed, and the Crew
and the Tasks that wait for it
(`internal/controller/container_adopt_standard_test.go`).

Not tested: an adoption on a real cluster against a real Gitea, whether
a given workflow of yours runs on the factory's runner images, and merging
the Standard's pull request on a real host whose `main` already has the
rule of step 4. If a step
here doesn't do what it says,
[report it](https://github.com/JairJosafath/theos/issues/new?template=user-report.yml).

## What comes next

[plans/adopt-existing.md](plans/adopt-existing.md) has the plan, what each
step found and what it left as it is.
