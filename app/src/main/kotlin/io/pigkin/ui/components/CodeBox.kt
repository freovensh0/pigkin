package io.pigkin.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.pigkin.ui.theme.PigkinFonts
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors

/**
 * A code card on [background], monospace, that scrolls sideways instead of wrapping, with its [label], such as the
 * language, over the code.
 */
@Composable
internal fun CodeBox(
  modifier: Modifier = Modifier,
  label: String? = null,
  background: Color = pigkinColors.surface1,
  radius: Dp = 16.dp,
  padding: Dp = PigkinTokens.Spacing.s12,
  content: @Composable ColumnScope.() -> Unit,
) {
  Column(modifier = modifier.fillMaxWidth().clip(RoundedCornerShape(radius)).background(background)) {
    if (label != null) {
      Text(
        text = label,
        modifier = Modifier.padding(start = padding, top = PigkinTokens.Spacing.s8, end = padding),
        color = pigkinColors.foregroundMuted,
        fontSize = 11.5.sp,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
      )
    }
    Column(
      modifier = Modifier
        .fillMaxWidth()
        .horizontalScroll(rememberScrollState())
        .padding(
          start = padding,
          top = if (label !=
            null
          ) {
            PigkinTokens.Spacing.s4
          } else {
            padding
          },
          end = padding,
          bottom = padding,
        ),
      content = content,
    )
  }
}

/** [text] in the code font, coloured for [language] (a fence name or file extension) when Highlights knows it. */
@Composable
internal fun CodeText(text: String, language: String? = null, color: Color = pigkinColors.foreground) {
  CodeText(rememberHighlightedCode(text, language), color)
}

/** Already styled [text] in the code font; [color] fills whatever its spans leave uncoloured. */
@Composable
internal fun CodeText(text: AnnotatedString, color: Color = pigkinColors.foreground) {
  Text(
    text = text,
    color = color,
    fontFamily = PigkinFonts.mono,
    fontSize = PigkinTokens.FontSize.code,
    lineHeight = PigkinTokens.LineHeight.code,
    softWrap = false,
  )
}
