# theos UI

A view of a theos project, drawn live from the custom resources.
Milestone 6 in [the MVP plan](../docs/plans/done/mvp-v1.md): someone who has
never used kubectl can read the story in a browser. It reads everything and,
with actions off, changes only three things: it creates a Brief from an app
idea, adds a follow-up prompt to one ([docs/plans/done/brief.md](../docs/plans/done/brief.md)),
and pauses or resumes one ([docs/plans/done/upgrade-in-place.md](../docs/plans/done/upgrade-in-place.md)).
With actions on it takes a few more (_Actions_ below). design.md's _The UI:
areas and object pages_ says what it shows and why.

**The shell** ([docs/plans/done/ui-shell.md](../docs/plans/done/ui-shell.md)). A
sidebar holds the project switcher and the areas in groups: _Needs you_, with
a count of what waits; _Build_: Loop, Briefs, Story, Work; _Operate_: Factory;
and at the bottom About, Settings and the versions running. Its button folds it
to a rail of icons, remembered in this browser; below 768px it is a drawer
behind the top bar's menu button. The sticky top bar says where you are, shows a
_Demo data_ chip in demo mode (its popover says what that means and leaves
it), how fresh the page is, _Search or jump_ (⌘K or Ctrl+K, or / when not
typing: any area, project or object the page watches, by kind, name and
namespace), the area's primary action (_New brief_ in the areas of one
project, opening the _Describe an app_ form in a modal) and the theme: System,
Light or Dark, kept in `localStorage` and applied as `data-theme` on `<html>`
before the first paint. _Settings_ (a modal) also sets the density
(`data-density`) and how the sidebar opens. The dialogs, the drawer, tabs,
menus and the palette are one set of primitives in
[src/components/ui/](src/components/ui/).

Each area fits its summary on a wide screen, in a grid of two or three
columns that falls to one on a phone; what is more sits in a tab, a fold or
the drawer. An area's tab, and the stage the Loop shows, are the address's
`tab=`, changed without a history entry so Back still closes the drawer; an
object page's tab is `otab=`. What folds (a Needs you group, the Story's side
panel) is remembered in this browser.

The page opens on _Needs you_ when it has items, else on _Story_:

- **Needs you**, across all projects, in a group per project that folds
  (remembered in this browser), with a chip per kind (Brief, Container, Task,
  Crew) and its count to show only those, the items two abreast on a wide
  screen: what waits on a
  person, read only from status ([src/model/needs.ts](src/model/needs.ts)): a
  Brief needing input or a decision, a Container that failed to scaffold or
  whose Standard update pull request waits, or one whose deploy waits for a
  person's approval, failed or runs degraded (with the `kubectl patch` line
  that approves the version waiting), a Task blocked or failed (until a
  newer Task or a round answers it), a Crew that left a release to a person or
  whose release run failed. Each item says why in the condition's own words,
  since when, and where it is done: the object's page, a link out, or a
  `kubectl` line to copy.
- **Loop** of one project: its software-development cycle drawn from the
  resources alone ([src/model/loop.ts](src/model/loop.ts)), read only. A ring
  of ten stages with a count each and, beside it, the items of the stage a
  node selects (`tab=`; it opens on the first review that waits on a person
  and has items, else the first stage with any), linked to their object
  pages or out to the Git host; on a phone, the ring, a chip per stage, then
  the stage: _Idea or bug_ (Briefs Pending or
  Planning), _Triage_, _Spec_ (Tasks by `spec.kind` and phase), _Reviewing the
  spec_ (a Spec Task `AwaitingApproval`; person), _Implement_, _Reviewing the
  code_, _Verify_ (Implement Tasks by phase), _Reviewing the product_ (deploys
  waiting for approval, the same logic as _Needs you_; person), _Ship_ (Crews
  whose release run is Waiting or Running, deploys on their way) and _Monitor_
  (degraded or failed deploys with the bug the monitor filed, and how many are
  healthy). The two stages that wait on a person are drawn with a person's
  icon and a dashed border.
