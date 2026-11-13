# Open-source launch, and the open alternative to Warp's factory

Parent issues: #45 (the launch) and #46 (the comparison). Written 2026-11-09
in a session the owner ran, with their words for it: "Create issues to improve
theos, we are moving towards opensourcing it. Keep in mind that we want to
compete with warp.dev that also has software factories."

## Goal

Two outcomes, in this order:

1. **The repository can go public.** A person who has never seen theos gets
   from the public README to a running factory, and from a fork to a merged
   pull request, with nothing but what the repository says. Nothing in the
   tree or its history is the owner's alone, and nothing a stranger's pull
   request runs can reach the owner's machines.
2. **theos is the open choice against Warp.** Where Warp's factory is ahead
   the gap has an issue and closes; where theos leads, the README says so
   with evidence.

## Where we stand (2026-11-09, main at 0d49b3a)

The factory runs the whole loop, triage to monitor, on a Kind cluster, and
develops itself. What was found for the launch is on #45, and against Warp
on #46 and in [docs/comparison.md](../comparison.md). In short:

- A proprietary `LICENSE` (Apache-2.0 since #47); contributor docs about the retired relay; one issue
  form, for "devices"; self-hosted runners that a fork's pull request would
  use; links to the private `c4factory`; no tag or release in this
  repository; only `SHA256SUMS` signed; a first run that takes a cluster, a
  1,410-line guide and a Claude token.
- Against Warp: Gitea only, Claude Code only, Matrix only, adoption as a
  footnote, no Prometheus metrics, a person decides at the spec and the
  deploy and nowhere between, nothing runs on a timer, and "monitor" is the
  deploy's health alone.

## Decisions, and why

1. **The licence is Apache-2.0** (#47 has the reasons: it is what
   Kubernetes and the projects theos plugs into use, it carries a patent
   grant, and companies run it without a legal review that AGPL would
   need). The owner handed the choice to the session that wrote this plan
   on 2026-11-09 ("no decisions for me, you do everything"). Contributions
   come in under the same licence, with a DCO sign-off and no contributor
   agreement.
2. **The history is published, with the author's address rewritten.** The
   scan of every ref found no credential (#52). 2,526 commits carry the
   owner's personal email address as author, and the owner had already
   moved to GitHub's noreply address for the later ones, so the public
   history carries only that: one `git filter-repo --mailmap` pass over a
   fresh clone, pushed to the repository in the same step that makes it
   public. That rewrites every commit id, so the plans' short ids (rare,
   and only in history) stop resolving; issue and pull request numbers,
   which the docs cite everywhere, keep working. The private `c4factory`
   stays the archive. The pass is a force-push to main, which no session
   can do: it is the first of the owner's steps below.
3. **Numbers from c4factory become plain text,** `c4factory #1094`, not
   links: the old repository stays private as the archive, so a link is a
   404 for every reader but the owner (#56).
4. **A fork's pull request runs on GitHub's hosted runners, always** (#49).
   The owner's devices never run a stranger's code; the cost is a slower
   first check for outside contributors.
5. **"Device" stays in the code and the history, and leaves the pages a
   newcomer reads first** (#55): README, CONTRIBUTING and the issue forms
   say "install" or "user".
6. **GitHub as a Git host comes first among the features** (#57). It is the
   gap that decides whether a team can try theos at all, and it makes the
   host ports the first with two real adapters. Harnesses (#58) are second:
   an open factory that runs one vendor's agent is not open where it counts.
7. **The comparison is a page in the repository, with sources and a date**
   (#65), rechecked at each minor release. It claims nothing about Warp that
   Warp's own pages don't say.
8. **theos does not become a hosted product to match Warp's first-run
   time.** It closes that gap with a one-command local run on the stand-in
   harness (#53) and with GitHub in place (#57), and keeps "everything runs
   on your cluster" as the difference.

## Steps

Each step is its own issue and lands on its own with `make check`. The
owner's acts are listed under *For the owner*.

### The launch (#45)

| Step | Issue | Needs | Done when (short) |
| --- | --- | --- | --- |
| Issue forms, pull request template, CODEOWNERS | #48 | | the forms and files exist and intake still labels |
| Docs without private links and logins | #56 | | no c4factory link outside history; a check keeps it so |
| Contributor docs without the relay | #55 | | CONTRIBUTING takes a fork to a merge; ROADMAP is current |
| Fork pull requests on hosted runners | #49 | | the `runs-on` rule, checked by `hack/check-workflows.sh` |
| A release from this repository | #50 | | a tag, the images, the chart and the GitHub release |
| The licence | #47 | | Apache-2.0: the text, the headers, the manifests |
| History and secrets scan | #52 | | the report, the list for the owner |
| Signed images, SBOM, provenance, scanning | #51 | #50 | `cosign verify` in the guide; Scorecard, CodeQL, updates |
| A first run in ten minutes | #53 | #50 | `hack/quickstart.sh`; README's first screen |
| macOS for `./dev` | #54 | | `./dev setup --lite` and `make check` on a Mac |

### The comparison (#46)

| Step | Issue | Needs | Done when (short) |
| --- | --- | --- | --- |
| docs/comparison.md | #65 | | the page, linked from README |
| Prometheus metrics and a dashboard | #59 | | the series, the chart's dashboard |
| GitHub as a Git host | #57 | | the adapter passes the host contracts |
| Harnesses and models | #58 | an owner's session (protectedPaths) | the interface, Codex, Gemini CLI, OpenCode |
| Approval rules on a Crew | #62 | | `spec.approval`, a person's review merges |
| Recurring Tasks | #63 | | `spec.schedules` files issues on time |
| Bring your own repository | #60 | better with #57 | `adopt` proposes a model; no Standard needed |
| Slack, Linear, Jira | #61 | | a second ChatHost, two more IssueTrackers ([requests-and-chat.md](requests-and-chat.md)) |
| Alerts file bugs | #64 | | `AlertSource`, Alertmanager and Sentry |
| The factory's own numbers, published | #66 | #59 helps | the report with each release |

## Progress

As of 2026-11-10, but for a row that names a later day. The issues hold the
live state; this is the short of it.

| Step | State |
| --- | --- |
| #48 issue forms, pull request template, CODEOWNERS | done (#67) |
| #65 docs/comparison.md | done (#67) |
| #47 the licence | done (#71): Apache-2.0 |
| #55 contributor and governance docs | done (#67, #71) |
| #63 work on a timer | done (#71), and the rest with #72, closed on 2026-11-13 ([crew-schedules.md](done/crew-schedules.md), six steps): the UI lists the schedules, a run that finds nothing to do closes its issue instead of failing, a person runs a schedule now, a Standard proposes schedules, and a Crew's page offers them for a person to take |
| #70 the flaky cost spec | done (#71) |
| #59 Prometheus metrics, the dashboard and alerts | done (#67, #71) |
| #53 a first run in ten minutes | `hack/quickstart.sh` and README's first screen are in; the script asks for the release's chart before it makes anything, and says the two reasons when it can't pull it (no such release, private packages); open: the guide cut into pages, the time measured, a run on a real machine |
| #49 fork pull requests on hosted runners | done: the rule is in (#67), and since the repository is public every pull-request workflow has run on GitHub's hosted runner (*Tests* in 21 minutes, run 42806713504). Not tried: a pull request from a real fork. The approval setting is the owner's step 3 |
| #56 private links and logins | the links are gone (#67), the guide clones with `git`, `SECURITY.md` names the report button and the owner's profile, and the sample, the planner's example and the UI's comments use `acme/cel-lens`; open: the guide's `gh` login for GHCR, until the packages are public (the owner's step 2) |
| #52 history and secrets | scanned clean, gitleaks on every pull request (#69); the repository is public since 2026-11-10 |
| #51 signing, SBOM, scanning | Dependabot is in (#71), with the Kubernetes libraries and the Go and Node versions left to us; CodeQL scans the Go code and the UI; open: image and chart signatures, SBOM, provenance, Scorecard |
| #50 a release from this repository | v0.32.0 is bumped on main and not tagged: the tag push is the owner's step 5 |
| #57 GitHub as a Git host | planned ([github-host.md](github-host.md)), nine steps filed as #74 to #82; steps 1 (#74) and 3 (#76) are built |
| #62 a Crew's approval rules | done ([approval-rules.md](done/approval-rules.md); #93, #105): the rules, the wait and the merge; a waiting change's thread in chat; the triager's `risk/high` label; the reviewer raises a change to a person, never lowers; merged Tasks split by who approved |
| #60 bring a repository you already have | planned ([adopt-existing.md](adopt-existing.md)), six steps; steps 1 to 3, 5 and 6 (#95, #96, #98, #99) are built: an adopting Container needs nothing from the catalog, a Crew works on it once main requires the repository's own checks, `theos-session adopt` proposes the model, the Crew's first issue there writes `AGENTS.md`, and [docs/adopt.md](../adopt.md) walks it; open: #97 (a Standard as a pull request) |
| #61 Slack, Linear and Jira | planned on 2026-11-13 ([requests-and-chat.md](requests-and-chat.md)): thirteen steps, none filed or started; the two ports' contract suites are merged (#131); nothing else is built |
| #58 more harnesses and models | planned ([harnesses.md](harnesses.md)), eight steps; step 1 (the harness table in the runner) and the proxy's part of step 2 (named upstreams) are built; open: the chart's upstreams, the price table, and the `codex` harness, which no session has run |
| #66 the factory's own numbers, published | planned ([results-report.md](results-report.md)), four steps; on 2026-11-13 steps 1 and 2 are built: the report's rules in Go with a fixture the UI's numbers are held to, and `theos-session report`, which writes a release's JSON and its page, the page the release notes link; open: the first report, which a person runs on the owner's cluster, and README's numbers |
| #73 scaffolded Node packages' license | done (#84) |
| #54, #64 | open |

## For the owner

Every decision in this plan is made. What is left for the owner is what no
session can reach: a setting, an account, a tag. Each is one step.

1. ~~Publish.~~ Done 2026-11-10: the repository is public, with its
   history as it was.
2. **Make the GHCR packages public**, after step 5: the first release
   from this repository makes them, private (each package has its own
   visibility: the chart and the thirteen images under
   `ghcr.io/jairjosafath/theos`). Until then nobody but the owner can
   install a release, and `hack/quickstart.sh` says so and stops.
3. **Settings, now it is public:** private vulnerability reporting on;
   Discussions on, for questions; Actions' "Require approval for all outside
   collaborators"; the main ruleset's required checks as
   `.github/required-checks` lists them.
4. **Register on bestpractices.dev** (docs/openssf.md, "For the owner") and
   answer its two questions about the owner's own knowledge.
5. **Push a release tag when a session can't** (#50): the session that cut
   v0.32.0 was refused the tag push, and the issue has the two commands.
6. ~~Keep Actions able to start jobs.~~ Over since the repository is
   public: its hosted minutes are free, and jobs started again at 08:27 UTC.
   What happened: on 2026-11-10 at 07:14 UTC every
   run in the repository began to fail within three seconds, with no runner
   and no log, on main and on every pull request, while githubstatus.com
   reported Actions as operational. The jobs ask for `ubuntu-latest` and the
   repository is private, so the likeliest cause is the account's Actions
   minutes or spending limit (*Settings → Billing and plans*) after a day
   of long *Tests* runs. Nothing merged while checks couldn't run.

## Done when

Every step's issue is closed or decided against with the reason on it, the
repository is public under an OSI licence with a release a stranger
installed from README's first screen, and [docs/comparison.md](../comparison.md)
has no "not yet" in theos's column for something Warp ships.
