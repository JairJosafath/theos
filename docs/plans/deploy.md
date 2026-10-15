# Plan: ship each Container to where it runs

GitHub issue #1068, filed by the owner (`source/dev`). Agent: otter-1068. This
is the plan #1068 asks for as its first step: the deploy model #1069 (images),
#1070 (GitHub), #1071 (AWS) and #1072 (Kubernetes) build on.

## Goal

A Container names the places it runs, and each release the factory makes is
shipped there by the tool that owns the place, with the user's credential
kept away from the code the agents wrote. A person approves what goes to
production, and the Container's status says which version runs where, at
which URL, and whether the target's tool calls it healthy.

Done when every step below is merged: a `DeployTarget` reports `Ready` from
its credential, a Container with `spec.deploy` ships its Crew's newest
release to each target in order and reports it in `status.deploys` and the
condition `Deployed`, a production target waits for a person's approval and
shows under *Needs you*, all verified by Go tests and envtest against the
Deployer's fake and a fake GitHub. No test uses a real cloud account or a
remote cluster.

## Where we stand

- **The `Deployment` catalog kind** writes the files a tool reads
  (`k8s-manifests`: a Dockerfile and `deploy/k8s/`; `gitea-release`: `make
  dist` attached to a Gitea release). It names no place and holds no
  credential, and it stays that way: it is a template.
- **The one thing that leaves the factory** is `Crew.spec.publishReleases.github`,
  the release copy to github.com, run by the operator so the token never
  reaches agent code (`docs/plans/done/release-publishing.md`).
- **The AWS Frameworks deploy themselves** (`deploysItself`) and nothing
  deploys them; [aws-production.md](aws-production.md) gives each stack a
  tier read from the `tier` context, outputs and CloudFormation parameters,
  and leaves environments, approval and the place to #1068.
