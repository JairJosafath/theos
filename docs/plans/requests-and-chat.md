# Requests and chat where teams already are: Slack, Linear and Jira

GitHub issue #61, a step of [open-source-launch.md](open-source-launch.md)
(#46). Written 2026-11-13 in a session the owner ran. The plan only: no code
is changed by it. The contract suites it builds on were merged in pull
request #131.

## Goal

A team that runs Slack follows and steers a project there as a team on
Matrix does today, and a team that tracks work in Linear or Jira labels an
item and the factory plans it, builds it and answers on the item. Both come
as second and third adapters behind ports that exist, so nothing in the
loop changes: the work still happens in the Git host's issues, where
agents, CI and review meet.

## What is true today

- **ChatHost has one adapter, Matrix** (`internal/adapters/matrix`), picked
  with no choice: `matrix.Hosts(nil)` in `cmd/main.go:554` and
  `cmd/theos-chat/main.go:93`. Its suite is `contract.ChatHost`
  (`internal/ports/contract/chathost.go:137`, 15 subtests), run on the fake
  homeserver `internal/adapters/matrix/matrixtest`. There is no ChatHost in
  `internal/ports/fake` (`internal/ports/fake/contracts_test.go:37-84` runs
  Issues, CI, AgentAccess and Registry only).
- **The port is written for Matrix.** `ports.ChatConfig` is a homeserver and
  one token (`internal/ports/chathost.go:25-31`); `ChatMessage.HTML` is
  Matrix's HTML subset (`chathost.go:61-65`,
  `internal/model/chat_message.go:69-71`); the refusal reads "the homeserver
  refused the access token" (`chathost.go:135`).
- **The Chat kind has no `spec.host`.** It names a `homeserver`, one Secret
  with `accessToken` (`internal/model/chat.go:96`), a Space, an
  `aliasPrefix` and `people` keyed by `matrixID`
  (`api/v1beta1/chat_types.go:43-102`, `:159-164`). The controller always
  makes a Space first and each room under it
  (`internal/controller/chat_controller.go:176-204`), and invites by
  `p.MatrixID` (`:234`). An admission policy lets a `spec.people` entry name
  only its writer (`config/policy/chat_people_policy.yaml:23-35`).
- **IssueTracker has one adapter, GitHub**
  (`internal/adapters/github/github_issues.go`), built through a seam that
  names it: `IssueTrackers.GitHubIssues(cfg ports.GitHubConfig)`
  (`internal/controller/brief_requests.go:46-51`), filled with
  `github.Client{}` (`cmd/main.go:531`). Its suite is
  `contract.IssueTracker` (`internal/ports/contract/intake.go`, 24
  subtests). There is no IssueTracker in `internal/ports/fake` either.
- **The port is written for GitHub.** Its types are `GitHubIssue` and
  `GitHubComment` (`internal/ports/intake.go:69-91`), its reasons
  `ReasonGitHubUnavailable` (`:37`) and, shared with the release target,
  `ReasonGitHubError` and `ReasonRepositoryNotFound`
  (`internal/ports/deployer.go:213-223`); an issue is an `int`
  (`intake.go:71`, `:131-138`), a comment's ID an `int64` (`:85`), and
  `TeamMember` takes `org/slug` and a login (`:126`).
- **A Brief takes requests only on a `spec.releases` entry,** from that
  entry's GitHub repository with its token
  (`api/v1beta1/brief_types.go:143-147`), allowed by `authors` and `teams`
  held to GitHub's patterns (`:165-180`), answered by number in `accepted`
  and `declined` (`[]int32`, `:187-195`). `status.requests[]` names a
  request by `repository` and `number`, both required (`:448-453`), and
  `status.requestsSettled` is a map keyed by `repository` (`:905-909`).
- **What a schema may not do** (docs/design.md, "Compatibility", checked by
  `test/compat/compat_test.go`): change a pattern, a list's type or its map
  keys (`:153-156`), add a required field (`:202-204`), add an enum to a
  free field (`:192`). Kubernetes itself refuses a list-map key that is
  neither required nor defaulted (`k8s.io/apiextensions-apiserver` v0.37.0,
  the version `go.mod:33` requires,
  `pkg/apis/apiextensions/validation/validation.go:1409`), and the guard
  refuses another key (`test/compat/compat_test.go:156`).

What Slack, Linear and Jira do is written below as their public
documentation describes it. Read on the vendors' pages on 2026-11-13:
Socket Mode's token, acknowledgement, limit of ten connections and its
refresh every few hours; the entities and `<…>` forms of Slack's text;
Linear's address and how its key is sent; the address a scoped Atlassian
API token calls. The rest (the scopes per method, Linear's and Jira's
calls, states and resolutions) is from memory of their references. Each
adapter's step checks it against the reference before it builds, and
corrects this plan where it differs.

## Decisions, and why

### Chat on Slack

1. **`Chat.spec.host` is a new optional field, `matrix` (the default) or
   `slack`.** A Chat written before the field reads back `matrix` and means
   what it meant. One Chat is on one host; a project that wants both makes
   no second Chat (`Duplicate` stays as it is). Teams would be a third enum
   value and a third adapter.
2. **Slack's settings and people are in a block of their own,
   `spec.slack`.** `people` can't take a Slack ID: `matrixID` is the list's
   map key, a key must stay required and the key can't change (the two
   rules above). So `spec.slack.people[]` is its own list, keyed by
   `slackID` (the member ID, `U…` or `W…`, which is what a message's sender
   carries), with `user` and `groups` held to the same rules as a Matrix
   person's. Not the e-mail address: it changes, it needs one more scope
   (`users:read.email`), and the ID needs no lookup at all.
3. **CEL keeps the two hosts apart,** each rule passing every Chat that was
   valid before: `slack` only with `host: slack`; with `host: slack` no
   `homeserver`, no `space`, no `people`, each of `rooms` left at its
   default, and an `aliasPrefix` Slack takes as a channel name (lowercase
   letters, digits, `-` and `_`, at most 70 characters). `host` is
   immutable: the rooms are on the host they were made on.
4. **A room is a private channel, named by its alias.** A Slack channel has
   one name, unique in the workspace, so the name is
   `<aliasPrefix>-<purpose>` (`theos-<namespace>-specs`), found again by ID
   and then by that name among the channels the bot is in, as on Matrix. A
   display name of its own doesn't exist, so a renamed room is refused
   (decision 3) instead of ignored; a bare `general` would collide in every
   workspace. The
   room's topic is the channel's topic. A name Slack says is taken, a
   channel the bot was removed from included, is `ChatAliasTakenError`.
5. **There is no Space.** The workspace is the Space. Whether a host has
   Spaces, which keys its Secret needs and which markup it reads is one row
   per host in `internal/model`, as `model.CI` is one row per CI system
   (docs/design.md, "What a CI system writes is a business rule"); the
   controller asks the row and makes no Space, and `status.space` stays
   unset.
6. **A thread is a Slack thread, a message its `ts`.** `Reply` posts with
   `thread_ts`, `Edit` is `chat.update`, `React` is `reactions.add` with the
   emoji's name for the key (✅ is `white_check_mark`; a key the adapter has
   no name for is an error, not a guess). The post is rendered for Slack in
   `internal/model` beside `HTML()`, as mrkdwn with its three characters
   escaped, so the adapter parses no HTML.
   What people type comes the other way round. Slack's text has `&`, `<`
   and `>` as entities, a link as `<https://x|label>` and a mention as
   `<@U…>` (Slack's formatting page; that a message event carries the same
   forms is from memory, and step 5 checks it). The adapter decodes inbound
   text in a pure function before it leaves the port, so a `ChatMessage`
   holds what the person typed: a link becomes its address, with its label
   before it when the two differ, a mention `@` and the member ID, and the
   entities their characters, last. Without it `brief` with a link in the
   idea, or `changes: a < b`, would reach the Brief and the issue mangled.
7. **Socket Mode, opened only by `theos-chat`.** The cluster needs no
   Ingress and no Service: the adapter asks `apps.connections.open` for a
   WebSocket address with the app-level token and dials out. It needs
   outbound HTTPS to Slack from the operator and `theos-chat`; the chart
   puts no NetworkPolicy on either (none under
   `dist/chart/templates/chat/`), so a cluster with its own egress rules
   allows the hosts Slack lists. Slack sends each event to any one of an
   app's open connections, so the socket is opened on the first `Sync` and
   never by the operator, which calls no `Sync`; of two `theos-chat`
   replicas only the leader reconciles (`cmd/theos-chat/main.go:59-60`), so
   only it holds one. The WebSocket client is `golang.org/x/net/websocket`,
   a module the build already requires (`go.mod:31`): no new dependency.
   Its limit is that it reads no proxy setting (nothing in the package
   reads `HTTPS_PROXY`, and its own comment names gorilla's and coder's
   clients for what it lacks), while the Web API calls go through
   `net/http`, which does. A cluster that reaches Slack only through an
   HTTPS proxy would get the channels and no commands, so the guide says
   the socket needs a direct route to Slack; a dial through the proxy by
   hand (`websocket.NewClient` takes a connection) is added when someone
   needs it. Slack closes a socket on purpose every few hours
   (`disconnect`) and each connection takes a new address from
   `apps.connections.open`: the adapter dials again, and step 5 tests it.
8. **`Sync` keeps its contract on a socket.** The adapter acknowledges each
   envelope, drops one whose `event_id` it has seen, and buffers people's
   messages in the order they arrive, each once by its channel and `ts`.
   Socket Mode promises no order across channels, so `since` can't be a
   time alone: a message with an older `ts` that arrives after a newer one
   from another room would never be returned. The token is a position in
   the buffer with the newest message time returned, and
   `Sync(since, wait)` returns what arrived after that position. A read
   empties nothing: Chats with the same tokens share one host
   (`internal/chatbot/bot.go:139-160`), each with its own `since`, and the
   buffer drops only its oldest messages, past a bound. The time is for
   the catch-up read alone. What was said while no socket was open is read
   through the Web API when one opens, and added unless its channel and
   `ts` are already there: each channel's history, and the replies of each
   thread among a channel's last 200 messages. After a redial it reads
   from a minute before the newest time in the buffer; after a restart,
   when the buffer is empty and a position means nothing, from the token's
   time. A reply to an older thread said during an outage is not read, nor
   a message Slack still held at a restart that is older than one already
   returned; the guide says so, and the person says it again. Without the
   catch-up a restart would lose commands, which Matrix never does.
