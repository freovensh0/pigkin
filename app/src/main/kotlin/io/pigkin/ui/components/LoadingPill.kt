package io.pigkin.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors

/** A pot and [text] in a pill, pinned over other content while something loads. */
@Composable
internal fun LoadingPill(text: String, modifier: Modifier = Modifier) {
  Row(
    modifier = modifier
      .clip(CircleShape)
      .background(pigkinColors.surface2)
      .padding(horizontal = PigkinTokens.Spacing.s12, vertical = PigkinTokens.Spacing.s8),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s8),
  ) {
    SimmeringPot(size = PigkinTokens.IconSize.sm)
    Text(text = text, color = pigkinColors.foregroundMuted, fontSize = PigkinTokens.FontSize.meta)
  }
}