- **The ports are decided** ([design.md](../design.md#factory-components-ports-and-adapters),
  #1093): the Deployer is a port, its credential is the target's Secret, read
  by the operator only, and "deploys get a new kind whose targets are a union
  keyed by field name; #1068 names it and its members". `internal/ports` does
  not exist yet; #1094 creates it for the Git host.
- **The Gitea runners** have no Docker and no cloud CLIs, and their jobs run
  Makefiles the agents wrote.

## Decisions

1. **The place is its own kind, `DeployTarget`.** Namespaced, beside the
   GitProvider, and usable by any number of Containers. Its spec is a union
   keyed by field name, as `spec.ci` and `spec.registry` are: `github`
   (repository `owner/name`) first, then `cloudFormation` (#1071: account,
   region, role ARN), `argoCD` and perhaps `flux` (#1072: where the
   Application or Kustomization goes and its destination). Each member is
   added with its adapter, never before, so no target offers a choice the
   operator cannot make. Beside the member:
   - `credentialsSecretRef`: a Secret the user makes; the keys are the
     member's (`token` for `github`). It is read by the operator only and
     never mounted in a sandbox or passed to a CI job.
   - `tier`: `development` (the default) or `production`. This is the tier of
     [aws-production.md](aws-production.md) decision 1: the AWS adapter
     synthesizes nothing, it ships the template CI synthesized for that tier
     (`cdk.out/<tier>/`).
   - `approval`: `Required` or `None`. Empty means `Required` for a
     `production` target and `None` for a `development` one, decided by
     `internal/model`, not CRD defaulting, so a stored object never changes
     shape.
   - `parameters`: a map of deploy-time values that differ per place and are
     not secret (an alert email, a certificate ARN, a domain), at most 50.
     Each adapter maps them (CloudFormation parameters for #1071). A secret
     value is never a parameter; a member that needs one reads it from its
     Secret.

   Why a kind and not a block on the Container: the place outlives the
   Container, several Containers share an account or a cluster, and its
   credential and approval are decided once by whoever owns the place.
   Why not a field on the Deployment catalog entry: a catalog entry is a
   pinned template shared by every Container that picks it, and the same
   template ships to staging and production.

   What differs per Container is derived from the Container's name (the
   stack name, the Application's name, the release's name) and the target's
   fields. A step of #1070 to #1072 adds a per-use field to `spec.deploy`
   only when a derived value is not enough, and says why.

2. **A Container names its targets in `spec.deploy`.** A list, keyed by
   `target` (the DeployTarget's name), of at most 5 entries:

   ```yaml
   spec:
     deploy:
       - target: staging
       - target: prod
         after: staging        # ships a version only once staging runs it
         approved: v1.3.0      # set by a person: the version let into prod
       - target: oss
         version: v1.2.0       # pinned; empty follows the newest release
   ```

   - `after` names an entry earlier in the list (CEL), so the order is a
     chain without cycles. A version reaches the entry only once the entry it
     names reports it `Deployed`, and healthy where the target reads health.
   - `version` pins a release tag; empty follows the newest. Pinning an
     older tag is how a person rolls back.
   - `approved` is the version a person let in. An entry whose target needs
     approval ships a version only when `approved` equals it. A person sets
     it with `kubectl patch` or, with UI actions on, the approve button; the
     cluster's RBAC on Containers decides who may.
   - The list is mutable. Adding a target, removing one or reordering them
     never scaffolds again (decision 5). Removing an entry stops the factory
     shipping there and leaves what runs there alone.

   A Crew or Brief names no target. A Crew's releases are the Container's
   releases, so naming targets on the Crew would let two Crews disagree.
   A Brief keeps writing `publishReleases` for its Crews until #1070 moves
   the release copy (decision 7). The planner picking targets comes after
   #1070 to #1072, as a follow-up of theirs.

3. **CI builds, the operator ships.** CI makes everything that ships, with
   no deploy credential: release files (`make dist`), an image pushed by
   digest to the Registry (#1069), a synthesized CloudFormation template and
   its assets attached to the release (#1071). The operator holds the
   target's credential and hands what CI built to the tool that owns the
   target: the GitHub API, CloudFormation change sets, an Argo CD
   Application or Flux Kustomization. It never builds, never runs a command
   from the repository, never rebuilds what CI made, and writes no rollout
   logic of its own. The rule of #1093 holds: a user's credential never
   reaches code the agents wrote.

   Why not a deploy job in CI with short-lived credentials (OIDC): the job
   runs a Makefile the agents wrote, so whatever the credential reaches, that
   code reaches; scoping it narrowly limits the damage but does not keep the
   rule. #1071 may weigh OIDC for the AWS case and say why it takes or leaves
   it; the model leaves the deploy in the operator either way.

4. **When a deploy runs: on each release, in order.** A release is the one
   the Container's Crew tagged, once its release runs succeeded, the same
   gate the release copy uses (`Crew.status.lastRelease`). For each entry of
   `spec.deploy` the operator works out the version it should run: its
   pinned `version`, else the newest release; then holds it while `after`'s
   entry doesn't run it yet or while approval is needed and not given, else
   ships it. A Container without a Crew has no release and deploys nothing
   (`Deployed=False`, `NoRelease`); tags made by hand are a later issue.
   - **Shipping is a start and a poll.** `Ship` starts the deploy and returns;
     the operator reads `State` back each poll, as it reads CI runs. A
     deploy that the target's tool reports failed stays `Failed` until a
     newer version is due or a person pins one; the operator never retries
     a failed rollout by itself, since the tool has already rolled it back
     or left it for a person. A call that fails before the tool has the
     deploy (a timeout, a 5xx) is retried each poll.
   - **One deploy at a time per entry.** A newer release that arrives while
     one is deploying waits for the first to end.

5. **Targets fit "scaffold once": they need files, they never write them.**
   The files a target's tool reads come from the Container's catalog choices
   at scaffold: the Deployment (`k8s-manifests` writes `deploy/k8s/`), or a
   Framework that deploys itself (the AWS stack), or nothing (a GitHub
   release needs only the release). A table in `internal/model`, one row per
   member, says what a member needs from a Container. The operator checks it
   whenever `spec.deploy` changes; a Container that lacks what a target
   needs gets `Deployed=False`, reason `Incompatible`, naming the target and
   what it lacks, and is shipped to its other targets. A Container that
   gains a target later is shipped to it from its next release, with no new
   scaffold; when what the target needs is missing from the release (an old
   Deployment version, no `cdk.out` asset), the deploy fails `ArtifactMissing`
   naming the file, and the remedy is the Standard's or the Deployment's
   bump, by the pull request the operator opens today, or a person's change.

6. **What a deploy reports.**
   - **`DeployTarget.status`:** the condition `Ready`, from the adapter's
     `Check` (the Secret exists, has the member's keys, and the target
     accepts it), checked when the spec or Secret changes and at most every
     10 minutes otherwise. Reasons: `SecretMissing`, `SecretIncomplete`,
     `CredentialRefused` (its message names the target and who sets the
     Secret, as the release copy's does), `Unreachable`. `status.checkedTime`
     says when.
   - **`Container.status.deploys[]`,** one per entry: `target`, `version`
     running, `wantedVersion`, `phase` (`Waiting`, `Deploying`, `Deployed`,
     `Failed`), `health` (`Healthy`, `Progressing`, `Degraded`, `Unknown`) as
     the target's tool reports it, `url` where the app answers when the tool
     knows it (a stack output, an Ingress host, the GitHub release page),
     `reason`, `message`, `since`.
   - **The condition `Deployed` on the Container:** True when every entry
     runs its wanted version; else False with the first reason in list
     order: `Incompatible`, `TargetNotReady`, `DeployFailed`,
     `WaitingForApproval`, `WaitingForPrevious`, `Deploying`, `NoRelease`.
     Its message names the target and version. A Normal Event `Deployed` when
     an entry turns `Deployed`, a Warning `DeployFailed` once per version and
     reason, `ApprovalNeeded` once per version.
   - **Needs you** lists a Container whose `Deployed` is `WaitingForApproval`
     or `DeployFailed`, or one of whose deploys is `Degraded`.

   **Runtime health, settled.** README said health is "left to existing
   tools", the design said it "comes later". Both hold in part: the factory
   reports the health the target's own tool already knows (Argo CD's
   `status.health`, the CloudFormation stack status, the GitHub release
   being there) and nothing more. It doesn't probe the app, scrape metrics or
   read alarms; that monitoring stays with existing tools. ROADMAP's *Later*
   line becomes "runtime health beyond what the target's tool reports".

7. **The release copy folds in later, without breaking a Crew.**
   `Crew.spec.publishReleases.github` keeps working unchanged. The `github`
   member ships the same thing (tag, notes, files) through the same code:
   step 2 moves the draft, upload and publish sequence of
   `internal/controller/crew_publish.go` into the GitHub adapter, and the
   Crew's copy calls the adapter. #1070 plans the rest: source and history,
   the checks before the first publish, images on GHCR, a GitHub App
   credential, and turning `publishReleases` into a DeployTarget plus a
   `spec.deploy` entry with a migration (and the conversion `v1beta1`, #1082,
   needs). Until then a Container may have both, and both copy to the same
   repository harmlessly: each finds the other's release by its marker and
   leaves a release it didn't make alone, so the second is a no-op that
   reports `Deployed`.

8. **The Deployer port.** `internal/ports/deployer.go`, plain Go with no
   Kubernetes:

   ```go
   type Deployer interface {
       // Check says whether target accepts its credential.
       Check(ctx context.Context, target Target) error
       // Ship starts shipping s to target. Repeating it for the same
       // app and version is safe and starts nothing new.
       Ship(ctx context.Context, target Target, s Shipment) error
       // State reads back what runs at target for app.
       State(ctx context.Context, target Target, app string) (DeployState, error)
   }
   ```

   `Target` is the member's settings, the tier, the parameters and the
   Secret's data. `Shipment` is the app's name and display name, the
   version, its commit, its notes, its release files (each a name, a size
   and an opener, streamed from the Git host as the release copy does), and
   its image digest once #1069 adds one. `DeployState` is the version, phase,
   health, URL and a reason and message. Errors the operator tells apart
   (`ErrCredentialRefused`, `ErrUnreachable`, `ErrArtifactMissing`) are in
   the port's file. The adapter picks itself from the member: one Deployer
   per member, chosen in `cmd/main.go` by a map from member name to adapter,
   and the controller only sees the port.
   - **The fake** in `internal/ports/fake` records shipments, lets a test
     set the state each target reports, and fails on demand.
   - **The contract suite** `contract.Deployer(t, env)` states: `Ship`
     twice is one deploy; `State` after `Ship` reports the version once the
     fake or tool says it is done; a refused credential is
     `ErrCredentialRefused` from `Check` and `Ship`; a missing file is
     `ErrArtifactMissing`. It runs against the fake and the GitHub adapter
     (on the fake GitHub server the publish tests use) in `make test`, and
     against each adapter #1071 and #1072 add.
   - **The Ports table** row in [architecture.md](../architecture.md#ports)
     names the DeployTarget's member as what picks it.

   If #1094 hasn't made `internal/ports` yet, step 2 makes the package with
   only the Deployer and the depguard rule its row in architecture.md
   states; #1094 adds its ports beside it.

## Steps

Each step is a GitHub issue that names #1068, lands on its own with `make
check`, and leaves main working.

| Step | Issue | Waits on | How we know |
| --- | --- | --- | --- |
| 1. This plan and the design change | #1068 (`Refs`) | | landed in `docs/plans/`, `docs/design.md`, `docs/architecture.md`, README and ROADMAP |
| 2. The Deployer port and `DeployTarget` with `github` | #1161 | | `kubebuilder create api` for `DeployTarget`; the `github` member, `credentialsSecretRef`, `tier`, `approval`, `parameters` and their CEL (exactly one member, the repository pattern, at most 50 parameters); `internal/ports/deployer.go`, its fake and contract suite; the GitHub adapter, the release copy's sequence moved into it and the Crew's copy calling it with no change in behaviour (the publish tests pass unchanged); `Ready` with its reasons; `internal/model`'s approval-by-tier rule and the member requirement table, table-tested; samples; envtest creates targets and reads `Ready` for each reason against the fake |
| 3. `Container.spec.deploy` and the deploy loop | #1162 | #1161 | the field and its CEL (`after` names an earlier entry, at most 5, unique targets); the operator ships the Crew's last release to each entry in order, holding for `after` and approval, pinned versions, one deploy at a time; `status.deploys` and `Deployed` with every reason; the Events; `Incompatible` from the requirement table; envtest against the fake Deployer covers each reason, the chain, approval, a rollback by pin and a target added later |
| 4. Deploys in the UI | #1163 | #1162 | the DeployTarget list and page; the Container page's deploys (version, phase, health, URL); *Needs you* items for `WaitingForApproval`, `DeployFailed` and `Degraded`; with UI actions on, the approve action (a JSON patch of one entry's `approved` only), listed in `ui/README.md`; `make ui-test` |

#1069 to #1072 wait on #1068, so they start when step 4 closes it.

## Out of scope

- Building images (#1069), and the members `cloudFormation` (#1071),
  `argoCD` and `flux` (#1072); GitHub's source, history and GHCR (#1070).
- Secret deploy-time values, monitoring the app, and retrying a failed
  rollout by itself.
- Deploying tags a person made without a Crew.
- Targets picked by a Brief's planner.
