package io.pigkin.ui.settings

import android.text.format.DateFormat
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.DatePicker
import androidx.compose.material3.DatePickerDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TimePicker
import androidx.compose.material3.TimePickerDialog
import androidx.compose.material3.rememberDatePickerState
import androidx.compose.material3.rememberTimePickerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import io.pigkin.R
import io.pigkin.data.ScheduledTask
import io.pigkin.data.ScheduledTaskRecurrence
import io.pigkin.data.ScheduledTaskStore
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import java.util.Calendar
import java.util.UUID
import kotlinx.coroutines.launch

private enum class ScheduledTaskDialog { PickDate, PickTime }

private data class TaskDraft(
  val prompt: String,
  val dueAt: Long,
  val recurrence: ScheduledTaskRecurrence,
  val days: List<Int>,
  val modelId: String?,
  val thinking: String?,
  val reminder: Boolean,
  val enabled: Boolean,
) {
  fun toTask(saved: ScheduledTask?): ScheduledTask = ScheduledTask(
    id = saved?.id ?: UUID.randomUUID().toString(),
    prompt = prompt.trim(),
    dueAtMillis = dueAt,
    recurrence = recurrence,
    icon = "bell",
    reminder = reminder,
    enabled = enabled,
    lastRunAtMillis = saved?.lastRunAtMillis,
    days = if (recurrence == ScheduledTaskRecurrence.WEEKLY || recurrence == ScheduledTaskRecurrence.MONTHLY) {
      days.ifEmpty { defaultDaysFor(recurrence, dueAt) }
    } else {
      emptyList()
    },
    modelId = modelId,
    thinking = thinking,
  )
}

