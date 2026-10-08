# Plan: the UI tells each level's lifecycle, and says what needs you

GitHub issue #705, asked for by the owner on 2026-10-09; direction decided by
the maintainer on the issue. Milestone *UI: one story, and what needs you*.
Agent: otter-705.

## Goal

The UI tells one story at every level, from a Brief's idea to a plan, the
model, scaffolded repos, Crews' Tasks and releases. Every object's page
answers, in the same order: what is it and what does it belong to, where is it
on its lifecycle, what is happening now, and what needs me. One *Needs you*
list across projects comes first, and the UI shows which UI, operator, chart
and CRD API versions are running.

Done when the steps below are merged, and with them the "Done when" list of
#705: every kind the UI shows has a page or panel with the lifecycle header;
one *Needs you* list covers every item the CRDs flag for a person; versions are
shown and skew warned about, none hard-coded; `ui/src` copies no CRD limit,
default or operator timing; the production bundle carries no demo fixture,
nothing re-renders the whole tree on a timer, the largest files are split by
area; Playwright against the fake API covers each area with screenshots in
`docs/screenshots/`; `ui/README.md` and design.md's UI text match what ships.

## Decisions, and why

The maintainer's comment on #705 settled the direction (areas, object pages,
where the operator's version comes from, no new writes, the word "session",
order). These are the choices it left to the plan.

1. **Areas.** *Needs you*, *Briefs*, *Story*, *Work*, *Factory*, *About*. The
   shell opens on *Needs you* when it has items, else on *Story*.
2. **Routes stay query parameters.** `server.mjs` serves files and has no
   fallback for unknown paths, and today's links (`view=`, `system=`, `task=`,
   `demo=1`) are already query parameters. The new shell reads
   `area=needs|briefs|story|work|factory|about` and, for an object page,
   `ns=<namespace>&kind=<Kind>&name=<name>`, all through `useUrlState`, so
   every page is a shareable link and Back works. Until the switch-over, the
   new shell is reached only with `shell=next`; nothing in the old layout
   links to it, so releases don't wait on it (ROADMAP, *Releases are a
   train*). The switch-over makes it the default and maps the old `view=`
   links to their areas, so bookmarks keep working.
