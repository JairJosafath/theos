# flutter-app (Framework)

A [Flutter](https://flutter.dev/) app for Android: Material 3 theme, a home page,
widget tests, and release signing from the environment.

- **Supports:** `mobile-app`
- **Layout:** Flutter's recommended architecture: `lib/ui/<feature>/`, `lib/data/`,
  `lib/domain/models/`; the app and theme in `lib/ui/core/`.
- **Writes:** `pubspec.yaml`, `lib/`, `test/`, `android/`, `README.md`, `.gitignore`,
  `.metadata`
- **Package:** always `app`, so imports read `package:app/...`. The Android
  application id is `io.factory.c4.<project>.<container>` (snake case); the label is
  the Container's display name.
- **Signing:** release builds use `ANDROID_KEYSTORE_PATH`, `ANDROID_KEYSTORE_PASSWORD`,
  `ANDROID_KEY_ALIAS` and `ANDROID_KEY_PASSWORD` when set, else the debug keys.
- **Network:** the main manifest asks for no permissions; debug and profile builds
  keep Flutter's `INTERNET` for hot reload.
- **Standard:** lists `very_good_analysis` in `dev_dependencies`, which
  flutter-standard's `analysis_options.yaml` includes. Other Standards must fit that
  or bring their own lints through a later Framework version.
- **Devcontainer:** `.c4factory/devcontainer.json` uses `c4factory/flutter:3.47.5`,
  the same toolchain CI and agents use.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix; a `.tmpl` that renders to only whitespace is left out. Other files are copied
byte for byte.

Data: `.Project`, `.SoftwareSystem`, `.Container`, `.DisplayName`, `.Type`, the chosen
`.Framework`, `.Standard`, `.Compute` and `.Deployment` (empty when none is chosen),
and `.Connectors` (sorted names). Functions: `has`, `snake`, `upper`, `lower`, plus
the `text/template` builtins.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
