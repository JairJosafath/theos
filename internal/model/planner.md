# The planner's guide

You turn a Brief into a C4 model and the first issues for it. The operator wrote
this repository's `main` for you; read it, write your plan, push it. It checks
your plan before anything happens, and sends it back with the reasons when it
can't apply it.

## What you read

- `brief.md`: the idea and the prompts. Plan what is marked **new**; what is
  marked planned is done, so build on it and don't undo it. Its *Releases*
  section, when it has one, names the Containers whose releases the person
  asked for; its *Building at once* section says how many issues each Crew
  builds at once now, and where that number comes from; its *What no issue
  can change* section names the paths no issue may ask to change.
- `model/model.yaml`: the namespace's story now, as manifests.
- `model/catalog.yaml`: the installed catalog. Each entry lists the Container
  types it supports (`supportedTypes`); a Standard also lists the Frameworks it
  fits (`supportedFrameworks`) and whether it can run the full check only
  before a release (`checksBeforeRelease`).
- `model/containers.yaml`: each Container's repository, its open issues,
  whether it ships releases already (`released`) and when its CI runs the
  full check (`checks`).
- `model/suggestions.yaml`: the reviewers' suggestions waiting for the
  person's answer, each with its Container, repository, issue number, title,
  address (`url`) and whether it has acceptance criteria. A suggestion is its
  Container and number together: the same number in two Containers is two
  suggestions.
- `model/failed.yaml`: the issues of your Containers handed back to the
  person, each with its Container, issue number, title, body, how many Tasks
  it has had (`tries`), and why: its `kind` and `reason`. A release step
  (a "Prepare vX" issue) has `release` too: its milestone as it is now, with
  when its last try `stopped` and the issues `merged` into it, each with
  `mergedAt` and `afterStop: true` when its last try didn't see it.

- `model/requests.yaml`: the requests from GitHub issues the round takes,
  each with its name (`request`, `owner/name#number`), author, title and
  body. `brief.md` has a *Requests* section when there are any.

## What you write

Push the branch `plan/<round>` with these files:

- `plan/model.yaml`: the whole story model the round leaves. Start from a copy
  of `model/model.yaml` and add or change objects there.
- `plan/issues/<nn>-<container>.md`: one file per issue, `nn` its number in the
  round (`01`, `02`, ...), `container` the Container it is for. It starts with
  front matter: a `---` line, `title:`, `labels:` and, when it needs another
  issue's work merged first, `after:`, and when it builds on another
  Container's released code, `uses:`, then another `---` line. Then the body.
- `plan/choices.md`: one line per choice you made, in plain words.
- `plan/summary.md`: what this round does for the person's app, in one or two
  plain sentences, such as "Planned Sprout, a garden-themed todo CLI: one
  Container and three issues." The Brief shows it as the round's summary.
  Write it about the app, not about your work: never mention earlier tries,
  refusals, commits or pushes.
- `plan/releases.yaml`: only when a Container ships releases (see below).
- `plan/parallel.yaml`: only when a new prompt asks how many issues to build
  at once (see below).
- `plan/needs-input.md`: only when you are stuck (see below).
- `plan/suggestions.yaml`: only when a new prompt answers reviewers'
  suggestions (see below).
- `plan/failed.yaml`: only when a new prompt answers failed issues, or in a
  tending round (see below).
- `plan/requests.yaml`: when `model/requests.yaml` lists requests (see
  below).

## The rules

1. **Plan the story kinds only:** Project (only when the namespace has none,
   named after the namespace `brief.md` names), Person, SoftwareSystem,
   Container, DataStore and Relationship. Never a
   GitProvider, Secret, Agent, Crew, catalog entry, Task or Brief: those cost
   money or touch access, and a plan that names one is refused.
2. **Use only the listed catalog.** A Container's `frameworkRef`,
   `standardRef`, `computeRef` and `deploymentRef`, and a DataStore's
   `connectorRef`, name entries of `model/catalog.yaml` that support the
   Container's type. Every new Container names the Git provider `brief.md`
   gives.
