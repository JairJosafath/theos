# Plan: Gitea calls the operator back, and polling becomes the fallback

GitHub issue #1084. Agent: heron-1084.

## Goal

The operator takes Gitea's webhooks for issues, pull requests, reviews,
commit statuses and workflow runs, and reconciles the objects a delivery
concerns at once, instead of at the next poll. Polling stays the contract
every host meets ([design.md](../design.md#factory-components-ports-and-adapters),
#1093): webhooks are Events, an optional capability of the GitHost port, and
only shorten the wait. While a GitProvider's deliveries arrive, the polls
that wait for the host stretch to 10 minutes; a missed delivery is still
seen. Every delivery is checked against a secret the operator makes at
runtime and sets on the hook.

Done when the steps below have closed: on Gitea (`make test-gitea`) the
operator's hook is registered and a signed delivery parses; in envtest a
delivery reconciles the Crew, Task, Container or Brief it concerns and a
forged one reconciles nothing; the GitProvider shows `Events`; the chart
installs the receiver's Service and lets Gitea reach it; `make check` passes
on each step.

## Where we stand

- **Every reconciler polls the host.** The intervals are compiled in, with no
  flag or field: the Crew's ready issues every minute (`defaultCrewPoll`,
  `internal/controller/crew_controller.go:53`), a Task's checks, pull request
  and runs every 15 s (`defaultChecksRecheck`, `task_controller.go:47`), a
  Standard change every minute (`task_standard.go:36`), an abandoned pull
  request every 10 minutes (`task_abandoned.go:27`), a Container's
  default-branch checks every minute (`container_checks.go:29`), a Brief
  waiting for answers every minute (`brief_controller.go:98`). An idle
  project still costs these calls, and a change waits up to one interval.
- **No hook code anywhere.** `internal/gitprovider` has no hook methods; no
  reconciler has a `source.Channel` or `WatchesRawSource`.
- **The operator listens on nothing Gitea could reach.** `cmd/main.go` builds
  controller-runtime's TLS webhook server (`--webhook-port`, 9443) with no
  handler on it; the chart renders only the metrics Service
  (`dist/chart/templates/metrics/`). Nothing tells the operator its own
  address.
- **Gitea refuses private addresses.** Its `[webhook] ALLOWED_HOST_LIST`
  defaults to `external`, and neither `config/gitea/gitea.env`, the chart's
  Gitea config nor `hack/gitea-local.sh` sets it, so a hook to a cluster
  Service would never be delivered.
- **GitProvider has no status and no reconciler**
  (`api/v1alpha1/gitprovider_types.go`;
  [api-and-controllers.md](../dev/api-and-controllers.md) lists it with the
  catalog kinds). `spec.owner` is an organization that holds every Container
  repository (named after the Container) and every Brief repository
  (`brief-<name>`, `model.BriefRepository`).
- **Tasks are found by Container only** (`indexTaskContainer`,
  `internal/controller/indexes.go`); nothing indexes a Task's issue, pull
  request or branch.
- **The ports are being cut** (#1094, [git-host-ports.md](git-host-ports.md)):
  `internal/ports`, the fake in `internal/ports/fake`, the contract suites in
  `internal/ports/contract` and Gitea in `internal/adapters/gitea` arrive
  with #1166 to #1171. This plan builds on them.

## Decisions, and why

1. **Events is an optional capability of GitHost** (`internal/ports/events.go`),
   found by a type assertion as the design says:
   ```go
   type Events interface {
       // EnsureEventHook makes the owner's hook deliver to hook.URL, signed
       // with hook.Secret; it creates it, or edits the one whose URL has the
       // same path, removes duplicates, and reports whether it created the
       // hook or changed its URL.
       EnsureEventHook(ctx context.Context, hook EventHook) (changed bool, err error)
       // ParseEvent checks a delivery's signature against secret and says
       // what it concerns. It makes no call to the host. ErrBadSignature
       // when the signature is missing or wrong.
       ParseEvent(header func(string) string, body, secret []byte) (Event, error)
   }
   type Event struct {
       Repository string // the repository's name under the owner
       Number     int    // the issue or pull request, 0 when none
       Branch     string // the branch a push or run is on, "" when none or unsaid
       Kind       string // the host's event name, for logs only
   }
   ```
   The port imports no `net/http`, so a header comes as a lookup function.
2. **One organization hook per GitProvider.** One registration covers every
   Container and Brief repository, those made later too, so scaffolding
   gains no call and "scaffold once" is untouched. On Gitea it is
   `/orgs/{owner}/hooks`, type `gitea`, JSON, active, with the events the
   operator reads: `issues`, `issue_comment`, `issue_label`, `issue_assign`,
   `pull_request` and its `_sync`, `_label`, `_comment` variants,
   `pull_request_review`, `push`, `create`, `release`, `status` and
   `workflow_run`. Step 1 confirmed each delivers on Gitea 1.27.3, the one
   the factory pins, signed with the hook's secret
   (`TestGiteaEventsIntegration`), and found three things. The hook API
   refuses `pull_request_review_approved`, `_rejected` and `_comment` as
   names; it takes `pull_request_review`, which delivers as those three
   event types. Gitea widens the groups (`issues` brings
   `issue_milestone` too), which costs a reconcile at most. A `status`
   names only its commit, never a branch, so its Event names only the
   repository, and the reconcile reads every object of that repository
   a status could move, as a poll would. A token that may not manage the
   organization's hooks gets `RegisterFailed`, and the factory polls as
   today.
3. **The hook is found by its path, `/git-events/<namespace>/<gitprovider>`,**
   which names the GitProvider whose Secret checks the delivery. No
   finalizer: a hook that outlives its GitProvider gets 404 replies, which
   Gitea only logs, and a GitProvider made again under that name takes it
   over. Deleting a GitProvider never waits on the host.
4. **The delivery secret is made by the operator at runtime** (the `secrets`
   skill): a GitProvider reconciler, new
   (`kubebuilder create api --group c4 --version v1alpha1 --kind GitProvider
   --resource=false --controller=true`), makes Secret
   `<gitprovider>-git-events`, key `secret`, from `crypto/rand`
   (`rand.Text()`), when it is missing, owned by the GitProvider, read
   uncached as the agent Git Secrets are. Nothing in the repository or in
   what Helm renders holds it. The reconciler sets it on the hook each time
   it ensures the hook (every 10 minutes), since the host never returns a
   hook's secret to compare; rotation is deleting the Secret. Gitea 1.27.3's
   hook edit ignores a new secret (step 1 found it), so Gitea's adapter
   puts a fingerprint of the secret in the hook URL's query, `?secret=`
   and the first 12 hex digits of its SHA-256, which tells nothing of a
   128-bit secret. A hook whose URL, fingerprint included, is the one
   wanted is edited; else a new one is made and the old removed, so a
   rotated secret reaches the host. The receiver routes by the path alone.
5. **The receiver is a plain HTTP listener in the operator**,
   `internal/controller/git_events.go`, a manager Runnable that runs on the
   leader, like the controllers it feeds. `--git-events-bind-address` (`0`,
   off, by default) and `--git-events-url` (the base address the host
   delivers to; empty means no hook) are its flags. It takes `POST
   /git-events/{namespace}/{name}`, reads at most 5 MiB (413 past it), finds
   the GitProvider (404 when there is none or its adapter lacks Events),
   reads its Secret uncached, calls `ParseEvent` (401 on a bad signature),
   hands the Event to the controllers and answers 202. Plain HTTP inside the
   cluster, as the operator reaches Gitea today: the signature makes a
   delivery authentic, and nothing in it is trusted beyond what to reconcile.
   Not controller-runtime's webhook server, which needs a TLS certificate
   and serves admission.
6. **A delivery is a hint, never a fact.** It names what to reconcile; the
   reconcile reads the host as a poll would. A duplicate, a late or replayed
   delivery costs at most one reconcile, the work queue folds a burst into
   one, and no replay protection is needed. The receiver writes nothing.
7. **Each controller maps an Event to its own requests,** from a typed
   `source.Channel` the receiver feeds, one buffered channel per controller,
   sent to without blocking (a full one drops the Event, logged at V(1);
   polling covers it). For a delivery in the GitProvider's namespace:

   | The repository is | Enqueued |
   | --- | --- |
   | a Container's (its name, `spec.gitProviderRef` this GitProvider) | the Container; its Crews; its Tasks whose issue, Standard-change issue or pull request is `Number`, or whose branch is `Branch`; every unfinished Task of it when the Event has neither |
   | a Brief's (`brief-<name>`) | the Brief; its Plan Tasks, matched the same way |
   | anything else | nothing |

   A new Task index, `indexTaskRepository`, holds `<repository>#<number>`
   for the issue and the pull request and `<repository>@<branch>`.
   Enqueuing generously is cheap; the saving is the idle poll.
8. **`Events` on the GitProvider says whether deliveries arrive.**
   `GitProvider` gains a status subresource with conditions and an `Events`
   print column. Reasons: `Disabled` (no `--git-events-url`), `Unsupported`
   (the adapter lacks Events), `RegisterFailed` (the host's message;
   retried at the next ensure), `Registered` (False: the hook was made or
   its URL changed, no verified delivery since), `Receiving` (True: a
   verified delivery came after it was registered) and `DeliveryRefused`
   (False: a delivery failed its signature after the last verified one).
   The receiver keeps, in memory, each GitProvider's last verified and last
   refused delivery and enqueues the GitProvider when either moves the
   condition; the reconciler writes it. After a restart the memory is empty
   and the condition stays as written.
9. **The polls that wait for the host stretch to 10 minutes while `Events`
   is True:** the Crew poll, a Task's checks and its Standard change, a
   Container's checks, a Brief waiting for answers. A pure rule,
   `model.HostRecheck(d, events) time.Duration`, returns the longer of `d`
   and `model.EventsFallbackPoll` (10 minutes) when `events`, else `d`.
   Error retries and waits on anything but the host (agent slots, Secrets,
   the merge retry, plan pushes) keep their intervals. With `Events` False
   for any reason, everything polls as today: the controllers' tests run
   without events unless they test them.
10. **The chart installs the receiver when it installs Gitea.**
    `gitEvents.enabled` (default true) and `gitEvents.port` (8082) render a
    Service selecting the manager, its container port, both flags (the URL
    is the Service's cluster DNS name), a NetworkPolicy letting the Gitea
    namespace reach that port, and Gitea's
    `GITEA__webhook__ALLOWED_HOST_LIST` set to that one host. `config/`
    gets the same, and `hack/gitea-local.sh` sets `loopback` for
    `make test-gitea`. A device with its own Gitea sets
    `gitEvents.url` to an address its Gitea can reach and allows it there.

## Steps

| Step | Issue | Depends on | How we know |
| --- | --- | --- | --- |
| This plan and the design | #1084 (`Refs`) | | it merges |
| Events on Gitea: `ports.Events`, `Event`, `EventHook`, `ErrBadSignature`; Gitea's organization hook and signed deliveries in its adapter, `var _ ports.Events`; `hack/gitea-local.sh` allows loopback | #1217 | #1169 | `make test` parses captured deliveries of each kind and refuses a wrong secret; `make test-gitea` ensures the hook twice (one hook), opens an issue and receives and parses its signed delivery on a local listener |
| The Events contract suite: the fake's hook and outbox, `contract.Events` on the fake and Gitea | #1219 | #1217, #1170 | `make test` runs it on the fake, `make test-gitea` on Gitea |
| The GitProvider reconciler: status and `Events`, the delivery Secret, the hook, `--git-events-url` | #1220 | #1219 | envtest: the Secret is made once, kept and owned; the hook is ensured with its value; `Disabled`, `Unsupported`, `RegisterFailed`, `Registered`; the generated role grants `gitproviders/status` |
| The receiver and the enqueueing: `--git-events-bind-address`, the Runnable, the channels, `indexTaskRepository`, `Receiving` and `DeliveryRefused` | #1221 | #1220 | envtest: a signed delivery reconciles the Container, Crew, Task or Brief it concerns; a forged one gets 401 and reconciles nothing; 404 and 413; the condition flips |
| The longer fallback poll: `model.HostRecheck` and its callers | #1222 | #1221 | a table test of `HostRecheck`; envtest: a Crew's requeue is 10 minutes with `Events` True, 1 minute without |
| The install: chart and `config/` Service, port, flags, NetworkPolicy, Gitea's `ALLOWED_HOST_LIST` | #1223 | #1221 | `make validate-yaml`; `helm template` renders the Service, the flags and the allowed host, and no secret |

The last step to close moves this plan to `docs/plans/done/`. What only a
real node shows, Gitea in the cluster delivering through the NetworkPolicy
to the operator's Service and `Events` turning `Receiving`, is for a human's
Kind run.

## Done when

Every step has closed with `make check` passing (and `make test-gitea` for
#1217 and #1219). #1084 closes itself with its last step.
