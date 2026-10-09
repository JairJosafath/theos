# C4 Factory UI

A view of a C4 Factory project, drawn live from the custom resources.
Milestone 6 in [docs/design.md](../docs/design.md#mvp-v1-plan): someone who has
never used kubectl can read the story in a browser. It reads everything and
changes only three things: it creates a Brief from an app idea, adds a
follow-up prompt to one ([docs/plans/done/brief.md](../docs/plans/done/brief.md)),
and pauses or resumes one ([docs/plans/upgrade-in-place.md](../docs/plans/upgrade-in-place.md)).

- **Briefs**, above the views: a _Describe an app_ box that creates a Brief (an
  idea and a name, suggested from the idea, and optionally its autopilot mode
  and how many issues each of its crews works on at once), and a card per Brief with its
  phase, what it waits for when it needs input, the planner's latest choices,
  the objects and issues it made, each app's releases (on or off, the
  latest tag linking to its Gitea release, and where it was published, linking to
  the GitHub release, and how many issues its crew works on at once), its prompts (those not yet planned marked
  _next round_), its repository and cost, a field to add a prompt, and a Pause or Resume
  button that sets `spec.paused`; while paused, the card shows its `Paused`
  condition: which Tasks still run, or that nothing does. The API's
  answer shows as is when it refuses. Only shown in a namespace with a Project.
- **System context** (C4 level 1): the People and SoftwareSystems, the
  Relationships between them, each system's needs, KPIs and roll-up summary.
- **Containers** (C4 level 2) of one system: its Containers and DataStores inside
  a dashed boundary, with the People and other systems they are linked with
  around it. Each Container shows its phase, repository, the templates it was
  started from and who is working on it. Hover for its checks (conditions) in
  plain words; click for everything, including its Crew: who implements and who
  reviews its issues, with their models, and the issue they work on.
- **Who's working right now**: the Sessions per Container, agent or human, Active
  or Stale, with the age of the last heartbeat.
- **The factory** (`?view=factory`, the _Factory_ switch in the top bar), across
  all projects: whether each part of the factory is healthy, with restarts, CPU
  and memory; the node's usage and the last hour (API server response, CPU,
  memory); the Tasks on a board by stage, each with its timeline; the Crews,
  each with its Agents and their models, the Task it works on and its ready and
  waiting issues; where the time goes across Tasks; the agent slots and who
  waits for one; and a list of what needs a look, such as a Task waiting for an
  unavailable agent or a Crew that can't pick up issues. Select a Task for its
  issue or goal, its Agents, rounds, timeline, feedback and follow-up issues.

Everything updates as the resources change: the browser lists each kind, then
watches it.

## Run it

From the repository root, with the Kind cluster from `make cluster` running:

| Command           | What it does                                                                                                          |
| ----------------- | --------------------------------------------------------------------------------------------------------------------- |
| `make ui-dev`     | Vite dev server with live reload on <http://localhost:5173>, reading the cluster through a `kubectl proxy` it starts. |
| `make ui-serve`   | Builds `dist/` and serves it with `server.mjs` on <http://localhost:8080>, also through a `kubectl proxy`.            |
| `make ui`         | Builds the image `c4factory/ui:dev`, loads it into Kind, applies [config/ui](../config/ui) and waits for the rollout. |
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
page's memory. It needs no cluster and is clearly labelled as demo data.

Other query parameters: `project=<namespace>`, `view=containers` or
`view=factory`, `system=<name>` and `task=<namespace>/<name>`, so any view can
be bookmarked. `?demo=1&view=factory` shows the factory view with every state.

## Read-only but for three writes

The UI enforces nothing; the rules live in the CRDs and the operator. It reads
every C4 Factory kind and writes only three things: it creates a Brief, it
appends a prompt to one, and it pauses or resumes one. Three layers keep it
that way:

1. The browser sends GET requests to `/k8s/...` and `/k8s-watch`, plus the three
   writes ([src/k8s/briefs.ts](src/k8s/briefs.ts)).
2. [server.mjs](server.mjs) passes on only GET under `/apis/c4.factory.io/`,
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
   `list` and `watch` on the `c4.factory.io` resources (Briefs included), plus
   `get` and `list` on pods, nodes and `metrics.k8s.io` for the health summary
   below. A separate ClusterRole, `c4-factory-ui-brief-writer`, grants only
   `create` and `patch` on `briefs`. No Secrets, no other writes.

### Actions: `ui.actions.enabled`

With the chart's `ui.actions.enabled` (off by default; design.md, _UI
actions_), the server's action table (`ACTIONS` in [server.mjs](server.mjs))
takes more writes, each one method, path, content type and body shape:

