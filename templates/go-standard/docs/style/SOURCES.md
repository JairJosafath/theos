# Sources

Every source read to write `docs/style/`. The Go sources were read on 2026-10-02.
The cross-language sources were read on 2026-09-30 for another Standard, whose
`principles.md` this one adapts; they were not read again. Version or date as
shown on the page; "live" when the page shows none.

## Go

| Source | Version / date |
|---|---|
| Effective Go — https://go.dev/doc/effective_go | live; written for Go's 2009 release and not kept current (no generics or modules), but mentions Go 1.26's `new(expr)` |
| Go Code Review Comments — https://go.dev/wiki/CodeReviewComments | live wiki |
| Google Go Style Guide, guide — https://google.github.io/styleguide/go/guide | live |
| Google Go Style Guide, decisions — https://google.github.io/styleguide/go/decisions | live |
| Google Go Style Guide, best practices — https://google.github.io/styleguide/go/best-practices | live |
| Go Doc Comments — https://go.dev/doc/comment | live |
| Organizing a Go module — https://go.dev/doc/modules/layout | live |
| Go Proverbs — https://go-proverbs.github.io/ | live (Rob Pike, Gopherfest 2015) |

## Lints

| Source | Version / date |
|---|---|
| golangci-lint — https://golangci-lint.run/ | **v2.14.0**, as installed in `c4factory/go:1.26`; config checked with `golangci-lint config verify` |
| golangci-lint JSON schema — https://golangci-lint.run/jsonschema/golangci.jsonschema.json | fetched 2026-10-02, for setting and revive rule names |

Every linter in `.golangci.yml` was checked to fire on a file written to break
its rule before this Standard was released.

## Cross-language (read 2026-09-30)

| Source | Version / date |
|---|---|
| LLVM Coding Standards — https://llvm.org/docs/CodingStandards.html | live |
| Chromium style guide index — https://chromium.googlesource.com/chromium/src/+/HEAD/styleguide/styleguide.md | HEAD |
| Mozilla C++ coding style — https://firefox-source-docs.mozilla.org/code-quality/coding-style/coding_style_cpp.html | live |
| Linux kernel coding style — https://docs.kernel.org/process/coding-style.html | live |
| C++ Core Guidelines — https://isocpp.github.io/CppCoreGuidelines/CppCoreGuidelines | Jun 14, 2026 |
| Rust API Guidelines, naming — https://rust-lang.github.io/api-guidelines/naming.html | live |
| GNU Coding Standards — https://www.gnu.org/prep/standards/standards.html | April 27, 2026 |
