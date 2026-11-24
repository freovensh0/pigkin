package io.pigkin.ui.theme

import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color

/**
 * Pigkin's palette: eight chrome roles, plus the content colours that code needs. Git status, diffs and terminal ANSI
 * output are content rather than chrome, so they keep their own hues in both themes.
 */
@Immutable
data class PigkinColors(
  val isDark: Boolean,
  /** The page. */
  val surface0: Color,
  /** A raised card or sheet. */
  val surface1: Color,
  /** One step above [surface1]: menus, pressed rows, code wells. */
  val surface2: Color,
  /** The top layer, such as a popup over a raised card. */
  val surface3: Color,
  val sidebar: Color,
  val foreground: Color,
  /** [foreground] at 70%: secondary text. */
  val foregroundMuted: Color,
  /** [foreground] at 45%: hints and disabled text. */
  val foregroundExtraMuted: Color,
  /** The outline ink at 20%: dividers and inactive field borders. */
  val border: Color,
  /** The outline ink at 32%: panel edges and status dots. */
  val borderAccent: Color,
  val accent: Color,
  val accentForeground: Color,
  /** [accent] at 16%: a selection wash behind accent text. */
  val accentSoft: Color,
  /** The accent on an inverse surface, such as a snackbar's action. */
  val accentInverse: Color,
  val destructive: Color,
  val destructiveForeground: Color,
  /** Git's "added", and the terminal's green: content, never chrome. */
  val dotSuccess: Color,
  val dotDanger: Color,
  val dotWarning: Color,
  /** Informational notices and the cyan ANSI colour. */
  val info: Color,
  /** Extension messages and the magenta ANSI colour. */
  val extension: Color,
  /** Code comments: quieter than code and still readable at 4.5:1. */
  val codeComment: Color,
) {
  val successSoft: Color get() = dotSuccess.copy(alpha = if (isDark) 0.13f else 0.10f)
  val warningSoft: Color get() = dotWarning.copy(alpha = if (isDark) 0.14f else 0.12f)
  val dangerSoft: Color get() = dotDanger.copy(alpha = if (isDark) 0.14f else 0.10f)
  val infoSoft: Color get() = info.copy(alpha = 0.12f)
}

private val OutlineLight = Color(0xFFC9C9D1)
private val OutlineDark = Color(0xFF1C1C1F)

/**
 * Porcelain: the light theme. It is a token flip of [StoneColors], with the same components and layout. The accent is
 * darker than the dark theme's so white text on it keeps 4.5:1.
 */
val PorcelainColors =
  PigkinColors(
    isDark = false,
    surface0 = Color(0xFFFAFAF8),
    surface1 = Color(0xFFFFFFFF),
    surface2 = Color(0xFFF0F0ED),
    surface3 = Color(0xFFE4E4E1),
    sidebar = Color(0xFFFAFAF8),
    foreground = Color(0xFF151517),
    foregroundMuted = Color(0xFF5C5C63),
    foregroundExtraMuted = Color(0xFF8E8E94),
    border = OutlineDark.copy(alpha = 0.20f),
    borderAccent = OutlineDark.copy(alpha = 0.32f),
    accent = Color(0xFF2F5C7A),
    accentForeground = Color(0xFFFFFFFF),
    accentSoft = Color(0xFF2F5C7A).copy(alpha = 0.16f),
    accentInverse = Color(0xFF2F5C7A),
    destructive = Color(0xFFC62A2F),
    destructiveForeground = Color(0xFFFFFFFF),
    dotSuccess = Color(0xFF187046),
    dotDanger = Color(0xFFC62A2F),
    dotWarning = Color(0xFF8A5A00),
    info = Color(0xFF25676A),
    extension = Color(0xFF7A4789),
    codeComment = Color(0xFF6A6A71),
  )

/** Stone: the dark theme, and the one Pigkin is designed on. */
val StoneColors =
  PigkinColors(
    isDark = true,
    surface0 = Color(0xFF0F0F11),
    surface1 = Color(0xFF17171A),
    surface2 = Color(0xFF1F1F23),
    surface3 = Color(0xFF29292E),
    sidebar = Color(0xFF0F0F11),
    foreground = Color(0xFFE9E9EC),
    foregroundMuted = Color(0xFFA8A8B0),
    foregroundExtraMuted = Color(0xFF6C6C74),
    border = OutlineLight.copy(alpha = 0.20f),
    borderAccent = OutlineLight.copy(alpha = 0.32f),
    accent = Color(0xFF4C6B8A),
    accentForeground = Color(0xFFF2F5F8),
    accentSoft = Color(0xFF4C6B8A).copy(alpha = 0.16f),
    accentInverse = Color(0xFF4C6B8A),
    destructive = Color(0xFFE5484D),
    destructiveForeground = Color(0xFFFFFFFF),
    dotSuccess = Color(0xFF5FD49A),
    dotDanger = Color(0xFFE5484D),
    dotWarning = Color(0xFFF0A22E),
    info = Color(0xFF5CCFE0),
    extension = Color(0xFFC9A0FF),
    codeComment = Color(0xFF8C8C94),
  )
