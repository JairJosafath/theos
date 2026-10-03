# Effective Dart digest

Source: https://dart.dev/effective-dart (style, documentation, usage, design).
`[lint: name]` = enforced by `make check` via very_good_analysis 11.0.0 (or the
analyzer). `[review]` = no lint; check it yourself. `[off]` = lint exists but is
disabled here (see `analysis_options.yaml`).

Two themes: **be consistent** (code looks different only when it is different)
and **be brief** (economical, not dense).

## Style

- UpperCamelCase types, extensions, enums, typedefs. [lint: camel_case_types, camel_case_extensions]
- lowercase_with_underscores for files, directories, packages, import prefixes.
  [lint: file_names, package_names, library_prefixes]
- lowerCamelCase for everything else, including constants. [lint: non_constant_identifier_names, constant_identifier_names]
- Acronyms longer than two letters are capitalised like words: `HttpRequest`, `Id`. [review]
- Unused callback parameters use `_`. [lint: no_wildcard_variable_uses, unnecessary_underscores]
- No leading `_` on non-private names; no Hungarian/prefix letters. [lint: no_leading_underscores_for_local_identifiers; rest review]
- No `library` names. [lint: unnecessary_library_name, unnecessary_library_directive]
- Imports: `dart:` then `package:`; exports after; sorted. [lint: directives_ordering]
- Always `package:` imports inside `lib/`. [lint: always_use_package_imports, avoid_relative_lib_imports]
- `dart format`; trailing commas automated by the formatter. [lint: formatter check in `make check`, require_trailing_commas]
- Lines ≤ 80 chars. [lint: lines_longer_than_80_chars]
- Braces on all flow control. [lint: curly_braces_in_flow_control_structures]
- Single quotes. [lint: prefer_single_quotes]

## Documentation

- `///` for doc comments, never `/** */`. [lint: slash_for_doc_comments]
- Doc every public API. [off: public_member_api_docs — app code; see below]
  Still document anything non-obvious: units, edge cases, lifecycle, ownership. [review]
- First sentence: a one-line summary, its own paragraph. [review]
- Don't restate the name or type; avoid redundancy with context. [review]
- Side-effect functions start with a third-person verb ("Saves..."); value-returning
  ones with a noun phrase; booleans with "Whether ...". [review]
- Refer to identifiers with `[name]`; references must resolve. [lint: comment_references]
- Explain parameters, returns and exceptions in prose, not `@param` tags. [review]
- Doc comments go before annotations. [review]
- Code blocks use backtick fences with a language. [lint: missing_code_block_language_in_doc_comment]
- No HTML in docs; no `<T>` unescaped. [lint: unintended_html_in_doc_comment]
- Every `// ignore:` has a reason comment. [lint: document_ignores, unnecessary_ignore]
- TODOs: `// TODO(owner): text, see issue`. [lint: flutter_style_todos]

## Usage

Null and variables
- Don't initialise to `null`; no explicit `null` default. [lint: avoid_init_to_null]
- No `== true`/`== false`. [lint: no_literal_bool_comparisons]
- Avoid `late` when you need to know if it was set; use a nullable + promotion. [review]
- Use one consistent `var`/`final` rule: `final` for locals never reassigned. [lint: prefer_final_locals, omit_local_variable_types]
- Don't store what you can compute. [review]

Strings and collections
- Interpolation over `+`; no needless `${}`. [lint: prefer_interpolation_to_compose_strings, unnecessary_brace_in_string_interps]
- Adjacent literals, not `+`, to join literals. [lint: prefer_adjacent_string_concatenation]
- Collection literals; `isEmpty`/`isNotEmpty`, not `length == 0`. [lint: prefer_collection_literals, prefer_is_empty, prefer_is_not_empty]
- `for-in`, not `forEach` with a literal. [lint: avoid_function_literals_in_foreach_calls]
- `whereType<T>()`, not `where(...).cast()`; avoid `cast()`. [lint: prefer_iterable_whereType; rest review]
- `List.of`/`toList()`, not `List.from()`, unless changing the type. [review]

