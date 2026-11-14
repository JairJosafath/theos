# Go digest

Distilled from Effective Go, Go Code Review Comments, the Google Go Style Guide
(guide, decisions, best practices), Go Doc Comments and the Go Proverbs (see
`SOURCES.md`). `[lint: x]` = enforced by `make check` through linter `x` in
`.golangci.yml` (or the formatter). `[review]` = no linter; check it yourself.

The order of importance, from Google's guide: **clarity, simplicity, concision,
maintainability, consistency.** Where there are several ways to say something,
use the most standard one: the language, then the standard library, then a
dependency.

## Formatting and imports

- gofumpt (a stricter gofmt) and goimports format everything; `make fmt` fixes
  it. Never hand-format. [lint: gofumpt, goimports]
- Parameters of one type share it: `func(a, b string)`. [lint: gofumpt]
- No line limit; break a line where the meaning breaks, or refactor it. [review]
- Imports: the standard library, a blank line, then the rest. Rename an import
  only to resolve a clash. [lint: goimports; renaming review]
- No dot imports. Blank imports (`_ "embed"`) only in `main` and tests. [lint: revive dot-imports, blank-imports]

## Naming

- MixedCaps, never underscores (test names and generated code aside). [lint: revive var-naming]
- Initialisms keep one case: `URL`, `userID`, `ServeHTTP`, never `Url`, `Id`. [lint: revive var-naming]
- Constants are MixedCaps named for their role, not their value; no `ALL_CAPS`. [lint: revive var-naming; role review]
- Package names: short, lower case, one word, named for what they provide
  (`yamlpos`, `lsp`); never `util`, `common`, `helpers`, `misc`, `types`, `base`. [review]
- Don't repeat the package in its names: `report.Writer`, not `report.ReportWriter`. [lint: revive exported]
- Don't repeat the receiver or types in method names: `cfg.WriteTo`, not `cfg.WriteConfigTo`. [review]
- Receivers: one or two letters from the type, the same in every method, never
  `this` or `self`. [lint: revive receiver-naming]
- Getters have no `Get`: `Owner()`, with `SetOwner()`; use `Fetch` or `Compute`
  when the call is expensive. [review]
- One-method interfaces are the method plus `-er`: `Reader`, `Checker`. [review]
- Name length grows with scope: `i`, `r`, `w` in a few lines; descriptive names
  for package-level ones. [review]
- Functions that return a value are noun phrases (`Diagnostics`); functions
  that act are verb phrases (`Publish`). Booleans read as assertions (`IsValid`,
  `hasMore`). [review]
- Errors: `ErrNotFound` for sentinels, `NotFoundError` for types; error
  variables `err`, or `errFoo` when several are in scope. [lint: errname, revive error-naming]
- Never shadow a predeclared name (`len`, `new`, `error`, `any`). [lint: predeclared, revive redefines-builtin-id]

## Doc comments

- Every exported name has a doc comment, a full sentence that starts with the
  name: `// Parse reads a config file and reports its errors.` [lint: revive exported]
- Every package has one package comment, `// Package lsp serves ...`, in one
  file; a command's starts `// Command name ...`. [lint: revive package-comments]
- Comments are sentences: capitalised, ending with a period; `// text`, not
  `//text`. [lint: revive comment-spacings; sentences review]
- Booleans "report whether": `// HasPrefix reports whether s begins with prefix.` [review]
- Document what a caller can't see from the signature: units, what nil or empty
  means, whether the zero value is ready to use, whether a type is safe for
  concurrent use, what must be closed, which errors to expect. Never restate the
  signature. [review]
- Unexported code gets comments where the why isn't obvious. [review]
- `Deprecated:` paragraphs mark replaced APIs. [review]
- Runnable `ExampleX` functions for the main entry points of a package others use. [review]

## Errors

- `error` is the last result; `nil` means success. [lint: revive error-return]
- Check every error: handle it, return it, or assign it to `_` with a comment
  saying why. [lint: errcheck, which allows fmt.Fprint* to stdout and stderr]
