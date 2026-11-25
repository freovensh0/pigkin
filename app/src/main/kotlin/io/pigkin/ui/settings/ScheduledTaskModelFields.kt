package io.pigkin.ui.settings

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.pigkin.R
import io.pigkin.appGraph
import io.pigkin.host.ModelConfig
import io.pigkin.ui.chat.thinkingLevelLabel
import io.pigkin.ui.components.SearchField
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors

private data class ModelChoice(val key: String, val label: String)

private enum class ChoiceKind { Model, Thinking }

/** Model and thinking level for one task; empty values mean Pi's own defaults. */
@Composable
internal fun ModelFields(
  modelId: String?,
  thinking: String?,
  onModel: (String?) -> Unit,
  onThinking: (String?) -> Unit,
) {
  val accounts = LocalContext.current.appGraph.accountRepository
  val status by accounts.status.collectAsStateWithLifecycle()
  var choosing by rememberSaveable { mutableStateOf<ChoiceKind?>(null) }
  var query by rememberSaveable { mutableStateOf("") }
  BackHandler(enabled = choosing != null) {
    choosing = null
    query = ""
  }
  LaunchedEffect(Unit) { if (accounts.status.value == null) accounts.refresh() }
  val models = remember(status) {
    val all = status?.builtin.orEmpty().flatMap { provider ->
      provider.models.map { ModelChoice("${provider.id}/${it.id}", it.name ?: it.id) }
    } + status?.custom.orEmpty().flatMap { provider ->
      provider.models.map { ModelChoice("${provider.id}/${it.id}", it.name ?: it.id) }
    }
    val scope = status?.scope
    all.filter { scope == null || it.key in scope }
  }

  SettingsSection(title = null) {
    val kind = choosing
    if (kind == null) {
      SettingsRow(
        title = stringResource(R.string.chat_model),
        subtitle = models.firstOrNull { it.key == modelId }?.label
          ?: modelId?.takeIf(String::isNotBlank)
          ?: stringResource(R.string.scheduled_model_default),
        onClick = { choosing = ChoiceKind.Model },
      ) { Chevron() }
      SettingsDivider()
      SettingsRow(
        title = stringResource(R.string.chat_reasoning),
        subtitle = thinking?.takeIf(String::isNotBlank)?.let { thinkingLevelLabel(it) }
          ?: stringResource(R.string.scheduled_thinking_default),
        onClick = { choosing = ChoiceKind.Thinking },
      ) { Chevron() }
      return@SettingsSection
    }

    // The choices are part of this screen, not a modal with a list in it.
    SettingsRow(title = stringResource(R.string.settings_back), onClick = { choosing = null }) { Chevron() }
    SearchField(query = query, onQueryChange = { query = it }, placeholder = stringResource(R.string.settings_search))
    val entries =
      if (kind == ChoiceKind.Model) {
        listOf(stringResource(R.string.scheduled_model_default) to null) + models.map { it.label to it.key }
      } else {
        listOf(stringResource(R.string.scheduled_thinking_default) to null) +
          ModelConfig.THINKING_LEVELS.map { thinkingLevelLabel(it) to it }
      }
    val matching = entries.filter { it.first.contains(query, ignoreCase = true) }
    if (matching.isEmpty()) NoChoiceMatch()
    matching.forEach { (label, value) ->
      SettingsRow(title = label, onClick = {
        if (kind == ChoiceKind.Model) onModel(value) else onThinking(value)
        choosing = null
        query = ""
      })
    }
  }
}

@Composable
private fun NoChoiceMatch() {
  Text(
    text = stringResource(R.string.settings_choice_no_match),
    modifier = Modifier.fillMaxWidth().padding(PigkinTokens.Spacing.s16),
    color = pigkinColors.foregroundMuted,
    fontSize = PigkinTokens.FontSize.base,
  )
}
