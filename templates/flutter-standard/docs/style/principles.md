# Cross-language principles

Distilled from long-lived style guides. Each rule names its sources (keys at the
bottom). Dart and Flutter specifics live in `dart.md` and `flutter.md`; where they
are stricter, they win.

## Consistency

- **Match the surrounding code.** When editing, follow the style already there,
  even if you would choose differently. [LLVM "Golden Rule"], [Google], [EffDart]
- **Look different only when different.** Same concept, same shape and name
  everywhere. [EffDart overview], [Rust C-WORD-ORDER]
- **Let tools format.** Never hand-format or argue about whitespace; run the
  formatter. [Chromium], [Flutter "Use dart format"]
- **Warnings are errors.** Fix the code instead of silencing the tool; they point
  at real bugs. [LLVM "Treat Compiler Warnings Like Errors"], [Mozilla]

## Naming

- **Names are documentation.** Descriptive, English, no cryptic abbreviations
  for anything used beyond a few lines. [GNU 5.4], [Kernel §4], [EffDart design]
- **Short names only for short scopes.** `i`, `x` are fine in a 3-line loop; if a
  local's name confuses, the function is too long. [Kernel §4], [GNU 5.4]
- **Acronyms are words.** `HttpClient`, `jsonId`, not `HTTPClient`. [Rust C-CASE],
  [EffDart style]
- **Verbs for actions, nouns for values.** `saveImage()` does; `imageSize` is.
  Booleans read as assertions: `isEnabled`, `hasError`. [LLVM naming], [EffDart design]
- **Name booleans positively.** `enabled`, not `disabled`; avoid double negatives.
  [Flutter "Avoid double negatives"], [EffDart design]
- **No `get` prefix on property-like reads**; conversions use `to...`/`as...`.
  [Rust C-GETTER, C-CONV], [EffDart design]
- **Consistent word order** across related names (`ParseError`, `FormatError`).
  [Rust C-WORD-ORDER]
- **No "new/old/2/temp/util/manager" names.** Name the actual idea.
  [Flutter "Avoid 'new'/'old'"]
- **Names that can't be misread.** Avoid `l`/`1`, `O`/`0` lookalikes. [CppCG NL.19]

## Small units

- **One function, one job.** If you need "and" to describe it, split it.
  [Kernel §6], [CppCG F.1, F.2]
- **Keep functions short**: about one screen, ≤ ~5-10 locals, ≤ 3 levels of
  nesting. [Kernel §1, §6], [CppCG F.3]
- **Few parameters.** Many parameters mean a missing type. [CppCG I.23]
- **Early return over nesting; no `else` after `return`.** [LLVM "Use Early
  Exits"], [Mozilla "Return immediately on failure"]
- **Turn predicate loops into named predicate functions.** [LLVM]
- **Keep scopes small; declare variables when you can initialise them.**
  [CppCG ES.5, ES.20, ES.22]
- **Prefer pure functions and immutable data.** [CppCG F.8, P.10], [Flutter arch]

## Comments and docs

- **Comments say why and intent, code says what.** Don't restate code. [CppCG NL.1,
  NL.2], [Kernel §8], [LLVM commenting]
- **Don't explain bad code; rewrite it.** [Kernel §8], [Flutter "Refactor when
  docs become incomprehensible"]
- **Document the non-obvious**: edge cases (empty, null, negative, NaN), units,
  lifecycle, who owns what, what errors mean. [Flutter docs], [GNU 5.2]
- **No useless docs** that only repeat the name. [Flutter "Avoid useless docs"]
- **Complete sentences, capitalised, in English.** [GNU 5.2], [EffDart docs]
- **No questions in comments; find and write the answer.** [Flutter]
- **TODOs name an owner and link an issue**: `// TODO(name): ... See #12.` [Flutter]

## Types and interfaces

- **Make interfaces explicit and strongly typed**; no `dynamic`, no stringly typed
  values, enums over magic strings. [CppCG I.1, I.4, P.4], [Flutter "Avoid dynamic"]
- **Prefer compile-time checks over run-time checks.** [CppCG P.5]
- **State preconditions; check them at the boundary.** [CppCG I.5, P.6, P.7]
- **No magic numbers.** Named constants or a derivation (`3.0 * sqrt(2)`).
  [CppCG ES.45], [Flutter "Avoid mysterious magic numbers"]
