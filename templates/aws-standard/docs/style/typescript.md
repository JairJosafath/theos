# TypeScript digest

Distilled from the Google TypeScript Style Guide, the TypeScript handbook's
"Do's and Don'ts" and typescript-eslint's configs (see `SOURCES.md`).
`[lint: x]` = enforced by `make check`: an ESLint rule in `eslint.config.mjs`, a
`tsconfig.json` flag (`tsc`) or Prettier. `[review]` = no tool; check it
yourself. Where a tool and Google's guide disagree, the tool wins and the rule
says so.

## Formatting

- Prettier formats everything (`make fmt`): 2 spaces, single quotes, 100
  columns, trailing commas. Never hand-format. [lint: prettier]
- Braces around every block, even one statement. [lint: curly]
- `===` and `!==` always, also for `null` (stricter than Google's `== null`). [lint: eqeqeq]

## Naming

- `lowerCamelCase` for variables, parameters, functions, methods, properties. [review]
- `UpperCamelCase` for classes, interfaces, types, enums, type parameters. [review]
- `CONSTANT_CASE` for module-level constants and enum members that never change. [review]
- Acronyms are words: `loadHttpUrl`, `jsonApiServer`, not `loadHTTPURL`. [review]
- No `_` prefix or suffix, not even for private members; `_` alone only for an
  unused destructured value. [review]
- File names are lower case with underscores: `yaml_scanner.ts`. [review]
- Names say what, not how: no `data`, `info`, `manager`, `util`, `helpers`. [review]

## Modules

- ES modules only: no `require`, no `namespace`. [lint: @typescript-eslint/no-require-imports, no-namespace]
- Named exports only; default exports only where a tool demands one
  (`eslint.config.mjs`, `vitest.config.mts`). [review]
- `import type` for type-only imports. [lint: tsc verbatimModuleSyntax]
- Relative imports within `src/`. [review]
- Export only what other modules use; unused locals fail the type check. [lint: tsc noUnusedLocals; exports review]

## Types

- `strict`, plus `noUncheckedIndexedAccess` (an index can be `undefined`) and
  `exactOptionalPropertyTypes` (optional is not `| undefined`). [lint: tsc]
- No `any`. Use `unknown` and narrow it, or a precise type. Values that arrive
  as `any` (`JSON.parse`, untyped libraries) get a type at the boundary,
  checked at runtime when the input is untrusted. [lint: no-explicit-any, no-unsafe-*; runtime checks review]
- No `@ts-ignore` or `@ts-nocheck`; `@ts-expect-error` only with a reason. [lint: ban-ts-comment]
- No non-null assertions (`x!`). Narrow with a check instead; in the rare
  case an assertion is right, an `eslint-disable-next-line` comment says why. [lint: no-non-null-assertion; reason review]
- `as` only with a reason the compiler can't see; never to silence an error.
  Annotate object literals (`const x: Foo = {...}`), don't assert them. [lint: no-unnecessary-type-assertion; rest review]
- Interfaces for object shapes, type aliases for unions and functions. [lint: consistent-type-definitions]
- `T[]` for arrays, always (`(string | number)[]`); `readonly T[]` for inputs
  you don't change. [lint: array-type; readonly review]
- `Record<K, V>` or `Map` for dictionaries, not index signatures. [lint: consistent-indexed-object-style]
- `string`, `number`, `boolean`, `object`: never `String`, `Number`, `Object`, `{}`
  or `Function`. [lint: no-wrapper-object-types, no-empty-object-type, no-unsafe-function-type]
- No generic type parameter that appears once. [lint: no-unnecessary-type-parameters]
- Optional fields (`field?: T`) over `field: T | undefined`. [review]
- Union types over overloads that differ in one argument; specific overloads
  before general ones. [lint: unified-signatures; order review]
- Callbacks whose result is ignored return `void`, not `any`. [review]
- Exported functions declare parameter and return types. [lint: explicit-module-boundary-types]

## Control flow

- A `switch` over a union or enum names every member (a `default` doesn't
  count), so a new member fails the lint where it must be handled; other
  switches end in `default`. [lint: switch-exhaustiveness-check]
- No condition that is always true or false for its type. [lint: no-unnecessary-condition]
- `??` and `?.` over `||` and `&&` chains for null checks. [lint: prefer-nullish-coalescing, prefer-optional-chain]
- `for (const x of xs)` over index loops and `forEach`. [lint: prefer-for-of; forEach review]
- `Array#sort` gets a compare function (except for strings). [lint: require-array-sort-compare]

## Errors

- Throw only `Error` (or a subclass), always with `new`. [lint: only-throw-error]
- A `catch` variable is `unknown`; narrow it before use. [lint: tsc useUnknownInCatchVariables, use-unknown-in-catch-callback-variable]
- No empty `catch`; a caught error is handled, rethrown with context
  (`new Error('read settings', { cause: e })`) or reported. [review]
- Expected failures that callers must handle return a result type
  (`{ ok: true, value } | { ok: false, error }`); exceptions are for bugs and
  I/O failures. [review]

## Async

- Every promise is awaited, returned, or explicitly `void`ed with a comment. [lint: no-floating-promises]
- No promise where a `void` callback is expected. [lint: no-misused-promises]
- No `async` without `await`; no `await` of a non-promise. [lint: require-await, await-thenable]
- Long operations take a cancellation token (or `AbortSignal`) and stop when
  it fires. [review]

## Classes

- Prefer functions and plain objects; a class when it owns state and a lifetime
  (a disposable). [review]
- `private` and `readonly`, not `#private` (Google's choice); members never
  reassigned are `readonly`. [lint: prefer-readonly; #private review]
- Parameter properties (`constructor(private readonly client: Client) {}`) over
  assigning fields by hand. [review]
- Getters are cheap and have no side effects. [review]

## Tests

- vitest, files next to the code: `foo.ts` and `foo.test.ts`. [review]
- `it.each` tables for cases that differ only in data; names say what is
  expected. [review]
- Test behaviour through exports; no test reads private state. [review]
- No real timers, network or file system in unit tests: inject them, or use
  `vi.useFakeTimers()` and temporary directories. [review]

## Do's and Don'ts (TypeScript handbook)

- Don't use the boxed types `Number`, `String`, `Boolean`, `Symbol`, `Object`.
- Don't use `any` except while migrating JavaScript; use `unknown`.
- Don't return `any` from callbacks whose value is ignored; return `void`.
- Don't make callback parameters optional when every caller passes them.
- Don't write overloads that differ only in callback arity or trailing
  parameters; use one signature with optional parameters.
- Don't write overloads that differ only by one argument's type; use a union.
- Don't declare type parameters a function doesn't use.