3. **One object page header, built once.** `components/ObjectHeader.tsx`
   renders kind, name and a breadcrumb to the parent; a lifecycle stepper
   (the kind's phases, the current one marked, side exits such as Failed or
   Blocked shown off the path); conditions with type, status, reason and
   message; the timeline from `status.history` where the kind has one; the
   children; links out (Gitea repo, issue, pull request); and the actions for
   this step. Each kind's path and its "what a person does" live in one pure
   table, `model/lifecycle.ts` (the table in #705, *Lifecycle of each level*),
   tested with Vitest, so a page can't invent its own.
4. **Actions the UI can't take say the exact next step.** The UI keeps its
   three writes (create a Brief, add a prompt, pause or resume). For anything
   else the header shows a link (the Gitea pull request or issue) or a
   `kubectl` line with a copy button, built from the object's own fields, for
   example `kubectl -n shop annotate container api c4.factory.io/retry=$(date +%s) --overwrite`.
   Adding a write is its own issue, after design.md changes.
5. **The *Needs you* list is a pure function over what the UI already
   watches.** `model/needs.ts` takes the cross-namespace data the Factory
   source reads (Projects, Briefs, Containers, Crews, Tasks) and returns items,
   each with the object, why (the condition's reason and message, verbatim),
   and where it is done (an object page, a link out, or a `kubectl` line).
   The items: a Brief whose `NeedsYou` or `NeedsInput` is True; a Container in
   `Failed`; a Task `Blocked` or `Failed`; a Crew whose last release waits on
   a person (the release fields as the CRD has them); a Container whose
   `StandardUpdated` condition says its update pull request waits on a
   person. Every item comes from `status`; none is guessed from timing.
6. **Where versions come from** (decided on #705; the details here). A new
   package `internal/buildinfo` holds `Version` (`dev` unless set with
   `-ldflags -X`), with no Kubernetes import. The operator `Dockerfile` takes
   `ARG VERSION=dev` into the ldflags, and `release.yml` passes
   `--build-arg VERSION=$VERSION` to the operator and UI builds. The chart
   gives the manager `C4_CHART_VERSION={{ .Chart.Version }}`. The Project
   reconciler writes both into an additive status field,
   `status.factory: {operatorVersion, chartVersion}`, so the browser still
   reads only `c4.factory.io`. The UI image gets `VERSION` as a build arg that
   Vite `define`s as `__C4_UI_VERSION__` (`dev` otherwise). The CRD API
   version is the `apiVersion` the UI reads. The UI warns when its version and
   the operator's differ, not when both are `dev`, and says "unknown" when no
   Project has reported one yet.
7. **"Session" means only the devcontainer Session CR.** An agent's run in a
   Task is an *agent run*: "run 2, handed off at the token limit". Step 2
   renames it in the current UI's copy; later steps keep it.
8. **The UI copies no rules.** `MAX_IDEA`, `MAX_PROMPT`, `MAX_PARALLEL`, the
   name regex, `DEFAULT_PARALLEL`, the default autopilot mode,
   `DEFAULT_SESSION_TOKEN_LIMIT` and "Stale after 2 minutes" leave
   `ui/src/k8s` and `ui/src/model`. Submit is enabled for any non-empty input,
   and a refused write shows the API server's message (the CEL rule's text).
   Where the UI needs a default to describe an object (the parallel count, the
   token limit), it says "the factory's default" and shows a number only when
   `status` carries the effective one. Demo mode plays the API server, so its
   writer may refuse like it; its limits sit in `demo/`, with a Vitest test
   that reads `config/crd/bases/` and fails when they differ from the CRD.
9. **The demo fixture leaves the production bundle** by a dynamic `import()`
   in demo mode, so it is a chunk loaded only with `demo=1`. Demo mode stays a
   feature (`ui/README.md`, *Demo mode*). A test of the build output checks
   the main chunk holds none of the fixture's names.
10. **No timer re-renders the tree.** `useNow` moves into the leaves that show
    a running clock (a relative time, an elapsed bar), each re-rendering only
    itself.
11. **Styles split by area,** one CSS file per component area imported by
    that component, with the theme's custom properties in one `theme.css`.
    `model/sizes.ts` becomes the source of the sizes the CSS reads, as custom
    properties set from it, so the two can't drift.
12. **Evidence is the fake API.** Each step runs `npm run check` and
    `npm run test:e2e` in `ui/` (Playwright against `test/live.e2e.mjs`'s
    fake Kubernetes API) besides `make check`, and saves its screenshots in
    `docs/screenshots/` with `npm run screenshot`. No cluster is involved.

## Steps

Each step is one issue in the milestone and one pull request that leaves main
working, saying `Closes` its own issue. Each depends on the one before unless
it says otherwise, because most touch `App.tsx` and the styles.

1. **#707: Clean up the UI code with no visible change.** Remove the duplicate
   `KIND_LABEL` (one in `model/describe.ts`) and merge `formatMs` with
   `formatDuration`; split `styles.css` and `components/Factory.tsx` by area
   (decision 11); load the demo fixture only in demo mode (decision 9); move
   `useNow` to the leaves (decision 10). Done when the screenshots of every
   view match before and after, the bundle test passes, and no source file in
   `ui/src` outside `demo/` and the tests is over 600 lines.
2. **#708: The UI copies no rules, and its copy is current.** Decisions 7 and 8; the
   footer and brand subtitle name the three writes; the Brief's create form
   and prompt box show the API server's refusal. Done when a grep of
   `ui/src` outside `demo/` finds none of the copied names, the demo limits
   test reads the CRDs, and a component test shows a refused create with the
   server's message.
3. **#709: Versions.** Decision 6, end to end: `internal/buildinfo`, the
   Dockerfiles, `release.yml`, the chart env, the Project status field
   (`make manifests generate`, an envtest that the reconciler writes it), the
   UI's version, and a version line in today's footer with the skew warning.
   design.md's *Status* section says what Project status carries. A complete
   small feature, so a release may carry it.
4. **#710: The new shell, on `shell=next`.** Decision 2: the six areas' navigation,
   their routes, Back and keyboard access; a freshness line (connection,
   last updated, the namespace in view); *About* with the versions from
   step 3 and links to the docs, the running version's release notes and the
   repo. *Story*, *Briefs*, *Work* and *Factory* show today's components
   inside the shell for now.
5. **#711: The *Needs you* inbox.** Decision 5: `model/needs.ts` with a table test
   per item kind, and the *Needs you* area listing them across projects,
   each linking to where it is done (today's views until the object pages
   land). Depends on step 4.
6. **#712: The object page header, with Brief and Container pages.** Decisions 3
   and 4: `model/lifecycle.ts`, `ObjectHeader`, and the pages for Brief
   (rounds, prompts, issues, suggestions, its three writes) and Container
   (scaffold conditions, Relationships with their `Applied` reason, Sessions,
   template refs, the retry `kubectl` line on Failed, the Standard update pull
   request). *Needs you* items for these kinds link to their pages. Depends on
   step 4.
7. **#713: Crew and Task pages.** The Crew's state, slots, its Tasks, its last
   release with the person's next step; the Task's stepper (implement, check,
   review, merge), rounds against `maxRounds`, the timeline, its agent runs
   with tokens, cost and handoffs. Depends on step 6.
8. **#714: Read-only pages for the rest.** Project (counts, conditions, versions),
   SoftwareSystem, Relationship, Session, Standard, Compute, Deployment and
   GitProvider (its Secret's name only, never its data). Kinds the UI doesn't
   read yet are added to `k8s/types.ts`, the sources and the fake API, as
   optional reads whose failure shows in the panel. Depends on step 6.
9. **#715: Story and Work in the shell.** The diagrams' nodes open object pages;
   *Work* shows Crews, the Task board and releases, each opening its page.
   Depends on steps 7 and 8.
10. **#716: States and accessibility.** Every panel shows its own loading, empty and
    error state (no hidden read failure of an optional kind, no single global
    banner); relative and absolute times on every timestamp; WCAG 2.1 AA in
    both themes (contrast, focus, labels), checked with axe in the Playwright
    run. Depends on steps 5 and 9.
11. **#717: Switch over.** The new shell becomes the default, the old layout and
    `shell=next` go, old `view=` links map to areas; `ui/README.md`, a
    design.md section on the UI (areas, object pages, *Needs you*, versions),
    and the screenshots match what ships. Its pull request finishes #705.
    Depends on step 10.

## Review focus

- A badge, phase or count that isn't read from `status` (a guessed state, a
  timing rule, a default copied from the CRD or operator).
- A *Needs you* item with no link or next step, or a flagged state that shows
  no item.
- A `kubectl` line that would not work as printed (namespace, kind, name).
- Anything secret-shaped reaching the browser (a GitProvider's Secret data).
- The new shell linked from the old layout before step 11.
