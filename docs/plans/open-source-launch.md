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
| Slack, Linear, Jira | #61 | | a second ChatHost, two more IssueTrackers |
| Alerts file bugs | #64 | | `AlertSource`, Alertmanager and Sentry |
| The factory's own numbers, published | #66 | #59 helps | the report with each release |

## Progress

As of 2026-11-10. The issues hold the live state; this is the short of it.

| Step | State |
| --- | --- |
| #48 issue forms, pull request template, CODEOWNERS | done (#67) |
| #65 docs/comparison.md | done (#67) |
| #47 the licence | done (#71): Apache-2.0 |
| #55 contributor and governance docs | done (#67, #71) |
| #63 work on a timer | done (#71); the rest is #72, whose UI part is done |
| #70 the flaky cost spec | done (#71) |
| #59 Prometheus metrics, the dashboard and alerts | done (#67, #71) |
| #53 a first run in ten minutes | `hack/quickstart.sh` and README's first screen are in; open: the guide cut into pages, the time measured, a run on a real machine |
| #49 fork pull requests on hosted runners | the rule is in (#67); open: a run from a real fork, the approval setting |
| #56 private links and logins | the links are gone (#67); open: the guide without `gh`, once the packages are public |
| #52 history and secrets | scanned clean, gitleaks on every pull request (#69); open: the publication itself, the owner's step 1 |
| #51 signing, SBOM, scanning | Dependabot is in (#71); open: image and chart signatures, SBOM, provenance, Scorecard, CodeQL |
| #50 a release from this repository | v0.32.0 is bumped on main and not tagged: the tag push is the owner's step 5 |
| #57 GitHub as a Git host | planned ([github-host.md](github-host.md)), nine steps filed as #74 to #82; steps 1 (#74) and 3 (#76) are built |
| #62 a Crew's approval rules | step 1 is built ([approval-rules.md](approval-rules.md)): the rules, the wait and the merge; open, as #92: the chat thread, the reviewer's `Risk: high`, the split by who approved |
| #60 bring a repository you already have | planned ([adopt-existing.md](adopt-existing.md)), six steps; steps 1 and 2 (#95) are built: an adopting Container needs nothing from the catalog, and a Crew works on it once main requires the repository's own checks; open: #96 to #99 |
| #54, #58, #61, #64, #66, #73 | open |

## For the owner

Every decision in this plan is made. What is left for the owner is what no
session can reach: a setting, an account, a force-push. Each is one step.

1. **Publish.** From a fresh clone, rewrite the author address and push,
   then make the repository public (*Settings → General → Change
   visibility*):

   ```bash
   git clone https://github.com/JairJosafath/theos theos-public && cd theos-public
   printf '%s\n' 'JairJosafath <58106977+JairJosafath@users.noreply.github.com> <OLD-ADDRESS>' >../mailmap
   git filter-repo --mailmap ../mailmap --force
   git remote add origin https://github.com/JairJosafath/theos
   git push --force --all origin && git push --force --tags origin
   ```

   `OLD-ADDRESS` is the address `git log --format=%ae | sort -u` shows that
   isn't a noreply one. Every open branch and pull request must be merged
   or closed first: a rewrite orphans them.
2. **Make the GHCR packages public** (each package has its own visibility:
   the chart and the thirteen images under `ghcr.io/jairjosafath/theos`).
3. **Settings when public:** private vulnerability reporting on;
   Discussions on, for questions; Actions' "Require approval for all outside
   collaborators"; the main ruleset's required checks as
   `.github/required-checks` lists them.
4. **Register on bestpractices.dev** (docs/openssf.md, "For the owner") and
   answer its two questions about the owner's own knowledge.
5. **Push a release tag when a session can't** (#50): the session that cut
   v0.32.0 was refused the tag push, and the issue has the two commands.

## Done when

Every step's issue is closed or decided against with the reason on it, the
repository is public under an OSI licence with a release a stranger
installed from README's first screen, and [docs/comparison.md](../comparison.md)
has no "not yet" in theos's column for something Warp ships.