/** Adds a task ([taskId] null) or edits one: its prompt, schedule, model and reminder. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ScheduledTaskEditorScreen(taskId: String?, onBack: () -> Unit, onDone: () -> Unit) {
  val scope = rememberCoroutineScope()
  val saved = remember(taskId) { ScheduledTaskStore.tasks.value.find { it.id == taskId } }
  var prompt by rememberSaveable { mutableStateOf(saved?.prompt.orEmpty()) }
  var dueAt by rememberSaveable { mutableStateOf(saved?.dueAtMillis ?: nextScheduledHour()) }
  var recurrence by rememberSaveable { mutableStateOf(saved?.recurrence ?: ScheduledTaskRecurrence.ONCE) }
  var days by rememberSaveable { mutableStateOf(saved?.days ?: defaultDaysFor(recurrence, dueAt)) }
  var modelId by rememberSaveable { mutableStateOf(saved?.modelId) }
  var thinking by rememberSaveable { mutableStateOf(saved?.thinking) }
  var reminder by rememberSaveable { mutableStateOf(saved?.reminder ?: true) }
  var enabled by rememberSaveable { mutableStateOf(saved?.enabled ?: true) }
  var dialog by remember { mutableStateOf<ScheduledTaskDialog?>(null) }
  var invalid by remember { mutableStateOf(false) }
  val title = stringResource(if (saved == null) R.string.scheduled_add_task else R.string.scheduled_edit_task)
  val draft = TaskDraft(prompt, dueAt, recurrence, days, modelId, thinking, reminder, enabled)
  SettingsPage(title = title, onBack = onBack) {
    if (taskId != null && saved == null) {
      SettingsCaption(text = stringResource(R.string.scheduled_task_gone))
      return@SettingsPage
    }
    PromptField(prompt, { prompt = it }, invalid)
    RecurrenceField(recurrence) {
      recurrence = it
      days = defaultDaysFor(it, dueAt)
    }
    ScheduleFields(recurrence, dueAt, days, { dialog = ScheduledTaskDialog.PickDate }, {
      dialog =
        ScheduledTaskDialog.PickTime
    }) {
      days = it
    }
    ModelFields(modelId, thinking, { modelId = it }, { thinking = it })
    ToggleFields(reminder, { reminder = it }, enabled) { enabled = it }
    SettingsSaveButton(
      label = stringResource(R.string.scheduled_task_save),
      saving = false,
      enabled = true,
      onClick = {
        if (draft.prompt.isBlank()) {
          invalid = true
          return@SettingsSaveButton
        }
        val task = draft.toTask(saved)
        scope.launch {
          ScheduledTaskStore.save(task)
          onDone()
        }
      },
    )
  }
  EditorDialogs(dialog, dueAt, { dueAt = withScheduledDay(it, dueAt) }, { dueAt = it }, { dialog = null })
}

@Composable
private fun PromptField(prompt: String, onPromptChange: (String) -> Unit, invalid: Boolean) {
  SectionLabel(text = stringResource(R.string.scheduled_task_prompt))
  OutlinedTextField(
    value = prompt,
    onValueChange = onPromptChange,
    modifier = Modifier.fillMaxWidth().padding(top = PigkinTokens.Spacing.s8),
    shape = SettingsFieldShape,
    colors = settingsFieldColors(),
    placeholder = { Text(stringResource(R.string.scheduled_task_prompt_placeholder)) },
    minLines = 4,
    maxLines = 8,
    isError = invalid && prompt.isBlank(),
  )
  if (invalid && prompt.isBlank()) {
    SettingsCaption(text = stringResource(R.string.scheduled_task_invalid_prompt), color = pigkinColors.destructive)
  }
}

@Composable
private fun RecurrenceField(selected: ScheduledTaskRecurrence, onSelect: (ScheduledTaskRecurrence) -> Unit) {
  SettingsSection(title = stringResource(R.string.scheduled_task_recurrence)) {
    SegmentedControl(
      options = ScheduledTaskRecurrence.entries,
      selected = selected,
      onSelect = onSelect,
      modifier = Modifier.fillMaxWidth().padding(PigkinTokens.Spacing.s8),
    ) { option, isSelected ->
      Text(
        text = stringResource(option.label),
        color = if (isSelected) pigkinColors.foreground else pigkinColors.foregroundMuted,
        fontSize = PigkinTokens.FontSize.sm,
        fontWeight = if (isSelected) FontWeight.SemiBold else FontWeight.Normal,
      )
    }
  }
}

@Composable
private fun ToggleFields(
  reminder: Boolean,
  onReminder: (Boolean) -> Unit,
  enabled: Boolean,
  onEnabled: (Boolean) -> Unit,
) {
  SettingsSection(title = null) {
    SettingsRow(
      title = stringResource(R.string.scheduled_task_reminder),
      subtitle = stringResource(R.string.scheduled_task_reminder_summary),
      onClick = { onReminder(!reminder) },
    ) { SettingsSwitch(checked = reminder) }
    SettingsDivider()
    SettingsRow(
      title = stringResource(R.string.scheduled_task_enabled),
      onClick = { onEnabled(!enabled) },
    ) { SettingsSwitch(checked = enabled) }
  }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun EditorDialogs(
  dialog: ScheduledTaskDialog?,
  dueAt: Long,
  onDate: (Long) -> Unit,
  onTime: (Long) -> Unit,
  onDismiss: () -> Unit,
) {
  when (dialog) {
    ScheduledTaskDialog.PickDate -> DateDialog(dueAt, onDate, onDismiss)
    ScheduledTaskDialog.PickTime -> TimeDialog(dueAt, onTime, onDismiss)
    null -> Unit
  }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun DateDialog(dueAt: Long, onDate: (Long) -> Unit, onDismiss: () -> Unit) {
  val state = rememberDatePickerState(initialSelectedDateMillis = dueAt)
  DatePickerDialog(
    onDismissRequest = onDismiss,
    confirmButton = {
      TextButton(onClick = {
        state.selectedDateMillis?.let(onDate)
        onDismiss()
      }) { Text(stringResource(R.string.scheduled_task_save)) }
    },
    dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.chat_cancel)) } },
  ) { DatePicker(state = state) }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun TimeDialog(dueAt: Long, onTime: (Long) -> Unit, onDismiss: () -> Unit) {
  val context = LocalContext.current
  val calendar = Calendar.getInstance().apply { timeInMillis = dueAt }
  val state = rememberTimePickerState(
    initialHour = calendar.get(Calendar.HOUR_OF_DAY),
    initialMinute = calendar.get(Calendar.MINUTE),
    is24Hour = DateFormat.is24HourFormat(context),
  )
  TimePickerDialog(
    title = { Text(stringResource(R.string.scheduled_task_pick_time)) },
    onDismissRequest = onDismiss,
    confirmButton = {
      TextButton(onClick = {
        onTime(withScheduledTime(dueAt, state.hour, state.minute))
        onDismiss()
      }) { Text(stringResource(R.string.scheduled_task_save)) }
    },
    dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.chat_cancel)) } },
  ) { TimePicker(state = state) }
}