- Error strings are lower case with no final punctuation: they get wrapped. [lint: staticcheck ST1005]
- Wrap with the operation and the input, not "failed to" or "error":
  `fmt.Errorf("parse %s: %w", path, err)`. Don't repeat what the wrapped error
  already says. Put `%w` at the end. [review]
- `%w` when callers may inspect the cause with `errors.Is`/`As`; `%v` at a
  boundary that hides the implementation. [lint: errorlint for verbs; choice review]
- Compare with `errors.Is` and `errors.As`, never `==` or a type switch. [lint: errorlint]
- Export a sentinel (`var ErrNotFound = errors.New("not found")`) when callers
  branch on a condition, an error type when they need its data. Never make
  callers match error strings. [review]
- No in-band errors: return `(value, ok)` or `(value, error)`, not `-1` or `""`. [review]
- Handle the error first and return; the happy path stays unindented, no `else`
  after `return`. [lint: revive indent-error-flow, superfluous-else, early-return]
- Never `return nil` when `err != nil`. [lint: nilerr]
- Handle an error once: return it or log it, never both. [review]
- Don't panic. Return errors, including for bad input. Panic only for an
  impossible state (a bug), with a message; a panic never crosses a package
  boundary. `MustX` functions only at program start. [review]
- `os.Exit` and `log.Fatal` only in `main` (and `init`). [lint: revive deep-exit]
- Type assertions use the two-value form. [lint: forcetypeassert]

## Context and cancellation

- `ctx context.Context` is the first parameter of anything that blocks, does
  I/O or may be cancelled. [lint: revive context-as-argument]
- Never store a context in a struct; pass it down. [review]
- Context keys are unexported types; no custom context types. [lint: revive context-keys-type]
- `context.Background()` only in `main`; tests use `t.Context()`. [lint: usetesting in tests; review elsewhere]
- Long loops check `ctx.Err()` and stop; a cancelled call returns `ctx.Err()`
  (wrapped), not a partial result. [review]

## Goroutines and concurrency

- Prefer synchronous functions; let callers add concurrency. [review]
- Every goroutine has an owner who knows when and how it stops (a cancelled
  context, a closed channel) and waits for it. No fire-and-forget. [review]
- `wg.Go(f)` (Go 1.25), or `errgroup` when goroutines return errors. [lint: revive use-waitgroup-go]
- Share memory by communicating; when a mutex fits better, keep it next to the
  fields it guards and say so in a comment. [review]
- The sender closes a channel. Signatures state direction: `<-chan T`, `chan<- T`. [review]
- Never copy a value holding a `sync.Mutex` or `WaitGroup`. [lint: govet copylocks]
- `t.Fatal` only from the test's own goroutine. [lint: govet testinggoroutine]
- Tests run with `-race`; a race is a bug, never flaky. [lint: make test]
- Loop variables are per iteration since Go 1.22: no `v := v`. [lint: copyloopvar]

## Types and interfaces

- Define an interface where it is used, with only the methods that user needs
  (one to three). Accept interfaces, return concrete types. [review]
- Don't write an interface until a second implementation or a test fake needs it. [review]
- Make the zero value useful (`var b bytes.Buffer` works); say so in the doc. [review]
- Pointer receivers when a method mutates, the type holds a mutex, or it is
  large; one receiver kind per type. Writes to a value receiver are lost. [lint: revive modifies-value-receiver; rest review]
- Don't pass pointers to save bytes: strings, slices, maps, interfaces and
  small structs go by value. [review]
- `any`, not `interface{}`; and use it only when the value really is anything. [lint: revive use-any; rest review]
- Generics only when the same code serves several types today. [review]
- Type definitions, not aliases (aliases are for migrations). [review]
- Enums are typed constants with `iota`; a switch over one names every value. [lint: exhaustive]
- `var s []T` for an empty slice; APIs treat nil and empty alike. [review]
- `time.Duration` and `time.Time` for time, never bare ints; compare with
  `t.Equal(u)`. [lint: revive time-equal, time-naming; rest review]

