# Cross-language principles

Distilled from long-lived style guides. Each rule names its sources (keys at the
bottom). TypeScript and VS Code specifics live in `typescript.md` and
`vscode.md`; where they are stricter, they win.

## Consistency

- **Match the surrounding code.** When editing, follow the style already there,
  even if you would choose differently. [LLVM "Golden Rule"], [Google]
- **Look different only when different.** Same concept, same shape and name
  everywhere. [Rust C-WORD-ORDER]
- **Let tools format.** Never hand-format or argue about whitespace; run the
  formatter. [Chromium]
- **Warnings are errors.** Fix the code instead of silencing the tool; they point
  at real bugs. [LLVM "Treat Compiler Warnings Like Errors"], [Mozilla]

## Naming

- **Names are documentation.** Descriptive, English, no cryptic abbreviations
  for anything used beyond a few lines. [GNU 5.4], [Kernel §4], [GoogleTS]
- **Short names only for short scopes.** `i`, `x` are fine in a 3-line loop; if a
  local's name confuses, the function is too long. [Kernel §4], [GNU 5.4]
- **Acronyms are words:** `HttpClient`, `xmlDocId`. [Rust C-CASE], [GoogleTS]
- **Verbs for actions, nouns for values.** `startServer()` does; `serverPath` is.
  Booleans read as assertions: `isEnabled`, `hasErrors`. [LLVM naming]
- **No `get` prefix on property-like reads**; conversions use `to...`/`as...`.
  [Rust C-GETTER, C-CONV]
- **Consistent word order** across related names (`ParseError`, `FormatError`).
  [Rust C-WORD-ORDER]
- **No `util`, `helpers`, `manager`, `data` names.** Name the actual idea. [review]
- **Names that can't be misread.** Avoid `l`/`1`, `O`/`0` lookalikes. [CppCG NL.19]

## Small units

- **One function, one job.** If you need "and" to describe it, split it.
  [Kernel §6], [CppCG F.1, F.2]
- **Keep functions short**: about one screen, few locals, at most 3 levels of
  nesting. [Kernel §1, §6], [CppCG F.3]
- **Few parameters.** Many parameters mean a missing type (an options object).
  [CppCG I.23]
- **Early return over nesting; no `else` after `return`.** [LLVM "Use Early
  Exits"], [Mozilla "Return immediately on failure"]
- **Turn predicate loops into named predicate functions.** [LLVM]
- **Keep scopes small; declare variables where you can initialise them.**
  [CppCG ES.5, ES.20, ES.22]
- **Prefer pure functions and immutable data** (`readonly`, `const`).
  [CppCG F.8, P.10]

## Comments and docs

- **Comments say why and intent, code says what.** Don't restate code. [CppCG NL.1,
  NL.2], [Kernel §8], [LLVM commenting]
- **Don't explain bad code; rewrite it.** [Kernel §8]
- **JSDoc (`/** */`) for what callers need, `//` for implementation notes.** [GoogleTS]
- **Document the non-obvious**: edge cases (empty, undefined, huge), units,
  who disposes what, what errors mean. [GNU 5.2]
- **Complete sentences, capitalised, in English.** [GNU 5.2]
- **No TODO without an issue number;** better, finish the work or leave it out. [review]

## Types and interfaces

- **Make interfaces explicit and strongly typed**; no `any`, no stringly typed
  values, unions of literals over magic strings. [CppCG I.1, I.4, P.4], [TSHandbook]
- **Prefer compile-time checks over run-time checks.** [CppCG P.5]
- **State preconditions; check them at the boundary**, where untrusted data
  enters. [CppCG I.5, P.6, P.7]
- **No magic numbers.** Named constants or a derivation. [CppCG ES.45]
- **Smallest useful public surface.** Export only what other modules use.
  [LLVM "Restrict visibility"]
- **No hidden global state.** Functions work on their arguments; inject
  dependencies. [review]

## Resource ownership

- **Every resource has exactly one owner that releases it**: in an extension,
  everything disposable goes into `context.subscriptions` or is disposed by
  the object that created it. [CppCG R.1, P.8], [VSCode]
- **Acquire and hand to the owner in one step:** push a registration into
  `subscriptions` in the same statement that creates it. [CppCG R.12]
- **Single source of truth.** Don't copy live state; derive it. [review]

## Errors

- **Fail loudly and early.** Check every fallible call; never swallow errors.
  [GNU 4.2], [Mozilla "Check early and often"], [CppCG P.7]
- **Pick one error strategy up front**: result types for expected failures,
  exceptions for bugs and I/O. [CppCG E.1, E.2, E.3]
- **"Impossible" branches are bugs**: throw, don't return a fake value.
  [GNU 4.2], [LLVM `llvm_unreachable`]
- **Useful error messages**: what happened, with which input, what to do next.
  [GNU 4.2, 4.4], [LLVM], [VSCodeUX]
- **Handle all input**: empty, huge, unicode, malformed. No arbitrary limits;
  never silently truncate. [GNU 4.2]
- **Log only actionable messages**; never log user data. [review]

## Simplicity and dead code

- **Write what you need and no more, but do it right.** No speculative code "for
  completeness". [review]
- **No dead code, commented-out code, or unused parameters.** Version control
  remembers. [review; lint: tsc noUnusedLocals, noUnusedParameters]
- **Don't reinvent helpers**; use the platform's. [Kernel §18], [CppCG P.13]
- **Encapsulate messy code** behind one clean interface. [CppCG P.11]
- **Respect layering**: pure modules don't import `vscode`; `extension.ts` wires
  them to VS Code. [LLVM "Library layering"]

## Tests

- **Write the failing test first when fixing a bug**; every feature ships with
  tests. [review]
- **Tests are self-contained**; duplication in tests is fine. [review]
- **Prefer fakes that implement the real interface** over mocks. [review]

## Source keys

- [LLVM] https://llvm.org/docs/CodingStandards.html
- [Chromium] https://chromium.googlesource.com/chromium/src/+/HEAD/styleguide/styleguide.md
- [Google] https://google.github.io/styleguide/
- [Mozilla] https://firefox-source-docs.mozilla.org/code-quality/coding-style/coding_style_cpp.html
- [Kernel] https://docs.kernel.org/process/coding-style.html
- [CppCG] https://isocpp.github.io/CppCoreGuidelines/CppCoreGuidelines
- [Rust] https://rust-lang.github.io/api-guidelines/naming.html
- [GNU] https://www.gnu.org/prep/standards/standards.html
- [GoogleTS] https://google.github.io/styleguide/tsguide.html
- [TSHandbook] https://www.typescriptlang.org/docs/handbook/declaration-files/do-s-and-don-ts.html
- [VSCode] https://code.visualstudio.com/api/references/extension-guidelines
- [VSCodeUX] https://code.visualstudio.com/api/ux-guidelines/notifications