- **Briefs** of one project, a grid of cards: a _Describe an app_ card whose _New brief_ opens the form that creates a Brief (an
  idea and a name, suggested from the idea, and optionally its autopilot mode
  and how many issues each of its crews works on at once), and a card per
  Brief with its name, phase, idea, what it waits for, its round and cost and
  its latest round in a line, which opens the Brief's page in the drawer.
  The page has its
  phase, what it waits for when it needs input, the planner's latest choices,
  the objects and issues it made, each app's releases (on or off, the
  latest tag linking to its Gitea release, and where it was published, linking to
  the GitHub release, and how many issues its crew works on at once), its prompts (those not yet planned marked
  _next round_), its repository and cost, a field to add a prompt, and a Pause or Resume
  button that sets `spec.paused`; while paused, the card shows its `Paused`
  condition: which Tasks still run, or that nothing does. The forms check none
  of the CRD's rules: submit is enabled for any non-empty input, and the API
  server's message shows as is when it refuses. Only shown in a namespace with a Project.
- **Story** of one project: its story in one compact row (Briefs, plan, model,
  repositories, Tasks and releases, each a link), then two diagrams, which
  take the width when the side panel is folded (remembered in this browser).
  **System context** (C4 level 1): the People and SoftwareSystems, the
  Relationships between them, each system's needs, KPIs and roll-up summary.
  **Containers** (C4 level 2) of one system: its Containers and DataStores inside
  a dashed boundary, with the People and other systems they are linked with
  around it. Each Container shows its phase, repository, the templates it was
  started from and who is working on it; hover for its checks (conditions) in
  plain words. A box of a kind with a page opens it; the side panels show
  the details of any other box, **who's working right now** (the Sessions per
  Container, agent or human, Active or Stale, with the age of the last
  heartbeat) and a legend.
- **Work**, across all projects: the headline numbers, then one line of what
  needs a look, such as a Task waiting for an unavailable agent or a Crew
  that can't pick up issues, which opens to the whole list; then tabs: _Board_
  (the Tasks by stage, each with its timeline), _Crews_ (each with its Agents
  and their models, the Task it works on and its ready and waiting issues),
  _Releases_, and _Time & tokens_ (where the time goes across Tasks, and what
  they used). Each Task and Crew opens its page.
- **Factory**, a dashboard: a card for each part of the factory, healthy or
  not, with restarts, CPU and memory; the node's usage; the last hour (API
  server response, CPU, memory) side by side; the agent slots and who waits
  for one.
- **About**: the versions running, and links to the running release's design
  and release notes, and to the repository.

**Object pages.** Brief, Container, Crew, Task, Project, SoftwareSystem,
Relationship, Session, Standard, Compute, Deployment, Bundle, Domain,
GitProvider, DeployTarget, Benchmark and TestEnvironment each have
a page, which opens in a drawer over the area it was opened from (Esc, × or
Back closes it; _Open full page_ adds `full=1` for the page at full width), with the same header
([src/components/ObjectHeader.tsx](src/components/ObjectHeader.tsx)): kind,
name and a breadcrumb to what it belongs to; the kind's lifecycle as a stepper
([src/model/lifecycle.ts](src/model/lifecycle.ts)), side exits such as Failed
or Blocked off the path; what a person does now, as an action the UI takes or
the exact next step (a link, or a `kubectl` line with a copy button). Its
sections follow as tabs, as the kind has them (`otab=`; another object opens
on its first): _Overview_ (the kind's own sections), _Status_ (conditions),
_Activity_ (timeline, rounds and runs) and _Related_ (children and links out).
The Task page adds its rounds,
checks, review and agent runs, and the test environments its run holds a lease
of, those its runs leased, and why its run waits for one; the Crew page its slots, Tasks, routes by label and last release;
the Container page, for a Container with a Standard, when CI runs its full
check (`spec.checks`), what its repository runs now and `ChecksApplied` when
False, with a choice that sets it when actions are on; and where it ships:
each `spec.deploy` entry's target, the version running and wanted, phase,
health and URL, and for a version waiting for approval the `kubectl patch`
line, or with actions on an Approve button. The DeployTarget page shows its
tool and where (the `github` repository or the `gitlab` project), tier, approval, `Ready` and the
Containers that ship to it, and of its credentials the Secret's name only; the
Project page lists its DeployTargets. The Project page also lists its
TestEnvironments ([docs/plans/test-environments.md](../docs/plans/test-environments.md)),
each with `Ready`, its live leases against `limits.maxLeases` and who holds
each, today's spend and how long a lease lasts; a TestEnvironment's page adds
what it has (image, devices, runtime class, placement, resources), its CI
pool and how a run uses it (`env/<name>`, `theos-env run <name> -- ...`).

**Versions.** The foot of the sidebar shows the UI's version (stamped by its
build, `dev` otherwise), the operator's and the chart's (from Project status,
`status.factory`), and the CRD API version, and warns when the UI and the
operator differ; _About_ repeats them, with what the page reads and the writes
it can make.

