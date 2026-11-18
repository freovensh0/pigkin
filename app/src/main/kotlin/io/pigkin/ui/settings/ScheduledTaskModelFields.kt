package io.pigkin.ui.settings

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.pigkin.R
import io.pigkin.appGraph
import io.pigkin.host.ModelConfig
import io.pigkin.ui.chat.thinkingLevelLabel
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors

private data class ModelChoice(val key: String, val label: String)

private enum class ModelDialogKind { Model, Thinking }

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
  var dialog by remember { mutableStateOf<ModelDialogKind?>(null) }
  LaunchedEffect(Unit) { if (accounts.status.value == null) accounts.refresh() }
  val choices = remember(status) {
    val all = status?.builtin.orEmpty().flatMap { provider ->
      provider.models.map { ModelChoice("${provider.id}/${it.id}", it.name ?: it.id) }
    } + status?.custom.orEmpty().flatMap { provider ->
      provider.models.map { ModelChoice("${provider.id}/${it.id}", it.name ?: it.id) }
    }
    val scope = status?.scope
    all.filter { scope == null || it.key in scope }
  }

  SettingsSection(title = null) {
    SettingsRow(
      title = stringResource(R.string.chat_model),
      subtitle = choices.firstOrNull { it.key == modelId }?.label
        ?: modelId?.takeIf(String::isNotBlank)
        ?: stringResource(R.string.scheduled_model_default),
      onClick = { dialog = ModelDialogKind.Model },
    ) { Chevron() }
    SettingsDivider()
    SettingsRow(
      title = stringResource(R.string.chat_reasoning),
      subtitle = thinking?.takeIf(String::isNotBlank)?.let { thinkingLevelLabel(it) }
        ?: stringResource(R.string.scheduled_thinking_default),
      onClick = { dialog = ModelDialogKind.Thinking },
    ) { Chevron() }
  }

  when (dialog) {
    ModelDialogKind.Model -> ChoiceDialog(
      title = stringResource(R.string.chat_model),
      labels = listOf(stringResource(R.string.scheduled_model_default)) + choices.map { it.label },
      onPick = { index -> onModel(if (index == 0) null else choices[index - 1].key) },
      onDismiss = { dialog = null },
    )

    ModelDialogKind.Thinking -> ChoiceDialog(
      title = stringResource(R.string.chat_reasoning),
      labels = listOf(stringResource(R.string.scheduled_thinking_default)) +
        ModelConfig.THINKING_LEVELS.map { thinkingLevelLabel(it) },
      onPick = { index -> onThinking(if (index == 0) null else ModelConfig.THINKING_LEVELS[index - 1]) },
      onDismiss = { dialog = null },
    )

    null -> Unit
  }
}

@Composable
private fun ChoiceDialog(title: String, labels: List<String>, onPick: (Int) -> Unit, onDismiss: () -> Unit) {
  AlertDialog(
    onDismissRequest = onDismiss,
    confirmButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.chat_cancel)) } },
    title = { Text(title) },
    text = {
      LazyColumn(modifier = Modifier.fillMaxWidth()) {
        items(labels.size) { index ->
          Text(
            text = labels[index],
            modifier = Modifier
              .fillMaxWidth()
              .clickable { onPick(index) }
              .padding(horizontal = PigkinTokens.Spacing.s16, vertical = PigkinTokens.Spacing.s12),
            color = pigkinColors.foreground,
          )
        }
      }
    },
  )
}
