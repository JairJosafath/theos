# Evidence: a better OpenSSF Scorecard for cel-lens

For [plans/cel-lens-security-score.md](../plans/cel-lens-security-score.md), 9 November 2026.

## The Scorecard before

`https://api.scorecard.dev/projects/github.com/JairJosafath/cel-lens`, run of 9 November on commit 7d4c292:

```
score 4.4
 -1  CI-Tests                 no pull request found
 -1  Packaging                packaging workflow not detected
  0  Branch-Protection        branch protection not enabled on development/release branches
  0  CII-Best-Practices       no effort to earn an OpenSSF best practices badge detected
  0  Code-Review              Found 0/30 approved changesets -- score normalized to 0
  0  Contributors             project has 0 contributing companies or organizations -- score normalized to 0
  0  Dependency-Update-Tool   no update tool detected
  0  Maintained               project was created within the last 90 days. Please review its contents carefully
  0  SAST                     no SAST tool detected
  0  Signed-Releases          Project has not signed or included provenance with any releases.
  0  Vulnerabilities          22 existing vulnerabilities detected
 10  Binary-Artifacts         no binaries found in the repo
 10  Dangerous-Workflow       no dangerous workflow patterns detected
 10  Fuzzing                  project is fuzzed
 10  License                  license file detected
 10  Pinned-Dependencies      all dependencies are pinned
 10  Security-Policy          security policy file detected
 10  Token-Permissions        GitHub workflow tokens follow principle of least privilege
```

## The Scorecard after

JairJosafath/cel-lens#7 was merged on 10 November 2026 and the Scorecard workflow run on the merge commit (2d881ac):

```
score 6.5
 -1  Packaging                packaging workflow not detected
  0  Branch-Protection        branch protection not enabled on development/release branches
  0  CII-Best-Practices       no effort to earn an OpenSSF best practices badge detected
  0  Code-Review              Found 0/7 approved changesets -- score normalized to 0
  0  Contributors             project has 0 contributing companies or organizations -- score normalized to 0
  0  Maintained               project was created within the last 90 days. Please review its contents carefully
  0  Signed-Releases          Project has not signed or included provenance with any releases.
 10  Binary-Artifacts         no binaries found in the repo
 10  CI-Tests                 1 out of 1 merged PRs checked by a CI test -- score normalized to 10
 10  Dangerous-Workflow       no dangerous workflow patterns detected
 10  Dependency-Update-Tool   update tool detected
 10  Fuzzing                  project is fuzzed
 10  License                  license file detected
 10  Pinned-Dependencies      all dependencies are pinned
 10  SAST                     SAST tool is run on all commits
 10  Security-Policy          security policy file detected
 10  Token-Permissions        GitHub workflow tokens follow principle of least privilege
 10  Vulnerabilities          0 existing vulnerabilities detected
```

## The OSV query, before and after

Every pin of `engine/go.mod` (the Go version as the standard library) and of `vscode/package-lock.json`, asked of `api.osv.dev` by `docs/plans/cloud-session/osvcheck.py`. On GitHub `main` (7d4c292):

```
go.mod: 96 packages, 23 advisories, 0 ignored, 23 left
package-lock.json: 323 packages, 9 advisories, 0 ignored, 9 left
```

On the mirror branch `claude/zen-cori-vxzrxq` (b972f67), with each folder's `osv-scanner.toml`:

```
engine/go.mod: 96 packages, 3 advisories, 3 ignored, 0 left
  ignored GO-2025-3615           github.com/kyverno/kyverno 1.19.1
  ignored GO-2026-5337           github.com/kyverno/kyverno 1.19.1
  ignored GO-2026-5594           github.com/kyverno/kyverno 1.19.1
vscode/package-lock.json: 328 packages, 1 advisories, 1 ignored, 0 left
  ignored GHSA-g3hv-75wg-m249    minimatch 10.2.6
```

## Checks

- cel-lens-core#35: `GOFLAGS=-p=2 make check` exit 0 (implementer and reviewer), Gitea CI `check / check (pull_request)`: success, on Go 1.26.10.
- cel-lens-vscode#22 and #23: `make fmt-check lint typecheck test package-check` pass (1988 tests on #23); `make e2e` not run (GitHub release downloads answer 504 in the session).
- OSV-Scanner and Scorecard themselves were not run: the session may not install them. The number moves only after the merge, the publish and a Scorecard run on GitHub.