- **Getters are cheap.** Expensive or side-effecting work is a method. [Flutter]
- **Smallest useful public surface.** Private by default; expose only what callers
  need. [EffDart design], [Flutter "Avoid API oceans"], [LLVM "Restrict visibility"]
- **No hidden global state.** Functions work on their arguments and instance data;
  inject dependencies. [Flutter "Avoid secret state"]
- **Predictable over magic.** Explicit behaviour beats heuristics. [Flutter]

## Resource ownership

- **Every resource has exactly one owner that releases it.** In Dart/Flutter:
  whoever creates a controller, subscription, timer, stream controller or
  notifier disposes it in `dispose()`/`close()`/`cancel()`. [CppCG R.1, C.31, P.8],
  [Flutter "Be explicit about dispose()"]
- **Acquire and hand to the owner in one step**; no half-owned objects.
  [CppCG R.12, E.6]
- **Treat Dart as if it had no GC** for listeners and subscriptions: a forgotten
  listener leaks the whole widget. [Flutter]
- **Cleanup must not fail.** `dispose()` never throws. [CppCG C.36, E.16]
- **Single source of truth.** Don't copy live state; derive it. [Flutter "Avoid
  duplicating state"], [EffDart "Avoid storing what you can calculate"]

## Errors

- **Fail loudly and early.** Check every fallible call; never swallow errors.
  [GNU 4.2], [Mozilla "Check early and often"], [CppCG P.7]
- **Pick one error strategy up front** (here: `Result` for expected failures,
  exceptions/`Error` for bugs). [CppCG E.1, E.2, E.3]
- **Assert invariants liberally**, with a message. Asserts catch bugs; they do
  not validate user input. [LLVM "Assert Liberally"], [Flutter "Use asserts"]
- **"Impossible" branches are bugs**: throw `StateError`/`UnimplementedError`,
  don't return a fake value. [GNU 4.2], [LLVM `llvm_unreachable`]
- **Useful error messages**: what happened, with which input, what to do next.
  [GNU 4.2, 4.4], [Flutter "Error messages should be useful"], [LLVM]
- **Handle all input**: empty, huge, unicode, malformed. No arbitrary limits;
  never silently truncate. [GNU 4.2]
- **Log only actionable messages**; never log user data. [Flutter], [Android core quality]

## Simplicity and dead code

- **Write what you need and no more, but do it right.** No speculative code "for
  completeness". [Flutter "Lazy programming"]
- **No dead code, commented-out code, or unused parameters.** Version control
  remembers. [Flutter], [VGA lints]
- **Don't reinvent helpers**; use the SDK/framework's. [Kernel §18], [CppCG P.13]
- **Encapsulate messy code** behind one clean interface. [CppCG P.11]
- **Respect layering**: a layer depends only on the one below. [LLVM "Library
  layering"], [Flutter arch]

## Tests

- **Write the failing test first when fixing a bug**; every feature ships with
  tests. [Flutter "Write test, find bug"]
- **Tests are self-contained**; duplication in tests is fine. [Flutter testing]
- **Prefer fakes that implement the real interface** over mocks. [Flutter arch]

## Source keys

- [LLVM] https://llvm.org/docs/CodingStandards.html
- [Chromium] https://chromium.googlesource.com/chromium/src/+/HEAD/styleguide/styleguide.md
- [Google] https://google.github.io/styleguide/
- [Mozilla] https://firefox-source-docs.mozilla.org/code-quality/coding-style/coding_style_cpp.html
- [Kernel] https://docs.kernel.org/process/coding-style.html
- [CppCG] https://isocpp.github.io/CppCoreGuidelines/CppCoreGuidelines
- [Rust] https://rust-lang.github.io/api-guidelines/naming.html
- [GNU] https://www.gnu.org/prep/standards/standards.html
- [EffDart] https://dart.dev/effective-dart
- [Flutter] https://github.com/flutter/flutter/blob/main/docs/contributing/Style-guide-for-Flutter-repo.md
- [Flutter arch] https://docs.flutter.dev/app-architecture
- [VGA] https://pub.dev/packages/very_good_analysis
