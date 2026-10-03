import 'package:flutter/material.dart';

/// The app's light and dark Material 3 themes, from one seed colour.
abstract final class AppTheme {
  static const _seed = Color(0xFF3F51B5);

  static ThemeData light() => _theme(Brightness.light);

  static ThemeData dark() => _theme(Brightness.dark);

  static ThemeData _theme(Brightness brightness) {
    return ThemeData(
      colorScheme: ColorScheme.fromSeed(
        seedColor: _seed,
        brightness: brightness,
      ),
    );
  }
}
