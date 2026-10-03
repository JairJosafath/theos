# android-release (Deployment)

Ships a Flutter Android app as a signed APK, attached to a Gitea release, whenever
a version tag is pushed. No store accounts, no review queue: install it from the
release page.

- **Supports:** `mobile-app`
- **Writes:** `.gitea/workflows/release.yaml`
- **Needs:** Actions secrets `ANDROID_KEYSTORE_B64` (base64 of a JKS keystore),
  `ANDROID_KEYSTORE_PASSWORD` and `ANDROID_KEY_ALIAS` on the organization, which the
  seed job creates; a runner labelled `flutter-android` (the full toolchain image with
  the Android SDK; `make runner-android`). The Framework's Gradle file must
  read `ANDROID_KEYSTORE_PATH`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS` and
  `ANDROID_KEY_PASSWORD`, as flutter-app's does.
- **Release:** `git tag v0.1.0 && git push origin v0.1.0`, or create the tag in Gitea.

The workflow is copied byte for byte: its `${{ }}` expressions are Gitea's, not the
factory's.
