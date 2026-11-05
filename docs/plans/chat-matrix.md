# Plan: talk to the factory in Matrix, one room per kind of work

## Goal

A person follows and steers theos from a chat app, organised the way a team
works: updates in one room, a general conversation in another, triage,
specs, reviews and alerts each in their own room, and every item that needs
a decision in its own thread. The owner chose [Matrix](https://matrix.org)
over Signal (2026-11-05): Spaces, rooms and threads give the structure, bots
are first-class, a homeserver can run in the cluster next to Gitea, and
bridges can carry rooms to Signal or WhatsApp later.

## The rooms

One Matrix Space per project namespace, named after the Project, with six
rooms. Each item that needs a decision is a thread whose root message the
bot edits as the item moves, so a room reads as a list of live items.

| Room | Posts | A person can |
| --- | --- | --- |
| `updates` | merges, releases, deploys, Brief rounds; batched into one message per `digest` window (default 10 minutes) | read |
| `general` | answers to questions | `status`, `brief <idea>`, `prompt <brief>: <text>`, `pause <brief>`, `resume <brief>`, `help` |
| `triage` | each Triage Task's decision; a parked issue's question as a thread | answer in the thread: it becomes a comment on the issue |
| `specs` | a thread per Spec Task waiting for approval, with the spec's criteria and its pull request | `approve`, or `changes: <text>` |
| `reviews` | a thread per deploy waiting for a person's approval, and each Implement Task's review verdict | `approve <version>` on a deploy |
| `alerts` | degraded or failed deploys and the bug filed, failed Tasks, `BaseRed`, an unhealthy factory part | `retry` on a failed Container |

## Decisions

- **The CRDs stay the truth; chat enforces nothing.** A command does only
  what the UI can do (`docs/design.md`, *UI actions*), as one request the
  chat component builds from a closed allowlist in `internal/model`, sent
  **as the person** by impersonation, so the cluster's RBAC decides and the
  API server's refusal is posted back in the thread.
- **A new kind, `Chat`,** one per project namespace (`kubebuilder create
  api`): `homeserver` (URL; default the chart's in-cluster homeserver),
  `credentialsSecretRef` (the bot's access token), `space` (display name),
  `rooms` (a name per purpose, defaulting to the table's), `digest`, and
  `people`: each Matrix user ID with the Kubernetes user (and groups) it acts
  as. Nobody else's commands are run; they get a one-line refusal. Each
  person adds themselves (an admission policy, step 2). Status:
  the Space's and each room's ID, `Ready`, and the last error.
- **Two parts, least privilege.** The operator reconciles `Chat`: it makes
  the Space and the rooms (private, invite-only, unencrypted: see below),
  invites the `people`, and writes the IDs into status. A separate
  Deployment, **`theos-chat`**, with its own ServiceAccount, watches theos's
  resources and posts, and reads the rooms through `/sync` and runs
  commands. Only `theos-chat` may impersonate, and only the users its Chats
  name. The operator never impersonates.
- **Matrix through a port.** `internal/ports` gains a `ChatHost` port
  (ensure Space and room, invite, send, reply in a thread, edit, react,
  sync); `internal/adapters/matrix` speaks the client-server API over
  `net/http`, with no SDK and no cgo; tests run against an `httptest`
  homeserver fake in `internal/ports/fake`. Signal or another network is a
  second adapter later.
- **No end-to-end encryption at first.** The rooms are private on a
  homeserver the owner runs, with federation off by default; Megolm would
  need a crypto store and device verification for the bot. The plan says
  so, and `Chat.spec` has no encryption field until it is built.
- **Specs approved from chat.** Today a spec merges only on a person's
  approval on the Git host. A Task annotation
  `theos.factory.io/approve-spec: <head commit>` written by a person (never
  the operator's or `theos-chat`'s own identity, which the admission policy
  refuses) also counts; the operator records who in `status.spec`. `changes:`
  starts the next round with the text, as a Git host review does.
- **Formatting.** Messages carry plain text and Matrix HTML: a bold title, a
  status line, links to the UI's object page and the Git host, and code
  blocks for `kubectl` lines. Thread roots are edited in place
  (`m.replace`); a ✅ reaction marks a thread whose item is done.
- **Idempotent.** `theos-chat` keeps what it posted in a ConfigMap per
  namespace (item key → event ID), so a restart edits rather than reposts.
- **The chart.** `chat.enabled` (off by default) installs `theos-chat`,
  and `chat.homeserver.enabled` an in-cluster homeserver (a lightweight
  Rust server, [Tuwunel](https://github.com/matrix-construct/tuwunel) or
  [Continuwuity](https://forgejo.ellis.link/continuwuation/continuwuity),
  pinned by digest; its image is a setting). A user who runs their own
  homeserver, or uses matrix.org, sets `Chat.spec.homeserver` instead.
- **The UI** shows a Project's Chat on the About page and in Settings: the
  homeserver, each room with a `matrix.to` link, and `Ready`.

## Steps

1. `Chat` (API, CEL rules), the model's rules (event → room, message text,
   command parsing, the allowlist), the `ChatHost` port, the Matrix adapter
   and its fake, and the operator's `Chat` controller; tests at every tier.
   Built, with these choices where the plan was silent:
   - `Chat` is in `v1beta1` alone: `v1alpha1` was deprecated before it was
     born, so no client stored one there; its CRD serves one version at
     strategy None and stays out of `WebhookCRDs` and the storage migrator.
   - `homeserver` unset is the operator's `--chat-homeserver` (the chart's
     in-cluster one, step 3), else `HomeserverUnset`; `digest` is
     `digestMinutes` (0 to 1440, default 10), as `timeoutMinutes` is.
   - Rooms are found by the IDs in status, then by alias
     (`#<aliasPrefix>-<purpose>:<server>`, `aliasPrefix` immutable, default
     `theos-<namespace>`), and only among rooms the bot is in: an alias
     another account holds is `AliasTaken`, never joined, so nobody can
     squat a name and read the project's news. A second Chat in a
     namespace is `Duplicate`. People are not kicked when removed from
     `people`; their commands stop running.
   - `people[].user` and `groups` refuse `system:` names (CEL), so a Chat
     can't make `theos-chat` act as the cluster's own identities.
   - The allowlist (`model.CheckChatRequest`) is the UI's entries a command
     reaches, each no wider than the UI's: a Brief made with a generated
     name and an idea, the three appends of `addPrompt`, pause and resume,
     a Container's retry, a deploy's approval. `changes: <text>` and a
     triage answer are Task annotations too, like the spec approval:
     `theos.factory.io/spec-changes` (`<head commit> <text>`) and
     `theos.factory.io/answer`, so `theos-chat` needs no Git host
     credential; step 2's operator acts on them.
   - A message is `m.notice`, so other bots don't answer it; a link is one
     only when it is http or https; every field is escaped.
2. `cmd/theos-chat`: posting (threads, edits, the updates digest,
   idempotence) and commands (sync, the people check, impersonated writes,
   refusals posted back), and the spec approval annotation in the operator.
   Built, with these choices where the plan was silent:
   - **Who wrote a word from chat can't be forged.** Each of the three Task
     annotations comes with a companion, `<annotation>-by`
     (`theos.factory.io/approve-spec-by` and so on), naming the writer's
     Kubernetes user, and the admission policy
     (`config/policy/task_chat_annotations_policy.yaml`) refuses a write
     that sets or changes an annotation unless its `-by` equals
     `request.userInfo.username`, which only the API server says. That beat
     managedFields, whose manager names are whatever the client sends, and a
     mutating policy, not on every cluster the chart supports. The operator
     counts no word without its `-by`; the allowlist's three entries are
     each an annotation and its `-by`, nothing else. A word changes the
     companion too, so approve and changes keep their own writers.
   - **Only a person writes them.** The same policy refuses the three
     annotations and their `-by`, set, changed or removed, from any
     `system:` identity, rather than from two ServiceAccounts by name: the
     operator's and `theos-chat`'s names differ between kustomize, the
     chart and envtest, and a person is never `system:` (Chat's CEL refuses
     such people), so the operator, `theos-chat` acting as itself and every
     agent are refused alike. Nobody removes them: a stale word is ignored,
     not cleared.
   - **Each person adds themselves to a Chat.** A second policy
     (`config/policy/chat_people_policy.yaml`) takes a `people` entry only
     when it is already there, exactly, or names the writer's own user and
     groups out of `request.userInfo`; so an admin can't map a Matrix
     account to someone else, nor anyone add a group they aren't in. This
     is what makes `theos-chat`'s `impersonate` on users and groups safe
     enough: it impersonates only a Chat's people, each of whom put
     themselves there, never a `system:` name. A compromised `theos-chat`
     could still impersonate anyone; narrowing its role to the names in
     the Chats (`resourceNames`, kept by the operator) is left for later.
   - **The operator's side.** A Spec Task waiting for a person reads the
     two spec annotations against the head commit
     (`model.ChatSpecVerdict`): an approval counts as the person's
     (`status.spec.approvedBy`, `approvedInChat`), changes start the next
     round with their text, and changes from either the Git host or chat
     outweigh an approval; a word about another commit, or with no `-by`,
     counts for nothing and the wait's condition says why. A parked Triage
     Task's answer (`model.PlanChatAnswer`) goes to the issue as
     "<person> answered in chat:" and the issue back to `status/proposed`
     unless a person moved it meanwhile; `status.triage.answeredBy`
     records it, one answer is taken, and the Crew's triage counts an
     answered park as deciding nothing, so the issue is triaged again as
     `<container>-<issue>-triage-2`.
   - **`theos-chat` is a reconciler of Chats** (`internal/chatbot`, a
     client of the API like the controllers, no adapters but in its tests):
     one pass per Ready Chat, when a Task, Container or Brief in its
     namespace changes and every `--interval` (five seconds), posts the
     news and reads the rooms with a `/sync` that doesn't wait. An error
     goes back to the queue, so it is retried with backoff, and a room that
     fails doesn't stop the others. It reads its objects from the manager's
     cache, and the Chat's Secret, its ConfigMap and what a command's write
     is made from live.
   - **What is said** is the model's (`model.ChatTaskNews`,
     `ChatContainerNews`, `ChatBriefNews`): a Spec Task waiting for a
     person, a parked issue's question, a deploy waiting for approval and
     a failed scaffold are threads, open while they need a decision and ✅
     once done (a new thread if one needs a decision again); triage
     decisions, review verdicts, failed Tasks and deploys are posts;
     merges, releases, deploys and Brief rounds go in the digest, one
     message at once and then at most one per `digestMinutes`. A thread's
     root carries what its commands act on: a spec's head commit only
     while it waits, so `approve` approves what the root shows.
   - **Idempotence.** The ConfigMap `theos-chat` in the Chat's namespace,
     owned by the Chat, holds each item's key, room, event ID, text hash,
     done and thread item, the `/sync` token and when the digest last went
     out; items whose objects are gone are dropped. Its first pass records
     the history as seen and starts only live threads, and its first sync
     only marks where the rooms stand, so nothing said before runs. The
     token moves on even when an answer couldn't be posted: a command never
     runs twice. A failed write of the ConfigMap after a post can repost.
   - **Commands.** Words that aren't a command outside a thread
     `theos-chat` started are people talking, and left alone; anyone not
     in `people` who says a command, or anything in its threads, gets one
     line saying nothing was done. A person's command is checked
     (`CheckChatRequest`) and sent over plain HTTP from a copy of
     `theos-chat`'s rest.Config with `Impersonate` set to the person's user
     and groups; the answer is "Done as <user>: …" or "The cluster refused
     it for <user> (<code>): <the API server's words>".
   - **Links** are the Git host's (pull request, issue, repository) and a
     deploy's address; links to the UI's object pages wait for step 3's UI
     work, which gives `theos-chat` the UI's address.
   - **RBAC** (`config/chat/rbac.yaml`, by hand, as no markers may reach the
     operator's role): get, list and watch on Chats, Tasks, Containers and
     Briefs; get on Secrets; create on ConfigMaps and get and update on the
     one named `theos-chat`; impersonate on users and groups. The image
     carries the binary beside the manager and the model proxy; its
     Deployment and the chart's copy of the role are step 3.
3. The chart (`theos-chat`, its RBAC, the in-cluster homeserver), the UI's
   Chat panel, `docs/design.md` and the README.

## Done when

- `make check` passes after each step (apart from `hack/relay_test.sh`,
  which fails in the agents' sandbox on untouched code).
- Table tests cover routing, formatting, command parsing and the allowlist;
  adapter tests run the client against the fake homeserver; envtest covers
  the `Chat` controller and an approval from chat merging a spec;
  `theos-chat`'s tests post a thread, edit it, batch updates, refuse a
  stranger's command and run a person's as that person.
- `make validate-yaml` renders the chart with chat on and off.
