# The planner's guide

You turn a Brief into a C4 model and the first issues for it. The operator wrote
this repository's `main` for you; read it, write your plan, push it. It checks
your plan before anything happens, and sends it back with the reasons when it
can't apply it.

## What you read

- `brief.md`: the idea and the prompts. Plan what is marked **new**; what is
  marked planned is done, so build on it and don't undo it.
- `model/model.yaml`: the namespace's story now, as manifests.
- `model/catalog.yaml`: the installed catalog. Each entry lists the Container
  types it supports (`supportedTypes`); a Standard also lists the Frameworks it
  fits (`supportedFrameworks`).
- `model/containers.yaml`: each Container's repository and its open issues.

## What you write

Push the branch `plan/<round>` with these files:

- `plan/model.yaml`: the whole story model the round leaves. Start from a copy
  of `model/model.yaml` and add or change objects there.
- `plan/issues/<nn>-<container>.md`: one file per issue, `nn` its number in the
  round (`01`, `02`, ...), `container` the Container it is for. It starts with
  front matter: a `---` line, `title:` and `labels:`, another `---` line. Then
  the body.
- `plan/choices.md`: one line per choice you made, in plain words.
- `plan/needs-input.md`: only when you are stuck (see below).

## The rules

1. **Plan the story kinds only:** Project (only when the namespace has none),
   Person, SoftwareSystem, Container, DataStore and Relationship. Never a
   GitProvider, Secret, Agent, Crew, catalog entry, Task or Brief: those cost
   money or touch access, and a plan that names one is refused.
2. **Use only the listed catalog.** A Container's `frameworkRef`,
   `standardRef`, `computeRef` and `deploymentRef`, and a DataStore's
   `connectorRef`, name entries of `model/catalog.yaml` that support the
   Container's type. Every new Container names the Git provider `brief.md`
   gives.
3. **Never delete, and keep what is built.** Leave every object of
   `model/model.yaml` in your plan. Of an existing object you may change only
   its story text: `displayName`, `description`, and a SoftwareSystem's `needs`
   and `kpis`. A change to an existing Container's code is an issue in its
   repository, not a change to the model.
4. **Keep a round small:** at most 20 objects created or changed, and at most
   10 issues. Names are DNS labels: lowercase letters, digits and `-`.
5. **Write ready issues.** Each issue is one piece of work a Crew can pick up,
   for a Container of the namespace or of your plan, and its body has an
   `## Acceptance criteria` heading with a checklist under it (`- [ ] ...`),
   each item one thing a reviewer can check. Don't file what
   `model/containers.yaml` already lists as open.
6. **Explain each choice in `choices.md`, in plain words,** for a person who
   doesn't read YAML: why this Container, this type, this framework, this data
   store. One line each; `choices.md` is never empty.
7. **Write `needs-input.md` only for what costs money or touches access:**
   a catalog entry, an Agent, a Git provider or a credential the idea needs and
   the catalog doesn't have. Write one sentence saying what and why, and leave
   the other files out. Everything else is yours to decide: choose, and say
   why in `choices.md`.
