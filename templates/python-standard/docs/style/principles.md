# Cross-language principles

Distilled from long-lived style guides. Each rule names its sources (keys at the
bottom). Python specifics live in `python.md`; where it is stricter, it wins.

## Consistency

- **Match the surrounding code.** When editing, follow the style already there,
  even if you would choose differently. [LLVM "Golden Rule"], [PEP8 "A Foolish Consistency"]
- **Look different only when different.** Same concept, same shape and name
  everywhere. [Rust C-WORD-ORDER], [PEP8 "A Foolish Consistency"]
- **Let tools format.** Never hand-format or argue about whitespace; run the
  formatter. [Chromium], [PEP8 "Code Lay-out"], [PyGuide "Line length"]
- **Warnings are errors.** Fix the code instead of silencing the tool; they point
  at real bugs. [LLVM "Treat Compiler Warnings Like Errors"], [Mozilla]

## Naming

- **Names are documentation.** Descriptive, English, no cryptic abbreviations
  for anything used beyond a few lines. [GNU 5.4], [Kernel §4]
- **Short names only for short scopes.** `i`, `r` are fine in a 3-line loop; if a
  local's name confuses, the function is too long. [Kernel §4], [PyGuide "Naming"]
- **Acronyms are words in Python:** `HttpClient`, `user_id`, `load_json`.
  [PyGuide "Naming"], [PEP8 "Naming Conventions"]
- **Verbs for actions, nouns for values.** `publish()` does; `diagnostics` is.
  Booleans read as assertions: `is_valid`, `has_more`. [LLVM naming], [PEP8 "Naming Conventions"]
- **No `Get` prefix on property-like reads.** [Rust C-GETTER], [PyGuide "Properties"]
- **Consistent word order** across related names (`ParseError`, `FormatError`, `parse_file`, `format_file`).
  [Rust C-WORD-ORDER]
- **No `util`, `common`, `helpers`, `manager` names.** Name the actual idea.
  [PyGuide "Naming"], [PEP8 "Package and Module Names"]
- **Don't repeat context** the reader already has: the module, the class,
  the type. [PyGuide "Naming"]
- **Names that can't be misread.** Avoid `l`/`1`, `O`/`0` lookalikes. [CppCG NL.19]

## Small units

- **One function, one job.** If you need "and" to describe it, split it.
  [Kernel §6], [CppCG F.1, F.2]
- **Keep functions short**: about one screen, few locals, at most 3 levels of
  nesting. [Kernel §1, §6], [CppCG F.3]
- **Few parameters.** Many parameters mean a missing type (a small dataclass
  in Python). [CppCG I.23], [PyGuide "Function length"]
- **Early return over nesting; no `else` after `return`.** [LLVM "Use Early
  Exits"], [Mozilla "Return immediately on failure"], [PyGuide "Function length"]
- **Turn predicate loops into named predicate functions.** [LLVM]
- **Keep scopes small; declare variables where you can initialise them.**
  [CppCG ES.5, ES.20, ES.22]
- **Prefer pure functions and immutable data.** [CppCG F.8, P.10]

## Comments and docs

- **Comments say why and intent, code says what.** Don't restate code. [CppCG NL.1,
  NL.2], [Kernel §8], [LLVM commenting]
- **Don't explain bad code; rewrite it.** [Kernel §8]
- **Document the non-obvious**: edge cases (empty, `None`, negative, huge), units,
  lifecycle, who owns or closes what, what errors mean, concurrency safety.
  [PEP257], [PyGuide "Comments and Docstrings"], [GNU 5.2]
- **No useless docs** that only repeat the name or signature. [PEP257 "What is a Docstring?"]
- **Complete sentences, capitalised, in English.** [GNU 5.2], [PyGuide "Comments and Docstrings"]
- **No TODO without an issue number;** better, finish the work or leave it out. [review]

## Types and interfaces

- **Make interfaces explicit and strongly typed**; no stringly typed values,
  `Enum`s over magic strings. [CppCG I.1, I.4, P.4], [Typing "Using Any and object"]