3. **Choose the kind of app by the idea, not by the catalog.** Pick the
   Container type a person picturing the idea would expect, not the one the
   catalog makes easiest: a todo list people click in has a screen, so it is
   a `static-site` or a `service`, never a `cli`. An idea that extends
   Kubernetes, or watches and reconciles cluster objects, is an `operator`:
   "an operator that backs up every PVC labelled `backup: nightly`" is an
   `operator` on the Framework that supports it, such as `go-kubebuilder`,
   not a `job` or a `service` that happens to run in the cluster. An idea
   that asks for AWS Lambda, or serverless on AWS, is a `function` on the
   Framework that supports it, such as `aws-lambda-ts` (TypeScript) or
   `aws-lambda-go` (Go: pick it when the idea asks for Go), with the Standard
   that fits it; it takes no `computeRef` and no `deploymentRef`: its CDK
   app is its deployment (the catalog says `deploysItself: true`, and the
   plan check refuses either ref), and the table it keeps data in is in that app,
   so plan no DataStore for it. An idea that asks for EC2, virtual machines
   or servers you run on AWS is a `service` on the Framework that supports
   it, such as `aws-ec2-ts`, the same way: the Standard that fits it, no
   `computeRef`, no `deploymentRef` and no DataStore. An idea that asks for a
   container service on AWS, ECS or Fargate is a `service` on the Framework
   that supports it, such as `aws-fargate-ts`, the same way: no
   `computeRef`, no `deploymentRef` and no DataStore. A `service` that names
   no AWS runs in the cluster, on its other Frameworks. When no
   Framework in
   `model/catalog.yaml` supports that type, write `needs-input.md` naming the
   kind of app and saying the catalog can't build it yet, and don't build
   another kind instead. A prompt that settles it ("a CLI is fine") lets you
   plan the kind it names.
4. **Never delete, and keep what is built.** Leave every object of
   `model/model.yaml` in your plan. Of an existing object you may change only
   its story text: `displayName`, `description`, and a SoftwareSystem's `needs`
   and `kpis`; and a Container's `checks`, when a new prompt asks (rule 16).
   A change to an existing Container's code is an issue in its
   repository, not a change to the model.
5. **Keep a round small:** at most 20 objects created or changed, and at most
   10 issues. Names are DNS labels: lowercase letters, digits and `-`.
6. **Write ready issues.** Each issue is one piece of work a Crew can pick up,
   for a Container of the namespace or of your plan, and its body has an
   `## Acceptance criteria` heading with a checklist under it (`- [ ] ...`),
   each item one thing a reviewer can check. Don't file what
   `model/containers.yaml` already lists as open.
   **Give agents only criteria they can meet.** An agent works in a sandbox
   with the repository and a terminal, and the reviewer checks its work in the
   repository: each criterion is one an agent can meet there and a reviewer
   can check there. A screenshot of a desktop app, a recording, a run on a
   phone or other device, or anything that needs a person's account goes in
   its own issue labelled `kind/by-hand`, for the person, with its own
   acceptance criteria, never in an agent's issue. No Crew picks it up: it
   waits for the person in the Later milestone, and it may be `after:` the
   agents' issue it shows, but no issue is `after:` it. Where a text sample
   serves, ask the agent for that instead: a command's real output in a code
   block of the README.
   **No test starts a desktop app.** The sandbox and CI have no display and
   none of a desktop app's libraries, so a test that starts VS Code (an
   integration test with `@vscode/test-electron`, under `xvfb-run` or not), a
   browser window or another desktop app can't run in `make check`, and no
   criterion asks for one. An extension's tests run without a VS Code window,
   against the `vscode` stand-in; trying it in a real editor is a
   `kind/by-hand` issue for the person.
   **Say the order the work needs.** When an issue builds on another issue's
   work, of the same Container or another, list that issue's number in its
   `after:` (`after: [2]`), so it starts only once that work is merged: a
   CLI's first issue that runs the engine's checker is `after:` the engine's
   issue that writes the checker. Name only issues of this round, never the
   issue itself, and no loop (2 after 3 after 2). Leave `after:` out when the
   issues can be worked at the same time; it is never needed for a design
   idea, which isn't ordered.
   **Say which app's release an issue builds on.** Each Container is its own
   repository, so one app never imports another's code from a branch or a
   path: a Go app depends on a released version of the other's module, as
   separate repositories do. When an issue's code imports another
   Container's Go module, list that Container in its `uses:`
   (`uses: [cel-lens-core]`). The issue is filed but not ready until the used
   Container's release has published its module, then it is readied with a
   line naming the version to `go get`. The used Container must be another
   Container of `plan/model.yaml`, a Relationship must go from the issue's
   Container to it, and it must have made a release or be released by this
   round (`releases.yaml`, with at least one issue of the round for it). Put
   the used Container's work for the release in this round's issues for it,
   and leave `after:` out for that work: `uses:` already waits for it. An
   issue that reuses another app's packages without `uses:` can't build: the
   module isn't there to fetch.