9. **The tokens, and their scopes.** Two, in the Secret
   `credentialsSecretRef` already names, which the user creates:
   `botToken` (`xoxb-…`) with `groups:write` (make a private channel, set
   its topic, invite), `groups:read` (find the bot's channels and a
   channel's members), `groups:history` (the `message.groups` event and the
   catch-up read), `chat:write` and `reactions:write`; and `appToken`
   (`xapp-…`) with `connections:write`. The operator reads `botToken` alone;
   `theos-chat` reads both, as it reads the Matrix token today
   (`internal/chatbot/bot.go:139-147`). A missing key is `SecretIncomplete`
   naming it; a scope Slack says is missing is a new reason, `ScopeMissing`,
   with the scope in the message.
10. **RBAC still decides, and nobody adds anyone else.** A sender is looked
    up in `spec.slack.people` and the command is sent as that Kubernetes
    user, unchanged. The admission policy that lets a `people` entry name
    only its writer covers `spec.slack.people` too, in the same step as the
    field: without it, whoever may write a Chat could map their Slack ID to
    another person's user.
11. **Slack's address is a setting.** The operator's `--chat-slack-api-url`
    (the chart's `chat.slack.apiURL`), by default `https://slack.com/api`;
    GovSlack names its own. The operator writes the address it reached into
    `status.homeserver`, which `theos-chat` already reads
    (`bot.go:148`), so `theos-chat` gets no flag; the field keeps its name
    and its comment says it is the chat host's address.

### Requests from Linear and Jira

12. **A Brief gets `spec.requestSources`, a list of sources,** each with a
    `name` and exactly one of `linear: {team, label}` and
    `jira: {site, cloudID, project, label}`. Not `spec.requests`: a Brief
    already has `spec.releases[].requests` (one entry's settings) and
    `status.requests` (the items), the status fields of decision 15 say
    `source`, and a field's name can't change once a release ships it. Not
    under `spec.releases[].requests`: a
    tracker is not where a Container's releases go, and that entry needs a
    GitHub repository a Linear or Jira team may not have. A request is the
    Brief's anyway: the planner decides which Containers it touches.

    ```yaml
    requestSources:
    - name: product
      linear: {team: ENG, label: factory}
      credentialsSecretRef: {name: product-linear}
      mode: Ask                 # Off, Ask (the default) or On
      accepted: [412]           # ENG-412
      declined: [415]
    - name: support
      jira:
        site: https://acme.atlassian.net
        cloudID: 00000000-0000-4000-8000-000000000000
        project: SHOP
        label: factory
      credentialsSecretRef: {name: acme-jira}
    ```

    CEL: one of `linear` and `jira`; `site` is https; no two entries with
    one team, or one site and project (an item is read once); a number not
    in both `accepted` and `declined`. At most ten entries, keyed by `name`.
13. **The label is the door, and `Ask` the default.** An open item of the
    team or project that carries the label is a request. Open is the
    tracker's own word for it: on Linear a state whose type is not
    `completed`, `canceled` or `duplicate` (the third is in Linear's schema,
    step 6 as built), on Jira a status whose category is not Done
    (both from memory; step 6 checks). `authors` and
    `teams` are not offered: their patterns are GitHub's and can't change,
    a Jira account ID doesn't fit them, and who may label an item is a rule
    the team's tracker already keeps, where anyone may file an issue on a
    public GitHub repository. `Ask` is the default because an entry exists
    to take requests, a person at the door costs one click, and `On` leaves
    nobody at it: whoever may label an item on the tracker then puts its
    text before the planner. On Jira a label is free text that anyone who
    may edit the item can add, and an item can come in by e-mail or a
    service desk. Each tracker's page in the guide says so where it
    explains `On` (step 13).

    Taking the label off reads as closing the item. It is no longer listed,
    so the request leaves `status.requests` as a closed one does
    (brief-requests, decision 10), and from then on the factory writes
    nothing on the item and doesn't close it: without the label it is the
    team's again. The issues it already became go on in the Git host. The
    label put back is a new request, as a reopened GitHub issue is.
14. **Numbers stay numbers.** One entry names one team or one project, and
    inside it an item has a number: Linear's `number`, the digits of a Jira
    key. The adapter is built for that team or project and writes
    `ENG-412` itself. So `accepted`, `declined` and `status.requests[].number`
    stay `int32`, which Compatibility would not let change anyway, and the
    port keeps `int`. The port's issue gains `Key` (`ENG-412`, `#12` on
    GitHub) for people to read. An item moved to another team or project
    no longer has its number here: `IssueNotFound`, which the Brief already
    drops (`internal/controller/brief_requests.go:324`).
15. **Status says where a request is from.** `status.requests[]` gains
    `source` (the entry's name) and `key`; for those `repository` is
    omitted, which makes it optional, a loosening the compat guard allows.
    `author` grows from 39 to 100 characters. On GitHub it is a login,
    held to letters, digits and `-`; on Linear and Jira it is the display
    name of whoever made the item, which is text that person chose. The
    adapter cuts it to one line of at most 100 characters. It shows in
    `needsYou` before anyone accepts, as a login does, so it is kept as
    data: `brief.md`, which tells the planner a GitHub request is from
    that user (brief-requests, decision 7), names a tracker's request by
    its source and key, and the display name is a field of
    `model/requests.yaml` and nothing more. A request's name in
    `needsYou` and `roundRequests` is `<source>:<key>` (`product:ENG-412`),
    which no `owner/name#N` can equal. The numbers that do nothing more are
    in a new `status.requestSourcesSettled`, keyed by `source`, since
    `requestsSettled`'s key is `repository`.
16. **A request goes the way a GitHub request goes:** a planning round
    takes it or skips it, and the operator answers on the item once per
    stage, with the marker, as `docs/plans/done/brief-requests.md`
    decisions 5 to 10 say. What is written to the source item, and when:
    - `planned`, when the round is applied: the issues it became, each with
      its link, and the release; or the round's reason for skipping it, and
      the item is closed as not planned.
    - `declined`, when its number is in `declined`: closed as not planned.
    - `released`, when every issue it became is closed and their releases
      are out: where it shipped, and the merged pull request of each issue,
      which `model.ReleasedComment` doesn't name today
      (`internal/model/brief_request.go:241-261`); closed as completed.
    Each issue a round files for a request links the source item. Whether
    a GitHub request's issues do so today was not checked for this plan;
    step 10 reads it and adds the line for every source.

    A tracker item's text is data, never instructions, and travels as a
    GitHub request's does. Nothing of its description is in `needsYou`
    before a person accepts (decision 6 there). Its title and its
    description, flattened to text and cut at 4000 characters, are taken
    once, on accept (decision 5), and reach the planner only in
    `model/requests.yaml` (decision 7). The issues a round files from it
    reach agents between the markers of docs/design.md, "Issue text is
    data, not instructions" (`docs/design.md:409`). Comments on the item
    are never read as requests: the adapter reads them only to find its
    own marker. Step 10 holds this with a spec.
17. **The factory closes the item,** as it closes a GitHub issue: a request
    leaves `status.requests` when its item is closed (brief-requests,
    decision 10), and an item left open would be listed for ever. Linear:
    the team's first state, by position, of type `completed`, or `canceled`
    for not planned. Jira: a transition the item offers into a status of category
    Done, with a resolution where the transition's screen takes one. Which
    status and which resolution are names the site holds: a Jira admin
    writes the workflow and the resolutions, older and translated sites
    say `Fixed` and `Won't Fix`, and a team-managed project has no
    resolution to send. So they are settings on the `jira` entry,
    `close.completed` and `close.notPlanned`, each with an optional
    `status` and `resolution`. The default status is the first of category
    Done the item offers a transition into; the default resolutions are
    `Done` and `Won't Do`, sent only when the transition asks for one. A
    workflow that offers no such transition, a `status` the item can't
    reach and a `resolution` the site doesn't have are a new reason,
    `CannotClose`, on the Brief's `Requests` condition, with the names the
    site does offer in the message; the comment is still posted.

    The port also asks a tracker to close a closed issue again with the
    new reason (`internal/ports/contract/intake.go:164`). Linear does: the
    item moves to the other state. Jira does not: a Done item usually
    offers no transition into another Done status, its resolution can be
    edited only where an admin put the field on a screen, and reopening it
    to close it again would run the team's automations twice. On Jira an
    item that is already closed stays as it is, `CloseIssue` returns no
    error, since closed is what the Brief asked for, and the comment says
    which of the two it was; the subtest is in Jira's `Cannot`
    (decision 22). The marker is the comment's last line and shows where
    the tracker has no hidden text.
18. **No ETag, so the adapter makes one.** Neither API has GitHub's
    conditional list. The adapter reads the list every ten minutes, as a
    Brief reads GitHub, and returns as ETag a hash of the open items' IDs
    and update times: `NotModified` holds the contract, and nothing
    downstream runs on an unchanged list.
19. **Jira Cloud, by its REST API version 3; Data Center later.** The
    descriptions are Atlassian's document format, which the adapter
    flattens to text in a pure function. Data Center has other auth and
    other search calls: it is not claimed until an adapter passes the suite
    against a fake shaped like it, as Teams is not.
20. **Where the credentials live.** A Secret the user creates in the
    Brief's namespace, named by the entry's `credentialsSecretRef`, read by
    the operator alone, never by an agent, CI or `theos-chat`
    (docs/design.md, "A user's credential never reaches code the agents
    wrote"). Linear: `token`, an API key of an account made for the
    factory, with read and write, limited to the team where Linear offers
    it. Jira: `email` and `token`, an API token with scopes of such an
    account, which may browse the project, comment and transition its
    items (`read:jira-work` and `write:jira-work`, and `read:jira-user`, for
    the account's own ID: step 6 as built, Jira). The chart
    renders none of them, and no test holds a literal: a fixture's token is
    `rand.Text()` (the `secrets` skill), never a string shaped like a Slack
    or Atlassian token, which the scanner on every pull request would flag.
    Linear's address is the operator's `--linear-api-url`, by default
    `https://api.linear.app/graphql`.

    A scoped Atlassian token does not call the site's own address:
    Atlassian's page on API tokens says it calls
    `https://api.atlassian.com/ex/jira/<cloudId>`. So the adapter calls
    `<address>/ex/jira/<cloudID>/rest/api/3/…`, the address being the
    operator's `--jira-api-url`, by default `https://api.atlassian.com`,
    and never the site. The entry gives both: `site`, which people open
    and the factory's links point to, and `cloudID`, the site's ID at
    Atlassian, which the guide says how to find. Both are required for
    now, since a required field may become optional later and never the
    other way (Compatibility): if Atlassian documents a lookup the token
    may make, `cloudID` is loosened then. A token without scopes can do
    all its account can; the guide doesn't ask for one and the adapter is
    not built for it. Step 9 reads Atlassian's reference for the address,
    a lookup, how a scoped token is sent and the scopes' names before the
    two fields are fixed in the CRD, and corrects this decision where it
    differs.

### Tests, and what is left out

21. **Each adapter has a fake of its product in its own package,** as
    `matrixtest` is: `internal/adapters/slack/slacktest` (the Web API over
    `httptest` and the socket on the same server),
    `internal/adapters/linear/lineartest` and
    `internal/adapters/jira/jiratest`. The adapter's tests run its port's
    suite on it in `make test`, with no network, and hold what the product
    does its own way. The controllers' envtest doesn't use them: it uses
    the ports' fakes (step 3), so a Brief's and a Chat's specs don't depend
    on a product's HTTP.
22. **What each adapter names in `Cannot`,** with the reason beside it.
    Slack: the Space subtest and "brings a room's name to the one asked
    for" (decisions 4 and 5). Linear and Jira: "never lists a pull request"
    (there is none among the items) and "says issues that are turned off
    are disabled" (nothing turns them off). Jira also: "closes a closed
    issue again, with the new reason, even one a person closed"
    (decision 17). Everything else runs.
23. **Teams is left for later,** a third ChatHost only if someone asks, as
    the issue says. Nothing here is built for it but what makes it small:
    a `host` enum, a per-host row and a port that names no product.

## What the suites' reviews found, each decided

| Finding | Verified at | Decision |
| --- | --- | --- |
| The intake port's types and reasons name GitHub | `internal/ports/intake.go:26-43`, `:69-91`, `:106-117`; used in `internal/controller/brief_requests.go:70`, `:160-203` | Step 1. `TrackerIssue`, `TrackerComment`, and the port's own reasons. Three values change with them, since one `Requests` condition covers every source: `GitHubUnavailable` to `TrackerUnavailable`, `GitHubError` to `TrackerError`, `RepositoryNotFound` to `TrackerNotFound` (not `SourceNotFound`: beside `Published`'s `SourceTooLarge` and `SourceTagTaken`, a source is an app's source code). A reason is not in the schema, so Compatibility allows it. No UI code reads the three: `ui/src/model/requests.ts:129` passes the condition's reason through, and the `GitHubError` and `RepositoryNotFound` of `ui/src/model/releases.ts:42,46` are the Crew's `Published` reasons, which stay. The docs that spell them change in the same pull request: the Issues row of `docs/architecture.md:147`, and the guide's table at `docs/getting-started.md:944` (where `RepositoryNotFound` is "as for publishing, above", so `TrackerNotFound` gets a row of its own) and `:946`; with a *Changed* line for the release notes. The release target keeps its own (`deployer.go:213-223`). A test beside `hostneutral_test.go` fails when `intake.go`, `chathost.go` or their suites name a product. |
| `int` issue numbers against string keys | `intake.go:71`, `:131-138`; `brief_types.go:187-195`, `:453` | Keep `int` (decision 14); add `Key`. Step 1. |
| A comment's ID is an `int64` | `intake.go:85`; nothing in `internal/controller/brief_request*.go` reads it | Step 1: a string. Linear's is a UUID. |
| `TeamMember` is GitHub's teams | `intake.go:126`; `internal/controller/brief_requests.go:297` | Step 1: an optional capability, `ports.TeamMembers`, with its three subtests in a suite of their own (docs/design.md, "What a host lacks"). Linear and Jira don't have it (decision 13). |
| The ChatHost port names Matrix | `internal/ports/chathost.go:25-31`, `:61-65`, `:95-110`, `:134-135`; `internal/model/chat_word.go:167-177` | Step 2. `ChatConfig{Address, Token, AppToken}`, still comparable since it is a map key (`internal/chatbot/bot.go:75`); `ChatMessage.Formatted`; `model.ChatPerson.ID`; comments and the error's text. |
| No ChatHost fake in `internal/ports/fake` | `internal/ports/fake/contracts_test.go:37-84` | Step 3: do it, and an IssueTracker fake with it. The ChatHost fake takes its shape as options (no Spaces, one name per channel) and runs the suite in both, which commits the Slack-shaped host #131 tried and did not commit. The Slack specs of the Chat controller and `theos-chat` run on it; the Matrix specs stay on `matrixtest`. |
| Does `internal/chatbot` assume event IDs are unique? | `internal/chatbot/state.go:71-81` matches an event with its room; `commands.go:99-105` answers with the event's room; `bot.go:219`, `:240`, `:259`, `:268` keep and use the room beside each ID; `commands.go:63-71` dedupes nothing and relies on `Sync`'s "each once" | No change: every use pairs an ID with its room, which is what Slack gives (a `ts` is unique in its channel, and an edit carries the edited message's own, which `Amend` in the suite already allows). Step 3 pins it: a spec on the fake where two rooms hold a message of one ID. Deliveries sent twice are the adapter's to drop (decision 8). |
| Three fake GitHubs for intake | `internal/controller/fakegithub_issues_test.go:33-58`; `internal/adapters/github/github_issues_test.go:40-80`; `internal/adapters/github/githubtest/issues.go:57-70` | Two become one, the third stays. Step 4 moves the Brief's request specs to the port's fake tracker and deletes the controller's fake; the client's own stays, since it holds GitHub's paging, ETag and status codes. `githubtest` models another slice (labels, milestones, dependencies, no author, no ETag) for GitHub as a Git host, and #57's unmerged steps edit it: folding the client's fake into it is left to [github-host.md](github-host.md), not done here. |

## Steps

Each row is one pull request (step 6 is two) that leaves main working with
`make check` passing, and can be filed as an issue as it stands. *Needs*
names the steps that are merged first; rows that don't need each other are
built side by side: 1 beside 2, then 3, then the chat track (5, 7, 8, 11)
beside the requests track (4, 6, 9, 10, 12), and 13 last. The tiers
are AGENTS.md's: 1 is `make test` without a cluster, an adapter on its fake
included; 2 is `make validate-yaml`; 4 is envtest. Steps 1 to 4 change no
behaviour a user sees but the three reason values.

Two groups ship in one release each (ROADMAP.md, "A release never ships a
half-built feature"). Steps 7, 8 and 11: step 7 shows fields nothing reads.
Steps 9, 10 and 12: step 9 does the same, and after step 10 the UI would
label a tracker's request `undefined #412` (`ui/src/model/requests.ts:79`)
and its *Accept* would patch `/spec/releases`, where no entry matches, until
step 12 is in.

Four steps are an owner's session, since they change a protected path. A
step that adds API fields runs `make manifests generate`, and
`make manifests` writes the chart's copy of the CRDs to
`dist/chart/templates/crd/`: steps 7 and 9. Steps 5 and 13 say theirs.

| Step | Needs | What | Done when | Tier | Change to docs/design.md |
| --- | --- | --- | --- | --- | --- |
| 1. The intake port names no product | none | Rename the types and reasons of `internal/ports/intake.go` as the table above says; `Key` on an issue, a string ID on a comment; `TeamMember` becomes the optional `ports.TeamMembers` with its own suite; the guard test. The docs that spell the three reasons: `docs/architecture.md:147` and `docs/getting-started.md:944` (split: `TrackerNotFound` has its own row) and `:946`. No CRD change and no UI change: the UI reads none of the three. | The GitHub client passes both suites; `docs/architecture.md` and `docs/getting-started.md` name the three new reason values and no old one for requests; the guard fails on a product's name in the port. | 1 (`make test`) | "Factory components": the Issues row says request intake is a port of its own, with any tracker behind it. |
| 2. The chat port names no product | none | `ChatConfig{Address, Token, AppToken}`, `ChatMessage.Formatted`, `model.ChatPerson.ID`, the per-host row in `internal/model` with Matrix alone in it, the controller and `theos-chat` taking a map from host to `ports.ChatHosts`. No CRD change. | The Matrix adapter passes `contract.ChatHost` unchanged; the Chat's and `theos-chat`'s envtest pass unchanged; the guard of step 1 covers `chathost.go`, whichever of the two merges last adding it. | 1 and 4 | None. |
| 3. Fakes of the two ports | 1, 2 | `fake.ChatHost` (shaped by options) and `fake.IssueTracker` in `internal/ports/fake`, each run by its suite in `contracts_test.go`, the ChatHost twice. `fake.IssueTracker` also implements `ports.TeamMembers` and runs `contract.TeamMembers`, and has a shape without teams, so that `inTeams`' "tracker has no teams" failure is reached. A `theos-chat` spec with one message ID in two rooms. | Both suites pass on the fakes with `-race -shuffle=on`; the Slack shape skips only the two subtests of decision 22. | 1 and 4 | "A port is a narrow Go interface with one fake": ChatHost and request intake now have theirs. |
| 4. A Brief reads any tracker through one seam | 3 | `IssueTrackers` builds a tracker for a source, not `GitHubIssues`; the Brief's request specs run on `fake.IssueTracker`, the team spec (`brief_requests_test.go:200`) on its `ports.TeamMembers`; `internal/controller/fakegithub_issues_test.go` goes. | The request specs pass on the fake with the same assertions; a spec that asserted GitHub's HTTP is in the client's tests or was already there. | 4 | None. |
| 5. The Slack adapter | 2, 3 | `internal/adapters/slack` and `slacktest`: decisions 4 to 9, with `ScopeMissing` on the port, after Slack's reference is read for what decision 6 and the scopes take from memory. Adds `slack`, `linear` and `jira` to `adapters-stay-out-of-kubernetes` in `.golangci.yml` (`:65-72`, which lists each adapter), a protected path: an owner's session. Wired to nothing. | `contract.ChatHost` passes on `slacktest` with the two skips; the adapter's tests hold the catch-up read, a delivery sent twice, a message that arrives late from another channel, two readers with their own `since` each given every message once, a dial again after Slack's `disconnect`, the socket opened by `Sync` alone, inbound text decoded (the entities, a labelled link, a mention), a taken name and a missing scope. | 1 | "A port is swappable only once its fake and two real adapters pass its contract; none is yet" (`docs/design.md:1573`): ChatHost is. |
| 6. The Linear and Jira adapters | 1, 3 | `internal/adapters/linear` with `lineartest`, then `internal/adapters/jira` with `jiratest`, one pull request each, side by side: decisions 13 (what open is), 14 and 17 to 20, after each reference is read for what they take from memory. Wired to nothing. | `contract.IssueTracker` passes on each fake with the two skips, and Jira's third; each adapter's tests hold its ETag, its way to close, an item whose label is taken off, a moved item, and for Jira the flattened description, the address with the cloud ID, a status and a resolution named in `close`, a closed item left as it is and `CannotClose`. | 1 | The same line (`docs/design.md:1573`): request intake is, with Linear's pull request. |
| 7. The Chat API takes Slack | none | `spec.host`, `spec.slack.people`, the CEL of decision 3, the admission policy over `spec.slack.people`, the sample and `test/invalid/` files; `make manifests generate`. Read by nothing. An owner's session: the CRD's copy under `dist/chart/templates/crd/` and the policy's copy, `dist/chart/templates/extras/chat-people-are-themselves.yaml`, which is kept by hand, are protected paths. | envtest refuses each failing case (`slack` without `host: slack`; `homeserver`, `space`, `people` or a renamed room with it; a prefix Slack won't take; `host` changed; a Slack person naming someone else); a Chat stored before the field still validates; `test/compat` passes; the chart's policy holds the same rules as `config/policy/`'s, read side by side, since nothing compares them. | 2 and 4 | "Chat": the heading loses "in Matrix"; `host`, the `slack` block and the policy's second list. |
| 8. The operator and `theos-chat` run a Slack Chat | 2, 3, 5, 7 | The Slack row in `internal/model` (no Space, the two keys, mrkdwn), the controller and `theos-chat` on it, `--chat-slack-api-url`, `ScopeMissing`; `cmd` wires the adapter. | envtest on the Slack-shaped fake: six channels and no Space, people invited, a thread opened, edited and marked done, `approve` sent as the person, a stranger refused, a missing key and a missing scope on `Ready`. | 1 and 4 | "Chat": a room is a private channel, no Space, Socket Mode and what a restart reads again; the ports table's ChatHost row names Slack. |
| 9. The Brief API takes `spec.requestSources` | 1 | The list and its CEL (decisions 12 and 13), the `jira` entry's `cloudID` and `close` (decisions 17 and 20) once Atlassian's reference is read as decision 20 says, the status fields of decision 15, the model's request identity and names, the sample and `test/invalid/` files; `make manifests generate`. Read by nothing. An owner's session: the CRD's copy under `dist/chart/templates/crd/` is a protected path. | envtest refuses each failing case (both or neither of `linear` and `jira`, a `site` that isn't https, a `jira` entry without `cloudID`, one team twice, a number accepted and declined); `internal/model` table tests name a source's request; `test/compat` passes. | 1, 2 and 4 | "Briefs": requests come from a releases repository's GitHub issues or from a source in `spec.requestSources`. |
| 10. A Brief reads and answers a tracker's requests | 4, 6, 9 | The controller reads each source and answers as decision 16 says, the merged pull requests and the link back for every source; the planner's workspace names the source, and `brief.md` a tracker's request by source and key (decision 15); `--linear-api-url` and `--jira-api-url`; `cmd` wires the two adapters. | envtest on the fake tracker: a labelled item waits in `needsYou` with its title and author and none of its description, is accepted, planned, answered and closed at each stage; its text reaches the planner in `model/requests.yaml` and nowhere else; a declined one; `On`; an item whose label is taken off leaves `status.requests` and is written to no more; `CannotClose` and `TrackerNotFound` on `Requests`; nothing is read with `mode: Off`. | 1 and 4 | "Briefs": what is written to the item and when, that its text is data, and what taking the label off does; "A user's credential…" names the sources' Secrets; the Issues row names Linear and Jira. |
| 11. The UI shows a Slack Chat | 7 | The chat panel shows a Slack Chat's channels and its people from `spec.slack.people`. | The UI's tests on the fixture with a Slack Chat. | the UI's check | None. |
| 12. The UI shows a tracker's requests | 9 | A Brief's page lists a source's request by its source and key, with *Accept* and *Decline*, which patch `/spec/requestSources`, one more write on the UI server's allowlist. | The UI's tests on the fixture with a Linear source: the request reads `product:ENG-412` and never `undefined #412`, and *Accept* patches the source's `accepted` and not `/spec/releases`; the server refuses a patch of `/spec/requestSources` that changes anything but `accepted` and `declined`. | the UI's check | "UI actions": the one more write. |
| 13. The guide and the chart | 8, 10, 11, 12 | `docs/chat-slack.md`, `docs/requests-linear.md` and `docs/requests-jira.md`, each with how to make the app or the account, every token's scopes and what the factory writes; the Slack page says the socket needs a direct route to Slack, the tracker pages who reaches the planner with `On`, what taking the label off does and, for Jira, how to find the cloud ID and name `close`; linked from `docs/getting-started.md` and the README. The chart's `chat.slack.apiURL`, Linear's address and Jira's, and a section each in `dist/chart/README.md`: protected paths, an owner's session. `docs/architecture.md`'s two rows and its line on swappable ports; `docs/comparison.md` loses "not yet (#61)". This plan moves to `done/`. | `make validate-yaml` passes with every option on and renders no credential; the link check passes; each page's scopes are the ones the adapters' tests ask their fakes for. | 2 | A last read against what steps 7 to 12 wrote. |

## Step 1 as built (#151)

What the step chose where the plan says nothing:

- **The `TeamMembers` suite has five subtests, not three.** The three the
  table names moved as they were. `TeamMember` was also read in two of the
  IssueTracker suite's failures (a credential the tracker doesn't know, a
  tracker that doesn't answer), which can't ask it any more, so its half of
  each is a subtest of the new suite: nothing the old suite pinned is lost.
- **A tracker without teams, asked for a team's members, fails.** The Brief
  finds the capability with a type assertion (`inTeams`,
  `internal/controller/brief_requests.go`). An entry that names teams on a
  tracker without it gets `TrackerError` with a message that says so, never
  a stranger let in on a guess. GitHub has teams, so nothing reaches it
  today; step 9's CEL keeps `teams` off the sources that lack them
  (decision 13).
- **`Unauthorized` stays one reason for both ports.** It names no product
  and its value doesn't change, so the intake port goes on using the
  constant in `internal/ports/deployer.go`.
- **The guard reads lines, not identifiers**
  (`internal/ports/productneutral_test.go`): `github`, `gitlab`, `gitea`,
  `linear`, `jira`, `slack` and `matrix`, without case, anywhere in a line
  of `intake.go` and its two suites. An import's path is left out, since
  this module's own is on a code host. A file of the list that is gone
  fails the test, so a move can't leave the port unguarded.
- **Whoever matches on a reason is told by the release notes.** A commit of
  the step carries the `BREAKING CHANGE` footer `hack/release-notes.sh`
  reads, and docs/design.md, "Compatibility", now says a reason may be
  renamed or removed that way.

## Step 2 as built (#152)

What the step chose where the plan says nothing:

- **The row is `model.ChatSystem`, in `internal/model/chat_system.go`.**
  Its fields are `Spaces`, `TokenKey`, `AppTokenKey` and `Markup`, with
  `Format` writing a post in that markup. The operator and `theos-chat`
  take `map[string]ports.ChatHosts`, keyed by the row's `Name`, and
  `cmd/main.go` and `cmd/theos-chat` give it one entry, `matrix`.
- **How a Chat picks its host today.** It doesn't: every Chat is on
  `model.ChatSystemDefault`, which is `matrix`. The Chat controller's
  `Reconcile` and `theos-chat`'s `Bot.host` look that name up. Step 7 adds
  `spec.host`, and step 8 reads it in those two places. A name that has no
  adapter is a plain reconcile error, retried with backoff and shown in no
  condition: until step 8, no Chat can name such a host. When step 8 reads
  `spec.host`, it turns that into a `Ready=False` reason a person can read,
  as a missing deployer shows (`ports.Deployers.For`,
  `internal/controller/container_deploy.go`).
- **The operator passes no `AppToken`.** It reads no rooms. `theos-chat`
  reads `AppTokenKey` only when the row names one, and a missing key is
  the same error as a missing `TokenKey`. `theos-chat` keeps one built host
  per host name and `ChatConfig`, so two hosts at one address with one
  token never share an adapter.
- **The Ready messages still use Matrix's words.** They name the Space and
  `accessToken`, which is true of the one row. Step 8 rewords them when it
  adds the Slack row, the no-Space branch of the controller with them. That
  branch is built and nothing reaches it yet; the Slack-shaped fake of
  step 3 and the envtest of step 8 test it.
- **The refusal's text changed and no user sees it.** It now reads "the chat
  host refused the bot's token". It reaches a condition only through the
  Matrix adapter's empty token, and the controller says `SecretIncomplete`
  before it builds an adapter.

## Step 3 as built (#153)

What the step chose where the plan says nothing:

- **The fakes give no more than the ports promise, in every shape.**
  `fake.ChatHost` numbers a message in its room alone, so the first message
  of every room has one ID, and a person's edit carries the ID of the
  message it replaces, as Slack's do; Matrix gives more, and nothing may
  rely on it. `fake.IssueTracker` keys an issue `REQ-<number>`, never the
  number alone. The shapes are options: `WithoutSpaces` and
  `OneNamePerRoom` for the chat host, `WithoutTeams` for the tracker, whose
  port is then no `ports.TeamMembers`.
- **The controller suite reaches the chat fake by its address.** Every
  Chat is on `matrix` until step 8, so the suite's `chatHosts()` gives the
  `matrix` row an adapter that sends a Chat naming the fake's address to
  the fake and every other to `matrixtest`. The spec with one message ID in
  two rooms (`task_spec_test.go`) runs on the fake shaped as Matrix, with
  Spaces, since the controller still makes one; the Matrix specs stay on
  `matrixtest`. Step 8 gives the Slack row the fake shaped as Slack.
- **The spec pins the room, and fails without it.** The ID collides in
  `specs` and `reviews`, where `approve` is a command in a thread: a
  `theos-chat` that matched a thread by its ID alone would approve the spec
  from a person's thread in `reviews`. With the room dropped from
  `state.thread` the spec failed; nothing in `internal/chatbot` changed.

## Step 4 as built (#154)

What the step chose where the plan says nothing:

- **The seam is `ports.IssueTrackers`, a map from a tracker's name to its
  adapter,** as `ports.Deployers` is. Each adapter is a
  `ports.TrackerAdapter`, whose one method, `Tracker`, takes a
  `ports.TrackerSource` (`Tracker`, `Address`, `Project`) and the data of
  the source's Secret. A releases entry's source is `model.TrackerGitHub`
  and its repository, with no address, as before: requests were never read
  from a GitHub Enterprise Server. `cmd/main.go` gives the map one entry,
  `github.Client{}`. Step 10 adds Linear's and Jira's, and whatever a
  source needs beyond the three fields (Jira's cloud ID) with them.
- **`GitHubToken` left the Brief's seam, as the issue proposed.** The Brief
  still reads the Secret live on every read and passes its data on, nil
  when there is none; reading the keys and minting a GitHub App's token are
  `github.Client.Tracker`'s. A Secret without a whole credential is a
  `ports.CredentialMissingError` whose `Keys` says, in the adapter's words,
  which keys it takes; the Brief writes "Secret X with <Keys> is missing",
  the same text as before for GitHub. The Crew's publish seam keeps its own
  `GitHubToken`.
- **A mint that fails keeps its reason.** The adapter turns the mint's
  `ReleaseError` into an `IssuesError` with the same reason, status and
  message, so `Requests` says what it said before (`Unauthorized`, or a
  release target's reason such as `GitHubError`). Those GitHub-named
  reasons on `Requests` are left for step 10, which may rename them as
  step 1 renamed the others.
- **Each request spec reads a fake tracker of its own.** The suite's adapter
  (`internal/controller/faketracker_test.go`) builds a source's tracker with
  the `fake.IssueTracker` made for its repository, so one spec's outage is
  no other's, and counts the lists that came back unchanged, which the
  fake doesn't. The factory's account's credential is also the token the
  fake GitHub takes for the repository's releases. A token that lacks
  Issues or a team's members is the Secret changed to an account of the
  fake without that access.
- **Two specs changed with the fake.** The spec that read with a GitHub
  App's Secret (#1601) asserted GitHub's HTTP, the mint; it is the
  client's `TestTrackerReadsWithATokenOrAnApp` now, and in its place the
  Brief says a missing Secret is missing, in the adapter's words, and reads
  with one made later. The fake closes an issue a person closes as
  completed, as GitHub does, where the controller's fake left no reason:
  the spec that held the Brief to leaving such an issue alone counts the
  closes it made instead of reading an empty reason.

## Step 5 as built (#155)

**What Slack's reference says, read on 2026-11-13** (docs.slack.dev, the
method and event pages named):

- **Decision 6 holds.** "Formatting message text" says `&`, `<` and `>`
  are Slack's control characters, sent as `&amp;`, `&lt;` and `&gt;`, a
  link is `<address|words>` and a mention `<@U…>`, and that messages read
  back carry the same forms: a bare address comes back as
  `<https://x|https://x>`, which the decoder gives as the address alone.
  The `message` event's page doesn't say what its `text` holds; the
  formatting page is what decision 6 rests on. Its `message_changed`
  example names the edited message by `message.ts` and the edit by
  `message.edited.ts`; it has no `previous_message`.
- **Decision 9's bot scopes hold:** `conversations.create` (private),
  `conversations.setTopic` and `conversations.invite` take `groups:write`
  (the last two also the narrower `groups:write.topic` and
  `groups:write.invites`); `users.conversations` and `conversations.members`
  `groups:read`;
  `conversations.history`, `conversations.replies` and the
  `message.groups` event `groups:history`; `chat.postMessage` and
  `chat.update` `chat:write`; `reactions.add` `reactions:write`;
  `auth.test` none. `slacktest.Scopes` is that table, and the fake refuses a
  call whose token lacks its scope, so step 13's guide lists what the tests
  asked for. A missing scope's answer carries `needed` and `provided` (the
  `search.all` and `admin.apps.requests.cancel` pages show it; the pages of
  the methods above don't), and `ScopeMissing` names `needed`.
- **`connections:write` is the app-level token's scope.** Its page in
  Slack's scope reference says it is for app-level tokens alone, for
  `apps.connections.open`; the fake asks for it.
- **Socket Mode, as decision 7 says,** with what the page adds: the token
  goes in the `Authorization` header (as a form field it is an error),
  hello comes first, an envelope is `envelope_id`, `type`, `payload` and
  `accepts_response_payload`, and `disconnect`'s reasons are `warning`
  (about ten seconds before), `refresh_requested` and `link_disabled`. The
  page doesn't name `retry_attempt` or `retry_reason`; the adapter reads
  neither and drops a repeat by `event_id`. App-level tokens may rotate
  (`xoxe.xapp-…`, twelve hours each): the adapter doesn't refresh one, so
  the guide asks for a token without rotation.
- **Other limits:** a channel's name is lowercase letters, digits, `-` and
  `_`, at most 80 characters (decision 3's prefix of 70 leaves room for
  `-<purpose>`); `conversations.invite` invites nobody when one of a batch
  fails, so the adapter invites one person a call; a reaction already put
  is `already_reacted`, which is no error. A section block's text takes
  3000 characters (the section block's page) and a message 50 blocks (the
  blocks page). A mrkdwn text object is not `verbatim` by default, and then
  Slack makes mentions and channel links of `@here`, `@channel`,
  `@everyone` and `#name` in it ("Formatting message text"): the adapter
  sends each section `verbatim`, and the fake refuses one that is not.

**What the step chose where the plan says nothing:**

- **A post goes out twice in one message.** `text` is the plain text with
  its three characters escaped, which notifications, search and the
  history show; the formatted text, mrkdwn on Slack's row
  (`model.ChatPost.Mrkdwn`, beside `HTML()`, and
  `model.ChatMarkupMrkdwn`, which no row names until step 8), goes as
  section blocks, cut at line breaks to fit one. Past 50 blocks the mrkdwn
  is the text alone. An edit without mrkdwn clears the blocks.
- **A channel's alias is `#<name>`.** `EnsureRoom` finds the channel by ID,
  then by name, among those `users.conversations` lists, and applies no
  name: a channel's one name is its alias. A Space is `ErrNoSpaces`.
- **A token is `slack1.<buffer>.<position>.<time>`.** The buffer's name is
  random per Client, so a token from before a restart is known as one.
  That token's first Sync opens the socket and reads Slack's history after
  its time into the buffer. A token from before a restart that comes once
  the buffer started later (another Chat synced first) gets what the
  buffer holds after its time and what the history holds that the buffer
  doesn't: what that read finds after the buffer's start goes into the
  buffer, so its event, if still on its way, is dropped as a repeat, and
  what is older is this reader's alone, so no other reader is given a
  message twice. The first Sync (`""`) marks the buffer's end and the cluster's clock.
  A Sync that comes while a socket's first catch-up is read waits for it,
  so that its position counts what the catch-up adds.
- **Clocks:** on a first Sync the buffer's start is the cluster's clock,
  and Slack's ts are Slack's; with the two apart, a message said near that
  start may be missed or given twice, so step 13's guide asks for NTP on
  the cluster's nodes.
- **Order:** each Sync returns its messages oldest first. Slack promises no
  order of delivery, so two Syncs may not be in order, even in one
  channel; the position, not the time, keeps every message once.
- **Bounds:** the buffer keeps 10000 messages and 10000 event IDs. A
  position older than the buffer reads from its oldest message.
- **Redial:** on `disconnect` the adapter dials a new socket, reads from a
  minute before the newest time heard, never from before the buffer's
  start (a token of before a restart, or a first Sync, may have read what
  is older), and then closes the old one; the old socket serves Syncs
  meanwhile. A socket that drops without a word is opened again by the
  next Sync, with the same read. An error names the socket's host, never
  its address, whose `ticket` is a credential. A catch-up that fails closes the new socket, so the next
  Sync opens one and reads from the same time again.
- **A reaction has no ID of its own:** `React` returns the message's ts.
  Its emoji names are a table in the adapter (`✅` is `white_check_mark`).
- **`Client.Close`** closes the socket; the port has no Close, and
  `theos-chat` keeps a host as long as it runs.
- **The fake waits for an acknowledgement.** `slacktest` sends an event to
  the newest open socket and returns from `Post` once it is acknowledged,
  as the suite's "returns only once the host would deliver it" asks; a
  message said with no socket open is only in the history. `PostHeld`,
  `Redeliver` and `Disconnect` make the late message, the delivery sent
  twice and Slack's refresh, which leaves the old socket open, as Slack's
  warning does; `HoldOpens` (`Hold` for any method), `RefuseDials` and
  `MaxPage` hold a redial or a catch-up, refuse a dial and page every
  listing.
- **Lint:** `slack` is added to `adapters-stay-out-of-kubernetes`, and
  `jira` with it, which Jira's own pull request left out; Linear's added
  its own.

## Step 6 as built, Linear (#156)

Read on 2026-11-13: Linear's developer pages for the GraphQL API, paging,
filtering, rate limits and OAuth (linear.app/developers), and Linear's
published schema (`packages/sdk/src/schema.graphql` in the `linear/linear`
repository), whose description of each field and argument is the reference
for a call. What that settled:

- **Corrected: a state's type.** The schema lists seven: `triage`, `backlog`,
  `unstarted`, `started`, `completed`, `canceled` and `duplicate`. A
  `duplicate` is closed too, so the list excludes those three, not two as
  decision 13 had it. An item in triage is open.
- **Confirmed: the calls.** `issues(filter:, first:, after:)` with
  `IssueFilter` `team: {key: {eq}}`, `labels: {some: {name: {eq}}}` and
  `state: {type: {nin: [...]}}`; `issue(id:)`, which takes the UUID or the
  key (`ENG-412`); `commentCreate(input: {issueId, body})`, whose `issueId`
  takes either; `issueUpdate(id:, input: {stateId})`; `team.states` with each
  state's `type` and `position`; `viewer` for the token's own user. A page is
  1 to 250 nodes (the default is 50) and a query over 10,000 points is
  refused, so a page is 100.
- **Confirmed: the key.** A personal API key is sent as it is in
  `Authorization`, with no `Bearer`, which is an OAuth token's. The address
  is `https://api.linear.app/graphql`.
- **Found: Linear lists newest first** with `first`, which the schema says of
  every connection. The adapter sorts a list oldest first and reads every
  page of a comment thread, taking the earliest of its own comments that has
  the marker.
- **Found: who made an item.** `creator` is null for an item made by an
  integration or whose maker's account is gone; `botActor` and
  `externalUserCreator` then say who. The author is the creator's
  `displayName` (the workspace-unique handle, not the full `name`), else the
  bot's `name`, else the external user's `displayName`, else empty, cut to
  one line of 100 characters.
- **Found: a moved item.** The schema has `previousIdentifiers` on an item
  and says `identifier` changes with a move. It doesn't say whether
  `issue(id: "ENG-412")` still finds an item that moved, so the adapter
  depends on neither answer: every call that names an item compares the team
  and the number Linear returned with its own and says `IssueNotFound` when
  they differ. `lineartest` answers a moved item by its old key, and the
  moved-item test fails without the comparison.
- **Not reached.** The reference names one error code, `RATELIMITED`, on a
  429 with `Retry-After`. The codes and statuses of a refused key, a key
  that may not read the team and an entity that isn't there are from memory
  (`AUTHENTICATION_ERROR`, `FORBIDDEN`, "Entity not found"); the adapter
  reads them with the status behind them, so a 401, a 403 or a 5xx says the
  same where the body says nothing. The page on API keys (their read and
  write permissions and their limit to a team) was not fetched: the OAuth
  page covers OAuth's scopes only. No page says whether a comment advances an
  item's `updatedAt`, or whether Linear keeps a `<!-- -->` marker in the body
  it gives back; the adapter finds the marker in that body, so a workspace
  that strips it would find no answer and the Brief would answer twice. Only
  a real workspace shows it: step 10 and the guide (step 13) say so. A key limited to other teams may see an empty
  `teams` list (`TrackerNotFound`) rather than `FORBIDDEN`: not reached either.

What the step chose where the plan says nothing:

- **The adapter is `linear.Client{HTTP}.Issues(linear.Config{Team, Label,
  Token, APIURL})`,** an `IssueTracker` and no `ports.TeamMembers`: Linear's
  teams are where the items are, and decision 13 offers no `teams` for it. A
  test holds that.
- **The ETag costs the IDs and times alone.** Given one, the adapter reads
  each page's `id` and `updatedAt` first and the items' text only when their
  hash differs. The ETag is the hash of what is returned, so it covers the
  list the caller got.
- **Closing** moves the item to the team's first state of the type, by
  position, and leaves it where it is when it is in a state of that type
  already, so closing twice is one move and an item a person closed into
  another `completed` state stays there. An item in the `duplicate` state is
  not planned already. A team with no state of the type is `TrackerError`
  naming the type, the item left open. A comment goes to the item's ID, after
  the key was read and compared, never to the key.
- **`lineartest` answers by operation name,** with the filter as a variable
  it reads and refuses what Linear's schema lacks; it parses no GraphQL, so
  a query Linear's validator would refuse passes it. The queries' fields were
  read against the schema by hand.
- **Two subtests are in `Cannot`,** with their reasons, as decision 22 says.
  `.golangci.yml`'s `adapters-stay-out-of-kubernetes` lists
  `internal/adapters/linear`; step 5 adds the others.

## Step 6 as built, Jira (#157)

Read on 2026-11-13: Atlassian's Jira Cloud platform REST API version 3
reference, as its published OpenAPI description
(`dac-static.atlassian.com/cloud/jira/platform/swagger-v3.v3.json`, whose text
is the page's: each operation's description, parameters, scopes and
responses, and each schema's fields), because the reference's own pages are
rendered by script and came back empty. Also Atlassian's pages on API tokens
(support.atlassian.com), on basic auth for REST APIs and on search and
reconcile (developer.atlassian.com). What that settled:

- **Corrected: the search call.** `GET /rest/api/3/search` is marked "Currently
  being removed" (changelog CHANGE-2046). The list is
  `GET /rest/api/3/search/jql`: `jql` (it must be bounded, and a `project`
  clause is), `fields` (the default is `id` alone, not all fields as for one
  item), `maxResults` (default 50, at most 5000) and `nextPageToken`, with
  `{issues, nextPageToken, isLast}` back and no `total`. The token is absent
  on the last page and expires in 7 days. The query is
  `project = "SHOP" AND labels = "factory" AND statusCategory != Done ORDER BY
  created ASC`, in a pure function that quotes the two names. A page is 100.
- **Found: the list lags.** Atlassian's page on search and reconcile says the
  search has no read-after-write consistency by default: an update shows
  "within seconds" and sometimes after minutes. So an item the factory just
  closed may be listed again by the next read, and one just labelled may not
  yet be. The adapter does not use `reconcileIssues` (50 ids at most, and a
  request lists whatever is labelled). It is safe by how the rest is built:
  a read of one item (`GET /issue/{key}`), a comment's list and a transition
  are not search and are consistent, and `CloseIssue` of an item already in
  category Done does nothing, so a stale list costs one read and no second
  close. The ETag follows the list, so a lagging list is a late change.
  That a closed item the list still shows is no new request is settled in
  step 10: see "An item the Brief closed is no new request" and "What the
  lag still leaves" under "Step 10 as built (#161)" (#245).
- **Confirmed: a moved item.** "Get issue" says that when the identifier
  matches no issue, "a case-insensitive search and check for moved issues is
  performed"; if one is found "its details are returned, a 302 or other
  redirect is **not** returned. The issue key returned in the response is the
  key of the issue found." So a call for `SHOP-412` answers with `OPS-88`,
  and the adapter's comparison of the returned `fields.project.key` with its
  own is needed, as the issue says. It is made after every read of an item,
  and the write that follows goes to the ID of what was read.
  `jiratest` answers a moved item by its old key, and the moved-item test
  fails without the comparison.
- **Confirmed: transitions.** `GET /issue/{key}/transitions?expand=transitions.fields`
  lists what the item can do now: each transition's `id`, `name`,
  `to.name`, `to.statusCategory.key`, `hasScreen` and `fields`, each field
  with `required`, `hasDefaultValue` and `allowedValues`. "Fields hidden from
  the screen are not returned": the resolution is sent only when it is in
  `fields`, since a field that is not on the screen makes the transition a
  400 ("a field that isn't included on the transition screen is defined in
  `fields` or `update`"). `POST` takes `{transition: {id}, fields:
  {resolution: {name}}}` and answers 204. The names a resolution takes are
  the field's `allowedValues`, which is where the site's own are read
  from, so no call to `/resolution` (which the reference marks deprecated) is
  made. A transition that is not offered, a condition or a validator is a 400
  with Jira's words, and the adapter reads that as `CannotClose` with them.
- **Corrected: the scopes, by one.** `GET /myself` asks for `read:jira-user`,
  which the plan's decision 20 did not name. FindComment must tell the
  factory's own comments from a stranger's, and a comment names its author
  by `accountId`, so the account's own ID is read once and kept. The token is
  made with `read:jira-work`, `write:jira-work` and `read:jira-user`. Search,
  one item, its comments and its transitions are `read:jira-work`; a
  comment and a transition are `write:jira-work`. Each has a finer "beta"
  scope list, which is not used.
- **Confirmed: the address.** Atlassian's page on API tokens: a scoped token
  calls `https://api.atlassian.com/ex/jira/{cloudId}` and not the site, and
  basic auth is the account's e-mail address and the token. The reference's
  basic auth is `email:token`.
- **Not reached.** The reference lists no 403 and no 429 for these calls: a
  call fails with 400 (a search it can't run), 401 ("credentials incorrect
  or missing") or 404 ("issue not found or the user does not have permission
  to view it"). So the adapter reads 401 as `Unauthorized`, 404 as
  `IssueNotFound`, a search of a project that isn't there or isn't visible as
  `TrackerNotFound` (a 400; its words, "does not exist for the field
  'project'", are from memory), and 403, 409, 429 and 5xx by the status's
  usual meaning. A scoped token that lacks a scope of a call is a 401 whose
  message says "scope" (from memory of the gateway; the reference has
  nothing): the adapter reads that as `TokenLacksIssues` with Jira's words.
  The status categories' keys are not enumerated either: the reference's
  examples are invented values (`in-flight`, `completed`). The adapter takes
  `done`, the key of Jira's third category, from memory, and the search says
  `statusCategory != Done`, which is JQL's own name for it. How to find the
  cloud ID was not in the reference or on the pages above (`_edge/tenant_info`
  on the site is from memory): step 9 and the guide say how, after reading
  Atlassian's developer guide on the request URL, which no page reached
  here links to. A real site shows what no fake can: a workflow with
  conditions, a screen with fields the factory can't fill, and what the
  gateway answers to a token without a scope.

What the step chose where the plan says nothing:

- **The adapter is `jira.Client{HTTP}.Issues(jira.Config{Site, CloudID,
  Project, Label, Email, Token, Address, Close})`,** an `IssueTracker` and no
  `ports.TeamMembers`, which a test holds. `Close` is `Closing{Completed,
  NotPlanned}`, each `Outcome{Status, Resolution}`: step 9's `close` fields.
  `Address` is `https://api.atlassian.com` when empty. `Site` must be https.
- **`CannotClose` is a port reason,** `ports.ReasonCannotClose`, added beside
  the others in `internal/ports/intake.go` so that the Brief can name it
  (step 10). Nothing reads it yet.
- **Closing** reads the item (the project check, and its status's category),
  returns when it is in category Done, reads the transitions with their
  screens, and picks: the first offered into a status of category Done, or the
  one into the `status` named (compared without case). It then holds the
  screen against what it would send: a required field other than the
  resolution that has no default, and a resolution that is not among the
  field's `allowedValues`, are `CannotClose` naming what the site has, before
  any write. Where the screen has no resolution, none is sent, even when one
  is named. A reason that is neither completed nor not planned is refused
  before any call.
- **A key whose number does not fit an int32 is no key.** The number is parsed
  with a bit size of 32, and one that is larger fails the list like a key that
  does not parse (`TrackerError`, with the key), since `number` is int32 in the
  API (decision 14) and a wrapped number would name another request (CodeQL #228).
- **The ETag costs the IDs and update times alone.** Given one, the adapter
  reads `fields=updated` first and the items with their text only when the
  hash differs. The ETag is the hash of what the full read returned.
- **The author is the reporter's display name, else the creator's,** cut to
  one line of 100 characters. Jira gives `displayName` for an account of any
  type, an automation's included, and may withhold it for privacy; when
  neither field has a name it is empty. The e-mail address is never read.
- **A comment is Atlassian's document format, a paragraph to a line,** so
  that its text reads back as it was written, an empty line included, and the
  marker is its last line, shown. The flattening of a description is
  `jira.FlattenADF`, a pure function: paragraphs and headings a line each,
  lists behind `- ` and `1. `, a link as `text (address)`, a mention, a card,
  a status and a date as text, a table a line a row, and a node it does not
  know read for the text inside it.
- **`jiratest` serves one query,** the adapter's own, and refuses any other
  with a 400, so a change of the query is a change of the fake. It enforces
  the gateway's path and the account's scopes, pages with tokens, bumps an
  item's `updated` on every change, and keeps the old key of a moved item.
  Its status categories and its error words are the memory above.
- **Three subtests are in `Cannot`,** with their reasons, as decision 22 says:
  a pull request among the items, issues turned off, and closing a closed item
  again. `TestClosedItemStaysClosed` holds what Jira does instead.

## Step 7 as built (#158)

- **`host` defaults to `matrix` in the CRD,** so a Chat stored before the
  field reads back `matrix`, and one written without it is on Matrix. Its
  immutability is `self == oldSelf` on the field: unsetting it on update
  defaults it to `matrix` again, which a Slack Chat's rule refuses. Each rule
  of decision 3 is a rule of its own on the spec, with its own message, and
  each starts with `!has(self.host) || self.host != 'slack'`, so it passes
  every Chat valid before.
- **The prefix rule says what Slack refuses, and the old pattern still
  holds.** `aliasPrefix`'s own pattern (`^[a-z0-9][a-z0-9._=-]*$`) stays on
  every Chat and holds `_`; with `host: slack` the CEL rule refuses `.` and
  `=` and more than 70 characters, so a Slack prefix takes lowercase
  letters, digits, `-` and `_` (`acme_shop` is accepted), as decision 3
  says. Nothing needs loosening, so there is no follow-up.
- **`HostNotSupported`, as the issue's review found:** the controller looks
  the host up in `internal/model`'s rows and its adapters before anything
  else but `Duplicate`, and a Chat on a host it has no row or no adapter
  for gets `Ready=False`, that reason, a message naming the host, and no
  requeue, since `host` never changes. A missing adapter for a known host
  is the same reason, where it was an error before. `theos-chat` checks
  the same before `Ready` and reads no Secret for such a Chat, even one
  whose status reads Ready (an operator that ran Slack). envtest holds both
  (`chat_controller_test.go`, `chatbot_test.go`, `#158`); each failed on the
  code before the change, run with `go test -overlay`.
- **`model.ChatSlack` names the host,** with no row: step 8 adds the row and
  the adapter, and `ChatHostName` maps an empty `host` to the default.

## Step 8 as built (#159)

What the step chose where the plan says nothing:

- **The row grew two fields.** Slack's row is no Space, `botToken` and
  `appToken`, mrkdwn, as decisions 5 and 9 say, and two more fields each row
  now holds: `People`, the Chat's field its people are in (`spec.people`,
  `spec.slack.people`), which the stranger's refusal names, and
  `AddressFlag`, the operator's flag for the host's address, which
  `HomeserverUnset` names. `Keys()` lists the Secret's keys, the bot token's
  first. The Ready messages read the row: a Slack Chat's `Ready` says "The 6
  rooms are ready on <address>, with no Space"; `SecretMissing` and
  `SecretIncomplete` name both keys; `CredentialRefused` says "the bot's
  token" on both hosts, where it said "the access token".
- **The operator checks both keys and uses one.** Decision 9 has the
  operator read `botToken` alone; it also checks that `appToken` is in the
  Secret, so a missing app-level token is `SecretIncomplete` naming it on
  `Ready`, where it would otherwise surface only as an error in `theos-chat`'s
  log. Only `botToken` reaches the adapter; the operator calls no `Sync`, so
  it never opens the socket.
- **One map of addresses, by host.** `ChatReconciler.Homeserver` became
  `Addresses`, keyed by the row's name: Matrix's is `--chat-homeserver`,
  Slack's `--chat-slack-api-url`. A Slack Chat names no address (CEL), so
  the flag is always its address; an empty flag is `HomeserverUnset` naming
  the flag.
- **`ScopeMissing`** is a new reason, checked before `CredentialRefused`; its
  message names the scope from `ports.ChatScopeMissingError` and keeps the
  adapter's error as its detail. The operator polls it like every other
  not-Ready reason, so a scope added (the app reinstalled) is picked up in a
  minute.
- **The people's list is picked by host name** in two small functions, the
  controller's `chatPeopleIDs` and `theos-chat`'s `people`, since
  `api/v1beta1` holds the two lists and `internal/model` may not import it.
- **Nobody who left a channel is pulled back (#159, #234).** On Slack an
  invite is a join, not a pending invite as on Matrix, so inviting a person
  again puts back whoever chose to leave, which is the surprise to avoid;
  decision 4 says only that the room's topic is the channel's topic. So the
  controller invites a person not yet in `status.invited` to every room, and
  a person in it only to a room `EnsureRoom` says is `Created`, that room
  alone. `Created` means nobody was invited to the room yet: made now, or
  found half made. On Slack, `EnsureRoom` gives a
  channel it finds without its topic, where the spec has one, its topic
  back; it is `Created` only when the bot is its one member
  (`conversations.members`, which takes `groups:read`, the scope
  `users.conversations` already takes): `conversations.setTopic` failed
  right after `conversations.create`, and nobody was ever invited. A topic
  someone emptied by hand is put back and nobody is invited. Before, every
  channel found without its topic was `Created`, the controller forgot
  `status.invited`, and every person was invited to all six channels
  (`TestSlackChatPullsNobodyBack`, `TestSlackChatInvitesToAChannelLeftHalfMade`,
  `TestEnsureRoomPutsBackATopicEmptiedByHand`).
- **A room made again is new by its ID (#159).** A room is new, and every
  person invited to it, when `EnsureRoom` says `Created` or when its ID is
  not the one status holds for it: a channel or room deleted and made again
  has a new ID. Status takes a new room's ID (the Space's too, on Matrix)
  only once everyone is invited to it, so when an invite fails partway (a
  Slack rate limit is ordinary use) status keeps the old ID, the next pass
  finds the room by name or alias, sees an ID that differs and invites the
  people the failed pass did not reach. The controller invites room by
  room, and records each new room as soon as its invites are done. A found
  room's ID and alias are recorded at once, as before, and so is a new
  room's while `status.invited` is empty (the first pass, which invites
  everyone to every room anyway), so a first pass that fails halfway, on
  `AliasTaken` say, still keeps what it made in status. The `Ready=False`
  pass loses nothing: `theos-chat` acts only on a `Ready` Chat, so it never
  reads the old ID in the meantime. This also covers the #234 case without
  `conversations.members`: a channel left half made never reached status.
  No API change (`TestSlackChatInvitesToAChannelRemadeAfterAFailedInvite`,
  `TestMatrixChatInvitesToARoomMadeAgain`).
- **`conversations.members` is only ever a reason not to invite (#159).**
  An error on it, a missing scope or a listing without the bot reads as
  "not alone": the channel is found, not `Created`, and its topic is put
  back all the same, so a failing call never makes a healthy channel
  `Created` and never holds `Ready` on a reason about a channel that is
  fine. A real fault in reaching Slack still shows on the
  `conversations.setTopic` right after, with its own reason
  (`TestEnsureRoomDoubtsTheMembersCall`).
- **Edges that remain.** A channel everyone left whose topic is then
  emptied looks half made, and its people are invited to it again. A person
  invited to a room made again on a pass whose later invites failed, who
  leaves it before the next pass, is invited to it again by that pass.
  The same holds for a newcomer: a person added after the first pass whose
  invite to one room fails, and who then leaves another room before the next
  pass, is invited back to that room by the next pass (an independent review
  reproduced it: carol's third invite fails, she leaves updates, and the next
  pass puts her back in it).
  A known gap, carried from main and not changed by this step: invites stop
  at the first failed invite, so one person whose invite always fails (a
  deactivated or unknown member) keeps everyone listed after them out of
  every room and the Chat not `Ready`, with the reason `Unreachable` though
  the host answered. It is filed as its own issue and is fixed on top of this
  step (#251, below).

## #251 as built

- **The adapter says who is refused.** `Invite` returns a
  `ports.ChatInviteRefusedError` (the person and the host's code; it is
  `ErrChatInviteRefused`) when the host answers it will not invite that
  person: on Matrix 404 `M_NOT_FOUND` and 400 `M_INVALID_PARAM`; on Slack,
  answered with HTTP 200, `user_not_found` (unknown or deactivated),
  `cant_invite`, `user_is_restricted`, `user_is_ultra_restricted` and
  `ura_max_channels`. Everything else (a transport error, a 5xx, a 429 past
  the retries, a 403 on Matrix, a refused token, a missing scope) is not the
  person's and keeps today's reason. The classification is in the adapters,
  not in the controller, and the ChatHost suite states it ("refuses to
  invite a person it has no account for, them alone"). The fake refuses a
  person it has no account for, or one `Deactivate` closed, with
  `user_not_found`; `matrixtest` answers 404 `M_NOT_FOUND` for a user of its
  own server it has no account for.
- **The others go on.** The controller skips a refused person for the rest
  of the pass (asked once, not once per room), invites everyone else, and
  records each room as it would without them: a refused person is left out
  of `status.invited`, so the next pass asks them again, to every room,
  and #159's rule (a new room's ID recorded once everyone else is invited)
  holds with them present.
- **Ready stays True; `Invited` names the refusal.** One misspelt member
  must not silence the Chat, and `theos-chat` acts only on a `Ready` Chat,
  so the refusal shows on a condition of its own: `Invited`, True with
  reason `Invited`, or False with `InviteRefused` naming each person and
  code ("<host> refused to invite <id> (<code>); the other N people in
  <field> are invited"). Each pass that reaches the invites sets it. A new
  condition type is a constant and a doc comment, so the CRD changes only in
  `status.conditions`' description. A Chat with a refusal polls (a minute),
  not the ten-minute check, so a fixed account is invited soon.
- **An edge that remains.** A person in `status.invited` who is refused on
  a room made again (deactivated meanwhile) leaves `status.invited`, so once
  reactivated they are invited to every room, and on Slack put back into
  one they had left.
- Tests: the envtest specs "invites the others when the homeserver refuses
  one person ..." and "... when Slack refuses one person ..." (people
  [ada, ghost, carol], with ghost's recovery),
  `TestSlackChatRemadeChannelWithARefusedPerson`,
  `TestInviteRefusedOrUnreachable` in both adapters, `TestChatInvitedMessage`,
  `TestChatHostDeactivate` and the suite's new subtest.
- **The port has `Close` (#234).** `ChatHost.Close()` lets go of what the
  value holds open: Slack's socket (`Client.Close`, which was there), nothing
  on Matrix or the fake (which counts its open values, `fake.ChatHost.Open`).
  `theos-chat` remembers which built host each Chat used; when a Chat's
  tokens change, or the Chat is gone, it closes the host no Chat uses any
  more, and keeps one another Chat still uses. The operator closes the host
  it builds at the end of each reconcile. Before, a client replaced after a
  token change kept its socket, and Slack, which sends each event to one
  open socket, gave some events to it.
- **The fake shaped as Slack has member IDs.** `fake.MemberIDs()` makes a
  user ID `U` and eight digits, so `spec.slack.people`'s CEL takes the fake's
  people; `fake.ChatHost.Revoke` and `Grant` take a scope from a token for
  the port methods named, and `NewToken` gives an account a second token.
  The suite's Slack is `chatSlackFake` at `https://slack.test/api`, which is
  also the suite's `--chat-slack-api-url`.
- **`HostNotSupported` keeps its specs, without Slack in the map.** The
  suite's operator runs Slack now, so an operator built without its adapter
  is a reconciler whose map has no `slack`, run on a fake client
  (`TestChatOnAHostWithoutAnAdapter`); `theos-chat`'s spec deletes `slack`
  from its map. The CRD's enum takes no third host to test with.

## Step 9 as built (#160)

Atlassian's reference was not read again: step 6 as built, Jira, records
the reading decision 20 asks for (the gateway's address with the cloud ID,
basic auth with the e-mail address and a scoped token, and the three
scopes), and decision 20 already carries its correction. `site` and
`cloudID` are both required, as decision 20 says. How to find the cloud ID
is still unread here; the guide (step 13) says how, and `cloudID` is
loosened only if Atlassian documents a lookup the token may make.

What the step chose where the plan says nothing:

- **The fields' shapes follow the adapters'.** `name` is a DNS label, so
  `<source>:<key>` holds no `/` or `#` and never equals an
  `owner/name#N`. Jira's `project` and `cloudID` take the patterns the
  Jira adapter checks (`^[A-Za-z][A-Za-z0-9_]*$` and
  `^[A-Za-z0-9][A-Za-z0-9-]*$`, which a UUID matches but does not have to),
  and its `label` is one word, since a Jira label has no space. Linear's
  `team` is letters, digits and `_` (from memory; the Linear adapter only
  refuses whitespace), and its `label` any text without space at either
  end. `site` is an https origin with no path, so one site has one
  spelling but for its case. Each takes at most 100 characters but the
  name (63), the site (253), the cloud ID (64) and `close`'s names (255).
- **One item is read once, whatever the mode.** "One team" and "one site
  and project" compare without case, and two Jira entries are one project
  when they share the project and either the site or the cloud ID. Unlike
  `spec.releases`, where an entry in `Off` may share a repository, an entry
  in `Off` counts: it becomes `Ask` by one field, and the rule stays simple.
- **The item number lists are the releases entry's:** sets of at most 200,
  each from 1, with the same CEL rule worded for an item.
- **A request names either its repository or its source and key.** A CEL
  rule on `status.requests[]` holds the operator to it: `repository`, or
  `source` and `key` together, never both. `author` became optional with
  `repository`, as "Found after the plan's review" in #160 asks: a tracker's
  item may name nobody (an automation, an e-mail, a service desk), the
  label is the door, and the adapters return an empty author (#156, #157).
  `status.requestSourcesSettled` has the shape of `requestsSettled`, keyed
  by `source`.
- **The model names a request by `Request.Name()`:** `source:key` when
  `Source` is set, else `owner/name#number`; `NewRequests`,
  `WorkspaceRequests` and `model/requests.yaml` use it. The `needsYou`
  line drops "by …" when there is no author, and its recommendation says
  "the accepted requests of request source product". `model/requests.yaml`
  gains `source` and `key`, and leaves `repository` and `author` out when
  they are empty; a GitHub request's entry reads as before.
  `ListRequests` breaks a tie of time by repository, then source, then
  number.
- **Left for step 10,** since nothing reads a source yet: the controller's
  `modelRequests`, `recordRequests` and `recordSettled` carry `source` and
  `key` (today they would drop a source's request, which no read makes),
  a source's own `IsRequest` (the label, no author needed; GitHub's is
  unchanged), `brief.md`'s words for a tracker's request and the
  `Requests` condition's message.

## Step 10 as built (#161)

Checked before building, as decision 16 asks: the issues a round filed for
a GitHub request did not link the request. `model.BriefIssueBodyUsing`
wrote the planner's text, the `Builds on` and `Uses` lines and the marker,
and nothing of the request; a planner may have named it in its own words,
which no rule held. The line is now the operator's, for every source.

What the step chose where the plan says nothing:

- **The adapter's own settings ride on `ports.TrackerSource`,** in words
  that name no product: `Label`, `Site` (where people open the pages),
  `Instance` (the site's ID at the tracker: Jira's cloud ID) and `Close`, a
  map from a close reason to its `CloseOutcome{Status, Resolution}`. Each
  adapter is a `ports.TrackerAdapter` through `Tracker`: Linear reads the
  Secret's `token`, Jira its `email` and `token`, and a source the adapter
  refuses is `TrackerError` with the adapter's words. The operator's
  `--linear-api-url` and `--jira-api-url` are the `linear.Client`'s
  `APIURL` and the `jira.Client`'s `Address`; `TrackerSource.Address` stays
  empty for a source, since no entry names one.
- **One loop reads both kinds.** A repository and a source are each a
  `requestOrigin` in the controller: what was read, the ETag, the answers
  said and the shipped checks are kept in memory by an ID, `owner/name` or
  `source:<name>`, which never equal each other. `model.RequestsRead` gains
  `Source`; a source's listed item is a request whoever made it, with or
  without an author (the label is the door, #160), and a GitHub issue is
  one by `IsRequest`, as before. A source's mode defaults to `Ask` when the
  API server didn't fill it.
- **The markers go last on a tracker.** `model.MarkersLast` moves the stage
  marker a comment starts with to its end, after the round's or the
  release's marker, for a source's item: Linear's and Jira's comments show
  `<!-- -->` as text (decision 17). A GitHub comment is as before.
- **Each issue's link, and each merged pull request.** The planned comment
  names each issue by its title and its page, as `status.issues` recorded
  it, for every source, GitHub included. The released comment adds
  `Merged in <pull request> for <issue>` for each issue whose Task merged
  its work (`mergedItsIssue`, the rule a release waits on), read from the
  namespace's Tasks when the comment is said; an issue no Task merged, one
  closed by hand, is named in no such line. It is read when said, not kept
  in status: a Task deleted before then leaves its line out.
- **The link back is `Requested in <name>: <page>`,** one line per request
  an answer `take`s the issue for, after the `Uses` lines and before the
  marker, the request's name and page with no `<!--` in them. A pinned
  `uses:` issue's body, edited when its packages publish, keeps it.
- **`brief.md` counts a round's requests by kind:** those from GitHub
  issues, from a user the person lets ask, and those from a request
  source, by `source:key` alone. The display name is in
  `model/requests.yaml` and nowhere else; a spec holds that the
  description's words reach no other workspace file.
- **`CannotClose`** is a port reason, the comment comes before the close in
  every stage, and a request whose close failed stays in
  `status.requests`, so a later read finds its comment by the marker, posts
  none again and tries the close again. A workflow that has no way to close
  an item won't have one a second later, so the refusal is kept in memory
  (`requestsHeld`) and the close, with the fresh read of the source an owed
  answer forces, waits for the suggestions recheck rather than coming on
  every reconcile; the `Requests` condition says `CannotClose` all along
  (review of #161, `TestACloseTheWorkflowRefusesWaitsForTheRecheck`). A
  held close goes when its request leaves `status.requests`, its label
  taken off say, so a request that comes back is closed at once (#245,
  `TestAHeldCloseGoesWithItsRequest`). The
  fake tracker gained `CannotClose(project, why)`, and `Unlabel` and
  `Label`, an open item no list shows, for the specs.
- **An item the Brief closed is no new request, whatever a lagging search
  says** (review of #161). A source's list is a search, and Jira's index
  can lag behind a change for minutes and sometimes longer (step 6 as
  built, Jira): the item a round skipped, or the Brief declined or released
  and closed, is listed again on the next fresh search. It was read as a
  new request, `Accepted` in `On`, or in `Ask` while its number was still
  in `accepted` (which nobody can have pruned inside the window, since
  `status.requestSourcesSettled` can't list a number the search still
  returns). A second round then planned it, posted a second comment (its
  round marker differs, so the dedupe missed it) and could file its issues
  twice. The fix rests on the item itself, not on the search: a tracker
  whose list is a search is a `ports.IssueReader`, whose `IssueOpen(number)`
  reads the one item, which no index lags (Jira's `GET /issue/{key}`,
  Linear's `issue(id:)`), and says whether the list should show it: open
  and carrying the label. Before an item of a source becomes a request, one
  no request is kept for in `status.requests`, the Brief reads it so, and
  leaves it out unless it is open. That holds after an operator restart and
  through a lag of any length, since the truth is the item's state, which
  the Brief's close set. In memory, as a cheap first filter, each read
  keeps what the Brief closed (`forgetIssue`) and what `IssueOpen` said,
  with when, until the list no longer shows the item: within a suggestions
  recheck the item is not read again, so a lagging item costs one read per
  recheck. A read that fails is kept so too (#245): it leaves the item out
  until the recheck, and names the `Requests` condition on each pass until
  then; one the tracker refuses as unavailable (`TrackerUnavailable`: a
  rate limit, an outage) ends the pass's reads of items, and those not read
  yet wait for the recheck with it. Both adapters and the fake tracker are
  `IssueReader`s; the fake gained `Lag(project, on)`, a list that goes on
  showing closed issues, and `jiratest` the same for its search. A
  repository's GitHub list doesn't lag that way and its adapter is no
  `IssueReader`: there the in-memory filter alone applies. A capability,
  not a fifth `IssueTracker` method, so the GitHub adapter and its contract
  suite are unchanged. A durable mark on the Brief (a list of closed
  numbers in status) was the other way; it would grow with every request
  and need its own pruning, where the item already holds the answer.
- **The condition names a source as `request source <name>`,** in
  `Reading the issues of …` and before a failure's message.

Done since:

- **An envtest of `Merged in <PR> for <issue>`** (review of #161, a
  nit; #245). Beside `TestRequestComments` and `TestMergedPulls`,
  the spec "names the pull request a Task merged for each issue of a
  request, and none for an issue closed by hand" sees the line end to end.
  A merged Task is read by its Crew's release logic too, so the helper
  `mergedFiledTask` keeps the two apart: it makes the Task only on a Crew
  that neither tags nor prepares releases (a plan with no releases file),
  and checks that first; the Task's Agent doesn't exist and it records no
  merge commit.

Left as it is, and why:

- **What the lag still leaves.** A person who reopens, or relabels, an
  item the Brief closed less than a suggestions recheck ago waits up to that
  recheck before it is a request again: the in-memory filter skips it
  unread until then (after a restart, it is read at once). On a repository,
  whose GitHub adapter is no `IssueReader`, a closed issue a lagging list
  still showed after a restart, or after the recheck, would be read as a
  request as before; GitHub's issue list has not been seen to lag so. The
  envtest specs of the lag run on the fake tracker; how long a real Jira
  site lags is still unread, and no longer matters to the outcome.
- **Whether Linear keeps a `<!-- -->` marker in a comment's body,** and
  whether Jira's document format does, is still unread (step 6 as built,
  Linear): the markers are the comment's last lines there, shown, and the
  adapters find them as text. A workspace that strips them would see the
  Brief answer twice; the guide (step 13) says so.
- **No chart value** sets the two flags; that is step 13's, as is the
  guide's page for each tracker. The UI (step 12) still reads a source's
  request as `undefined #412`: steps 9, 10 and 12 ship in one release.

## How we will know #61 is done

The issue's done-when, by step:

| The issue says | Proven by |
| --- | --- |
| A plan in `docs/plans/`; steps as issues | This file; the thirteen rows above, filed as fourteen issues (step 6 is two) |
| The Slack adapter passes the ChatHost suite against a fake Slack API in `make test` | Step 5 |
| The Linear and Jira adapters pass the IssueTracker suite the same way | Step 6 |
| The Chat and Brief CRDs take the new values, with their CEL rules and failing cases in envtest | Steps 7 and 9, and steps 8 and 10 for what the operator does with them |
| The chart's README and the guide have a page for each, with the scopes each token needs | Step 13 |

ChatHost then has a fake and two adapters that pass its suite, which is when
docs/design.md and `docs/architecture.md` call a port swappable: step 5
changes the first's line, step 6 adds request intake to it and step 13
changes the second's. No step needs a cluster. What only a real workspace
would show (Slack's rate limits, a workflow a Jira admin wrote, a route to
Slack through a proxy) is not tested before release, as node behaviour is
not.
