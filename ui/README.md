# C4 Factory UI

A read-only view of a C4 Factory project, drawn live from the custom resources.
Milestone 6 in [docs/design.md](../docs/design.md#mvp-v1-plan): someone who has
never used kubectl can read the story in a browser.

- **System context** (C4 level 1): the People and SoftwareSystems, the
  Relationships between them, each system's needs, KPIs and roll-up summary.
- **Containers** (C4 level 2) of one system: its Containers and DataStores inside
  a dashed boundary, with the People and other systems they are linked with
  around it. Each Container shows its phase, repository, the templates it was
  started from and who is working on it. Hover for its checks (conditions) in
  plain words; click for everything.
- **Who's working right now**: the Sessions per Container, agent or human, Active
  or Stale, with the age of the last heartbeat.

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
human Sessions, a Stale one, and a Relationship added after scaffolding. It needs
no cluster and is clearly labelled as demo data.

Other query parameters: `project=<namespace>`, `view=containers` and
`system=<name>`, so any view can be bookmarked.

## Read-only by construction

The UI enforces nothing and changes nothing; the rules live in the CRDs and the
operator. Three layers keep it that way:

1. The browser only ever sends GET requests, to `/k8s/...` and `/k8s-watch`.
2. [server.mjs](server.mjs) passes on only GET, only under
   `/apis/c4.factory.io/`, and answers everything else with 405 (other
   methods) or 403 (other paths). It drops the browser's own headers (cookies,
   `Authorization`, impersonation) and adds its own credentials.
3. In the cluster, the ServiceAccount's ClusterRole allows only `get`, `list` and
   `watch` on the `c4.factory.io` resources.

`server.mjs` has no dependencies. Its upstream is `K8S_API` if set (for example
`http://127.0.0.1:8001` for a `kubectl proxy`), otherwise the in-cluster API with
the Pod's ServiceAccount token and CA. It listens on `PORT` (default 8080) and
answers `/healthz`. The Vite dev server mounts the same handler, so `npm run dev`
behaves the same way.

### One connection for all watches

A page watches about ten kinds, and browsers open at most six HTTP/1.1
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
server.mjs              static files + GET-only gateway to the Kubernetes API (/k8s, /k8s-watch)
src/k8s/types.ts        the CRD shapes, mirroring api/v1alpha1
src/k8s/watch.ts        list-then-watch: NDJSON events, reconnect with backoff, relist on 410
src/k8s/mux.ts          many watches over one connection
src/k8s/sources.ts      one watcher per kind, combined into a story snapshot
src/model/story.ts      resources -> diagram model (boxes, arrows, roll-ups)
src/model/layout.ts     a small layered layout and edge routing
src/model/describe.ts   plain-language wording for phases, conditions and times
src/demo/               the demo fixture
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
need Chromium once: `npx playwright install --with-deps chromium`.

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
