# Plan: a Brief takes requests from its releases repository's GitHub issues

GitHub issue #796, reported by gray-lenovo on 9 October 2026; the
maintainer's constraints are on the issue. Agent: otter-796.

## Goal

A Brief whose app publishes its releases to a GitHub repository
(`spec.releases`) can take requests from that repository's issues by itself.
An issue opened by a person the Brief allows becomes input to a planning
round, like a prompt, at once or once a person accepts it. The factory then
comments on the issue: what the request became, which release it is in, and
when that release is out, and closes it then. It is off unless turned on,
per entry of `spec.releases`.

Done when the steps below are merged, and design.md, *Briefs*, says what
they built.

## Decisions

1. **On per `spec.releases` entry, off by default.** Requests come from the
   repository an entry names, so they are turned on there:

   ```yaml
   releases:
   - container: cel-lens
     github:
       repository: gray-lenovo/cel-lens
       tokenSecretRef: {name: cel-lens-github}
     requests:
       mode: Ask            # Off (the default), Ask or On
       authors: [gray-lenovo, a-colleague]
       teams: [gray-org/maintainers]
       accepted: [12]       # Ask: issue numbers a person let through
       declined: [13]       # Ask: issue numbers a person turned down
   ```

   CEL: `authors` or `teams` must be non-empty unless `mode` is `Off`; at most
   one entry per `github.repository` may have a `mode` other than `Off` (two
   Containers can publish to one repository with a `tagPrefix`, and an issue
   must be read once); a number can't be in both `accepted` and `declined`.
   Logins match GitHub's rule (`^[A-Za-z0-9](-?[A-Za-z0-9])*$`, at most 39),
   teams are `org/slug`. Removing `requests`, or `mode: Off`, stops reading
   issues; what was already planned stays planned, and nothing more is said
   on GitHub.
2. **Only allowed authors, and nothing else from a stranger.** The operator
   reads open issues (not pull requests) of the repository and keeps those
   whose author is in `authors`, or a member of one of `teams`. Every other
   issue is left alone: no comment, no label, nothing in status, and its text
   never reaches the Brief's repository or an agent. Comments on an issue are
   never read; a follow-up is a new issue. Why not "anyone with write
   access": the owner names the people, and a collaborator added later for
   another reason doesn't start spending on agents.
3. **No new credential.** The entry's existing token Secret is used. The
   fine-grained token it holds needs *Issues: read and write* beside
   *Contents: read and write*, and *Organization → Members: read* only when
   `teams` is set. A token without them makes the condition `Requests`
   False, reason `TokenLacksIssues` (or `TokenLacksMembers`), with GitHub's
   message, and a Warning Event once per reason; releases still publish.
   getting-started.md says which permissions to tick.
4. **Read every ten minutes, cheaply.** The Brief reads each such
   repository on the suggestions recheck (ten minutes) and when the entry
   changes, with `If-None-Match` on the issue list so an unchanged list costs
   no rate limit. A rate limit or an unreachable GitHub is retried on the next
   recheck and shows on the condition, reason `GitHubUnavailable`; it never
   fails a round.
5. **The request's text is taken once.** When a request is let through (On:
   when first seen; Ask: when its number is in `accepted`), the operator
   keeps its title, body (cut at 4000 characters, on a whole sentence),
   author and URL in `status.requests`. A later edit of the issue changes
   nothing that was planned. A request closed on GitHub before a round takes
   it is dropped.
6. **Ask waits in *Needs you*.** In `Ask`, a new request is one
   `status.needsYou` item, kind `Request`, name `owner/name#N`, with its title
   and author, and no text of its body. It is answered by adding its number to
   `accepted` or `declined`, not by a prompt, so a stranger's words can't reach
   the planner before a person said yes, and so the CRD holds the answer. The
   UI's Brief page gets *Accept* and *Decline* buttons (UI actions already
   patch `/spec/releases`). `needsYou` lists requests whatever
   `spec.autopilot` says: autopilot is about failed issues, this is a door.
7. **An accepted request is a prompt, not a line in `spec.prompts`.** The
   operator doesn't write the person's spec: GitOps would undo it, and
   `spec.prompts` holds at most 50. Instead a round starts on new prompts
   *or* new accepted requests. The workspace gets `model/requests.yaml` (each
   request the round takes: repository, number, author, title, body), and
   `brief.md` says each is a request from that GitHub user, to plan like a
   prompt. `status.roundRequests` says which the running round took, as
   `roundPrompts` does for prompts. A paused Brief starts no round for them.
8. **The planner answers each request.** `plan/requests.yaml` has an entry
   per request the round took: `take` with `issues:` (the round's issue
   numbers, as in `after:`), or `skip` with `why:` (already there, out of
   scope, unclear: the words go on GitHub). A round that leaves a request
   unanswered, answers one it didn't take, or `take`s with no issue of the
   round, is refused like any bad plan, and the planner's next try gets the
   reasons. The rules live in `internal/model`. The stand-in planner files an
   issue per request, so the loop is tested without a model.