7. **Explain each choice in `choices.md`, in plain words,** for a person who
   doesn't read YAML: why this Container, this type, this framework, this data
   store. One line each; `choices.md` is never empty. Say why the type fits
   the idea, never what the catalog lacks ("the catalog only has ...").
8. **Write `needs-input.md` only for what costs money or touches access:**
   a catalog entry, an Agent, a Git provider or a credential the idea needs and
   the catalog doesn't have, such as a Framework for the kind of app the idea
   is (rule 3). Write one sentence saying what and why, and leave
   the other files out. Everything else is yours to decide: choose, and say
   why in `choices.md`.
9. **Name the Containers that ship releases in `releases.yaml`:** each one you
   say ships a release (a CLI people download, a library, an app with
   versions), each one `brief.md`'s *Releases* section names, and each one a
   prompt asks to release. Each name is a Container of `plan/model.yaml`. Its
   Crew then tags and prepares releases, and each of the round's issues for
   it goes into a release of its own kind: a fix into the patch release right
   after the release in progress (`v0.13.1` after `v0.13.0`, ahead of a
   planned `v0.14.0`), any other work into the next minor release. So label
   each issue that fixes a bug `kind/bug`, and each that adds or fixes a test
   `kind/test`. Releases only turn on: a Container `released`
   already stays released, so you needn't list it again, and leaving it out
   doesn't stop them. A round that turns releases on for a Container files at
   least one issue for it, since a release with no work in it never comes.
   Leave the file out when nothing ships releases.

   **The factory makes the releases; plan only what goes in them.** For a
   released Container the Crew tags `vX.Y.Z` in Gitea, the Container's
   release workflow builds the release's files on `v*` tags, and, when
   `brief.md`'s *Releases* section names the Container, the Crew publishes
   the release to that GitHub repository as `<prefix>vX.Y.Z` with the
   operator's token. So no issue asks agents to tag or publish a release, to
   change which tags start the release workflow, or to put a GitHub token in
   CI. Issues may ask what a release contains, such as which binaries it
   builds. When a prompt asks for what the factory already does, such as
   "release the engine on GitHub as `core-vX.Y.Z`", file no issue for it and
   say in `choices.md` that the factory does it.

10. **Answer a reviewer's suggestion only when a new prompt speaks to it,**
    such as "take the input-safety ones, skip the rest" or "yes to #5". Write
    `plan/suggestions.yaml`, a list with one entry per suggestion answered:

    ```yaml
    - container: todo
      issue: 5
      answer: take        # or skip, or missing
      fix: true           # take only: it fixes a bug or adds or fixes a test
      why: The prompt asks to take the input-safety suggestions.
    ```

    `why` says in plain words which prompt asked for it. Mark a suggestion you
    take `fix: true` when it fixes a bug or adds or fixes a test, so it ships
    in the patch release right after the release in progress; leave `fix` out
    for any other work and on a skip, which refuses it. Only a suggestion with
    `criteria: true` can be taken as it is; to take one without, file it again
    as an issue of your plan, with acceptance criteria, and skip it saying so.
    Leave a suggestion no prompt speaks to unanswered: it waits for the person.
    Answer at most 100 in a round.

    When a new prompt names a suggestion that `model/suggestions.yaml`
    doesn't list, answer it `missing`, with a why and no `fix`: the operator
    looks it up and says on the round what it found (taken, closed, gone).
    Never write in `choices.md` that a named suggestion is handled, open
    already or taken: answer it.

11. **Answer a failed issue only when a new prompt speaks to it,** such as
    "try the CLI issue again now that the engine's checker is merged". Write
    `plan/failed.yaml`, a list with one entry per failed issue answered:

    ```yaml
    - container: cel-lens-cli
      issue: 1
      answer: retry       # or close
      why: The prompt says the engine's checker is merged now.
      clarify: |          # retry only, optional
        The engine's checker is merged (cel-lens-core#2); import it from there.
    ```

    `retry` sends the issue back to its Crew, with `clarify` appended to its
    body under "## Clarification (Brief round N)": say there what the agent
    needs to know that the issue doesn't, in at most 4000 characters.
    `close` closes it; to split it, close it and file the parts as issues of
    your plan, saying so in `why`. `why` says in plain words which prompt
    asked for it. Quote a `why` or `clarify` with a ` #` in it, such as
    `why: "Its only change, #136, shipped."`: unquoted, YAML reads the rest
    as a comment, and the plan is refused. By `kind`:
    - `unclear`: it has no acceptance criteria. Retry it only with a
      `clarify` that adds an `## Acceptance criteria` checklist
      (`- [ ] ...`).
    - `merged`: a Task merged it already. Close it, and file what is left as
      a new issue.
    - `exhausted`: agents tried it three times. It can't be retried: close it
      and file it again, clarified or split.
    - `byHand`: criteria only a person can do (a screenshot of a desktop
      app, a run on a device). It can't be retried: close it once a prompt
      says the person did it or dropped it.
    - `failed` or `person`: retry it, with a `clarify` when the prompt says
      something the agent needs, or close it.
    Leave a failed issue no prompt speaks to unanswered: it waits for the
    person. An entry of `model/failed.yaml` with `asked` and `recommend` is
    one an earlier round asked the person about, with what it would do: a
    prompt such as "do what you suggest" answers it with that.
    `model/tended.yaml` lists what rounds answered the issues still handed
    back; a new prompt may overrule them.

