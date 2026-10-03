# Flutter app digest

Sources: Flutter architecture guide (docs.flutter.dev/app-architecture), Flutter
repo style guide, Flutter performance, testing, accessibility, l10n and state
restoration docs (see SOURCES.md). `[lint: x]` enforced by `make check`;
`[review]` check yourself.

## Architecture (MVVM, two layers + optional domain)

```
lib/
  ui/<feature>/            view (widgets) + view_model.dart
  domain/models/           immutable models (and use cases, only if needed)
  data/repositories/       abstract Repository + implementations
  data/services/           stateless wrappers: files, platform plugins, codecs
  utils/                   result.dart, command.dart
  l10n/                    app_en.arb (+ generated code)
test/                      mirrors lib/; testing/fakes/ for fake repositories
```

- UI layer: **view** (widgets) + **view model** (`ChangeNotifier`), 1:1. [review]
- Data layer: **repositories** are the single source of truth; **services** wrap
  one data source (file system, image codec, platform channel) and hold no state. [review]
- Dependencies point down only: View → ViewModel → Repository → Service.
  Repositories never know about each other. [review]
- **No logic in widgets.** A view may contain only: simple `if` to show/hide,
  layout, animation, simple routing. All data logic is in the view model. [review]
- View models take repositories in the constructor, expose read-only state
  (`UnmodifiableListView`, getters) and **commands** for user actions. [review]
- Unidirectional flow: event → view model command → repository → new state →
  `notifyListeners()` → rebuild. Never mutate state from a widget. [review]
- Immutable data models (`final` fields, `copyWith`, `const`). [review; prefer_final_fields]
- Expected failures return `Result<T>` (`sealed`: `Ok`/`Error`); view models
  `switch` over it exhaustively. Exceptions are for bugs. [review; exhaustive_cases]
- Abstract repository classes + dependency injection (constructor or
  `provider`), so tests can swap in fakes. [review]
- Add a domain layer (use cases) only when logic spans repositories or is reused
  across view models. [review]
- Apps whose needs say "offline" have no network code. Services wrap local IO;
  heavy work (image processing) runs off the UI isolate (`compute`/`Isolate.run`). [review]

## Widgets

- Split big `build` methods into small **widget classes**, not helper methods
  returning widgets. [review; perf doc]
- `const` constructors and `const` instances wherever possible.
  [lint: prefer_const_constructors, prefer_const_literals_to_create_immutables, prefer_const_constructors_in_immutables]
- Every public widget constructor takes `super.key`. [lint: use_key_in_widget_constructors, use_super_parameters]
- Use keys (`ValueKey(item.id)`) on stateful children of lists that reorder,
  insert or remove. Avoid `GlobalKey` unless you truly need cross-tree access. [review]
- `child` argument last. [lint: sort_child_properties_last]
- `SizedBox` for spacing, `ColoredBox` for colour, no needless `Container`.
  [lint: sized_box_for_whitespace, use_colored_box, avoid_unnecessary_containers]
- No logic in `createState`. [lint: no_logic_in_create_state]
- Private `State` types stay private without leaking into public API. [lint: library_private_types_in_public_api]
- Callbacks: typedef `FooCallback`, property `onFoo`, handler `_handleFoo`;
  present tense (`onTap`). [review]
- Constructors first; `build` last before `==`/`toString`. [lint: sort_constructors_first; rest review]

## State and lifecycle

- `StatelessWidget` by default; `StatefulWidget` only for ephemeral UI state
  (animation, text field, focus). App state lives in view models. [review]
- `setState` as low in the tree as possible; comment empty `setState(() {})`. [review]
- **Whoever creates it disposes it**: `AnimationController`, `TextEditingController`,
  `ScrollController`, `FocusNode`, `ChangeNotifier`, `Timer`, `StreamSubscription`,
  `StreamController`, `ui.Image`. Dispose in `dispose()` in reverse creation order,
  before `super.dispose()`. [review; cancel_subscriptions lint covers subscriptions]
- Never create controllers in `build`. Create in `initState` or a field
  initialiser. [review]
- Don't pass a `ChangeNotifier` you did not create to something that disposes it. [review]
- `BuildContext` across `await`: check `if (!context.mounted) return;`
  (or `mounted` in `State`). [lint: use_build_context_synchronously]
- Don't start async work in `build`; start it in a command or `initState`. [review]
- Every `Future` is awaited or `unawaited(...)`. [lint: unawaited_futures, discarded_futures]

## State restoration (rotation, process death)

- `MaterialApp(restorationScopeId: 'app')`. [review]
- `restorationId` on `TextField`, scrollables and routes that hold user input. [review]
- Custom state that the user would lose: `RestorationMixin` + `Restorable*`
  properties; navigation via `restorablePush*`. [review]
- Persist real user data (edits, settings) through a repository, not restoration;
  restoration holds instance state only (≤ 1 MB on Android). [review]

## Theming

- One `ThemeData(colorScheme: ColorScheme.fromSeed(...))` (Material 3 is the default)
  plus `darkTheme`; `themeMode: ThemeMode.system`. [review]
- Read colours and text styles from `Theme.of(context).colorScheme` /
  `.textTheme`; no hard-coded `Color(...)`/font sizes in widgets. [review]
- Spacing and sizes are named constants, not magic numbers. [review]

## Localisation readiness

- `flutter: generate: true`, `l10n.yaml` with `arb-dir: lib/l10n`,
  `template-arb-file: app_en.arb`; `flutter_localizations` + `intl`. [review]
- **No user-visible string literals in widgets**; every string is an ARB entry
  with an `@key` description; plurals and placeholders use ICU syntax. [review]
- Numbers, dates and units are formatted with `intl` for the current locale. [review]
- Layouts work RTL: `EdgeInsetsDirectional`, `AlignmentDirectional`, `start/end`. [review]

## Performance

- Keep `build` cheap: no IO, parsing, sorting or image decoding in `build`. [review]
- Lazy lists: `ListView.builder`/`GridView.builder` for anything unbounded. [review]
- Avoid `Opacity` in animations (use `AnimatedOpacity`/`FadeTransition`), avoid
  needless `saveLayer`/clipping; use `borderRadius` over clips. [review]
- Work longer than a frame (~16 ms) goes to an isolate. [review]
- `StringBuffer` for building strings in loops. [lint: use_string_buffers]
- No `print`; use `debugPrint`/`log` only for actionable diagnostics. [lint: avoid_print]

## Testing

- Every change ships with tests; bug fixes start with a failing test. [review]
- Unit tests for view models and repositories with **fakes** implementing the
  abstract repository/service (no network, no real file system). [review]
- Widget tests for every screen: renders, empty state, error state, main
  interactions (`tester.tap`, `pumpAndSettle`). [review]
- Accessibility guideline test per screen: `androidTapTargetGuideline`,
  `iOSTapTargetGuideline`, `labeledTapTargetGuideline`, `textContrastGuideline`
  (see `mobile-quality.md`). [review]
- Golden tests (`matchesGoldenFile`) for key screens in light and dark; update
  with `flutter test --update-goldens`, commit PNGs, and generate them on the
  same OS/Flutter version as CI (fonts render differently per platform). [review]
- Tests are self-contained: local setup, `addTearDown`, duplication is fine,
  one feature per test file. [review]
- `throwsA(isA<...>())` for error expectations. [lint: use_test_throws_matchers]
- Integration test for the main end-to-end flow (`integration_test`). [review]