9. **The operator speaks on the issue, once per stage, with a marker.**
   Every comment carries `<!-- c4factory:request brief=NAME stage=S -->`, and
   the operator looks for its own before posting, so a retry never posts
   twice. Its own is one written by the token's account (`GET /user`, read
   once per read of the repository): anyone can copy a marker into a
   comment, and a stranger's copy is ignored (#816). The stages:
   - `planned`: applying the round, "Planned as: <issue titles>, in release
     vX.Y.Z" (the milestone the round filed them in), or for `skip` the why,
     then the issue is closed as *not planned*.
   - `declined`: a number in `declined`, "Not taken by the app's owners", and
     closed as *not planned*.
   - `released`: once every issue the request became is closed and the
     releases that hold them are published (`status.apps[].lastRelease`
     says `Published`), "Shipped in vX.Y.Z: <published URL>", and the issue
     is closed as *completed*. A request whose issues landed in a Container
     that publishes nowhere says "Done in <issue URL>" when they close.
   An issue a person closed on GitHub in the meantime gets no more comments.
   The texts are built in `internal/model`, plain sentences, no agent's
   reply.
10. **Status remembers only what is open.** `status.requests` lists the
    requests in flight, oldest first, at most 50 (`requestsWaiting` counts
    past that), each with its stage, round, the issues it became and its
    release. A request leaves the list once it is closed on GitHub, so the
    list never grows with the repository's history. A reopened issue is a new
    request; a number in `declined` stays declined.

## Steps

Each step is one pull request that leaves main working, with `make check`
passing. No step needs a cluster: evidence is the `internal/model` table
tests, a fake GitHub (`internal/controller/fakegithub_test.go`, `httptest`
for `internal/gitprovider`), envtest and Vitest/Playwright against the fake
API.