Everything updates as the resources change: the browser lists each kind, then
watches it. The area on screen and the object open over it watch their kinds;
the work of every project (Tasks, Crews, Containers, Sessions, Agents, Briefs)
is watched on every area, for the Needs you count and search.

## Run it

From the repository root, with the Kind cluster from `make cluster` running:

| Command           | What it does                                                                                                          |
| ----------------- | --------------------------------------------------------------------------------------------------------------------- |
| `make ui-dev`     | Vite dev server with live reload on <http://localhost:5173>, reading the cluster through a `kubectl proxy` it starts. |
| `make ui-serve`   | Builds `dist/` and serves it with `server.mjs` on <http://localhost:8080>, also through a `kubectl proxy`.            |
| `make ui`         | Builds the image `theos/ui:dev`, loads it into Kind, applies [config/ui](../config/ui) and waits for the rollout.     |
| `make ui-forward` | Forwards the in-cluster UI to <http://localhost:8080>.                                                                |
| `make ui-test`    | Typecheck, lint and all tests.                                                                                        |

Without make: `npm ci`, then `kubectl proxy --port 8001` in one terminal and
`npm run dev` in another.

**Demo mode:** add `?demo=1` to the address (for example
<http://localhost:5173/?demo=1>) to see a built-in version of the webshop story
with every state: containers Pending, Scaffolding, Ready and Failed, agent and
human Sessions, a Stale one, a Relationship added after scaffolding, and Crews
working on issues, paused and waiting for their Container; and Briefs Planned,
needing input and Planning. A Brief created or a prompt added there stays in the
page's memory, and one the CRD would refuse is refused there too, by the limits
in `src/demo/limits.ts` that a test checks against `config/crd/bases/`. It
needs no cluster and is clearly labelled as demo data.

**The address** says what is on screen, so any page can be bookmarked or
shared and Back works: `area=needs|briefs|story|work|factory|about`; an
object's page, `ns=<namespace>&kind=<Kind>&name=<name>`; and for Story and
Briefs `project=<namespace>`, `view=containers` and `system=<name>`.
`?demo=1&area=work` shows Work with every state. Links from before the areas
still work: `view=factory` opens Work (with `task=<namespace>/<name>`, that
Task's page), and `view=`, `system=` or `project=` alone open Story; the page
rewrites the address to the area's.

## Read-only but for three writes

The UI enforces nothing; the rules live in the CRDs and the operator. It reads
every theos kind and writes only three things: it creates a Brief, it
appends a prompt to one, and it pauses or resumes one. Three layers keep it
that way:

1. The browser sends GET requests to `/k8s/...` and `/k8s-watch`, plus the three
   writes ([src/k8s/briefs.ts](src/k8s/briefs.ts)).
2. [server.mjs](server.mjs) passes on only GET under `/apis/theos.factory.io/`,
   and these writes:
   - `POST .../namespaces/<ns>/briefs`, `Content-Type: application/json`: a
     Brief, only into a namespace that holds a Project. Only its `name` (or
     `generateName`) and `spec` go upstream: no labels, annotations or owners.
   - `PATCH .../namespaces/<ns>/briefs/<name>`, `Content-Type:
application/json-patch+json`: a patch whose one operation is `add` at
     `/spec/prompts/-`. When the Brief has no prompts yet, the server sends the
     equivalent `add /spec/prompts`, guarded by a `test` of the
     resourceVersion it read.
   - the same `PATCH`, whose one operation is `add` of `true` or `false` at
     `/spec/paused`: Pause and Resume.

   A write whose `Origin` names another host is refused. Everything else is
   answered with 405 (other methods), 403 (other paths or bodies) or 415
   (another content type). It drops the browser's own headers (cookies,
   `Authorization`, impersonation) and adds its own credentials. Whether a
   Brief or prompt is valid (lengths, names, append-only prompts) is the CRD's
   to say; the page shows its answer.

3. In the cluster, the ServiceAccount's reader ClusterRole allows only `get`,
   `list` and `watch` on the `theos.factory.io` resources (Briefs included), plus
   `get` and `list` on nodes and their `metrics.k8s.io` figures for the health
   summary below, cluster-wide. Pods and theirs it may read only where the
   factory runs: a RoleBinding in each component's namespace, and one the
   operator makes in each project namespace (`--ui-service-account`). A separate ClusterRole, `theos-ui-brief-writer`, grants only
   `create` and `patch` on `briefs`. No Secrets, no other writes.

### Actions: `ui.actions.enabled`

With the chart's `ui.actions.enabled` (off by default; design.md, _UI
actions_), the server's action table (`ACTIONS` in [server.mjs](server.mjs))
takes more writes, each one method, path, content type and body shape:

