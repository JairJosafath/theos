# OpenSSF Best Practices: the passing criteria

1.0 needs the [OpenSSF Best Practices](https://www.bestpractices.dev/en/criteria/0)
passing criteria ([ROADMAP.md](../ROADMAP.md), "What 1.0 means"). This page
checks each criterion against the repository, with the evidence, as of
2026-10-15 (#1077). Each criterion gets one of these states:

- **Met**, with the evidence.
- **Gap**: an agent can close it; the follow-up issue is named.
- **Owner**: only the owner can close it (a setting, an account, the licence,
  making the repository public). The list for the owner is at the end.
- **N/A**: the criterion doesn't apply, and the reason is given.

MUST criteria decide the badge. SHOULD and SUGGESTED ones are listed too,
because the form asks for them.

## Basics

| Criterion | Level | State | Evidence |
| --- | --- | --- | --- |
| description_good | MUST | Met | [README.md](../README.md), its opening and *How it works* |
| interact | MUST | Met | [README.md](../README.md) *Quickstart*, *Develop the factory*, *Report an issue*, *Contributing* |
| contribution | MUST | Met | [CONTRIBUTING.md](../CONTRIBUTING.md) |
| contribution_requirements | SHOULD | Met | [CONTRIBUTING.md](../CONTRIBUTING.md): the commit format, tests and lint required, `make check` |
| floss_license | MUST | Met | [LICENSE](../LICENSE) is the Apache License 2.0 (#47); `hack/check-license-headers.sh` keeps every Go file's header |
| floss_license_osi | SUGGESTED | Met | Apache-2.0 is OSI-approved |
| license_location | MUST | Met | `LICENSE` at the root |
| documentation_basics | MUST | Met | [docs/getting-started.md](getting-started.md), [docs/design.md](design.md), [docs/architecture.md](architecture.md) |
| documentation_interface | MUST | Met | the CRDs' field descriptions (`config/crd/bases/`, `kubectl explain`), the model in [docs/design.md](design.md), the chart's `values.yaml` |
| sites_https | MUST | Met | GitHub, `ghcr.io`, all over HTTPS |
| discussion | MUST | Met | GitHub issues are searchable, addressable and need no proprietary client, and the repository is public |
| english | MUST | Met | every document, issue and pull request is in English |
| maintained | MUST | Met | the relay and the maintainer ([GOVERNANCE.md](../GOVERNANCE.md)); releases every few days (v0.23.0, 2026-10-13) |

## Change control

| Criterion | Level | State | Evidence |
| --- | --- | --- | --- |
| repo_public | MUST | Met | <https://github.com/JairJosafath/theos>, public since 2026-11-10 |
| repo_track | MUST | Met | Git: who changed what and when, pull requests with `Closes #N` |
| repo_interim | MUST | Met | every pull request merges to main between releases |
| repo_distributed | SUGGESTED | Met | Git |
| version_unique | MUST | Met | tags `vX.Y.Z` ([ROADMAP.md](../ROADMAP.md), *Releases are a train*) |
| version_semver | SUGGESTED | Met | SemVer, `0.y.z` before 1.0 |
| version_tags | SUGGESTED | Met | each release is a Git tag |
| release_notes | MUST | Met | each GitHub release has Keep a Changelog notes, drafted by `hack/release-notes.sh` |
| release_notes_vulns | MUST | Met | the notes' *Security* section; [SECURITY.md](../SECURITY.md) says a fixed vulnerability is listed with its advisory and CVE. None has been reported yet |

## Reporting

| Criterion | Level | State | Evidence |
| --- | --- | --- | --- |
| report_process | MUST | Met | the *user report* template, [CONTRIBUTING.md](../CONTRIBUTING.md) |
| report_tracker | SHOULD | Met | GitHub issues |
| report_responses | MUST | Met | the maintainer answers each report ([GOVERNANCE.md](../GOVERNANCE.md)); triage runs on every relay pass |
| enhancement_responses | SHOULD | Met | as above; triage readies or answers every `status/proposed` issue |
| report_archive | MUST | Met | the GitHub issues are the archive, public with the repository |
| vulnerability_report_process | MUST | Met | [SECURITY.md](../SECURITY.md), linked from [CONTRIBUTING.md](../CONTRIBUTING.md) |
| vulnerability_report_private | MUST | **Owner** | [SECURITY.md](../SECURITY.md) names GitHub private vulnerability reporting; the owner turns it on in the repository's settings when it goes public |
| vulnerability_report_response | MUST | Met | [SECURITY.md](../SECURITY.md): an answer within 3 business days, never later than 14. No report yet |

## Quality

| Criterion | Level | State | Evidence |
| --- | --- | --- | --- |
| build | MUST | Met | `make build`, `make docker-build`, the *Release* workflow |
| build_common_tools | SUGGESTED | Met | Go, Make, Docker, Helm |
| build_floss_tools | SHOULD | Met | the same, all FLOSS |
| test | MUST | Met | `make test` (unit and envtest), `make test-gitea`, documented in [docs/dev/testenv.md](dev/testenv.md) |
| test_invocation | SHOULD | Met | `make test`, `make check` |
| test_most | SUGGESTED | Met | business rules in table tests, every reconciler in envtest ([AGENTS.md](../AGENTS.md), *Test the rules where they live*) |
| test_continuous_integration | SUGGESTED | Met | the *Tests*, *Lint* and *UI* workflows on each pull request |
| test_policy | MUST | Met | [CONTRIBUTING.md](../CONTRIBUTING.md), *Tests and warnings*: new behaviour comes with tests |
| tests_are_added | MUST | Met | pull requests add the tests for what they change: `git log -- '*_test.go'`, for example ad56e9a5 and 87c0ec17 |
| tests_documented_added | SUGGESTED | Met | [CONTRIBUTING.md](../CONTRIBUTING.md), *Tests and warnings* |
| warnings | MUST | Met | `go vet`, golangci-lint (`.golangci.yml`), ESLint and `tsc` for the UI, all in `make check` |
| warnings_fixed | MUST | Met | `make check` fails on a warning, and a pull request merges only once it passes |
| warnings_strict | SUGGESTED | Met | golangci-lint with staticcheck, revive and the layering rules; `tsc` in strict mode |

## Security

| Criterion | Level | State | Evidence |
| --- | --- | --- | --- |
| know_secure_design | MUST | **Owner** | the owner answers this one on the form. The design's fence (no ServiceAccount token in the sandbox, NetworkPolicy, the admission policy) is in [docs/design.md](design.md) |
| know_common_errors | MUST | **Owner** | as above |
| crypto_published | MUST | Met | Go's standard library only: `crypto/rand`, `crypto/sha256`, `crypto/tls` |
| crypto_call | SHOULD | Met | as above; nothing is implemented by hand |
| crypto_floss | MUST | Met | Go's standard library |
| crypto_keylength | MUST | Met | TLS through Go's defaults; generated passwords are 24 random bytes (192 bits) from `crypto/rand` |
| crypto_working | MUST | Met | no MD4, MD5, DES or RC4. SHA-1 appears only to name Git objects (`internal/gitprovider/gitea_agents.go`, `internal/model/publish_source.go`), which Git requires; it guards nothing |
| crypto_weaknesses | SHOULD | Met | as above |
| crypto_pfs | SHOULD | Met | Go's TLS defaults (ECDHE) |
| crypto_password_storage | MUST | N/A | the factory authenticates no one by password: the UI uses the cluster's RBAC and OIDC, and Gitea stores its own users' passwords |
| crypto_random | MUST | Met | passwords and tokens come from `crypto/rand`; no `math/rand` outside tests |
| delivery_mitm | MUST | Met | images and the chart from `ghcr.io` over HTTPS; images pinned by digest where the chart or Dockerfile pins them |
| delivery_unsigned | MUST | Met | downloaded tools are checked against pinned SHA-256 sums (`hack/testenv.mk`, the Helm download in the `Makefile`), never fetched over HTTP |
| vulnerabilities_fixed_60_days | MUST | Met | `govulncheck ./...` on 2026-10-16 finds no vulnerability the code reaches ([docs/screenshots/1147-govulncheck.txt](screenshots/1147-govulncheck.txt)). #1147 fixed the 16 it found on 2026-10-15: Go 1.26.9 (`go.mod`'s `toolchain`, which CI's `setup-go` follows, and the images' builders), `golang.org/x/net`, `golang.org/x/crypto`, `google.golang.org/grpc`, `go.opentelemetry.io/otel` and `golang.org/x/image`. Two remain that the code doesn't reach: `github.com/google/cel-go` (fixed in v0.30.0; `k8s.io/apiserver` holds v0.29.2) and `golang.org/x/crypto/openpgp` (unmaintained, no fix; nothing imports it) |
| vulnerabilities_critical_fixed | SHOULD | Met | the *Vulnerabilities* workflow runs `govulncheck ./...` (`make vulncheck`) on every change to the Go code or the modules and every Monday, and fails on a vulnerability the code reaches; two failed runs on main file an urgent "main is red" issue the relay takes first (#1148, [docs/screenshots/1148-vulncheck-gosec.txt](screenshots/1148-vulncheck-gosec.txt)) |
| no_leaked_credentials | MUST | Met | the [`secrets` skill](../.claude/skills/secrets/SKILL.md), `make helm-lint` fails on a rendered Secret, GitGuardian on pull requests, and the *Secrets* workflow: gitleaks (`make secrets-scan`) over every ref's whole history on every pull request and push to main, with the checked false positives in `.gitleaks.toml` (#52, [docs/screenshots/52-gitleaks.txt](screenshots/52-gitleaks.txt)) |

## Analysis

| Criterion | Level | State | Evidence |
| --- | --- | --- | --- |
| static_analysis | MUST | Met | golangci-lint (staticcheck, govet, revive and more) in `make check` and the *Lint* workflow, before every release; CodeQL on the Go code and the UI's TypeScript (the *CodeQL* workflow), on every pull request that changes them and every Monday |
| static_analysis_common_vulnerabilities | SUGGESTED | Met | `gosec` runs in golangci-lint (`make check`, the *Lint* workflow). `.golangci.yml` turns off G115 (int to the API's int32 fields) and gosec in tests; each other finding is fixed or carries a `//nolint:gosec // why` (#1148) |
| static_analysis_fixed | MUST | Met | `make check` fails on a finding, so none merges |
| static_analysis_often | SUGGESTED | Met | on every pull request |
| dynamic_analysis | SUGGESTED | Met | envtest runs the reconcilers against a real API server; `make test-gitea` against a real Gitea |
| dynamic_analysis_unsafe | SUGGESTED | N/A | Go is memory-safe; no cgo |
| dynamic_analysis_enable_assertions | SUGGESTED | Met | the tests run with Gomega's assertions; production code keeps its own checks on |
| dynamic_analysis_fixed | MUST | Met | a failing test fails `make check` |

## For the owner

Only the owner can close these. Nothing else stands between the repository and
the passing badge.

1. ~~Choose a FLOSS licence~~: done, Apache-2.0 (#47), by the owner's word of
   2026-11-09.
2. ~~Make the repository public~~: done 2026-11-10 (repo_public, discussion,
   report_archive).
3. **Turn on private vulnerability reporting** in *Settings → Code security*
   (vulnerability_report_private), then drop the "while the repository is
   private" sentence from [SECURITY.md](../SECURITY.md).
4. **Register the project on [bestpractices.dev](https://www.bestpractices.dev/)**
   with the owner's GitHub account, answer the form from this page, and answer
   know_secure_design and know_common_errors for themselves.
5. **Add the badge** to [README.md](../README.md) once it's earned; an agent
   can do that from the project's number.

## Keeping it

Recheck this page when a release changes how the project builds, tests,
releases or handles a vulnerability, and before 1.0.
