package io.pigkin.ui.settings

import android.text.format.DateFormat
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.CircleShape
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import io.pigkin.R
import io.pigkin.data.ScheduledTask
import io.pigkin.data.ScheduledTaskRecurrence
import io.pigkin.data.ScheduledTaskStore
import io.pigkin.ui.components.LucideIcon
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import java.text.DateFormat as JavaDateFormat
import java.util.Calendar
import java.util.Date
import java.util.UUID
import kotlinx.coroutines.launch

private enum class ScheduledTaskDialog { PickDate, PickTime }

private val taskIcons = listOf(
  "bell" to R.string.scheduled_icon_bell,
  "checklist" to R.string.scheduled_icon_checklist,
  "repeat" to R.string.scheduled_icon_repeat,
  "check" to R.string.scheduled_icon_check,
  "shield" to R.string.scheduled_icon_shield,
)

/** Adds a task ([taskId] null) or edits one: its prompt, when it runs, its repeat, icon and reminder. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ScheduledTaskEditorScreen(taskId: String?, onBack: () -> Unit, onDone: () -> Unit) {
  val scope = rememberCoroutineScope()
  val saved = remember(taskId) { ScheduledTaskStore.tasks.value.find { it.id == taskId } }
  var prompt by rememberSaveable { mutableStateOf(saved?.prompt.orEmpty()) }
  var dueAt by rememberSaveable { mutableStateOf(saved?.dueAtMillis ?: nextScheduledHour()) }
  var recurrence by rememberSaveable { mutableStateOf(saved?.recurrence ?: ScheduledTaskRecurrence.ONCE) }
  var icon by rememberSaveable { mutableStateOf(saved?.icon ?: "bell") }
  var reminder by rememberSaveable { mutableStateOf(saved?.reminder ?: true) }
  var enabled by rememberSaveable { mutableStateOf(saved?.enabled ?: true) }
  var dialog by remember { mutableStateOf<ScheduledTaskDialog?>(null) }
  var invalid by remember { mutableStateOf(false) }
  val title = stringResource(if (saved == null) R.string.scheduled_add_task else R.string.scheduled_edit_task)

  SettingsPage(title = title, onBack = onBack) {
    if (taskId != null && saved == null) {
      SettingsCaption(text = stringResource(R.string.scheduled_task_gone))
      return@SettingsPage
    }
    PromptField(prompt, { prompt = it }, invalid)
    RecurrenceField(recurrence) { recurrence = it }
    ScheduleFields(dueAt, { dialog = ScheduledTaskDialog.PickDate }, { dialog = ScheduledTaskDialog.PickTime })
    IconField(icon) { icon = it }
    ToggleFields(reminder, { reminder = it }, enabled) { enabled = it }
    SettingsSaveButton(
      label = stringResource(R.string.scheduled_task_save),
      saving = false,
      enabled = true,
      onClick = {
        if (prompt.isBlank()) {
          invalid = true
          return@SettingsSaveButton
        }
        val task = ScheduledTask(
          id = saved?.id ?: UUID.randomUUID().toString(),
          prompt = prompt.trim(),
          dueAtMillis = dueAt,
          recurrence = recurrence,
          icon = icon,
          reminder = reminder,
          enabled = enabled,
          lastRunAtMillis = saved?.lastRunAtMillis,
        )
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
private fun ScheduleFields(dueAt: Long, onDate: () -> Unit, onTime: () -> Unit) {
  SettingsSection(title = null) {
    SettingsRow(
      title = stringResource(R.string.scheduled_task_date),
      subtitle = JavaDateFormat.getDateInstance(JavaDateFormat.MEDIUM).format(Date(dueAt)),
      onClick = onDate,
    ) { Chevron() }
    SettingsDivider()
    SettingsRow(
      title = stringResource(R.string.scheduled_task_time),
      subtitle = DateFormat.getTimeFormat(LocalContext.current).format(Date(dueAt)),
      onClick = onTime,
    ) { Chevron() }
  }
}

@Composable
private fun IconField(icon: String, onSelect: (String) -> Unit) {
  SettingsSection(title = stringResource(R.string.scheduled_task_icon)) {
    Row(
      modifier = Modifier.fillMaxWidth().padding(PigkinTokens.Spacing.s12),
      horizontalArrangement = Arrangement.SpaceEvenly,
    ) {
      taskIcons.forEach { (key, label) ->
        val selected = icon == key
        Box(
          modifier = Modifier
            .size(48.dp)
            .clip(CircleShape)
            .background(if (selected) pigkinColors.accentSoft else pigkinColors.surface2)
            .selectable(selected = selected, role = Role.RadioButton, onClick = { onSelect(key) }),
          contentAlignment = Alignment.Center,
        ) {
          LucideIcon(
            icon = iconFor(key),
            contentDescription = stringResource(label),
            tint = if (selected) pigkinColors.accent else pigkinColors.foregroundMuted,
          )
        }
      }
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
