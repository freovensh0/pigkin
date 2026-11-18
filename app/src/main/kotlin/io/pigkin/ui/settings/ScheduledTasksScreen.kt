package io.pigkin.ui.settings

import android.content.Context
import android.text.format.DateUtils
import androidx.compose.foundation.layout.size
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.pigkin.R
import io.pigkin.data.ScheduledTask
import io.pigkin.data.ScheduledTaskRecurrence
import io.pigkin.data.ScheduledTaskStore
import io.pigkin.ui.chat.ConfirmDialog
import io.pigkin.ui.components.LucideIcon
import io.pigkin.ui.theme.pigkinColors
import kotlinx.coroutines.launch

/** Settings' row into the scheduled tasks, counting them without starting a host just for opening Settings. */
@Composable
internal fun ScheduledTasksSection(onOpenScheduledTasks: () -> Unit) {
  val tasks by ScheduledTaskStore.tasks.collectAsStateWithLifecycle()
  val subtitle = if (tasks.isEmpty()) {
    stringResource(R.string.scheduled_tasks_none)
  } else {
    stringResource(R.string.scheduled_tasks_count, tasks.size)
  }
  SettingsSection(title = stringResource(R.string.scheduled_settings_section)) {
    SettingsRow(
      title = stringResource(R.string.scheduled_tasks_title),
      subtitle = subtitle,
      onClick = onOpenScheduledTasks,
      leading = {
        LucideIcon(
          icon = R.drawable.ic_lucide_bell,
          contentDescription = null,
          tint = pigkinColors.foregroundMuted,
        )
      },
    ) {
      Chevron()
    }
  }
}

/** The saved tasks, each with its next run, then Add task. */
@Composable
internal fun ScheduledTasksScreen(onBack: () -> Unit, onAdd: () -> Unit, onEdit: (String) -> Unit) {
  val tasks by ScheduledTaskStore.tasks.collectAsStateWithLifecycle()
  val scope = rememberCoroutineScope()
  var pendingDelete by remember { mutableStateOf<ScheduledTask?>(null) }
  LaunchedEffect(Unit) { ScheduledTaskStore.refresh() }

  SettingsPage(title = stringResource(R.string.scheduled_tasks_title), onBack = onBack) {
    if (tasks.isEmpty()) {
      SettingsCaption(text = stringResource(R.string.scheduled_tasks_empty))
    } else {
      SettingsSection(title = null) {
        tasks.forEachIndexed { index, task ->
          if (index > 0) SettingsDivider()
          TaskRow(task, { onEdit(task.id) }) { pendingDelete = task }
        }
      }
    }
    DashedActionRow(
      label = stringResource(R.string.scheduled_add_task),
      icon = R.drawable.ic_lucide_plus,
      onClick = onAdd,
    )
    pendingDelete?.let { task ->
      ConfirmDialog(
        title = stringResource(R.string.scheduled_delete_title),
        text = stringResource(R.string.scheduled_delete_text),
        confirmLabel = stringResource(R.string.scheduled_task_delete),
        onConfirm = {
          scope.launch { ScheduledTaskStore.remove(task.id) }
          pendingDelete = null
        },
        onDismiss = { pendingDelete = null },
        destructive = true,
      )
    }
  }
}

@Composable
private fun TaskRow(task: ScheduledTask, onEdit: () -> Unit, onDelete: () -> Unit) {
  val context = LocalContext.current
  val subtitle = if (task.enabled) {
    stringResource(
      R.string.scheduled_task_subtitle,
      formatScheduledDue(context, task.dueAtMillis),
      stringResource(task.recurrence.label),
    )
  } else {
    stringResource(R.string.scheduled_task_paused)
  }
  SettingsRow(
    title = task.prompt,
    subtitle = subtitle,
    onClick = onEdit,
    leading = {
      LucideIcon(
        icon = iconFor(task.icon),
        contentDescription = null,
        tint = pigkinColors.foregroundMuted,
      )
    },
  ) {
    IconButton(onClick = onDelete, modifier = Modifier.size(48.dp)) {
      LucideIcon(
        icon = R.drawable.ic_lucide_trash_2,
        contentDescription = stringResource(R.string.scheduled_task_delete),
        tint = pigkinColors.destructive,
      )
    }
  }
}

/** A shared, locale-formatted due time for the list and the editor. */
internal fun formatScheduledDue(context: Context, millis: Long): String = DateUtils.formatDateTime(
  context,
  millis,
  DateUtils.FORMAT_SHOW_DATE or DateUtils.FORMAT_SHOW_TIME or DateUtils.FORMAT_SHOW_YEAR,
)

internal val ScheduledTaskRecurrence.label: Int
  get() = when (this) {
    ScheduledTaskRecurrence.ONCE -> R.string.scheduled_once
    ScheduledTaskRecurrence.DAILY -> R.string.scheduled_daily
    ScheduledTaskRecurrence.WEEKLY -> R.string.scheduled_weekly
    ScheduledTaskRecurrence.MONTHLY -> R.string.scheduled_monthly
  }

/** The icon keys are stored in the task, so a renamed drawable never leaves a blank row. */
internal fun iconFor(key: String): Int = when (key) {
  "checklist" -> R.drawable.ic_lucide_list_checks
  "repeat" -> R.drawable.ic_lucide_refresh_cw
  "check" -> R.drawable.ic_lucide_circle_check
  "shield" -> R.drawable.ic_lucide_shield_check
  else -> R.drawable.ic_lucide_bell
}
