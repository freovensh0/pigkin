package io.pigkin.ui.auth

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import io.pigkin.R
import io.pigkin.appGraph
import io.pigkin.data.AccountRepository
import io.pigkin.host.HostRejected
import io.pigkin.ui.components.LucideIcon
import io.pigkin.ui.components.PillButton
import io.pigkin.ui.components.PillTone
import io.pigkin.ui.components.SimmeringPot
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import java.io.IOException
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** Pi's provider for the Claude subscription. */
internal const val CLAUDE_PROVIDER = "anthropic"

/**
 * pi-anthropic-auth's npm package. It shapes Claude subscription requests like Claude Code's, which Anthropic bills
 * against the plan instead of as extra usage.
 */
internal const val CLAUDE_PLUGIN_PACKAGE = "@gotgenes/pi-anthropic-auth"
private const val CLAUDE_PLUGIN_URL = "https://github.com/gotgenes/pi-anthropic-auth"
private const val PROGRESS_MAX_LINES = 2

internal sealed interface PluginStep {
  data object Offered : PluginStep

  /** [line] is the newest line `pi install` printed, if any. */
  data class Installing(val line: String?) : PluginStep

  data object Installed : PluginStep

  /** [message] is null when the host gave no reason. */
  data class Failed(val message: String?) : PluginStep
}

/** The install's step and job, kept across rotation so the offer can't start a second `pi install`. */
internal class ClaudePluginViewModel : ViewModel() {
  var step by mutableStateOf<PluginStep>(PluginStep.Offered)
    private set

  private var job: Job? = null

  fun install(accounts: AccountRepository) {
    if (job?.isActive == true) return
    step = PluginStep.Installing(null)
    job = viewModelScope.launch {
      step = try {
        accounts.installPackage("npm:$CLAUDE_PLUGIN_PACKAGE") { line ->
          if (line.isNotBlank()) step = PluginStep.Installing(line)
        }
        PluginStep.Installed
      } catch (failure: IOException) {
        PluginStep.Failed(failure.message)
      } catch (rejected: HostRejected) {
        PluginStep.Failed(rejected.error)
      }
    }
  }

  /** Cancels the install and returns to the offer, also when the sheet is dismissed. */
  fun reset() {
    job?.cancel()
    job = null
    step = PluginStep.Offered
  }

  override fun onCleared() {
    job?.cancel()
  }
}

/**
 * Offered once Claude sign-in succeeds: what pi-anthropic-auth does and risks, with Skip and Install. The install runs
 * while the offer is shown; dismissing the sheet aborts it. [onDone] closes the sheet after Skip or a finished install.
 */
@Composable
internal fun ClaudePluginOffer(onOpen: (String) -> Unit, onDone: () -> Unit, installer: ClaudePluginViewModel) {
  val accounts = LocalContext.current.appGraph.accountRepository
  val colors = pigkinColors
  val close = {
    installer.reset()
    onDone()
  }
  val currentClose by rememberUpdatedState(close)
  LaunchedEffect(installer.step) {
    if (installer.step != PluginStep.Installed) return@LaunchedEffect
    delay(1_000)
    currentClose()
  }

  Column(verticalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s8)) {
    LeadingRow(text = stringResource(R.string.claude_plugin_warning)) {
      LucideIcon(icon = R.drawable.ic_lucide_triangle_alert, contentDescription = null, tint = colors.dotWarning)
    }
    Text(
      text = stringResource(R.string.claude_plugin_explanation),
      color = colors.foregroundMuted,
      fontSize = PigkinTokens.FontSize.base,
    )
    TextButton(onClick = { onOpen(CLAUDE_PLUGIN_URL) }) { Text(stringResource(R.string.claude_plugin_link)) }
    PluginStepContent(
      step = installer.step,
      onSkip = close,
      onInstall = { installer.install(accounts) },
    )
  }
}

@Composable
private fun PluginStepContent(step: PluginStep, onSkip: () -> Unit, onInstall: () -> Unit) {
  val colors = pigkinColors
  when (step) {
    PluginStep.Offered -> PluginActions(onSkip = onSkip, onInstall = onInstall)

    is PluginStep.Installing -> {
      LeadingRow(text = stringResource(R.string.claude_plugin_installing), color = colors.foregroundMuted) {
        SimmeringPot()
      }
      step.line?.let {
        Text(
          text = it,
          color = colors.foregroundExtraMuted,
          fontSize = PigkinTokens.FontSize.base,
          maxLines = PROGRESS_MAX_LINES,
          overflow = TextOverflow.Ellipsis,
        )
      }
    }

    PluginStep.Installed -> LeadingRow(text = stringResource(R.string.claude_plugin_installed)) {
      LucideIcon(icon = R.drawable.ic_lucide_check, contentDescription = null, tint = colors.accent)
    }

    is PluginStep.Failed -> {
      LeadingRow(text = step.message ?: stringResource(R.string.claude_plugin_failed)) {
        LucideIcon(icon = R.drawable.ic_lucide_circle_alert, contentDescription = null, tint = colors.destructive)
      }
      PluginActions(onSkip = onSkip, onInstall = onInstall)
    }
  }
}

@Composable
private fun PluginActions(onSkip: () -> Unit, onInstall: () -> Unit) {
  Row(horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s8)) {
    PillButton(label = stringResource(R.string.claude_plugin_skip), onClick = onSkip, tone = PillTone.Tonal)
    PillButton(label = stringResource(R.string.claude_plugin_install), onClick = onInstall)
  }
}
