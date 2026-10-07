# A project's name is enough: shared credentials and namespaces

GitHub issue #427, reported by gray-lenovo on v0.5.0. Agent: otter-427. This
plan holds the design; its steps are their own issues.

## Goal

Adding a project's name to the chart's `gitea.projects` and running `helm
upgrade` is all it takes to add a project. Nobody creates the namespace, copies
the pull Secret, patches the `default` ServiceAccount or copies the Claude
token. The token is given once, and a project can still have its own.

Done when a device adds a project with only its name in `gitea.projects` and
`helm upgrade`, a Brief in it starts its planner without a by-hand step, and
docs/getting-started.md says so. Each step's issue has its own "Done when".

## What happens today

A person does four things per project namespace: creates it, makes Secret
`ghcr` in it, adds `ghcr` to its `default` ServiceAccount, and makes Secret
`claude-credentials`. The operator already holds `create`, `get`, `patch` and
`update` on Secrets in every namespace, and the Project reconciler already
makes per-namespace objects (the Session ServiceAccount, Role and RoleBinding,
the agents' NetworkPolicy).

## Decisions, and why

1. **The chart makes the project namespaces.** A Namespace per entry in
   `gitea.projects`, with `helm.sh/resource-policy: keep` like Gitea's: it holds
   the person's model and Briefs, so uninstall never deletes it. Helm refuses
   to take over a namespace it didn't make, and v0.5.0 devices made theirs by
   hand, so the chart renders a project's Namespace only when `lookup` finds
   it missing or already this release's; an existing one is left to the seed
   job (decision 3). `lookup` is empty under `helm template`, so GitOps
   renders every Namespace, which is what a GitOps user wants from it. A
   Namespace is no credential: the `secrets` skill's objection to `lookup`
   (a value that changes per render) doesn't apply. `projectNamespaces.create`
   (default true) turns it off for a device that makes its own.
2. **The operator shares Secrets from its own namespace.** A person makes
   `claude-credentials` (and, while the images are private, `ghcr`) once, in
   the release namespace. The Project reconciler copies each shared Secret
   into every namespace that has a Project and opts in (decision 3), under the
   same name, with the label `c4.factory.io/shared-from: <name>`. It keeps a
   copy it made in step with the source, so a rotated token reaches every
   project, and deletes its copies when the namespace opts out. Why the
   operator and not the seed job: the seed runs only on `helm upgrade`, so a
   rotated token and a NeedsInput Brief would wait for the next one; the
   operator already has the Secret permissions and a reconciler per project
   namespace. Why copies: a pod reads Secrets from its own namespace only.
   The operator reads Secrets with its uncached reader, as `needAgents` does,
   and requeues each Project every five minutes: watching Secrets
   cluster-wide would cache every Secret in the cluster in the operator.
   A Project reports it in a condition `SecretsShared`, naming each shared
   Secret, whether it is a copy or the project's own, and a missing source.
3. **A project opts in by a namespace label, which only an admin or the chart
   sets.** `c4.factory.io/shared-secrets: "true"`. A Project alone is not
   enough: anyone allowed to create a Project in some namespace would receive
   the factory's token. Namespace labels need `patch` on the cluster-scoped
   namespace, which namespace admins don't have. The chart's Namespaces carry
   the label; for an existing namespace the seed job sets it, with a
   ClusterRole granting `get` and `patch` on `namespaces` narrowed by
   `resourceNames` to `gitea.projects`.
4. **A project's own token is a Secret of that name without the label.** The
   operator never overwrites or deletes a Secret it didn't make, so a person
   who wants `cel-lens` on another token makes `claude-credentials` in
   `cel-lens` as before, and the condition says it is the project's own. To
   give a token once for a group of projects, they make it in the release
   namespace: one token for every project is the default the issue asks for,
   and a second shared source per group is more than anyone has asked for.
5. **Sandbox pods name the pull Secrets; nobody patches `default`.** The
   chart passes `global.imagePullSecrets` to the operator as shared pull
   Secrets: copied like the token, and listed in `imagePullSecrets` of every
   pod the operator builds in a project namespace (the Task sandboxes). A
   patched `default` ServiceAccount from v0.5.0 keeps working.
6. **The operator's configuration comes from flags, set by the chart.**
   `--shared-secret NAME` and `--shared-pull-secret NAME` (repeatable), read
   from the operator's own namespace (`POD_NAMESPACE`, from the downward API).
   With none, the operator shares nothing, as today. The chart sets them from
   `agents.credentialsSecret` (when `agents.enabled`) and
   `global.imagePullSecrets`. Not a CRD field: which Secrets a cluster's
   operator may hand out is the installer's decision, not a namespace user's,
   and the opt-in that a user could reach (decision 3) needs an admin.
7. **The Brief's NeedsInput message names where to give the token.** With
   sharing on: "Secret claude-credentials of Agent planner is missing: make
   it in namespace c4-factory-system, which the factory shares with every
   project, or in cel-lens for this project only". It names the label when the
   namespace hasn't opted in, and keeps today's message when sharing is off.
8. **The fence.** Unchanged: the token is still readable inside the sandbox,
   the gap 1.0 must close (ROADMAP.md). One source in the release namespace
   makes that fix easier, since egress credential injection then reads one
   Secret instead of one per project. A copy is as readable as the hand-made
   Secret it replaces; the copies add no reader.
9. **Secrets skill.** Nothing here renders a credential: the source Secrets
   are the user's (a credential only they have), the copies are made in the
   cluster by the operator, and Helm renders only names.

## Steps

Each lands on its own, with new behaviour off until the step that turns it on.

1. **#435: The operator shares Secrets into opted-in project namespaces**
   (decisions 2, 3, 4, 6). Flags, the Project reconciler's copies, the
   `SecretsShared` condition, `delete` on Secrets in the generated role.
   Envtest: a copy is made, follows its source, is left alone when unlabeled,
   is removed on opt-out; nothing is shared into a namespace without the label.
   Off until flags are set.
2. **#436: Sandbox pods name the shared pull Secrets** (decision 5). Unit test on
   the sandbox pod spec. Depends on step 1.
3. **#437: The Brief says where to give the token** (decision 7). Envtest on the
   NeedsInput message for each case. Depends on step 1.
4. **#438: The chart makes project namespaces and turns sharing on** (decisions 1,
   3, 6): Namespaces with the lookup guard, the seed's label and its
   ClusterRole, the manager's flags and `POD_NAMESPACE`; `make
   validate-yaml`. docs/getting-started.md and dist/chart/README.md lose the
   per-project steps. Depends on steps 1 and 2.
5. **#439: The test environment shares the token** (`make claude-secret` writes it
   once into the operator's namespace; `config/` sets the flags and labels
   the sample project namespaces). Depends on step 1.

## What a human run of Kind would check

That a `helm upgrade` adding `cel-lens` to a v0.5.0 install takes over nothing
it shouldn't (the hand-made `todo` namespace stays unowned, gets the label),
and that a sandbox pod in the new namespace pulls its private image with no
ServiceAccount patch.