| Kind      | Request              | Fields                                                                                                                               |
| --------- | -------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| Brief     | `POST`, JSON         | a new Brief, as above                                                                                                                |
| Brief     | `PATCH`, JSON patch  | `add` at `/spec/prompts/-` alone, as above; or `add` of any of `/spec/paused`, `/spec/autopilot`, `/spec/parallel`, `/spec/releases` |
| Crew      | `PATCH`, JSON patch  | `add` of any of `/spec/paused`, `/spec/parallel`, `/spec/tagReleases`, `/spec/prepareReleases`, `/spec/publishReleases`              |
| Crew      | `PATCH`, merge patch | annotation `c4.factory.io/rerun-release` alone                                                                                       |
| Container | `PATCH`, merge patch | annotation `c4.factory.io/retry` alone                                                                                               |
| Task      | `POST`, JSON         | `spec.kind: Implement` with `containerRef`, `agentRef`, `reviewerRef`, `goal`; only its name and spec go upstream                    |
| Standard  | `PATCH`, JSON patch  | `add` at `/spec/template/ref`                                                                                                        |

The server checks each body's shape and the JSON type of each value, not the
values: limits and enums are the CRD's, and the API server's refusal comes
back as-is. The chart sets `UI_ACTIONS` from the values:

- over port-forward (`ui.allowedHosts` empty), `service-account`: the UI's
  ServiceAccount makes the writes, bound to ClusterRole
  `c4-factory-ui-actions` (`create` on briefs and tasks, `patch` on briefs,
  crews, containers and standards) in place of the brief writer;
- with `ui.auth.oidc`, `oidc`: each write is made as the person signed in
  (below), and the ServiceAccount has no write binding, only `impersonate`
  on users and groups; `ui.auth.oidc.actionGroups` binds
  `c4-factory-ui-actions` to those groups, prefixed;
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
  `nonce`, and a short-lived `c4ui_login` cookie that ties the answer to this
  browser;
- `GET /ui/callback` exchanges the code (client secret by HTTP Basic), checks
  the ID token with `node:crypto` against the issuer's JWKS (RS256 or ES256;
  `iss`, `aud`, `azp`, `exp`, `nbf`, `nonce`), and requires `email_verified`
  when the username claim is `email`. The person is then `usernamePrefix` +
  that claim and `groupsPrefix` + each group, never a `system:` name; the
  session holds only those and the token's expiry, in memory, behind
  `c4ui_session` (`HttpOnly`, `Secure`, `SameSite=Lax`). A restart, or the
  ID token's expiry, signs everyone out;
- `POST /ui/logout` ends the session.

Every write then carries `Impersonate-User` and `Impersonate-Group` (one
header per group) with the UI's own credentials; reads, the ones a write makes
first included, carry none. A write with no session answers 401, "Sign in to
act".

### The factory's health

`GET /factory/health` is answered by [health.mjs](health.mjs), not proxied: the
server lists pods, nodes and metrics-server's figures itself and sends only a
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
src/k8s/types.ts        the CRD shapes, mirroring api/v1alpha1
src/k8s/watch.ts        list-then-watch: NDJSON events, reconnect with backoff, relist on 410
src/k8s/mux.ts          many watches over one connection
src/k8s/sources.ts      one watcher per kind, combined into a story snapshot
src/k8s/health.ts       polls /factory/health while the factory view is open
src/k8s/briefs.ts       the three writes: create a Brief, add a prompt, pause or resume; a name from the idea
src/model/story.ts      resources -> diagram model (boxes, arrows, roll-ups)
src/model/layout.ts     a small layered layout and edge routing
src/model/describe.ts   plain-language wording for phases, conditions and times
src/model/factory.ts    the factory view: stages, timelines, stats, Crews, what needs a look
src/model/brief.ts      a Brief for display: phase, what it waits for, planned and waiting prompts
src/demo/               the demo fixture; in demo mode the Brief writes stay in memory
src/components/         the diagram, cards and side panels
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
events and the test checks that the page follows.

## Screenshots

Two scripts, used for the whole project's evidence in `docs/screenshots/`. They
need Chromium once: `npx playwright install chromium-headless-shell`.

Chromium also needs a few system libraries a minimal Linux lacks (libnspr4,
libnss3, libasound2). `sudo npx playwright install-deps chromium` installs them;
without root (a headless agent), the scripts fetch them themselves: on the first
launch that fails for a missing library, `scripts/browser.mjs` downloads the
packages with `apt-get download`, unpacks them into
`$XDG_CACHE_HOME/c4factory/chromium-libs` (`.dev/cache/` in the project shell) and
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
node scripts/screenshot.mjs 'http://localhost:8080/?view=containers' ../docs/screenshots/ui-containers.png --full --wait-for .node
node scripts/screenshot.mjs 'http://localhost:8080/?demo=1' demo-dark.png --full --dark --wait-for .node
node scripts/screenshot.mjs http://localhost:3000/webshop/orders-api gitea-repo.png
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
