# Cross-language principles

Distilled from long-lived style guides. Each rule names its sources (keys at the
bottom). Go specifics live in `go.md`; where it is stricter, it wins.

## Consistency

- **Match the surrounding code.** When editing, follow the style already there,
  even if you would choose differently. [LLVM "Golden Rule"], [GoStyle "Local consistency"]
- **Look different only when different.** Same concept, same shape and name
  everywhere. [Rust C-WORD-ORDER], [GoStyle "Consistency"]
- **Let tools format.** Never hand-format or argue about whitespace; run the
  formatter. [Chromium], [CRC "Gofmt"], [Proverbs]
- **Warnings are errors.** Fix the code instead of silencing the tool; they point
  at real bugs. [LLVM "Treat Compiler Warnings Like Errors"], [Mozilla]

## Naming

- **Names are documentation.** Descriptive, English, no cryptic abbreviations
  for anything used beyond a few lines. [GNU 5.4], [Kernel §4]
- **Short names only for short scopes.** `i`, `r` are fine in a 3-line loop; if a
  local's name confuses, the function is too long. [Kernel §4], [CRC "Variable Names"]
- **Initialisms keep one case in Go:** `URL`, `userID`. Other languages treat
  them as words; Go doesn't. [CRC "Initialisms"]
- **Verbs for actions, nouns for values.** `Publish()` does; `Diagnostics()` is.
  Booleans read as assertions: `IsValid`, `hasMore`. [LLVM naming], [GoStyle best practices "Function and method names"]
- **No `Get` prefix on property-like reads.** [Rust C-GETTER], [EffGo "Getters"]
- **Consistent word order** across related names (`ParseError`, `FormatError`).
  [Rust C-WORD-ORDER]
- **No `util`, `common`, `helpers`, `manager` names.** Name the actual idea.
  [GoStyle best practices "Util packages"], [CRC "Package Names"]
- **Don't repeat context** the reader already has: the package, the receiver,
  the type. [GoStyle "Naming"]
- **Names that can't be misread.** Avoid `l`/`1`, `O`/`0` lookalikes. [CppCG NL.19]

## Small units

- **One function, one job.** If you need "and" to describe it, split it.
  [Kernel §6], [CppCG F.1, F.2]
- **Keep functions short**: about one screen, few locals, at most 3 levels of
  nesting. [Kernel §1, §6], [CppCG F.3]
- **Few parameters.** Many parameters mean a missing type (an options struct in
  Go). [CppCG I.23], [GoStyle best practices "Option structure"]
- **Early return over nesting; no `else` after `return`.** [LLVM "Use Early
  Exits"], [Mozilla "Return immediately on failure"], [CRC "Indent Error Flow"]
- **Turn predicate loops into named predicate functions.** [LLVM]
- **Keep scopes small; declare variables where you can initialise them.**
  [CppCG ES.5, ES.20, ES.22]
- **Prefer pure functions and immutable data.** [CppCG F.8, P.10]

## Comments and docs

- **Comments say why and intent, code says what.** Don't restate code. [CppCG NL.1,
  NL.2], [Kernel §8], [LLVM commenting]
- **Don't explain bad code; rewrite it.** [Kernel §8]
- **Document the non-obvious**: edge cases (empty, nil, negative, huge), units,
  lifecycle, who owns or closes what, what errors mean, concurrency safety.
  [GoDoc], [GoStyle best practices "Documentation"], [GNU 5.2]
- **No useless docs** that only repeat the name or signature. [GoStyle best practices "Documentation"]
- **Complete sentences, capitalised, in English.** [GNU 5.2], [CRC "Comment Sentences"]
- **No TODO without an issue number;** better, finish the work or leave it out. [review]

## Types and interfaces

- **Make interfaces explicit and strongly typed**; no stringly typed values,
  typed constants over magic strings. [CppCG I.1, I.4, P.4], [Proverbs "interface{} says nothing"]