Functions and members
- Function declarations, not closures in variables; tear-offs over lambdas. [lint: prefer_function_declarations_over_variables, unnecessary_lambdas]
- No trivial getter/setter wrappers; `final` for read-only fields. [lint: unnecessary_getters_setters, prefer_final_fields]
- No `this.` except to disambiguate. [lint: unnecessary_this]
- Initialise fields at declaration; initialising formals; `super.` parameters.
  [lint: initialize_in_field_declaration, prefer_initializing_formals, use_super_parameters]
- `;` for empty constructor bodies; no `new`; no redundant `const`.
  [lint: empty_constructor_bodies, unnecessary_new, unnecessary_const]
- Declaring (primary constructor) parameters where they fit. [lint: use_declaring_parameters, unnecessary_primary_constructor_body]

Errors
- `catch` always has an `on` clause. [lint: avoid_catches_without_on_clauses]
- Never catch `Error` (bugs); throw `Error` only for programmer mistakes. [lint: avoid_catching_errors; rest review]
- Only throw `Exception`/`Error` subtypes. [lint: only_throw_errors]
- `rethrow`, not `throw e`. [lint: use_rethrow_when_possible]
- Never an empty `catch`. [lint: empty_catches]

Async
- `async`/`await` over `.then`. [review]
- Every `Future` is awaited or explicitly `unawaited(...)`. [lint: unawaited_futures, discarded_futures]
- `Future<void>`, never `void`, for async functions. [lint: avoid_void_async]
- No `async` that does nothing; no `Completer` unless bridging callbacks. [lint: async_return_with_no_await; rest review]
- Cancel every `StreamSubscription`. [lint: cancel_subscriptions]
- Close every `StreamController` you create. [review — close_sinks has false positives, ignored]
- No slow sync IO (`File.existsSync` in async code). [lint: avoid_slow_async_io]

## Design

Names
- Use terms consistently; avoid abbreviations. [review]
- Most descriptive noun last (`pageCount`); read like a sentence. [review]
- Booleans: non-imperative verb/adjective, positive form (`isEnabled`). [review]
- Side effect → imperative verb (`save()`); value → noun (`size`, not `getSize()`). [review]
- `toX()` copies, `asX()` views. [lint: use_to_and_as_if_applicable]

Libraries and classes
- Private by default. [review]
- Several related small classes per library is fine. [review]
- No one-method abstract class when a function type will do. [review]
- Use class modifiers (`final`, `sealed`, `base`, `interface`) to state intent;
  `sealed` for closed hierarchies so `switch` is exhaustive. [review; exhaustive_cases lint]
- Pure `mixin`/`class`, not `mixin class`. [lint: prefer_mixin]
- `const` constructors whenever the class allows. [lint: prefer_const_constructors_in_immutables]

Members and types
- `final` fields and top-level variables. [lint: prefer_final_fields]
- Getters for property-like reads, setters come with a getter. [lint: avoid_setters_without_getters, use_setters_to_change_properties]
- Don't fake overloading with runtime `is` checks. [review]
- Never return nullable `Future`/`Stream`/collections; return empty. [review]
- No `return this` for fluent APIs; use cascades. [lint: avoid_returning_this]
- Annotate return and parameter types on declarations; not on closures or
  initialised locals. [lint: always_declare_return_types, type_annotate_public_apis,
  avoid_types_on_closure_parameters, omit_local_variable_types, strict_top_level_inference]
- No raw generics, no implicit `dynamic`, no implicit downcasts.
  [analyzer: strict-raw-types, strict-inference, strict-casts; lint: avoid_dynamic_calls]
- Avoid `FutureOr<T>` as a return type. [review]

Parameters
- No positional `bool` parameters; use named. [lint: avoid_positional_boolean_parameters]
- Required named parameters first. [lint: always_put_required_named_parameters_first]
- No "magic" sentinel arguments; make the parameter optional. [review]
- Ranges are start-inclusive, end-exclusive. [review]

Equality
- Override `hashCode` with `==`; use `Object.hash`. [lint: hash_and_equals]
- No custom `==` on mutable classes. [lint: avoid_equals_and_hash_code_on_mutable_classes]
- `==` parameter is `Object`, not nullable; obeys equality laws. [review]
- Never override `==` on widgets (O(N²) rebuild cost). [review]