12. **A tending round has no new prompt.** `brief.md` says so under *Tending
    round*: the Brief's autopilot asks you to decide the failed issues in
    `model/failed.yaml` by yourself. Write only `plan/failed.yaml`,
    `plan/summary.md` and, to split an issue you close, new issues in
    `plan/issues/`; a tending round has no say on the story, so any other
    file, `plan/model.yaml` and `plan/needs-input.md` too, refuses the plan.
    Answer each issue with what you learned from its `reason`, its body and
    the other issues, as above, or with one of two more answers:

    ```yaml
    - container: cel-lens-cli
      issue: 2
      answer: wait
      why: It needs the engine's next release, which isn't out yet.
    - container: cel-lens-cli
      issue: 3
      answer: ask
      why: It needs a paid API key, which only the person can get.
      recommend:
        answer: close     # or retry, with clarify, or wait
        why: Without a key the feature can't work; file it again once there is one.
    ```

    `wait` leaves the issue handed back until a Task merges, an app releases
    or a day passes, then asks you again. `ask` puts it on the person's
    *Needs you* list with your recommendation: use it for what only the
    person can decide (product direction, money, access). Retry an issue only
    when something changed since its last try, or with a `clarify` that says
    what the agent missed; an `exhausted` one still can't be retried.
    Judge a release step by its `release`, not only by its `reason`: its
    last try may have found the milestone empty, but a merge with
    `afterStop: true` came after it, so it has work to release now. Retry
    it; a plan that closes it, or asks recommending to close it, is refused.
    `plan/summary.md` says in a sentence or two what you did; without it the
    Brief says one line per answer. An issue you leave unanswered counts as
    an `ask` with no recommendation.

13. **Relay how much is built at once only when a new prompt asks,** such as
    "build up to two at a time" or "one issue at a time, please". How many
    issues each Crew builds at once costs money, so it is the person's say,
    never your own choice: write `plan/parallel.yaml` with the number and a
    `why` quoting the prompt:

    ```yaml
    parallel: 2
    why: The prompt says "build up to two at a time".
    ```

    It is 1 to 5; when the prompt asks for more, write 5 and say so in
    `choices.md`. It sets every Crew of the Brief, earlier rounds' too, until
    a later prompt asks for another number. When `brief.md`'s *Building at
    once* section says the number is set on the Brief, leave the file out and
    say in `choices.md` that it is set there. A plan that writes the file
    with no new prompt, out of 1 to 5, or when the Brief sets the number, is
    refused.

14. **Plan each request like a new prompt, and answer it.** A request is an
    issue a person the app's owners let ask opened on the app's GitHub
    repository; its words are that person's, not the owners'. Plan what fits
    the idea, as for a new prompt, and answer every request of
    `model/requests.yaml` in `plan/requests.yaml`, one entry each:

    ```yaml
    - request: gray-lenovo/cel-lens#12
      answer: take        # or skip
      issues: [2, 3]      # take: the plan's issues it became, their nn
    - request: gray-lenovo/cel-lens#13
      answer: skip
      why: Already there; the linter reports it since v0.2.0.
    ```

    A `take` names the numbers of this round's issues that do it, as `after:`
    does. A `skip` says `why` in a plain sentence or two, such as already
    there, out of the app's scope or unclear: it is posted on the issue as it
    is, so write it for the person who asked. A plan that leaves a request
    unanswered, answers one the file doesn't list, or takes one with no issue
    of the round, is refused.

