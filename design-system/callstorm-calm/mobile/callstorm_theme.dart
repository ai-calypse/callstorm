// Generated tokens and a light theme adapter. See mobile/README.md.
import 'package:flutter/material.dart';

abstract final class CallstormColors {
  static const canvas = Color(0xFFF3F2EE);
  static const surface = Color(0xFFFFFFFF);
  static const sidebar = Color(0xFFEEEEE8);
  static const surfaceDetail = Color(0xFFFAF9F5);
  static const ink = Color(0xFF20211E);
  static const textSecondary = Color(0xFF666B5D);
  static const textCaption = Color(0xFF697060);
  static const mutedOriginal = Color(0xFF74766D);
  static const border = Color(0xFFE4E4DB);
  static const borderStrong = Color(0xFF828876);
  static const olive = Color(0xFF657F3B);
  static const lime = Color(0xFFE2ECCB);
  static const blue = Color(0xFFC6DCFA);
  static const lavender = Color(0xFFDBC5E2);
  static const peach = Color(0xFFF7D1A8);
  static const warm = Color(0xFFFAE9D7);
  static const action = Color(0xFF343E29);
  static const actionHover = Color(0xFF4C5B3B);
  static const onAction = Color(0xFFFFFFFF);
  static const focus = Color(0xFF657F3B);
  static const selection = Color(0xFFDFE8CB);
  static const selectionText = Color(0xFF34442C);
  static const summary = Color(0xFFE9EDDE);
  static const summaryBorder = Color(0xFFE0E5D5);
  static const successSurface = Color(0xFFE7EDDA);
  static const successText = Color(0xFF526A35);
  static const warningSurface = Color(0xFFF3E7D8);
  static const warningText = Color(0xFF624D34);
  static const dangerSurface = Color(0xFFF4E1DB);
  static const dangerText = Color(0xFF8A3E32);
  static const infoSurface = Color(0xFFE8EFF7);
  static const infoText = Color(0xFF3E5877);
  static const chartPrimary = Color(0xFF607DA2);
  static const chartComparison = Color(0xFF8A6698);
  static const chartEfficiency = Color(0xFF657F3B);
  static const chartTarget = Color(0xFF897047);
  static const disabledSurface = Color(0xFFE8E9E0);
  static const disabledText = Color(0xFF747A6C);
}

abstract final class CallstormSpace {
  static const s0 = 0.0;
  static const s4 = 4.0;
  static const s8 = 8.0;
  static const s12 = 12.0;
  static const s16 = 16.0;
  static const s20 = 20.0;
  static const s24 = 24.0;
  static const s32 = 32.0;
  static const s40 = 40.0;
  static const s48 = 48.0;
  static const s64 = 64.0;
}

abstract final class CallstormRadius {
  static const small = 4.0;
  static const control = 6.0;
  static const nested = 8.0;
  static const card = 12.0;
  static const dialog = 14.0;
  static const pill = 999.0;
}

abstract final class CallstormType {
  static const page = TextStyle(fontSize: 33.0, height: 1.2, letterSpacing: -1.25, fontWeight: FontWeight.w400);
  static const insight = TextStyle(fontSize: 25.0, height: 1.2, letterSpacing: -0.7, fontWeight: FontWeight.w400);
  static const section = TextStyle(fontSize: 21.0, height: 1.35, letterSpacing: -0.5, fontWeight: FontWeight.w400);
  static const card = TextStyle(fontSize: 20.0, height: 1.35, letterSpacing: -0.5, fontWeight: FontWeight.w400);
  static const body = TextStyle(fontSize: 16.0, height: 1.6, letterSpacing: 0.0, fontWeight: FontWeight.w400);
  static const label = TextStyle(fontSize: 14.0, height: 1.4, letterSpacing: 0.0, fontWeight: FontWeight.w400);
  static const caption = TextStyle(fontSize: 12.0, height: 1.5, letterSpacing: 0.0, fontWeight: FontWeight.w400);
  static const eyebrow = TextStyle(fontSize: 12.0, height: 1.5, letterSpacing: 1.5, fontWeight: FontWeight.w400);
  static const metric = TextStyle(fontSize: 37.0, height: 1.1, letterSpacing: -1.0, fontWeight: FontWeight.w400);
}

ThemeData callstormTheme() {
  final scheme = ColorScheme.fromSeed(
    seedColor: CallstormColors.olive,
    brightness: Brightness.light,
  ).copyWith(
    primary: CallstormColors.action,
    onPrimary: CallstormColors.onAction,
    secondary: CallstormColors.lavender,
    onSecondary: CallstormColors.ink,
    surface: CallstormColors.surface,
    onSurface: CallstormColors.ink,
    error: CallstormColors.dangerText,
    onError: CallstormColors.onAction,
    outline: CallstormColors.borderStrong,
  );
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    scaffoldBackgroundColor: CallstormColors.canvas,
    dividerColor: CallstormColors.border,
    textTheme: const TextTheme(
      headlineLarge: CallstormType.page,
      headlineMedium: CallstormType.insight,
      titleLarge: CallstormType.section,
      titleMedium: CallstormType.card,
      bodyLarge: CallstormType.body,
      bodyMedium: CallstormType.label,
      labelSmall: CallstormType.caption,
    ).apply(bodyColor: CallstormColors.ink, displayColor: CallstormColors.ink),
    appBarTheme: const AppBarTheme(
      backgroundColor: CallstormColors.canvas,
      foregroundColor: CallstormColors.ink,
      elevation: 0,
      scrolledUnderElevation: 0,
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        minimumSize: const Size(48, 48),
        backgroundColor: CallstormColors.action,
        foregroundColor: CallstormColors.onAction,
        elevation: 0,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(CallstormRadius.control)),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        textStyle: CallstormType.label,
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(48, 48),
        foregroundColor: CallstormColors.ink,
        side: const BorderSide(color: CallstormColors.borderStrong),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(CallstormRadius.control)),
      ),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: CallstormColors.surface,
      contentPadding: const EdgeInsets.all(16),
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(CallstormRadius.control)),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(CallstormRadius.control),
        borderSide: const BorderSide(color: CallstormColors.borderStrong),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(CallstormRadius.control),
        borderSide: const BorderSide(color: CallstormColors.focus, width: 2),
      ),
    ),
  );
}
