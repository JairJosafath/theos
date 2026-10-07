# Python digest

Distilled from PEP 8, PEP 257, the Google Python Style Guide, the typing and
mypy docs, pytest's good practices and the FastAPI tutorial (see `SOURCES.md`).
`[lint: x]` = enforced by `make check`: a ruff rule in `ruff.toml` (code
shown), a mypy flag in `mypy.ini`, or the formatter. `[review]` = no tool;
check it yourself. Where a tool and a guide disagree, the tool wins and the
rule says so.

## Layout and formatting

- `ruff format` formats everything (`make fmt`): 4 spaces, double quotes,
  trailing commas, blank lines around definitions. Never hand-format. [lint: ruff format]
- Lines are at most 100 columns (PEP 8 says 79, Google 80; the tool wins). [lint: E501]
- Imports are sorted by `make fmt`; the rest of the layout is the formatter's. [lint: I001]
- One statement per line; no `;` and no `if x: y` on one line. [lint: E701, E702]
- Compare to `None` with `is`, never `==`; use `isinstance` for type checks. [lint: E711, E721]
- Use the truth of a value (`if items:`), not `len(items) == 0` or `== []`;
  but `is None` when `None` is what you mean. [review]
- A `def`, not a `lambda` bound to a name. [lint: E731]
- Format strings with f-strings; build large strings with `"".join`, not `+`
  in a loop. [lint: UP032; join review]
- A function is about 40 lines at most; a longer one is two functions. [review]

## Imports

- Imports go at the top of the module, one per line, grouped standard
  library, third party, first party, each group sorted. [lint: I001, E402, E401]
- Absolute imports only; no relative imports, even within a package
  (stricter than PEP 8). [lint: TID252]
- Import modules and packages (`import x`, `from pkg import module`); import a
  name directly only from `typing`, `collections.abc` or when it is the point
  of the module. [review]
- No `from x import *`. [lint: F403]
- No import inside a function to dodge a cycle; fix the cycle. [lint: PLC0415; cycles review]
- Imports used only in annotations go under `if TYPE_CHECKING:` (the TC rules
  fire only once annotations are deferred). [review]
- No unused imports; no import of a private name (`_x`) from another module. [lint: F401; private names review]

## Naming

- `snake_case` for modules, packages, functions, methods, variables and
  parameters. [lint: N802, N806, N999]
- `CapWords` for classes and type aliases. [lint: N801]
- `UPPER_CASE` for module-level constants; a constant never changes. [review]
- Exception classes end in `Error`. [lint: N818]
- Acronyms are words: `HttpClient`, `load_json`, not `HTTPClient`. [review]
- A leading underscore marks a name private to its module or class; no
  double-underscore mangling unless a subclass clash is real. [review]
- No single-letter names beyond a short loop or comprehension; never `l`, `O`,
  `I`. [lint: E741; scope review]
- Don't shadow a built-in (`id`, `type`, `list`, `input`). [lint: A001, A002, A003]
- No `get_` prefix on plain reads; verbs for actions, nouns for values,
  booleans read as assertions (`is_valid`, `has_more`). [review]
- Don't repeat the module in a name: `report.Writer`, not
  `report.ReportWriter`. [review]
- Module names say what they provide; never `utils`, `common`, `helpers`,
  `misc`, `base`. [review]

## Docstrings and comments

- Every public module, class, function and method has a docstring in triple
  double quotes. [lint: D100, D101, D102, D103, D104, D107]
- The summary is one line, in the imperative ("Return the total."), ending
  with a period, then a blank line, then the detail. [lint: D205, D415; mood review]
- Google style: `Args:`, `Returns:` (`Yields:`), `Raises:` sections, one entry
  per parameter. [lint: D417; sections review]
- A docstring says what the code does not: units, what `None` and empty mean,
  what it raises, side effects, who closes what; never a restatement of the
  name. [review]
- A class docstring describes the object, and `Attributes:` lists public
  attributes. [review]
- Comments say why, not what; complete sentences. [review]
- No commented-out code. [lint: ERA001]
- A `TODO` names its issue: `# TODO(#12): ...`. [lint: TD003, TD004]
- `noqa` and `type: ignore` name one code and give a reason; never bare. [lint: PGH003, PGH004, RUF100; reason review]

## Types

- `mypy --strict`: every function is annotated, no implicit `Any`, no
  implicit `Optional`, no untyped decorators, no returning `Any`. [lint: mypy strict]
- Annotate every argument and return, `-> None` included. [lint: ANN001, ANN201, ANN202, ANN204]
- No `Any` in an annotation; use `object`, a `Protocol` or a type variable
  (`Any` is for a value that truly is anything, narrowed before use). [lint: ANN401; narrowing review]
- Built-in generics and `X | Y`, not `typing.List`, `Optional` or `Union`. [lint: UP006, UP007, UP045, UP035]
- Unreachable code and always-true checks are errors. [lint: mypy warn_unreachable, redundant-expr, truthy-bool]
- `# type: ignore` carries its error code. [lint: mypy ignore-without-code]
- A name has one type; an empty collection is annotated (`a: list[int] = []`). [lint: mypy strict]
- Arguments take abstract types (`Sequence`, `Mapping`, `Iterable`); returns
  are concrete (`list`, `dict`). [review]