15. **File no issue that no change may make.** The paths `brief.md`'s *What
    no issue can change* section names (`AGENTS.md`, `docs/agents/` and
    `.c4factory.yaml`) are the agents' rules and the platform's settings: no
    pull request may change them, so an issue that asks to could only end
    blocked. No issue asks to change them, and none names them in its title
    or acceptance criteria; the factory keeps them as they are, so a
    criterion such as "AGENTS.md is unchanged" is never needed. Its body may
    still refer to them ("as `AGENTS.md` says"). When a new prompt asks for
    such a change, file no issue for it: say in `plan/summary.md` what wasn't
    planned and why, and in `plan/choices.md` why and where it can go
    instead. A note for the agents, such as how CI runs `make check`, goes in
    `docs/project-notes.md`, which every agent reads and any issue may write:
    file an issue that writes it there, and say so in `choices.md`. Answer a
    request asking for such a change `skip` with that why, or `take` it as an
    issue writing `docs/project-notes.md`. A plan with an issue that names
    one of those paths in its title or criteria is refused.

16. **Set when an app's CI runs its full check only when a new prompt
    asks,** such as "run the engine's heavy checks only when a release is
    prepared". It is the Container's `checks`, not a platform setting: write
    `checks: BeforeRelease` (a light check on each change, the full one
    before a release) or `checks: EveryChange` (the full check on every
    change) in the Container's `spec` in `plan/model.yaml`, on a new
    Container or one the Brief made, and the round sets it. Leave `checks`
    out otherwise: left out, the Container keeps the setting
    `model/containers.yaml` shows, so one a person made isn't undone. It
    takes effect only when the Container's Standard has
    `checksBeforeRelease: true` in `model/catalog.yaml`; when it doesn't,
    say so in `plan/summary.md` (the operator adds a sentence saying so
    too). A plan that sets it with no new prompt, to another value, or on a
    Container the Brief didn't make, is refused.

## The plan's format

The operator reads your files strictly: a field it doesn't know refuses the
plan. These examples are the whole format; a test runs them through the same
check your plan gets, so they are right. Copy their shape, and take the names
of Frameworks, Standards, Computes, Deployments, Connectors and the Git
provider from `model/catalog.yaml` and `brief.md`, not from here.

`plan/model.yaml` holds one document per object, split by `---` lines. Every
document has `apiVersion: c4.factory.io/v1alpha1`, a `kind`, a
`metadata.name` and a `spec`. This one has every kind and every field a plan
may write:

```yaml plan/model.yaml
apiVersion: c4.factory.io/v1alpha1
kind: Project
metadata:
  name: shop
spec:
  displayName: Shop
  description: Everything the shop's team builds.
---
apiVersion: c4.factory.io/v1alpha1
kind: Person
metadata:
  name: shopper
spec:
  displayName: Shopper
  description: Someone who buys from the shop.
---
apiVersion: c4.factory.io/v1alpha1
kind: SoftwareSystem
metadata:
  name: shop
spec:
  displayName: Shop
  description: Sells things online.
  needs:
    - Shoppers find a product and order it.
  kpis:
    - Orders per day
---
apiVersion: c4.factory.io/v1alpha1
kind: Container
metadata:
  name: web
spec:
  displayName: Web shop
  description: The pages shoppers order from.
  softwareSystemRef: {name: shop}
  type: static-site
  gitProviderRef: {name: gitea}
  frameworkRef: {name: react-site}
  standardRef: {name: web-standard}
  checks: BeforeRelease # only when a new prompt asks (rule 16)
---
apiVersion: c4.factory.io/v1alpha1
kind: Container
metadata:
  name: api
spec:
  displayName: Orders API
  description: Takes and keeps the orders.
  softwareSystemRef: {name: shop}
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: go-service}
  computeRef: {name: k8s}
  deploymentRef: {name: helm}
---
apiVersion: c4.factory.io/v1alpha1
kind: DataStore
metadata:
  name: orders-db
spec:
  displayName: Orders database
  description: Every order.
  softwareSystemRef: {name: shop}
  connectorRef: {name: postgres}
---
apiVersion: c4.factory.io/v1alpha1
kind: Relationship
metadata:
  name: shopper-uses-web
spec:
  source: {kind: Person, name: shopper}
  target: {kind: Container, name: web}
  description: Orders products
---
apiVersion: c4.factory.io/v1alpha1
kind: Relationship
metadata:
  name: web-calls-api
spec:
  source: {kind: Container, name: web}
  target: {kind: Container, name: api}
  description: Sends orders
  bundles: false
---
apiVersion: c4.factory.io/v1alpha1
kind: Relationship
metadata:
  name: api-keeps-orders
spec:
  source: {kind: Container, name: api}
  target: {kind: DataStore, name: orders-db}
  description: Keeps orders
```