- **Prefer compile-time checks over run-time checks.** [CppCG P.5]
- **State preconditions; check them at the boundary.** [CppCG I.5, P.6, P.7]
- **No magic numbers.** Named constants or a derivation. [CppCG ES.45]
- **Smallest useful public surface.** A leading underscore for
  everything the module doesn't promise to others. [LLVM "Restrict visibility"], [PEP8 "Public and Internal Interfaces"]
- **No hidden global state.** Functions work on their arguments and receivers;
  inject dependencies. [PyGuide "Mutable Global State"]
- **Predictable over magic; the least mechanism that works.** [PEP8 "Programming Recommendations"], [PyGuide "Power Features"]

## Resource ownership

- **Every resource has exactly one owner that releases it**: files, connections,
  clients, tasks and threads. [CppCG R.1, P.8], [PyGuide "Files, Sockets, and similar Stateful Resources"]
- **Acquire and hand to the owner in one step**: `with open(...) as f:`, never
  a bare `open`. [CppCG R.12], [PyGuide "Files, Sockets, and similar Stateful Resources"]
- **A task's or thread's exit is designed, not hoped for.** Something awaits or
  joins it. [CppCG CP.26], [PyGuide "Threading"]
- **Single source of truth.** Don't copy live state; derive it. [review]

## Errors

- **Fail loudly and early.** Check every fallible call; never swallow errors.
  [GNU 4.2], [Mozilla "Check early and often"], [PyGuide "Exceptions"]
- **Raise specific exceptions**: add context with `raise ... from`, let the
  caller decide. [PyGuide "Exceptions"], [PEP8 "Programming Recommendations"]
- **"Impossible" branches are bugs**: raise with a message (or end a `match`
  with `assert_never`), don't return a fake value. [LLVM `llvm_unreachable`], [PyGuide "Exceptions"]
- **Useful error messages**: what happened, with which input, where (file:line:col),
  what to do next. [GNU 4.2, 4.4], [LLVM]
- **Handle all input**: empty, huge, unicode, malformed. No arbitrary limits;
  never silently truncate. [GNU 4.2]
- **Log only actionable messages**, once, without user data.
  [PyGuide "Strings"]

## Simplicity and dead code

- **Write what you need and no more, but do it right.** No speculative code "for
  completeness". [review]
- **No dead code, commented-out code, or unused parameters.** Version control
  remembers. [review; lint: ERA001, ARG001, F401]
- **Don't reinvent helpers**; use the standard library's. [Kernel §18], [CppCG P.13], [PyGuide "Power Features"]
- **A little copying is better than a little dependency.** [review]
- **Encapsulate messy code** behind one clean interface. [CppCG P.11]
- **Respect layering**: a package depends only on the ones below it; no cycles.
  [LLVM "Library layering"], [PyGuide "Imports"]

## Tests

- **Write the failing test first when fixing a bug**; every feature ships with
  tests. [review]
- **Tests are self-contained**; duplication in tests is fine. [Pytest]
- **Prefer real implementations and fakes** over mocks; use a real client
  against the app (`TestClient`). [Pytest], [FastAPI "Testing"]

## Source keys

- [LLVM] https://llvm.org/docs/CodingStandards.html
- [Chromium] https://chromium.googlesource.com/chromium/src/+/HEAD/styleguide/styleguide.md
- [Mozilla] https://firefox-source-docs.mozilla.org/code-quality/coding-style/coding_style_cpp.html
- [Kernel] https://docs.kernel.org/process/coding-style.html
- [CppCG] https://isocpp.github.io/CppCoreGuidelines/CppCoreGuidelines
- [Rust] https://rust-lang.github.io/api-guidelines/naming.html
- [GNU] https://www.gnu.org/prep/standards/standards.html
- [PEP8] https://peps.python.org/pep-0008/
- [PEP257] https://peps.python.org/pep-0257/
- [PyGuide] https://google.github.io/styleguide/pyguide.html
- [Typing] https://typing.python.org/en/latest/reference/best_practices.html
- [Pytest] https://docs.pytest.org/en/stable/explanation/goodpractices.html
- [FastAPI] https://fastapi.tiangolo.com/tutorial/