## Functions and declarations

- `x := value` for a value, `var x T` for the zero value. [lint: revive var-declaration]
- Named results only to tell same-typed results apart; no naked returns. [lint: revive bare-return, confusing-results]
- `defer f.Close()` right after a successful open; check `Close` on files you
  wrote. No `defer` in a loop: extract a function. [lint: errcheck, revive defer]
- Close every HTTP response body. [lint: bodyclose]
- More than about four parameters, or several optional ones: an options struct. [review]
- No parameter that is unused or always gets the same value. [lint: revive unused-parameter, unparam]
- `for i := range n`; `strconv` over `fmt` for simple conversions; `%q` to quote. [lint: intrange, perfsprint; %q review]
- `http.MethodGet`, `http.StatusOK`, not `"GET"`, `200`. [lint: usestdlibvars]
- No needless conversions. [lint: unconvert]

## Global state and dependencies

- No mutable package-level variables and no `init()` with side effects. Flags
  are defined and parsed in `main` and passed down as values. [review]
- Inject what varies: `io.Writer`, `fs.FS`, a clock function, a config struct. [review]
- "A little copying is better than a little dependency": add a module only for
  real work, maintained and well known, with a licence that fits. [review]

## Security

- gosec findings are fixed, not silenced: `crypto/rand` for secrets, file
  modes `0o600`/`0o750`, no shell with user input, checked integer conversions. [lint: gosec]
- Paths from untrusted input (a request, a config file, a YAML document) are
  cleaned and confined to the directory they belong in. [review; gosec G304 is off for CLI arguments]
- Never log secrets or whole documents a user didn't ask to see. [review]

## Tests

- Table tests with `t.Run`, case names that say what is tested, struct fields
  named. [review]
- `t.Parallel()` in tests and subtests that share nothing mutable. [review]
- Failure messages name the call, input, got and want, got first:
  `t.Errorf("Parse(%q) = %v, want %v", in, got, want)`. [review]
- `t.Error` to keep going; `t.Fatal` only when the test can't continue. [review]
- Helpers call `t.Helper()` and take `t` first; they set up and return values,
  they don't assert. [lint: thelper; asserting review]
- No assertion libraries. Compare with `==`, `slices.Equal`, `maps.Equal`,
  `reflect.DeepEqual`, or `cmp.Diff` if go-cmp is already a dependency. [review]
- Golden files live in `testdata/` and are rewritten with `go test ./... -update`
  (a `flag.Bool("update", ...)` in the test file); review every golden diff. [review]
- `t.TempDir`, `t.Setenv`, `t.Chdir`, `t.Context`, `t.Cleanup`. [lint: usetesting]
- No `time.Sleep` to wait for something: use channels or an injected clock. [review]
- Test behaviour through the package's API; a test that breaks on every
  refactor tests the implementation. [review]
- Fuzz (`FuzzX`) parsers and anything that reads untrusted input. [review]

## Performance

- Measure before optimising. A budget in the issue or the system's needs (such
  as "within 200 ms on a 1,000-line file") becomes a `BenchmarkX` using
  `for b.Loop()` on realistic input, with the numbers in the pull request. [review]
- Preallocate (`make([]T, 0, n)`) when the size is known and it matters;
  `strings.Builder` to build strings in loops. [review]
- Don't optimise what no benchmark shows to be slow. [review]

## Go Proverbs, applied

- Don't communicate by sharing memory, share memory by communicating.
- Concurrency is not parallelism. Channels orchestrate; mutexes serialize.
- The bigger the interface, the weaker the abstraction.
- Make the zero value useful.
- `interface{}` says nothing.
- Gofmt's style is no one's favorite, yet gofmt is everyone's favorite.
- A little copying is better than a little dependency.
- Cgo is not Go; with the unsafe package there are no guarantees: use neither
  without a reason in the issue.
- Clear is better than clever. Reflection is never clear.
- Errors are values. Don't just check errors, handle them gracefully.
- Design the architecture, name the components, document the details.
- Documentation is for users.
- Don't panic.
