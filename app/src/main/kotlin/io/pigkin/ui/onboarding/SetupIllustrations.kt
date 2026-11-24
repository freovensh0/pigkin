package io.pigkin.ui.onboarding

import androidx.annotation.DrawableRes
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import io.pigkin.R
import io.pigkin.ui.components.LucideIcon
import io.pigkin.ui.theme.PigkinFonts
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors

/** A step's icon on a rounded accent-washed tile; it turns into an accent check once [done]. */
@Composable
internal fun IconTile(@DrawableRes icon: Int, done: Boolean, modifier: Modifier = Modifier) {
  val colors = pigkinColors
  val shape = RoundedCornerShape(PigkinTokens.Radius.xxl)
  AnimatedContent(
    targetState = done,
    modifier = modifier.size(TILE_SIZE),
    transitionSpec = {
      (
        fadeIn(tween(FADE_MILLIS)) + scaleIn(tween(FADE_MILLIS), initialScale = 0.9f)
        )
        .togetherWith(fadeOut(tween(FADE_MILLIS / 2)))
    },
    label = "step icon",
  ) { isDone ->
    val tile = Modifier.fillMaxSize()
    if (isDone) {
      Box(tile.background(colors.accent, shape), contentAlignment = Alignment.Center) {
        LucideIcon(
          icon = R.drawable.ic_lucide_check,
          contentDescription = stringResource(R.string.onboarding_status_done),
          size = ICON_SIZE,
          tint = colors.accentForeground,
        )
      }
    } else {
      Box(tile.background(colors.accentSoft, shape), contentAlignment = Alignment.Center) {
        LucideIcon(icon = icon, contentDescription = null, size = ICON_SIZE, tint = colors.accent)
      }
    }
  }
}

/** Where onboarding is: a dot per step, the current one stretched, finished ones in accent. */
@Composable
internal fun ProgressDots(done: List<Boolean>, current: Int, modifier: Modifier = Modifier) {
  val colors = pigkinColors
  val description = stringResource(R.string.onboarding_progress, current + 1, done.size)
  Row(
    modifier = modifier.clearAndSetSemantics { contentDescription = description },
    horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s6),
    verticalAlignment = Alignment.CenterVertically,
  ) {
    done.forEachIndexed { index, isDone ->
      val width by animateDpAsState(if (index == current) DOT_CURRENT_WIDTH else DOT_SIZE, label = "dot width")
      val color by animateColorAsState(
        when {
          index == current -> colors.accent
          isDone -> colors.accent.copy(alpha = DONE_DOT_ALPHA)
          else -> colors.borderAccent
        },
        label = "dot color",
      )
      Box(Modifier.size(width, DOT_SIZE).background(color, CircleShape))
    }
  }
}

/**
 * A small terminal showing what the setup command does once it has run: the command, then Termux's last line. It
 * holds still; a demo that loops on its own would draw the eye away from the step it belongs to.
 */
@Composable
internal fun PasteDemo(modifier: Modifier = Modifier) {
  // A drawing of a terminal at a fixed size, which the page's text explains; scaled with large fonts, its lines
  // would wrap inside the box.
  val density = LocalDensity.current
  CompositionLocalProvider(LocalDensity provides Density(density.density, fontScale = 1f)) {
    SetupTerminal(modifier)
  }
}

@Composable
private fun SetupTerminal(modifier: Modifier) {
  val colors = pigkinColors
  val shape = RoundedCornerShape(PigkinTokens.Radius.xxl)
  val mono = PigkinTokens.FontSize.code
  val description = stringResource(R.string.onboarding_setup_body)
  Column(
    modifier = modifier
      .widthIn(max = DEMO_WIDTH)
      .fillMaxWidth()
      // One description in place of the drawing's fake terminal lines, which a screen reader would read out.
      .clearAndSetSemantics { contentDescription = description }
      .background(colors.surface1, shape)
      .padding(PigkinTokens.Spacing.s16),
    verticalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s8),
  ) {
    Text(
      text = PROMPT + COMMAND,
      color = colors.foreground,
      fontFamily = PigkinFonts.mono,
      fontSize = mono,
      maxLines = 1,
    )
    Row(
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s6),
    ) {
      LucideIcon(
        icon = R.drawable.ic_lucide_check,
        contentDescription = null,
        size = PigkinTokens.IconSize.sm,
        tint = colors.accent,
      )
      Text(
        text = stringResource(R.string.onboarding_demo_done),
        color = colors.foreground,
        fontFamily = PigkinFonts.mono,
        fontSize = PigkinTokens.FontSize.code,
      )
    }
  }
}

private val TILE_SIZE = 88.dp
private val ICON_SIZE = 40.dp
private const val FADE_MILLIS = 200
private val DOT_SIZE = 6.dp
private val DOT_CURRENT_WIDTH = 20.dp
private const val DONE_DOT_ALPHA = 0.45f

private val DEMO_WIDTH = 300.dp
private const val PROMPT = "~ $ "
private const val COMMAND = "curl -fsS …/setup.sh | bash"
