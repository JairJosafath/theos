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
  neither required nor defaulted (`k8s.io/apiextensions-apiserver` v0.35.0,
  `pkg/apis/apiextensions/validation/validation.go:1407`).

What Slack, Linear and Jira do is written below as their public
documentation describes it. Socket Mode's token, acknowledgement and limit
of ten connections were read on Slack's pages on 2026-11-13; the scopes per
method and Linear's and Jira's calls are from memory of their references.
Each adapter's step checks them against the reference before it builds, and
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
8. **`Sync` keeps its contract on a socket.** The adapter acknowledges each
   envelope, drops one whose `event_id` it has seen, and buffers people's
   messages; `Sync(since, wait)` returns the buffer after `since`, a token
   that is the newest message time it returned. What was said while no
   socket was open is read through the Web API when one opens, from
   `since`: each channel's history, and the replies of each thread among a
   channel's last 200 messages. A reply to an older thread said during an
   outage is not read; the guide says so, and the person says it again.
   Without this a restart would lose commands, which Matrix never does.
9. **The tokens, and their scopes.** Two, in the Secret
   `credentialsSecretRef` already names, which the user creates:
   `botToken` (`xoxb-…`) with `groups:write` (make a private channel, set
   its topic, invite), `groups:read` (find the bot's channels),
   `groups:history` (the `message.groups` event and the catch-up read),
   `chat:write` and `reactions:write`; and `appToken` (`xapp-…`) with
   `connections:write`. The operator reads `botToken` alone; `theos-chat`
   reads both, as it reads the Matrix token today
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

12. **A Brief gets `spec.requests`, a list of sources,** each with a `name`
    and exactly one of `linear: {team, label}` and
    `jira: {site, project, label}`. Not under `spec.releases[].requests`: a
    tracker is not where a Container's releases go, and that entry needs a
    GitHub repository a Linear or Jira team may not have. A request is the
    Brief's anyway: the planner decides which Containers it touches.

    ```yaml
    requests:
    - name: product
      linear: {team: ENG, label: factory}
      credentialsSecretRef: {name: product-linear}
      mode: Ask                 # Off, Ask (the default) or On
      accepted: [412]           # ENG-412
      declined: [415]
    - name: support
      jira: {site: https://acme.atlassian.net, project: SHOP, label: factory}
      credentialsSecretRef: {name: acme-jira}
    ```

    CEL: one of `linear` and `jira`; `site` is https; no two entries with
    one team, or one site and project (an item is read once); a number not
    in both `accepted` and `declined`. At most ten entries, keyed by `name`.
13. **The label is the door, and `Ask` the default.** An open item of the
    team or project that carries the label is a request. `authors` and
    `teams` are not offered: their patterns are GitHub's and can't change,
    a Jira account ID doesn't fit them, and who may label an item is a rule
    the team's tracker already keeps, where anyone may file an issue on a
    public GitHub repository. `Ask` is the default because an entry exists
    to take requests, and a person at the door costs one click.
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
    `author` grows from 39 to 100 characters for display names. Its name in
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
17. **The factory closes the item,** as it closes a GitHub issue: a request
    leaves `status.requests` when its item is closed (brief-requests,
    decision 10), and an item left open would be listed for ever. Linear:
    the team's first state of type `completed`, or `canceled` for not
    planned. Jira: the first transition the item offers into a status of
    category Done, with the resolution `Done` or `Won't Do` where the
    transition takes one. A workflow that offers none is a new reason,
    `CannotClose`, on the Brief's `Requests` condition; the comment is still
    posted. The marker is the comment's last line and shows where the
    tracker has no hidden text.
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
    it. Jira: `email` and `token`, an API token of such an account, which
    may browse the project, comment and transition its items (the scopes
    `read:jira-work` and `write:jira-work` on a scoped token). The chart
    renders none of them, and no test holds a literal: a fixture's token is
    `rand.Text()` (the `secrets` skill), never a string shaped like a Slack
    or Atlassian token, which the scanner on every pull request would flag.
    Linear's address is the operator's `--linear-api-url`, by default
    `https://api.linear.app/graphql`.

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
    are disabled" (nothing turns them off). Everything else runs.
23. **Teams is left for later,** a third ChatHost only if someone asks, as
    the issue says. Nothing here is built for it but what makes it small:
    a `host` enum, a per-host row and a port that names no product.

## What the suites' reviews found, each decided

| Finding | Verified at | Decision |
| --- | --- | --- |
| The intake port's types and reasons name GitHub | `internal/ports/intake.go:26-43`, `:69-91`, `:106-117`; used in `internal/controller/brief_requests.go:70`, `:160-203` | Step 1. `TrackerIssue`, `TrackerComment`, and the port's own reasons. Three values change with them, since one `Requests` condition covers every source: `GitHubUnavailable` to `TrackerUnavailable`, `GitHubError` to `TrackerError`, `RepositoryNotFound` to `SourceNotFound`. A reason is not in the schema, so Compatibility allows it; the UI (`ui/src/model/requests.test.ts`) and the guide's table (`docs/getting-started.md:942`) read them and change in the same pull request, with a *Changed* line for the release notes. The release target keeps its own (`deployer.go:213-223`). A test beside `hostneutral_test.go` fails when `intake.go`, `chathost.go` or their suites name a product. |
| `int` issue numbers against string keys | `intake.go:71`, `:131-138`; `brief_types.go:187-195`, `:453` | Keep `int` (decision 14); add `Key`. Step 1. |
| A comment's ID is an `int64` | `intake.go:85`; nothing in `internal/controller/brief_request*.go` reads it | Step 1: a string. Linear's is a UUID. |
| `TeamMember` is GitHub's teams | `intake.go:126`; `internal/controller/brief_requests.go:297` | Step 1: an optional capability, `ports.TeamMembers`, with its three subtests in a suite of their own (docs/design.md, "What a host lacks"). Linear and Jira don't have it (decision 13). |
| The ChatHost port names Matrix | `internal/ports/chathost.go:25-31`, `:61-65`, `:95-110`, `:134-135`; `internal/model/chat_word.go:167-177` | Step 2. `ChatConfig{Address, Token, AppToken}`, still comparable since it is a map key (`internal/chatbot/bot.go:75`); `ChatMessage.Formatted`; `model.ChatPerson.ID`; comments and the error's text. |
| No ChatHost fake in `internal/ports/fake` | `internal/ports/fake/contracts_test.go:37-84` | Step 3: do it, and an IssueTracker fake with it. The ChatHost fake takes its shape as options (no Spaces, one name per channel) and runs the suite in both, which commits the Slack-shaped host #131 tried and did not commit. The Slack specs of the Chat controller and `theos-chat` run on it; the Matrix specs stay on `matrixtest`. |
| Does `internal/chatbot` assume event IDs are unique? | `internal/chatbot/state.go:71-81` matches an event with its room; `commands.go:99-105` answers with the event's room; `bot.go:219`, `:240`, `:259`, `:268` keep and use the room beside each ID; `commands.go:63-71` dedupes nothing and relies on `Sync`'s "each once" | No change: every use pairs an ID with its room, which is what Slack gives (a `ts` is unique in its channel, and an edit carries the edited message's own, which `Amend` in the suite already allows). Step 3 pins it: a spec on the fake where two rooms hold a message of one ID. Deliveries sent twice are the adapter's to drop (decision 8). |
| Three fake GitHubs for intake | `internal/controller/fakegithub_issues_test.go:33-58`; `internal/adapters/github/github_issues_test.go:40-80`; `internal/adapters/github/githubtest/issues.go:57-70` | Two become one, the third stays. Step 4 moves the Brief's request specs to the port's fake tracker and deletes the controller's fake; the client's own stays, since it holds GitHub's paging, ETag and status codes. `githubtest` models another slice (labels, milestones, dependencies, no author, no ETag) for GitHub as a Git host, and #57's unmerged steps edit it: folding the client's fake into it is left to [github-host.md](github-host.md), not done here. |

## Steps

Each row is one pull request (step 6 is two) that leaves main working with
`make check` passing, and can be filed as an issue as it stands. The tiers
are AGENTS.md's: 1 is `make test` without a cluster, an adapter on its fake
included; 2 is `make validate-yaml`; 4 is envtest. Steps 1 to 4 change no
behaviour a user sees but the three reason values. Steps 7 and 8, and 9 and
10, ship in one release: the first of each pair shows a field nothing reads
(ROADMAP.md, "A release never ships a half-built feature").

| Step | What | Done when | Tier | Change to docs/design.md |
| --- | --- | --- | --- | --- |
| 1. The intake port names no product | Rename the types and reasons of `internal/ports/intake.go` as the table above says; `Key` on an issue, a string ID on a comment; `TeamMember` becomes the optional `ports.TeamMembers` with its own suite; the guard test. No CRD change. | The GitHub client passes both suites; the UI and `docs/getting-started.md` show the three new reason values; the guard fails on a product's name in the port. | 1 (`make test`), the UI's check | "Factory components": the Issues row says request intake is a port of its own, with any tracker behind it. |
| 2. The chat port names no product | `ChatConfig{Address, Token, AppToken}`, `ChatMessage.Formatted`, `model.ChatPerson.ID`, the per-host row in `internal/model` with Matrix alone in it, the controller and `theos-chat` taking a map from host to `ports.ChatHosts`. No CRD change. | The Matrix adapter passes `contract.ChatHost` unchanged; the Chat's and `theos-chat`'s envtest pass unchanged; the guard of step 1 covers `chathost.go`. | 1 and 4 | None. |
| 3. Fakes of the two ports | `fake.ChatHost` (shaped by options) and `fake.IssueTracker` in `internal/ports/fake`, each run by its suite in `contracts_test.go`, the ChatHost twice. A `theos-chat` spec with one message ID in two rooms. | Both suites pass on the fakes with `-race -shuffle=on`; the Slack shape skips only the two subtests of decision 22. | 1 and 4 | "A port is a narrow Go interface with one fake": ChatHost and request intake now have theirs. |
| 4. A Brief reads any tracker through one seam | `IssueTrackers` builds a tracker for a source, not `GitHubIssues`; the Brief's request specs run on `fake.IssueTracker`; `internal/controller/fakegithub_issues_test.go` goes. | The request specs pass on the fake with the same assertions; a spec that asserted GitHub's HTTP is in the client's tests or was already there. | 4 | None. |
| 5. The Slack adapter | `internal/adapters/slack` and `slacktest`: decisions 4 to 9, with `ScopeMissing` on the port. Adds `slack`, `linear` and `jira` to `adapters-stay-out-of-kubernetes` in `.golangci.yml` (`:65-72`, which lists each adapter), a protected path: an owner's session. Wired to nothing. | `contract.ChatHost` passes on `slacktest` with the two skips; the adapter's tests hold the catch-up read, a delivery sent twice, the socket opened by `Sync` alone, a taken name and a missing scope. | 1 | None yet. |
| 6. The Linear and Jira adapters | `internal/adapters/linear` with `lineartest`, then `internal/adapters/jira` with `jiratest`, one pull request each: decisions 14 and 17 to 19. Wired to nothing. | `contract.IssueTracker` passes on each fake with the two skips; each adapter's tests hold its ETag, its way to close, a moved item, and for Jira the flattened description and `CannotClose`. | 1 | None yet. |
| 7. The Chat API takes Slack | `spec.host`, `spec.slack.people`, the CEL of decision 3, the admission policy over `spec.slack.people`, the sample and `test/invalid/` files. Read by nothing. | envtest refuses each failing case (`slack` without `host: slack`; `homeserver`, `space`, `people` or a renamed room with it; a prefix Slack won't take; `host` changed; a Slack person naming someone else); a Chat stored before the field still validates; `test/compat` passes. | 2 and 4 | "Chat": the heading loses "in Matrix"; `host`, the `slack` block and the policy's second list. |
| 8. The operator and `theos-chat` run a Slack Chat | The Slack row in `internal/model` (no Space, the two keys, mrkdwn), the controller and `theos-chat` on it, `--chat-slack-api-url`, `ScopeMissing`; `cmd` wires the adapter. | envtest on the Slack-shaped fake: six channels and no Space, people invited, a thread opened, edited and marked done, `approve` sent as the person, a stranger refused, a missing key and a missing scope on `Ready`. | 1 and 4 | "Chat": a room is a private channel, no Space, Socket Mode and what a restart reads again; the ports table's ChatHost row names Slack. |
| 9. The Brief API takes `spec.requests` | The list and its CEL (decisions 12 and 13), the status fields of decision 15, the model's request identity and names, the sample and `test/invalid/` files. Read by nothing. | envtest refuses each failing case (both or neither of `linear` and `jira`, a `site` that isn't https, one team twice, a number accepted and declined); `internal/model` table tests name a source's request; `test/compat` passes. | 1, 2 and 4 | "Briefs": requests come from a releases repository's GitHub issues or from a source in `spec.requests`. |
| 10. A Brief reads and answers a tracker's requests | The controller reads each source and answers as decision 16 says, the merged pull requests and the link back for every source; the planner's workspace names the source; `--linear-api-url`; `cmd` wires the two adapters. | envtest on the fake tracker: a labelled item waits in `needsYou`, is accepted, planned, answered and closed at each stage; a declined one; `On`; `CannotClose` and `SourceNotFound` on `Requests`; nothing is read with `mode: Off`. | 1 and 4 | "Briefs": what is written to the item and when; "A user's credential…" names the sources' Secrets; the Issues row names Linear and Jira. |
| 11. The UI shows both | The chat panel shows a Slack Chat's channels and people; a Brief's page lists a source's requests with *Accept* and *Decline*, which patch `/spec/requests`, one more write on the UI server's allowlist. | The UI's tests on the fixture with a Slack Chat and a Linear source; the server refuses a patch of `/spec/requests` that changes anything but `accepted` and `declined`. | the UI's check | "UI actions": the one more write. |
| 12. The guide and the chart | `docs/chat-slack.md`, `docs/requests-linear.md` and `docs/requests-jira.md`, each with how to make the app or the account, every token's scopes and what the factory writes; linked from `docs/getting-started.md` and the README. The chart's `chat.slack.apiURL` and Linear's address, and a section each in `dist/chart/README.md`: protected paths, an owner's session. `docs/architecture.md`'s two rows and its line on swappable ports; `docs/comparison.md` loses "not yet (#61)". This plan moves to `done/`. | `make validate-yaml` passes with every option on and renders no credential; the link check passes; each page's scopes are the ones the adapters' tests ask their fakes for. | 2 | A last read against what steps 7 to 11 wrote. |

## How we will know #61 is done

The issue's done-when, by step:

| The issue says | Proven by |
| --- | --- |
| A plan in `docs/plans/`; steps as issues | This file; the twelve rows above, filed |
| The Slack adapter passes the ChatHost suite against a fake Slack API in `make test` | Step 5 |
| The Linear and Jira adapters pass the IssueTracker suite the same way | Step 6 |
| The Chat and Brief CRDs take the new values, with their CEL rules and failing cases in envtest | Steps 7 and 9, and steps 8 and 10 for what the operator does with them |
| The chart's README and the guide have a page for each, with the scopes each token needs | Step 12 |

ChatHost then has a fake and two adapters that pass its suite, which is when
`docs/architecture.md` calls a port swappable; step 12 changes that line.
No step needs a cluster. What only a real workspace would show (Slack's
rate limits, a workflow a Jira admin wrote, a socket Slack closes to move
it) is not tested before release, as node behaviour is not.