1. **The API and the rules, read by nothing yet** (#798). Decisions 1, 5, 9 and 10
   as types and pure functions: `BriefRelease.requests`, `status.requests`,
   `status.roundRequests`, `needsYou` kind `Request`, the CEL rules with a
   failing envtest case each, and in `internal/model` which issues are
   requests, the stage a request is in, and the comment texts, as table
   tests. Done when `make check` passes and nothing reads the new fields.
2. **A GitHub issues client** (#799), in `internal/gitprovider`. List open issues
   with an ETag, read team membership, find a marked comment, comment, close
   with a reason, and the permission errors of decision 3 as reasons. Done
   when `httptest` tests cover each call, a 304, a rate limit and a 403 for
   each missing permission.
3. **The Brief reads requests** (#800, depends on #798 and #799). Decisions 2 to 6:
   poll, filter by author and team, keep `status.requests`, list Ask's in
   `needsYou`, the `Requests` condition and its Events, `declined` commented
   and closed. No round takes a request yet. Done when envtest against the
   fake GitHub covers On and Ask, a stranger's issue left alone, a team
   member let through, a missing permission, an unreachable GitHub and a
   decline commented once.
4. **A round takes requests** (#801, depends on #800). Decisions 7 and 8 and the
   `planned` stage: rounds start on accepted requests, `model/requests.yaml`,
   `brief.md`, `plan/requests.yaml` checked and applied, the stand-in
   planner's issue per request, the comment and the close for `skip`.
   design.md, *Briefs*, gets the bullet. Done when envtest takes a request
   from an issue to a filed Gitea issue and a `planned` comment, a skip to a
   closed issue, and a bad `plan/requests.yaml` to a refused plan.
5. **The issue hears when it ships** (#802, depends on #801). Decision 9's
   `released` stage. Done when envtest takes a planned request through a
   faked tag and publish to a `released` comment and a close, once, and a
   request in a Container that publishes nowhere to "Done in".
6. **The UI shows requests** (#803, depends on #800). The Brief page lists
   `status.requests` and the `Requests` condition, Ask's items get *Accept*
   and *Decline* (patches of `/spec/releases`), and the create form and
   settings can set `requests`. getting-started.md says how to turn it on
   and which token permissions to tick. Done when Vitest and Playwright
   against the fake API cover the list, both buttons and a refusal.

## What step 3 settled

Choices the decisions left open, made in #800:

- **What was read lives in memory.** The ETag, the open issues and the team
  answers of each repository are kept by the operator, not in status; a
  restart reads each repository once, in full. Spec changes to `mode`,
  `authors`, `teams` or the token Secret read again at once; `accepted` and
  `declined` only re-sort what was read.
- **Team answers last while the list is the same.** An author found in or
  out of `teams` keeps that answer until the issue list changes (a 200, not
  a 304); one whose membership couldn't be read is asked again on each read,
  and a request already kept for that author stays meanwhile.
- **A request no round took follows its author.** If its author leaves
  `authors` and `teams`, it leaves `status.requests` with no word on GitHub;
  once a round took it, it stays until it closes.
- **A missing Secret is a reason too.** The condition `Requests` says
  `SecretMissing` or `SecretUnreadable`, as publishing does, besides the
  reasons of decision 3 and `Unauthorized`, `RepositoryNotFound`,
  `IssuesDisabled` and `GitHubError` from the client. The Warning Event's
  note starts with the reason.
- **The text is taken when a request enters `status.requests`.** Past the
  50 it holds, a request is only counted in `requestsWaiting`; its text is
  taken, as it reads then, when one ahead of it leaves, and an Ask request
  shows in `needsYou` from then on.
- **A decline reads the list again first,** with the ETag, so a request its
  author closed since the last read is dropped, not commented on.
- **A declined number names only a request.** A number in `declined` whose
  issue isn't an allowed author's open issue is ignored: a stranger's issue
  is never commented on, even by number.

## What step 4 settled

Choices the decisions left open, made in #801:

- **An answer names its request.** `plan/requests.yaml` is a list of
  `{request: owner/name#N, answer: take|skip, issues: [n...], why}`;
  `model/requests.yaml` gives each request that name. A why is at most 500
  bytes, as for suggestions, and is posted as written.
- **A round that doesn't apply hands its requests back.** A round that fails
  or asks for input puts its requests back to `Accepted`; the next round
  takes them, but they don't start it by themselves, as that round's prompts
  don't, so a failing round doesn't start again and again.
- **A request closed during its round needn't be answered.** The plan must
  answer the round's requests still open; it may answer one closed since.
- **The workspace is written again when a round takes requests,** so a
  request let through in the same reconcile is in `model/requests.yaml`
  before the planner starts.
- **The planned comment names its round.** It ends with a second marker,
  `<!-- c4factory:request brief=NAME round=N -->`, and the Brief looks for
  that one, so a request reopened on GitHub and answered by a later round
  hears that round's answer too. A reopened request that an applied round
  took counts as new and starts a round.
- **The planned comment is remembered in memory.** Once the Brief posted it,
  or found its marker, it doesn't look again; after a restart it looks once
  per planned request. A request owed a comment is read again first, with the
  ETag, as a decline is. The release in the comment is the title of the
  round's release milestone of the first of its issues' Containers that has
  one.
- **The stand-in takes every request it has room for,** one issue each after
  the prompts', and skips the rest, or all of them when there is no Container.
- **A tending round takes no request.** A request let through waits for, and
  starts, a prompt round, as a waiting prompt does.

## What step 5 settled

Choices the decisions left open, made in #802:

- **A later release holds an earlier one's work.** A closed issue in a
  Container that publishes is shipped once its Crew's latest release is
  published and is the issue's milestone or a later version; the comment
  names the release that is out. A Crew tags its next release whether or
  not the last one published, so waiting for the milestone's own release
  could wait forever. The Brief also remembers, in memory, the newest
  release of each Crew it saw published, so a later one whose publishing
  failed doesn't hide it.
- **A deleted issue or Container is done.** An issue Gitea no longer has,
  or one whose Container is gone, counts as done, with the page the Brief
  recorded when it filed it.
- **Closed outside a release counts as done.** A closed issue with no
  milestone, or in a Container that publishes nowhere, says "Done in" its
  page. A request split over Containers waits for all of its issues and says
  both, "Shipped in" then "Done in", in one comment.
- **Where it shipped is kept in status.** `status.requests[].shipped` lists
  the releases and pages once the stage is `Released`, so a comment GitHub
  refused is posted after a restart with the same words.
- **The Brief looks at a request's issues every ten minutes,** and at once
  when the latest release of one of their Crews changes, which reconciles the
  Brief. A request waits for its planned comment first, so the two come in
  order; the released comment ends with
  `<!-- c4factory:request brief=NAME released=N -->`, N the round that
  planned it, which the Brief looks for before posting.

## What step 6 settled

Choices made in #803:

- **"The create form" is the form that adds a releases entry.** A new Brief
  has made no Container yet, so the *Describe an app* form has no entry to
  hang requests on; the settings' *Publish an app's releases* form, which
  creates an entry, gets the requests fields, and each entry gets a folded
  *Requests of NAME* form to change them later.
- **Accept and Decline are settings, not prompts.** They send the whole
  `spec.releases` behind a test of the resourceVersion, like every releases
  write, so they need UI actions on; with actions off the item says to add
  the number with kubectl. Once `accepted` or `declined` holds the number,
  the item says so until the operator moves the request on.
- **The forms keep the answered numbers.** Changing an entry's mode, authors
  or teams keeps its `accepted` and `declined`; `Off` with nothing else to
  keep removes `requests`. Values go as typed: the CRD judges them, and a
  refusal shows the API server's message.

## What a human run of Kind would check

That a real fine-grained token with the permissions of decision 3 reads,
comments on and closes issues of a real repository, and that a request goes
all the way to a published release on GitHub. No agent runs it.