- Avoid union return types; split the function or return a small type. [review]
- Parse untrusted input into a typed value once, at the edge; no `cast()` to
  silence mypy. [review]
- Overriding methods say `@override`. [lint: mypy explicit-override]
- A `Protocol` for what a caller needs, not a base class. [review]
- Boolean flags are keyword-only. [lint: FBT001, FBT002]

## Errors

- Raise specific built-in or own exceptions, never bare `Exception`; your own
  subclass when callers branch on it. [lint: TRY002; own class review]
- Catch the narrowest exception; no bare `except:` and no `except Exception`
  to keep going. [lint: E722, BLE001]
- `raise ... from err` when translating an exception. [lint: B904]
- Keep `try` bodies small; do the success path in `else`. [lint: TRY300; size review]
- Never swallow an error: no `except: pass`. [lint: S110, SIM105]
- Messages are built in a variable, not inline in `raise`, and say what was
  wrong, with which input. [lint: EM101, EM102; content review]
- Handle an error once: raise it or log it, never both. In a handler, log
  with `logger.exception`. [lint: TRY400; once review]
- No `assert` in library code (it is stripped under `-O`); an impossible
  branch raises an error, and an exhaustive `match` ends in `assert_never`. [lint: S101]
- Logging uses lazy `%s` arguments, not f-strings, and a module logger; no
  `print` in library code. [lint: G004, T201]

## Functions and classes

- One function, one job; early return over nesting, no `else` after
  `return`. [lint: RET505; job review]
- At most 5 parameters; more means a missing type. [lint: PLR0913, PLR0917]
- Complexity stays low: at most 10 (cyclomatic), 12 branches, 50 statements. [lint: C901, PLR0912, PLR0915]
- Never a mutable default argument; default to `None` and build it inside. [lint: B006]
- Don't reassign a loop variable or a parameter inside its loop. [lint: PLW2901]
- No `global` statement; no mutable module-level state, no work at import
  time. [lint: PLW0603; state review]
- No mutable class attribute without `ClassVar`. [lint: RUF012]
- A `@property` is a cheap read with no side effects. [review]
- Dependencies are passed in (clocks, clients, file systems), not reached
  for. [review]
- Don't touch another object's private members. [lint: SLF001]
- Unused arguments are removed. [lint: ARG001, ARG002]
- Files, connections and clients are opened in `with` (or `async with`). [lint: SIM115, PTH123; scope review]
- `main()` holds the entry logic; the module guard is `if __name__ == "__main__":`. [review]
- Use `pathlib`, not `os.path`. [lint: PTH]
- Timezone-aware datetimes only. [lint: DTZ005]
- Prefer comprehensions over `append` loops; no comprehension with more than
  one `for`. [lint: PERF401, PERF402; multi-for review]

## Data

- Records are `@dataclass(frozen=True, slots=True)` or pydantic models, not
  dicts passed around. [review]
- `enum.Enum` for a closed set; never a string compared against literals. [review]
- A named constant for a magic number. [lint: PLR2004]
- Immutable data and pure functions where you can. [review]

## Tests

- pytest functions, no `unittest.TestCase`; files `test_*.py` under `tests/`,
  which is outside the package. [lint: PT009; layout review]
- Plain `assert` in tests, one claim each; no `assert a and b`. [lint: PT018]
- `pytest.raises` names the exception and `match=`, around one statement. [lint: PT011, PT012]
- Parametrize with a tuple of names and a list of tuples, and give `ids`. [lint: PT006, PT007; ids review]
- A fixture decorator takes no empty parentheses, and `scope` only when not
  the default. [lint: PT001, PT003]
- Test names say what is expected: `test_total_of_empty_order_is_zero`. [review]
- Fixtures do setup; `monkeypatch` and `tmp_path` replace the real
  environment. [review]
- No `time.sleep`, real network, shared state or order dependence. [review]
- Test behaviour through the public API; prefer real implementations and
  fakes to mocks. [review]
- Markers and config are strict: unknown ones fail (`--strict-markers`,
  `--strict-config`). [lint: pytest flags in `make test`]
- A bug fix starts with the failing test. [review]

## FastAPI (applies to FastAPI services)

- A path operation is thin: parse, call a function that holds the logic, map
  its result and errors to HTTP. [review]
- Pydantic models for every request and response body, never bare dicts; set
  `response_model` or annotate the return. [review]
- Declare every parameter's type; path and query parameters are validated from
  it. [lint: ANN001, FAST003]
- Shared resources come through `Annotated[T, Depends(f)]`, never
  `= Depends(f)`; tests override them with `app.dependency_overrides`. [lint: FAST002; use review]
- A dependency that holds a resource uses `yield` so cleanup runs. [review]
- Raise `HTTPException` (never return one) with the right status code (201
  for a created resource, 404 for a missing one) and a `detail` a client can
  act on; register handlers for own exceptions. [review]
- Don't repeat `response_model` as a return annotation. [lint: FAST001]
- `async def` only when everything awaited is async; a blocking call goes in a
  plain `def`, which FastAPI runs in a thread. [lint: ASYNC210, ASYNC230, ASYNC251; choice review]
- Test with `TestClient` and plain `def` tests, `assert` on status and JSON. [review]
- `/healthz` stays cheap and calls no dependency. [review]
