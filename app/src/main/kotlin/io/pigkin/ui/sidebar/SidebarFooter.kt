package io.pigkin.ui.sidebar

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.pigkin.R
import io.pigkin.appGraph
import io.pigkin.data.ScheduledTaskStore
import io.pigkin.host.TermuxWaker
import io.pigkin.ui.components.HeaderButton
import io.pigkin.ui.components.StatusDot
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import kotlinx.coroutines.launch

/**
 * A card at the foot of the sidebar: Termux/Pi health (tap fixes what needs attention), the scheduled tasks, then
 * Archive and Settings.
 */
@Composable
internal fun SidebarFooter(
  onOpenArchive: () -> Unit,
  onOpenSettings: () -> Unit,
  onOpenOnboarding: () -> Unit,
  onOpenScheduledTasks: () -> Unit,
) {
  val tasks by ScheduledTaskStore.tasks.collectAsStateWithLifecycle()
  val failed = tasks.any { it.failed }
  Row(
    modifier = Modifier
      .fillMaxWidth()
      .padding(start = PigkinTokens.Spacing.s12, end = PigkinTokens.Spacing.s12, bottom = PigkinTokens.Spacing.s8)
      .clip(FOOTER_SHAPE)
      .background(pigkinColors.surface1)
      .heightIn(min = 60.dp)
      .padding(start = PigkinTokens.Spacing.s4, end = 10.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s6),
  ) {
    HostStatus(onOpenOnboarding, Modifier.weight(1f))
    HeaderButton(
      icon = R.drawable.ic_lucide_bell,
      contentDescription = stringResource(R.string.scheduled_tasks_title),
      onClick = onOpenScheduledTasks,
      filled = true,
      tint = if (failed) pigkinColors.destructive else pigkinColors.foreground,
    )
    HeaderButton(
      icon = R.drawable.ic_lucide_archive,
      contentDescription = stringResource(R.string.archive_open),
      onClick = onOpenArchive,
      filled = true,
    )
    HeaderButton(
      icon = R.drawable.ic_lucide_settings,
      contentDescription = stringResource(R.string.shell_settings),
      onClick = onOpenSettings,
      filled = true,
    )
  }
}

/**
 * Termux/Pi health. Tapping it opens onboarding when setup needs attention, or opens Termux when the Play build's
 * helper isn't running there; either way the dot turns yellow, as there's something the user can do.
 */
@Composable
private fun HostStatus(onOpenOnboarding: () -> Unit, modifier: Modifier) {
  val colors = pigkinColors
  val context = LocalContext.current
  val scope = rememberCoroutineScope()
  val hostClient = context.appGraph.hostClient
  val hello by hostClient.lastHello.collectAsStateWithLifecycle()
  val failure by hostClient.lastFailure.collectAsStateWithLifecycle()
  val installing by hostClient.installing.collectAsStateWithLifecycle()
  val onClick: (() -> Unit)? = when {
    installing -> null
    failure?.needsSetup == true -> onOpenOnboarding
    failure?.needsTermuxOpen == true -> { -> scope.launch { TermuxWaker.wake(context, asked = true) } }
    else -> null
  }
  val dot = when {
    installing || onClick != null -> colors.dotWarning
    failure != null -> colors.dotDanger
    hello != null -> colors.dotSuccess
    else -> null
  }
  val label = when {
    installing -> null
    failure?.needsSetup == true -> stringResource(R.string.shell_termux_attention)
    failure?.needsTermuxOpen == true -> stringResource(R.string.shell_termux_open)
    else -> null
  } ?: hello?.piVersion?.let { stringResource(R.string.shell_host_status, it) }
    ?: stringResource(R.string.shell_host_status_unknown)
  Row(
    modifier = modifier
      .clip(RoundedCornerShape(PigkinTokens.Radius.lg))
      .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
      .padding(horizontal = PigkinTokens.Spacing.s8, vertical = PigkinTokens.Spacing.s12),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(10.dp),
  ) {
    dot?.let { StatusDot(it) }
    Text(
      text = label,
      color = colors.foreground,
      fontSize = PigkinTokens.FontSize.base,
      fontWeight = if (onClick != null) FontWeight.Bold else FontWeight.SemiBold,
      maxLines = 1,
      overflow = TextOverflow.Ellipsis,
    )
  }
}

private val FOOTER_SHAPE = RoundedCornerShape(12.dp)
