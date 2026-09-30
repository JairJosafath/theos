# Mobile quality and accessibility checklist

From Android core app quality (IDs in brackets, page updated 2026-09-21), Flutter
accessibility docs, and Android accessibility docs. Material 3's accessibility
pages could not be fetched (JS-only); their numbers match the Android/Flutter
ones below. Check every item before calling a screen done. **How** = how to
verify.

## Accessibility

- [ ] **Tap targets ≥ 48×48 dp** (iOS ≥ 44×44 pt). [Touch_Target_Size]
      How: `meetsGuideline(androidTapTargetGuideline)` and `iOSTapTargetGuideline`.
- [ ] **Contrast ≥ 4.5:1** for normal text, **≥ 3:1** for large text (≥ 18 sp,
      or ≥ 14 sp bold) and icons/graphics. [Visual_Contrast]
      How: `meetsGuideline(textContrastGuideline)`; check dark theme too.
- [ ] **Every interactive or meaningful element has a label.** Icon buttons have
      `tooltip`; images have `semanticLabel` or are `excludeFromSemantics` if
      decorative; custom gestures are wrapped in `Semantics(button: true,
      label: ...)`. [Content_Description]
      How: `meetsGuideline(labeledTapTargetGuideline)`; `find.bySemanticsLabel`.
- [ ] Labels describe purpose, not looks, and omit the type ("Save", not "Save
      button"); list items have unique labels; labels come from ARB. [Android a11y]
- [ ] **Colour is never the only signal** (add icon, text or shape). Usable in
      grayscale. [Flutter a11y]
- [ ] **Text scales to 200%** without clipping or overflow; no fixed-height
      text containers; no manual `textScaler` clamping. [Flutter a11y]
      How: widget test with `MediaQuery(data: ...copyWith(textScaler:
      TextScaler.linear(2.0)))` on a small screen size; no overflow errors.
- [ ] Every button does something; disabled controls are `onPressed: null`,
      not silent no-ops. [Flutter a11y "Active interactions"]
- [ ] No automatic context change (navigation, focus jump) while typing. [Flutter a11y]
- [ ] Destructive actions are undoable or confirmed; input errors say how to fix
      them. [Flutter a11y "Errors"]
- [ ] Focus/reading order follows visual order; related items merged with
      `MergeSemantics`. [review]
- [ ] Manual pass with TalkBack/VoiceOver on the main flow (note if skipped).

Test template (one per screen):

```dart
testWidgets('meets a11y guidelines', (tester) async {
  final handle = tester.ensureSemantics();
  await tester.pumpWidget(const App());
  await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
  await expectLater(tester, meetsGuideline(iOSTapTargetGuideline));
  await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
  await expectLater(tester, meetsGuideline(textContrastGuideline));
  handle.dispose();
});
```

## Visual and layout

- [ ] Works in portrait and landscape with the same features; fills the window,
      no letterboxing; handles fast rotation without losing state.
      [Display_State_Parity, Fullscreen_Display, Orientation_Transitions]
      How: widget tests at phone portrait, landscape and tablet sizes.
- [ ] Light and dark themes both supported. [Theme_Support]
- [ ] Text line length ~45-75 characters on wide screens (constrain width). [Line_Length]
- [ ] Graphics are crisp: vector icons, resolution-aware assets (`2.0x`, `3.0x`). [Graphic_Quality]
- [ ] Content respects safe areas and cut-outs (`SafeArea`). [review]

## Navigation and state

- [ ] System back button and back gesture work on every screen; no custom
      on-screen "back" replacing them; `PopScope` only to guard unsaved work.
      [Back_Button_Nav, Back_Gesture_Nav]
- [ ] Back or leaving the app never silently loses user work: unsaved edits
      prompt or auto-save. [State_Preservation]
- [ ] State survives rotation, backgrounding, sleep/lock and **process death**
      (restoration IDs, persisted drafts).
      [State_Preservation, App_Switcher, Sleep_Resume, Lock_Resume]
      How: Android dev option "Don't keep activities", background, return.
- [ ] Settings and user data survive reinstall via Auto Backup (don't exclude
      them without reason). [App_Data_Backup]

## Robustness and performance

- [ ] **No crash on empty, huge or malformed input**: empty text fields, zero,
      negative, very large numbers, non-numeric text, empty lists, corrupt or
      huge image files, cancelled pickers. Each has a visible, localised message.
      How: unit tests per input edge case; widget test for the empty state.
- [ ] No ANR: nothing blocks the UI isolate; heavy work in an isolate with a
      progress indicator. [Stability_ANR]
- [ ] Startup shows content or progress within 2 s. [App_Startup_Time]
- [ ] Frames render within 16 ms in profile mode on the main flow. [Rendering_Performance]
- [ ] Uncaught errors are reported via `FlutterError.onError` /
      `PlatformDispatcher.instance.onError` and shown as a friendly error, not a
      red screen in release. [review]
- [ ] No debug-only packages or flags in release builds. [Production_Build_Quality]
- [ ] targetSdk/compileSdk are current (Flutter defaults, not pinned low).
      [Target_SDK_Version, Compile_SDK_Version]

## Offline, privacy and permissions

- [ ] **Works offline when the needs say so**: no `INTERNET` permission unless a feature needs
      it; no feature silently fails without network. [review]
- [ ] **Minimal permissions**: prefer system pickers (photo picker, SAF) that need
      no storage permission; no permission the app does not use.
      [Minimize_Permissions]
- [ ] Permissions requested only when the feature is used, with a rationale;
      denial degrades gracefully with a way forward.
      [Runtime_Permissions, Permission_Rationale, Graceful_Degradation]
- [ ] User data stays in app-internal storage; nothing personal is logged.
      [Sensitive_Data_Storage, Sensitive_Data_Logging]
- [ ] Sharing uses the system share sheet. [System_Sharesheet]
- [ ] No hardware IDs; no dynamically loaded code; strong platform crypto only.
      [Hardware_IDs, App_Bundles, Cryptographic_Algorithms]