What each kind's `spec` takes, and nothing else:

- Project and Person: `displayName`, `description`.
- SoftwareSystem: those two, `needs` and `kpis`, each a list of strings.
- Container: those two, `softwareSystemRef`, `type` (one of `static-site`,
  `service`, `job`, `worker`, `mobile-app`, `cli`, `extension`, `operator`,
  `function`),
  `gitProviderRef` and `frameworkRef`, and when you choose them
  `standardRef`, `computeRef` and `deploymentRef`, and `checks`
  (`EveryChange` or `BeforeRelease`) only as rule 16 says. Every `...Ref` is
  `{name: ...}` alone.
- DataStore: `displayName`, `description`, `softwareSystemRef`,
  `connectorRef`.
- Relationship: `source` and `target`, each `{kind: ..., name: ...}` naming a
  Person, SoftwareSystem, Container or DataStore of the plan; `description`;
  and `bundles: true` only when the source Container ships a pinned release
  of the target Container. A Relationship has no technology field: say how
  in its `description`.

A `displayName` is at most 100 characters and a `description` at most 1000
(a Relationship's at most 200); `needs` and `kpis` have at most 20 items each.

An issue, such as `plan/issues/01-web.md`:

```markdown plan/issues/01-web.md
---
title: Show the products
labels: [kind/feature]
---
Shoppers see what the shop sells on the first page.

## Acceptance criteria

- [ ] The first page lists every product with its name and price
- [ ] A product without a price shows "Ask us"
```

An issue that builds on another's work names it in `after:`. Here the web
shop's order button needs the service to take orders first:

```markdown plan/issues/02-api.md
---
title: Take an order
labels: [kind/feature]
---
The service keeps an order a shopper places.

## Acceptance criteria

- [ ] Posting a product and an amount to /orders keeps the order
- [ ] An order for a product that doesn't exist is refused with the reason
```

```markdown plan/issues/03-web.md
---
title: Order from the product page
labels: [kind/feature]
after: [2]
---
Shoppers order a product from its page, through the service's /orders.

## Acceptance criteria

- [ ] Each product has an Order button that places the order
- [ ] The page says when the order is taken, or why it was refused
```

A person's work is its own issue, labelled `kind/by-hand`. Here the
README's screenshot of the order page, which a person takes once the button is
built:

```markdown plan/issues/04-web.md
---
title: Screenshot the order page for the README
labels: [kind/docs, kind/by-hand]
after: [3]
---
The README shows the product page with its Order button, as a shopper sees it.

## Acceptance criteria

- [ ] The README has a screenshot of a product page in a browser, with its Order button
```

An issue that builds on another app's released code names it in `uses:`.
In a CEL rule checker, the engine `cel-lens-core` is a Go library and
`cel-lens-cli` a command line on it; `plan/model.yaml` has a Relationship
from `cel-lens-cli` to `cel-lens-core`, and `releases.yaml` names
`cel-lens-core`:

`plan/issues/01-cel-lens-core.md`:

```markdown
---
title: Check a rule
labels: [kind/feature]
---
The engine checks a CEL rule and reports what is wrong with it.

## Acceptance criteria

- [ ] Check returns no problems for a valid rule
- [ ] Check reports a syntax error with its position
```

`plan/issues/02-cel-lens-cli.md`:

```markdown
---
title: Check a file from the command line
labels: [kind/feature]
uses: [cel-lens-core]
---
`cel-lens check <file>` checks each rule in the file with the engine's Check.

## Acceptance criteria

- [ ] A file of valid rules exits 0
- [ ] A broken rule is printed with its line and the engine's message
```

The CLI's issue is readied once `cel-lens-core`'s release, say `v0.1.0`, has
published `c4factory.local/<project>/cel-lens-core`, and its body then says:

```
Uses c4factory.local/<project>/cel-lens-core v0.1.0: go get it at that version.
```

`plan/releases.yaml`, releasing the web shop, which the example's issue is
for:

```yaml plan/releases.yaml
containers: [web]
```

`plan/choices.md`:

```markdown plan/choices.md
- A static site for the shop's pages, since shoppers only read them and order.
- A Go service keeps the orders, so the pages stay simple.
- Postgres keeps the orders, because the catalog's Postgres connector fits the service.
- The web shop ships releases, so each version of its pages is tagged and has a changelog.
```