| Kind      | Request              | Fields                                                                                                                                                                                                                                                                                                     |
| --------- | -------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Brief     | `POST`, JSON         | a new Brief, as above                                                                                                                                                                                                                                                                                      |
| Brief     | `PATCH`, JSON patch  | `add` at `/spec/prompts/-` alone, as above; or `add` of any of `/spec/paused`, `/spec/autopilot`, `/spec/parallel`, `/spec/releases`; `/spec/releases` only after a `test` of `/metadata/resourceVersion`, and a Brief changed since that version is refused with `409 Conflict`                           |
| Crew      | `PATCH`, JSON patch  | `add` of any of `/spec/paused`, `/spec/parallel`, `/spec/tagReleases`, `/spec/prepareReleases`, `/spec/publishReleases` (or `remove` of it), `/spec/publishReleases` only after a `test` of `/metadata/resourceVersion`, and a Crew changed since that version is refused with `409 Conflict`              |
| Crew      | `PATCH`, merge patch | annotation `theos.factory.io/rerun-release` alone                                                                                                                                                                                                                                                          |
| Container | `PATCH`, merge patch | one annotation alone: `theos.factory.io/retry`, or the answer to its Standard's bump, `theos.factory.io/standard-take` or `theos.factory.io/standard-keep`                                                                                                                                                 |
| Container | `PATCH`, JSON patch  | `add` at `/spec/checks`                                                                                                                                                                                                                                                                                    |
| Container | `PATCH`, JSON patch  | the approval of a deploy: a `test` of `/spec/deploy/N/target` (a string), optionally a `test` of `/spec/deploy/N/approved` as it was, then its one write, `add` of a string at `/spec/deploy/N/approved`, the same N; the tests refuse a list reordered since the page read it, and an approval made since |
| Task      | `POST`, JSON         | `spec.kind: Implement` with `containerRef`, `agentRef`, `reviewerRef`, `goal`; only its name and spec go upstream                                                                                                                                                                                          |
| Standard  | `PATCH`, JSON patch  | `add` at `/spec/template/ref`                                                                                                                                                                                                                                                                              |