- **Prefer compile-time checks over run-time checks.** [CppCG P.5]
- **State preconditions; check them at the boundary.** [CppCG I.5, P.6, P.7]
- **No magic numbers.** Named constants or a derivation. [CppCG ES.45]
- **Smallest useful public surface.** Unexported by default; `internal/` for
  everything the module doesn't promise to others. [LLVM "Restrict visibility"], [GoLayout]
- **No hidden global state.** Functions work on their arguments and receivers;
  inject dependencies. [GoStyle best practices "Global state"]
- **Predictable over magic; the least mechanism that works.** [Proverbs "Clear
  is better than clever", "Reflection is never clear"], [GoStyle "Simplicity"]

## Resource ownership

- **Every resource has exactly one owner that releases it**: files, response
  bodies, timers, tickers and goroutines. [CppCG R.1, P.8], [CRC "Goroutine Lifetimes"]
- **Acquire and hand to the owner in one step**: `defer f.Close()` right after
  the open succeeds. [CppCG R.12], [EffGo "Defer"]
- **A goroutine's exit is designed, not hoped for.** [CRC "Goroutine Lifetimes"],
  [GoStyle decisions "Goroutine lifetimes"]
- **Single source of truth.** Don't copy live state; derive it. [review]

## Errors

- **Fail loudly and early.** Check every fallible call; never swallow errors.
  [GNU 4.2], [Mozilla "Check early and often"], [CRC "Handle Errors"]
- **Errors are values**: return them, add context, let the caller decide.
  [Proverbs], [GoStyle best practices "Error handling"]
- **"Impossible" branches are bugs**: panic with a message, don't return a fake
  value. [LLVM `llvm_unreachable`], [GoStyle best practices "Program checks and panics"]
- **Useful error messages**: what happened, with which input, where (file:line:col),
  what to do next. [GNU 4.2, 4.4], [LLVM]
- **Handle all input**: empty, huge, unicode, malformed. No arbitrary limits;
  never silently truncate. [GNU 4.2]
- **Log only actionable messages**, once, without user data.
  [GoStyle best practices "Logging errors"]

## Simplicity and dead code

- **Write what you need and no more, but do it right.** No speculative code "for
  completeness". [GoStyle "Simplicity"]
- **No dead code, commented-out code, or unused parameters.** Version control
  remembers. [review; lint: unused, unparam]
- **Don't reinvent helpers**; use the standard library's. [Kernel §18], [CppCG P.13]
- **A little copying is better than a little dependency.** [Proverbs]
- **Encapsulate messy code** behind one clean interface. [CppCG P.11]
- **Respect layering**: a package depends only on the ones below it; no cycles.
  [LLVM "Library layering"]

## Tests

- **Write the failing test first when fixing a bug**; every feature ships with
  tests. [review]
- **Tests are self-contained**; duplication in tests is fine. [GoStyle best practices "Tests"]
- **Prefer real implementations and fakes** over mocks; use real transports
  with test servers. [GoStyle best practices "Use real transports"]

## Source keys

- [LLVM] https://llvm.org/docs/CodingStandards.html
- [Chromium] https://chromium.googlesource.com/chromium/src/+/HEAD/styleguide/styleguide.md
- [Mozilla] https://firefox-source-docs.mozilla.org/code-quality/coding-style/coding_style_cpp.html
- [Kernel] https://docs.kernel.org/process/coding-style.html
- [CppCG] https://isocpp.github.io/CppCoreGuidelines/CppCoreGuidelines
- [Rust] https://rust-lang.github.io/api-guidelines/naming.html
- [GNU] https://www.gnu.org/prep/standards/standards.html
- [EffGo] https://go.dev/doc/effective_go
- [CRC] https://go.dev/wiki/CodeReviewComments
- [GoStyle] https://google.github.io/styleguide/go/ (guide, decisions, best-practices)
- [GoDoc] https://go.dev/doc/comment
- [GoLayout] https://go.dev/doc/modules/layout
- [Proverbs] https://go-proverbs.github.io/
