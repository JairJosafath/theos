# Plan: a better OpenSSF Scorecard for cel-lens

The owner, 9 November 2026: "Improve the security score." The score is the
OpenSSF Scorecard of JairJosafath/cel-lens, the badge on its README. Scorecard
measures GitHub `main`, so nothing here moves the number until the work is
merged in Gitea and published, and the front page is copied to the
repository's root.

## Where it stands (9 November, commit 7d4c292)

4.4 of 10. Seven checks score 10 (Dangerous-Workflow, Token-Permissions,
Security-Policy, Binary-Artifacts, Pinned-Dependencies, License, Fuzzing).
Nine score 0, and each weighs on the total:

| Check | Why 0 | Who can change it |
| --- | --- | --- |
| Vulnerabilities | 22 advisories in `engine/go.mod` and `vscode/package-lock.json` | this plan |
| SAST | no static analysis tool | this plan |
| Dependency-Update-Tool | none configured | this plan |
| Branch-Protection | `main` has none | the owner (repository settings) |
| Signed-Releases | release files carry no signature or provenance | a later plan, and the owner's key |
| Code-Review | the factory pushes each release's source; no pull request is approved on GitHub | the publishing model |
| Maintained | the repository is younger than 90 days | time |
| CII-Best-Practices | no badge | the owner (bestpractices.dev account) |
| Contributors | one organization | nothing to do |

Packaging and CI-Tests are not scored (no publishing workflow, no pull
request on GitHub).

## Goal

The three checks the code decides go to 10 with real fixes, not with files
that only look the part: about 6.4 once published. The owner has the exact
settings for the rest.

## Decisions

1. **Fix the vulnerabilities; ignore only what is shown not to apply.** The
   22 are:
   - **13 Go advisories:**
     - five HTTP/2 advisories in the standard library (fixed in Go 1.26.9);
     - two in `golang.org/x/net`'s HTML parser;
     - one each in `x/crypto` (openpgp), OpenTelemetry's OTLP exporter and
       `klauspost/compress`;
     - three for Kyverno.
   - **9 npm advisories,** all in development dependencies (Vite and its
     `launch-editor`, `qs`, `minimatch`, `@azure/msal-node` under
     `@vscode/vsce`).

   Each is fixed by moving to the fixed version, the Go ones with `go 1.26.9`
   in `go.mod`.

   The three Kyverno advisories have no fixed version in the Go vulnerability
   database, although their GitHub advisories end at 1.13.4 and 1.17.1 and
   the engine uses 1.19.x. They also describe the admission controller's
   outbound calls, which a language server never makes. They go in
   `engine/osv-scanner.toml`, the file Scorecard's own documentation names
   for this, each with that reason. `govulncheck` in the public CI proves
   that no vulnerable symbol is reachable, and fails when one becomes so.
2. **CodeQL on the public repository** for Go, TypeScript and the workflows
   themselves, on every push to `main`, every pull request and weekly. It
   reads the published source, which is what users install.
3. **Dependabot opens the update; the factory applies it.** GitHub's `main`
   only receives what the factory publishes, so a Dependabot pull request is
   never merged there. It is the signal, and CI runs on it: the bump is made
   in Gitea, the next publish carries it, and Dependabot closes its pull
   request by itself. `CONTRIBUTING.md` says so. Weekly, grouped, for Go
   modules in `engine/`, npm in `vscode/` and the workflows' actions. A
   configuration that opens nothing would only buy the check; this one keeps
   the dependencies moving.
4. **The front page comes back into Gitea.** The GitHub root (README,
   `.github/`, SECURITY and the rest) is `public-repo/` of cel-lens-vscode,
   kept out of the published source, and this session's Gitea was imported
   from that published source. It is put back from the GitHub root as it is,
   so the tests that guard it run again, and the new workflows are added
   there with those tests extended.
5. **Signed releases are a factory feature, with the user's key.** Release
   files are built by the factory's release workflow in Gitea and copied to
   GitHub. The honest fix is a signature over `SHA256SUMS` made in that
   workflow, with a key the user holds in a Secret (a setting, off by
   default), and the public key in the repository. A provenance file nobody
   can verify would score the same and mean nothing, so none is added. This
   is its own plan, in theos's `gitea-release` template; it waits for the
   owner to say where the key lives.
6. **Code review on GitHub is not chased.** Review happens in Gitea, by the
   reviewer agent, before a release exists. Publishing each release through
   a pull request the owner approves would raise two checks, and would add a
   human gate the design does not have; that is the owner's to want.

## Steps

1. cel-lens-core: no known vulnerability in the engine's dependencies
   (`go.mod`, `go.sum`, `osv-scanner.toml`), checked with osv-scanner and
   govulncheck.
2. cel-lens-vscode: no known vulnerability in the extension's dependencies
   (`package.json`, `package-lock.json`), checked with osv-scanner and
   `npm audit`.
3. cel-lens-vscode: `public-repo/` put back, then `codeql.yml`,
   `dependabot.yml`, a `govulncheck` job in `ci.yml`, the tests, and
   `CONTRIBUTING.md`.
4. Each is reviewed by a separate reviewer, opened as a pull request in
   Gitea on top of the open ones, and mirrored to JairJosafath/cel-lens
   branch `claude/zen-cori-vxzrxq` (the front page at the root).

Done when osv-scanner reports no vulnerability on the mirrored tree, the
workflows parse and are pinned, and the three pull requests are approved.

## What the owner does

1. **Merge and publish,** then copy `public-repo/` to the GitHub root (or
   merge the mirror branch's root files), and run the Scorecard workflow
   (Actions, Scorecard, Run workflow).
2. **Branch protection** on `main`, as a ruleset: block force pushes and
   deletion (safe for the factory's pushes, 3 of 10), and add the factory's
   token as a bypass actor before requiring pull requests or status checks.
3. **The best-practices badge:** register the project at
   bestpractices.dev; the README, SECURITY.md, CI, fuzzing and this plan
   answer most of its questions.
4. **A signing key** for releases, if decision 5 is wanted.