The server checks each body's shape and the JSON type of each value, not the
values: limits and enums are the CRD's, and the API server's refusal comes
back as-is. The chart sets `UI_ACTIONS` from the values:

- over port-forward (`ui.allowedHosts` empty), `service-account`: the UI's
  ServiceAccount makes the writes, bound to ClusterRole
  `theos-ui-actions` (`create` on briefs and tasks, `patch` on briefs,
  crews, containers and standards) in place of the brief writer;
- with `ui.auth.oidc`, `oidc`: each write is made as the person signed in
  (below), and the ServiceAccount has no write binding, only `impersonate`
  on users and groups; `ui.auth.oidc.actionGroups` binds
  `theos-ui-actions` to those groups, prefixed;
- with `ui.allowedHosts` and no `ui.auth.oidc`, the chart refuses to render.

`GET /ui/session` tells the page whether actions are on, the mode (`off`,
`service-account` or `oidc`), who is signed in and the writes the table takes;
never a token. With `oidc` it also names `/ui/login` and `/ui/logout`. `UI_ACTIONS` unset is off, as with `npm run dev`; set it to
`service-account` to try the actions against a `kubectl proxy`.

### Sign-in: `ui.auth.oidc`

With `UI_ACTIONS=oidc`, [oidc.mjs](oidc.mjs) signs a person in at the issuer
the chart names (`OIDC_*` from `ui.auth.oidc`), with only Node's standard
library:

- `GET /ui/login?return=/path` reads the issuer's discovery and sends the
  browser there: an authorization code with PKCE (`S256`), a `state` and a
  `nonce`. The sign-in in flight (`state`, `nonce`, PKCE verifier, the path
  to return to) lives only in its own short-lived `__Host-c4ui_login_<id>`
  cookie, named by its `state` and sealed (AES-256-GCM) with a key the process
  makes at start. The server keeps nothing for it, so no flood of sign-ins can
  push one out, and two tabs signing in at once each keep their own (a browser
  keeps its newest five, and the return path is printable ASCII under 512
  characters or `/`, so their cookies stay a few kilobytes); a restart ends the
  sign-ins in flight;
- `GET /ui/callback` exchanges the code (client secret by HTTP Basic), checks
  the ID token with `node:crypto` against the issuer's JWKS (RS256 or ES256;
  `iss`, `aud`, `azp`, `exp`, `nbf`, `nonce`; ES256 on P-256 only), and requires `email_verified`
  when the username claim is `email`. The person is then `usernamePrefix` +
  that claim and `groupsPrefix` + each group, never a `system:` name; the
  session holds only those and the token's expiry, in memory, behind
  `__Host-c4ui_session` (`HttpOnly`, `Secure`, `SameSite=Lax`). A restart, or the
  ID token's expiry, signs everyone out;
- `POST /ui/logout` ends the session.

Every write then carries `Impersonate-User` and `Impersonate-Group` (one
header per group) with the UI's own credentials; reads, the ones a write makes
first included, carry none. A write with no session answers 401, "Sign in to
act".

The page asks `GET /ui/session` once: with `oidc` its top bar shows a **Sign
in** link to `/ui/login` (returning to the page) while nobody is signed in,
else who it acts as and **Sign out**; a write refused with 401 shows the same
link under its message.

### The factory's health

`GET /factory/health` is answered by [health.mjs](health.mjs), not proxied: the
server lists nodes, the pods of the components' and the Projects' namespaces,
and metrics-server's figures itself and sends only a
summary (component states, restarts, CPU and memory, agent slots and sandboxes,
the last hour), never a pod spec. It samples every 15 seconds in the background
(`FACTORY_SAMPLE_SECONDS`; 0 turns it off) and keeps an hour in memory, timing
its own API calls as the API server's response time. Components are found by
namespace and labels; `FACTORY_COMPONENTS` (a JSON list of `{key, name,
namespace, selector, optional}`) replaces the built-in list. Without
metrics-server (`make metrics-server`) the view shows states but no figures.

