# Bring a repository you already have

GitHub issue #60, a step of [open-source-launch.md](open-source-launch.md)
(#46). Written 2026-11-10 in a session the owner ran.

## Goal

A person with a repository that works today puts a Crew on it in an
afternoon, without rewriting it to the factory's taste first: no Framework it
was never scaffolded from, no Standard whose every file must match, its own
check command and its own CI. Most teams that look at a software factory
have code already, and Warp's offer starts there.

## Where we stand (main at 39fe7fa)

Read from the code, with what was run on #60:

- `Container.spec.adopt: true` takes over a repository with commits and
  writes nothing. It checks main for `.theos.yaml` naming this project and
  Container, and for every file the Container's Standard renders
  (`adopt()` in `internal/controller/container_scaffold.go`).
- **A Container can't adopt without a Framework.** The CRD's rule is "name a
  bundleRef or a frameworkRef", though adoption renders nothing of the
  Framework. Without that rule the reconciler would panic twice: it takes
  `res.entries[0]` for the Framework (`container_controller.go`, the
  self-deploy check, and `adopt()`'s last line).
- docs/design.md says "a Container without a Standard adopts on the
  identity alone". No spec holds that.
- **No agent works on a Container without a Standard.**
  `model.Protected(hasStandard)` decides both that main is protected
  (`protectRepository`) and that a Task may start: "agents work only on
  protected repositories". A Crew on such a Container is Ready, starts
  Tasks, and each fails at once.
- The check is fixed: `make check` in the agent's workspace
  (`model.CheckCommand`), and a status whose context matches `check*` on the
  default branch's rule (`model.CI`). theos-agent reads
  `THEOS_CHECK_COMMAND`; the operator never sets it. A Task waits for the
  commit's combined status, whatever its contexts are called.
- The UI's types already take a Container with no Framework.

## Decisions, and why

1. **An adopting Container needs nothing from the catalog.** The rule
   becomes "name a bundleRef or a frameworkRef, unless the Container
   adopts". The factory renders nothing into an adopted repository, so a
   Framework there says nothing true; a Rust service has none to name. A
   Container may still name one, and a Standard, as today.
2. **The repository's own check is named on the Container,** for a Container
   without a Standard: `spec.checkCommand` (default `make check`), what the
   agent's workspace runs and the agent is told to run, and
   `spec.checkContexts` (default the CI system's, `check*`), the status
   contexts main requires. A Standard brings its Makefile and its workflow,
   so CEL refuses both fields beside one. What depends on how a user's
   repository is built is a setting, never the factory's pick.
3. **Agents work on an adopted repository only once main is protected by
   checks that exist.** The fence's rule stays: no agent where the Git host
   doesn't enforce the checks. For a Container that adopts with no Standard
   the operator reads main's head: when a status there matches
   `checkContexts`, it protects main (those contexts, the platform's paths,
   release tags) and the Container gets `Protected=True`; when none does,
   it protects nothing and says so, `Protected=False`, reason
   `NoMatchingCheck`, with the contexts the head does report, so the person
   can name theirs. A pattern nothing reports would block every merge, the
   people's too; so the operator never requires one. A Crew on a Container
   that isn't protected starts no Task and says why, instead of failing
   three Tasks an issue.
4. **The CI system's workflow files stay out of an agent's reach, wherever
   the host reads them.** Gitea Actions reads `.github/workflows/` when the
   repository has no `.gitea/workflows/`, which is what a repository that
   came from GitHub has. For a Container that adopts with no Standard both
   are protected paths.
5. **The identity file is the person's to commit.** `.theos.yaml` on main,
   naming the project and the Container, is how a repository's owner says
   "this factory may work here"; the operator still writes nothing at
   adoption. What it must hold is printed for them (step 3).
6. **`theos-session adopt <git url>` proposes; a person applies.** It reads
   a clone: the language and build files, the check command it finds, the
   CI workflows and the contexts they report, a Dockerfile. It prints the
   model as YAML (SoftwareSystem, Container with `adopt: true`, Crew), the
   `.theos.yaml` to commit, and what it could not tell. The detector is a
   function in `internal/model`, table-tested for Go, Node and TypeScript,
   and Python. It names a Framework or a Standard only when asked to.
7. **A Standard is something an adopted repository grows into.** Naming one
   whose files differ opens the operator's pull request with the
   difference, as a bump of the Standard's ref does, instead of failing
   `StandardFilesDiffer`: the person reviews the files, and the repository
   keeps working meanwhile.
8. **The first issue on an adopted repository is the Crew's own:** "Write
   AGENTS.md for this repository, and say what you found", filed once,
   labelled `kind/onboarding`. Its pull request adds the guide every later
   agent reads (how to build, test and run it, where things are), and its
   description lists what is flaky or missing. A person sees the factory
   read their code before it changes any.
9. **Where the repository lives.** Until `provider: github` (#81) the
   repository is pushed to the project's organization on the factory's
   Gitea, and the guide says how and what that means for its history and
   its CI. With #81 it is adopted in place.

## Steps

Each step is its own issue, names #60 and lands with `make check`.

| Step | Issue | Depends on | Done when |
| --- | --- | --- | --- |
| 1. An adopting Container needs nothing from the catalog (done) | #60 | | the CRD takes it; it goes Ready on the identity alone, with and without a Framework, in envtest; a Container that doesn't adopt still needs a Framework |
| 2. The repository's own check, and the fence on it (done) | #95 | 1 | `checkCommand`, `checkContexts`, the `Protected` condition; envtest: a Task runs the named command on a protected adopted repository; a Crew waits on one that isn't |
| 3. `theos-session adopt` (done) | #96 | 2 | the detector's table tests; the YAML it prints passes `make validate-yaml` |
| 4. A Standard that differs arrives as a pull request (done) | #97 | 1 | envtest: adopting with a Standard whose files differ opens the pull request and the Container is Ready |
| 5. The Crew's first issue: AGENTS.md and what it found (done) | #98 | 2 | envtest: one `kind/onboarding` issue per adopted Container, never a second |
| 6. The guide's second page (done) | #99 | 2, 3 | "Bring a repository you already have" follows the quickstart, and follows what the specs show |

## What step 1 found

- With no catalog entry at all `res.entries` is empty: the self-deploy check
  and `adopt()` took `entries[0]` for the Framework. Both now ask for the
  Framework's entry and get none.
- The spec that held "naming neither a bundleRef nor a frameworkRef is
  refused" keeps holding it for a Container that doesn't adopt.
- A Connector that names the Frameworks that wire it (`wiredBy`) refused a
  Container with none, in words about a Framework with no name. The factory
  renders no Connector into a repository it never scaffolded, so a Container
  with no Framework passes that check (`model.CheckWiring`).
- "A Container without a Standard adopts on the identity alone" had no
  spec; it has one now, with a Framework and without.
- The adopting Container is Ready and unprotected, and a Task on it still
  fails "has no Standard": step 2 is what lets a Crew work there.

## What step 2 found

- A Task waits for the commit's combined status, whatever its contexts are
  called, so only two things needed the Container's contexts: main's rule
  and the reading of main's state before round 1 (`BaseState`), and the
  merge that skips main's push run.
- The probe sits where a Ready Container already reads main's checks
  (`refreshChecks`), so it costs no call of its own, and a Container is
  protected at its first reading after adoption, not in `adopt()`.
- A check that failed still protects: the rule is about what reports, and a
  red main holds round 1 as on any Container (`BaseRed`).
- A CEL rule with `\s` in a quoted string doesn't compile, and envtest says
  so only when it installs the CRDs; `checkCommand`'s rule is
  `startsWith`/`endsWith`.
- The full suite found what the focused specs didn't: a unit test builds a
  sandbox with no Container, so the check command is a run input of its
  own, as the registries are.
- Once protected, an adopted repository stays so in the Container's eyes: a
  head with no result yet, as after every merge until its run starts, says
  nothing against the rule, and reading it as "no matching check" would
  stop the Crew after each of its own merges.
- A Crew's triager waits with the rest: a triage pushes nothing, but its
  clone's token could, and main takes pushes until it is protected. A
  Triage Task made by hand on such a Container still runs, as on any
  Container without a Standard.
- Left as it is: the words "`make check` fails on main too" in a round's
  note name the default command even when the Container names another.

## What step 3 found

- The command reads a clone, `--dir`, not a URL: the person has one, and a
  command that clones needs credentials the factory shouldn't ask for
  twice. It lists what Git tracks, or every file when the folder is no
  clone.
- The detector reads five files at the root and the workflows, nothing
  else. Where workflows are comes from the CI systems the factory knows
  (`model.WorkflowDirs`), and a host that has its own reads no others, as
  the host does.
- A check's pattern is `<workflow name> / <job name or id>*`: the job on
  every event and for every matrix entry. A workflow without a name is left
  out with a note, since the host names its checks after its file in a way
  the detector can't know; so is a job whose name is an expression.
- What it can't tell is a note in the proposal, never a guess: no language,
  no kind of application (the proposal says `service`, the type being
  required, and says to change it), no check, no workflow that runs on a
  pull request.
- `--git-provider` has no default: the name is the user's.
- The proposals of eight repositories are files under
  `config/samples/adopt/proposed/`, which `make validate-yaml` holds to the
  CRDs; the table test fails when the detector's output leaves them.

## What step 4 found

Choices the issue left open, and why:

- **A person merges the pull request; the operator never does.** A bump's
  pull request changes files the factory wrote, so the operator merges it
  once CI passes. This one may replace a Makefile and a workflow the
  repository's people wrote, in a repository whose main has no rule yet,
  so it waits on them: the reason is `PullRequestOpen`, not `Updating`,
  whose message promises a merge.
- **The Ready Container's pass opens it, not `adopt()`.** `adopt()` only
  finds that no render matches, picks the Domains of the closest one and
  goes Ready with no Standard recorded. "Adopts its repository, names a
  Standard, records none" is then the whole trigger
  (`awaitsStandard`), so a Standard named after a bare adoption arrives
  the same way, where it used to say `AddedAfterScaffold`.
- **Its state is `status.standardAdoption`,** a field of its own:
  `status.standard` means "these files are on main at this ref" to every
  reader (the checks, the Task's fence, the updates), and stays unset until
  that is true.
- **Merged and closed are judged differently.** After a merge only the
  files the Standard protects must be as rendered: a person may have kept
  their own README in the pull request, and that file is the repository's
  from then on, which later updates merge with. A protected file that
  differs is `StandardFilesDiffer`, the one case the reason stays for.
  After a close the person took nothing, so only a main that holds every
  file has the Standard.
- **A stop is read again when main moves,** one read of main's head a
  pass, so files brought by hand are found without an annotation; a change
  of the Standard's ref or the Container's checks plans a new pull request.
  An open one stays at its ref, and an ordinary update follows its merge.
- **The closest render gets Domains only when strictly closer than none.**
  A repository with none of the files ties on every set; rendering it with
  the system's Domains would point its guides at Domain files adoption
  never writes, and `DomainsApplied` would say they are there.
- **No agent works there meanwhile.** `model.Protected` took "names a
  Standard" for "main has checks"; the Task and the Crew now ask whether
  the Standard is on main (`standardOnMain`, `standardNotOnMain`) and wait
  with `ContainerNotProtected`, as on a bare adoption. The helpers sit
  beside the adoption, not in `protection.go`, which is the owner's; it
  would be their natural home.

Left as it is:

- The UI shows the condition's reason and message, and nothing of
  `status.standardAdoption`; `describe.ts` still describes
  `StandardFilesDiffer` as a failed adoption.
- A status write lost right after the pull request opened opens a second
  one on the same branch at the next pass, as a Standard update's does.
- A bare adoption that main protects by its own checks and then names a
  Standard keeps the old rule on the host while the pull request is open:
  a person with the rights merges it over the protected workflow paths.

## What step 5 found

- "Filed once" is the Crew's status plus the open issue's marker: the Git
  host's port lists open issues only, so a Crew made anew after its
  onboarding issue closed files another. That is the right answer to
  "delete the Crew and start over", and the issue then asks to check and
  complete the guide that is there.
- The issue needs nothing of its own in the loop: it has an *Acceptance
  criteria* checklist, so the Crew builds it, and "change no other file" is
  a criterion the reviewer holds, not a rule the runner enforces as for a
  spec.
- It waits for what every agent waits for: main protected, the Crew not
  paused.

## What step 6 found

- The page's order is the one the operator gives for free: apply the model,
  which has it make the empty repository in the project's organization and
  fail `NothingToAdopt`; push the history; retry. No call to the Git host's
  API is needed to make the repository, and a spec holds the order.
- The hard part of an adoption on the factory's own Git host is not the
  model, it is CI: a workflow written for another host's runners
  (`runs-on: ubuntu-latest`) never runs here. The page says so where the
  `Protected` condition does, and what to do; it is the strongest reason
  for `provider: github` (#57), where a repository's CI runs where it
  always has.
- The page says what no test holds: an adoption on a real cluster, and
  whether a person's workflow runs on the factory's runner images.
- It is a page of its own, `docs/adopt.md`, after the quickstart in README
  and at the head of getting started's "Next"; cutting the guide itself
  into pages is #53's.

## Done when

Step 6 is closed: a person follows the guide's second page with a
repository of their own and gets a merged pull request from an issue, and
[docs/comparison.md](../comparison.md) has no "poorly guided" for adoption.