`server.mjs` has no dependencies. Its upstream is `K8S_API` if set (for example
`http://127.0.0.1:8001` for a `kubectl proxy`), otherwise the in-cluster API with
the Pod's ServiceAccount token and CA. It listens on `PORT` (default 8080) and
answers `/healthz`. It answers nothing else unless the request's `Host` is
`localhost`, `127.0.0.1`, `[::1]` or a name in `UI_ALLOWED_HOSTS` (comma-separated,
for an Ingress; the chart's `ui.allowedHosts`): a page on another site that
rebinds its name to this address (DNS rebinding) sends a matching `Origin`, but
its own name in `Host`, and gets 421. The Vite dev server mounts the same handler, so `npm run dev`
behaves the same way.

### One connection for all watches

A page watches about a dozen kinds, and browsers open at most six HTTP/1.1
connections per host: with one request per watch, some watches would wait
forever and those kinds would silently stop updating. So the watchers share a
`WatchMux` ([src/k8s/mux.ts](src/k8s/mux.ts)): watches started together go out
as one `GET /k8s-watch?w=<path>&rv=<resourceVersion>&...`, for which `server.mjs`
opens the individual watches upstream and streams their events back over one
response, each line tagged with its watch. Listing, resourceVersions,
reconnecting with backoff and relisting on 410 Gone stay in the browser, per
kind ([src/k8s/watch.ts](src/k8s/watch.ts)). `/k8s/<path>?watch=1` still works for
a single watch.

## Code

```
server.mjs              static files + gateway to the Kubernetes API (/k8s, /k8s-watch): GETs and the action table's writes
health.mjs              the factory's health summary and last hour (/factory/health)
src/k8s/types.ts        the CRD shapes, mirroring api/v1beta1
src/k8s/watch.ts        list-then-watch: NDJSON events, reconnect with backoff, relist on 410
src/k8s/mux.ts          many watches over one connection
src/k8s/sources.ts      one watcher per kind, combined into a story snapshot
src/k8s/health.ts       polls /factory/health while Work or Factory is open
src/k8s/briefs.ts       the three writes: create a Brief, add a prompt, pause or resume; a name from the idea
src/model/story.ts      resources -> diagram model (boxes, arrows, roll-ups)
src/model/layout.ts     a small layered layout and edge routing
src/model/describe.ts   plain-language wording for phases, conditions and times
src/model/factory.ts    Work: stages, timelines, stats, the board and the agent slots
src/model/needs.ts      the Needs you items, from status
src/model/lifecycle.ts  each kind's lifecycle and next step, for the object pages
src/model/brief.ts      a Brief for display: phase, what it waits for, planned and waiting prompts
src/model/deploys.ts    a DeployTarget's member, tier, approval and Ready; a Container's deploys and the approve patch
src/model/testenvs.ts   a TestEnvironment's member, Ready, leases against its limits, spend and CI pool; a Task's leases
src/demo/               the demo fixture; in demo mode the Brief writes stay in memory,
                        refused by the CRD's limits (limits.ts) as the API server would
src/components/         the shell (Shell.tsx), the areas, the diagram and side panels;
                        objects/ holds the object pages, ObjectHeader.tsx their header
```

Everything under `src/k8s` and `src/model` is plain TypeScript without React, so
it is tested directly.

## Tests

```
npm test            # vitest (src/**/*.test.ts), then node --test for the server and the ANSI converter
npm run test:e2e    # builds, then drives Chromium against server.mjs and a fake API: live updates, 410 recovery
npm run typecheck   # tsc -b
npm run lint        # eslint
npm run format      # prettier
npm run check       # typecheck, lint, format check and npm test
```

`test:e2e` needs Chromium (see below) but no cluster: the fake API pushes watch
events and the test checks that the page follows. The UI workflow runs it after
`npm run check`; `make check` (`make ui-test`) doesn't, so run it for a change to
the pages, the demo fixture or `server.mjs`.

## Screenshots

Three scripts, used for the whole project's evidence in `docs/screenshots/`. They
need Chromium once: `npx playwright install chromium-headless-shell`.

Chromium also needs a few system libraries a minimal Linux lacks (libnspr4,
libnss3, libasound2). `sudo npx playwright install-deps chromium` installs them;
without root (a headless agent), the scripts fetch them themselves: on the first
launch that fails for a missing library, `scripts/browser.mjs` downloads the
packages with `apt-get download`, unpacks them into
`$XDG_CACHE_HOME/theos/chromium-libs` (`.dev/cache/` in the project shell) and
launches again with `LD_LIBRARY_PATH` set. Where it can't (no `apt-get`, or a
library it doesn't know), it exits saying which libraries are missing and the
command that installs them.

### A web page: `scripts/screenshot.mjs`

```
node scripts/screenshot.mjs <url> <out.png> [--width 1440] [--height 900] [--full]
    [--wait-for <css>] [--delay <ms>] [--dark] [--scale 2] [--hover <css>] [--click <css>]
```

`--full` captures the whole page, `--dark` emulates a dark colour scheme,
`--wait-for` waits for a selector, `--hover`/`--click` interact first. Pages that
keep connections open never become "network idle", so the script waits for
`load`, the selector and fonts, then `--delay` (300 ms by default).

```
node scripts/screenshot.mjs 'http://localhost:8080/?area=story&view=containers' ../docs/screenshots/ui-containers.png --full --wait-for .node
node scripts/screenshot.mjs 'http://localhost:8080/?demo=1' demo-dark.png --full --dark --wait-for .node
node scripts/screenshot.mjs http://localhost:3000/webshop/orders-api gitea-repo.png
```

### Every view, before and after: `scripts/views.mjs`

Screenshots every view of the built UI in demo mode and live, against the
end-to-end test's fake API (`test/fakeApi.mjs`) serving the demo fixture, at a
fixed clock, so two runs of the same UI give the same pictures: the system
areas, each system's containers and each Task's page; in Story, every box
without a page of its own selected in turn; each also with every `<details>`
open, and the areas and containers in the dark theme. A change that should
look the same compares its after with its before:

```
npm run screenshot:views -- /tmp/before
# ... change ...
npm run screenshot:views -- /tmp/after --compare /tmp/before
```

`--compare` exits 1 naming the pictures that differ; `--only <prefix>` limits
the run.

### What ships: `THEOS_UI_SHOTS`

The end-to-end tests save the pictures in `docs/screenshots/` that show what
ships: each area (`ui-shell-*.png`), each object page (`ui-object-*.png`),
_Needs you_, the version line with and without a skew, the actions' dialogs and
the refused reads. Set `THEOS_UI_SHOTS` to the directory:

```
THEOS_UI_SHOTS=$PWD/../docs/screenshots npm run test:e2e
```

### Terminal output: `scripts/term-shot.mjs`

Renders a text file as a terminal window, keeping ANSI colours and bold, and
captures it full length.

```
node scripts/term-shot.mjs <input.txt|-> <out.png> [--title "make test"] [--width 1100]
    [--no-wrap] [--font-size 13] [--scale 2] [--html out.html]
```

Long lines wrap at `--width`; `--no-wrap` widens the image to the longest line
instead, which suits tables. Use `-` to read stdin. Many tools drop colours when
their output is not a terminal; force them on (`FORCE_COLOR=1`, `--color=always`)
or record a terminal with `script`:

```
make test 2>&1 | tee /tmp/make-test.txt
node ui/scripts/term-shot.mjs /tmp/make-test.txt docs/screenshots/make-test.png --title "make test"

kubectl get containers,sessions -n webshop | node ui/scripts/term-shot.mjs - docs/screenshots/kubectl-get.png --title "kubectl get" --no-wrap

script -qc "make e2e" /tmp/e2e.txt >/dev/null
node ui/scripts/term-shot.mjs /tmp/e2e.txt docs/screenshots/e2e.png --title "make e2e"
```
