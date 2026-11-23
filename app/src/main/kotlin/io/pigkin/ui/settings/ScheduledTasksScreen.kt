package io.pigkin.ui.settings

import android.content.Context
import android.text.format.DateUtils
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.pigkin.R
import io.pigkin.agent.ChatLocation
import io.pigkin.data.ScheduledTask
import io.pigkin.data.ScheduledTaskRecurrence
import io.pigkin.data.ScheduledTaskStore
import io.pigkin.scheduler.ScheduledTaskRunner
import io.pigkin.ui.chat.ConfirmDialog
import io.pigkin.ui.components.HeaderBar
import io.pigkin.ui.components.HeaderButton
import io.pigkin.ui.components.LucideIcon
import io.pigkin.ui.components.PigkinSheet
import io.pigkin.ui.components.SheetRow
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import java.util.Date
import kotlinx.coroutines.launch

/**
 * The saved tasks, grouped by what they need: failed ones first, then the active ones, then the paused ones. Tapping a
 * task opens the chat its runs append to, once it has run; before that, its editor. The add button and the rows'
 * sheets add, edit, run, pause and delete them.
 */
@Composable
internal fun ScheduledTasksScreen(
  onBack: () -> Unit,
  onAdd: () -> Unit,
  onEdit: (String) -> Unit,
  onOpenChat: (ChatLocation) -> Unit,
) {
  val tasks by ScheduledTaskStore.tasks.collectAsStateWithLifecycle()
  val actions = remember { TaskActionsState() }
  LaunchedEffect(Unit) { ScheduledTaskStore.refresh() }
  Column(modifier = Modifier.fillMaxSize().background(pigkinColors.surface0)) {
    HeaderBar(
      title = stringResource(R.string.scheduled_tasks_title),
      leading = {
        HeaderButton(
          icon = R.drawable.ic_lucide_chevron_left,
          contentDescription = stringResource(R.string.scheduled_tasks_back),
          onClick = onBack,
        )
      },
      trailing = {
        HeaderButton(
          icon = R.drawable.ic_lucide_plus,
          contentDescription = stringResource(R.string.scheduled_add_task),
          onClick = onAdd,
        )
      },
    )
    if (tasks.isEmpty()) {
      EmptyTasks(onAdd, modifier = Modifier.weight(1f))
    } else {
      TaskList(
        tasks = tasks,
        onOpen = { task ->
          val path = task.sessionPath
          val cwd = task.sessionCwd
          if (path != null && cwd != null) onOpenChat(ChatLocation(cwd, path)) else onEdit(task.id)
        },
        onMenu = { actions.menuFor = it.id },
        modifier = Modifier.weight(1f),
      )
    }
  }
  TaskOverlays(tasks, actions, onEdit)
}

/** Which task's actions sheet or delete confirmation is open. */
private class TaskActionsState {
  var menuFor by mutableStateOf<String?>(null)
  var pendingDelete by mutableStateOf<ScheduledTask?>(null)
}

/** The actions and confirmations over the list, kept out of the page so its state stays in one place. */
@Composable
private fun TaskOverlays(tasks: List<ScheduledTask>, actions: TaskActionsState, onEdit: (String) -> Unit) {
  val context = LocalContext.current
  val scope = rememberCoroutineScope()
  tasks.find { it.id == actions.menuFor }?.let { task ->
    TaskActions(
      task = task,
      onDismiss = { actions.menuFor = null },
      onRun = {
        scope.launch {
          // A failed run consumed its alarm, so it runs only once the user retries it.
          if (task.failed) ScheduledTaskStore.retry(task.id)
          ScheduledTaskRunner.runNow(context.applicationContext, task.id)
        }
      },
      onEdit = { onEdit(task.id) },
      onToggle = { ScheduledTaskStore.save(task.copy(enabled = !task.enabled, failed = false)) },
      onDelete = { actions.pendingDelete = task },
    )
  }
  actions.pendingDelete?.let { task ->
    ConfirmDialog(
      title = stringResource(R.string.scheduled_delete_title),
      text = stringResource(R.string.scheduled_delete_text),
      confirmLabel = stringResource(R.string.scheduled_task_delete),
      onConfirm = {
        scope.launch { ScheduledTaskStore.remove(task.id) }
        actions.pendingDelete = null
      },
      onDismiss = { actions.pendingDelete = null },
      destructive = true,
    )
  }
}

/** The tasks under the heading of what they need, each with what it does next. */
@Composable
private fun TaskList(
  tasks: List<ScheduledTask>,
  onOpen: (ScheduledTask) -> Unit,
  onMenu: (ScheduledTask) -> Unit,
  modifier: Modifier,
) {
  val groups = remember(tasks) {
    listOf(
      R.string.scheduled_section_attention to tasks.filter { it.failed },
      R.string.scheduled_section_active to tasks.filter { !it.failed && it.enabled },
      R.string.scheduled_section_paused to tasks.filter { !it.failed && !it.enabled },
    ).filter { it.second.isNotEmpty() }
  }
  LazyColumn(
    modifier = modifier.fillMaxWidth(),
    contentPadding = PaddingValues(
      start = PigkinTokens.Spacing.s12,
      end = PigkinTokens.Spacing.s12,
      top = PigkinTokens.Spacing.s8,
      bottom = PigkinTokens.Spacing.s24,
    ),
    verticalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s8),
  ) {
    for ((label, group) in groups) {
      item(key = "section-$label") { SectionLabel(stringResource(label)) }
      items(group, key = { it.id }) { task ->
        TaskCard(task, onClick = { onOpen(task) }, onMenu = { onMenu(task) })
      }
    }
  }
}

@Composable
private fun SectionLabel(text: String) {
  Text(
    text = text,
    modifier = Modifier.padding(start = PigkinTokens.Spacing.s4, top = PigkinTokens.Spacing.s8),
    color = pigkinColors.foregroundMuted,
    fontSize = PigkinTokens.FontSize.sm,
    fontWeight = FontWeight.SemiBold,
  )
}

/** One task: what it asks, when it runs next, and the button for its actions; tapping it opens the task itself. */
@Composable
private fun TaskCard(task: ScheduledTask, onClick: () -> Unit, onMenu: () -> Unit) {
  Row(
    modifier = Modifier
      .fillMaxWidth()
      .clip(TASK_SHAPE)
      .background(pigkinColors.surface1)
      .clickable(onClickLabel = stringResource(R.string.scheduled_task_open), onClick = onClick)
      .heightIn(min = 64.dp)
      .padding(start = PigkinTokens.Spacing.s12, end = PigkinTokens.Spacing.s4, top = 10.dp, bottom = 10.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s12),
  ) {
    LucideIcon(
      icon = task.statusIcon,
      contentDescription = null,
      tint = task.statusTint,
      modifier = Modifier.size(20.dp),
    )
    Column(modifier = Modifier.weight(1f)) {
      Text(
        text = task.prompt,
        color = pigkinColors.foreground,
        fontSize = PigkinTokens.FontSize.content,
        maxLines = 2,
        overflow = TextOverflow.Ellipsis,
      )
      TaskSubtitle(task)
    }
    IconButton(onClick = onMenu, modifier = Modifier.size(48.dp)) {
      LucideIcon(
        icon = R.drawable.ic_lucide_ellipsis,
        contentDescription = stringResource(R.string.scheduled_task_actions),
        tint = pigkinColors.foregroundMuted,
      )
    }
  }
}

@Composable
private fun TaskSubtitle(task: ScheduledTask) {
  val context = LocalContext.current
  val time = if (task.recurrence == ScheduledTaskRecurrence.ONCE) {
    formatScheduledDue(context, task.dueAtMillis)
  } else {
    formatScheduledTime(context, task.dueAtMillis)
  }
  val schedule = stringResource(R.string.scheduled_task_subtitle, time, stringResource(task.recurrence.label))
  val text = when {
    task.failed -> stringResource(R.string.scheduled_task_failed)
    task.enabled -> schedule
    else -> stringResource(R.string.scheduled_task_paused_schedule, schedule)
  }
  Text(
    text = text,
    color = if (task.failed) pigkinColors.destructive else pigkinColors.foregroundMuted,
    fontSize = PigkinTokens.FontSize.sm,
    maxLines = 2,
    overflow = TextOverflow.Ellipsis,
  )
}

/** The sheet under a task's actions: run it, edit it, pause or resume it, or delete it. */
@Composable
private fun TaskActions(
  task: ScheduledTask,
  onDismiss: () -> Unit,
  onRun: () -> Unit,
  onEdit: () -> Unit,
  onToggle: () -> Unit,
  onDelete: () -> Unit,
) {
  PigkinSheet(onDismissRequest = onDismiss) { dismiss ->
    SheetRow(
      label = if (task.failed) {
        stringResource(
          R.string.scheduled_task_retry,
        )
      } else {
        stringResource(R.string.scheduled_run_now)
      },
      icon = R.drawable.ic_lucide_refresh_cw,
      onClick = { dismiss(onRun) },
    )
    SheetRow(
      label = stringResource(R.string.scheduled_edit_task),
      icon = R.drawable.ic_lucide_pencil,
      onClick = { dismiss(onEdit) },
    )
    if (!task.failed) {
      SheetRow(
        label = stringResource(if (task.enabled) R.string.scheduled_pause_task else R.string.scheduled_resume_task),
        icon = if (task.enabled) R.drawable.ic_lucide_pause else R.drawable.ic_lucide_play,
        onClick = { dismiss(onToggle) },
      )
    }
    SheetRow(
      label = stringResource(R.string.scheduled_task_delete),
      icon = R.drawable.ic_lucide_trash_2,
      labelColor = pigkinColors.destructive,
      onClick = { dismiss(onDelete) },
    )
  }
}

/** No tasks yet: what they do, and the way to add the first one. */
@Composable
private fun EmptyTasks(onAdd: () -> Unit, modifier: Modifier) {
  Column(
    modifier = modifier.fillMaxWidth().padding(horizontal = PigkinTokens.Spacing.s32),
    verticalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s16, Alignment.CenterVertically),
  ) {
    LucideIcon(
      icon = R.drawable.ic_lucide_bell,
      contentDescription = null,
      tint = pigkinColors.foregroundMuted,
      modifier = Modifier.size(32.dp),
    )
    Text(
      text = stringResource(R.string.scheduled_tasks_empty),
      modifier = Modifier.fillMaxWidth(),
      color = pigkinColors.foregroundMuted,
      fontSize = PigkinTokens.FontSize.lg,
      textAlign = TextAlign.Center,
    )
    SmallButton(label = stringResource(R.string.scheduled_add_task), onClick = onAdd)
  }
}

private val TASK_SHAPE = RoundedCornerShape(16.dp)

private val ScheduledTask.statusIcon: Int
  get() = if (failed) R.drawable.ic_lucide_triangle_alert else R.drawable.ic_lucide_bell

private val ScheduledTask.statusTint
  @Composable get() = when {
    failed -> pigkinColors.destructive
    enabled -> pigkinColors.accent
    else -> pigkinColors.foregroundMuted
  }

/** A shared, locale-formatted due time for the list and the editor. */
internal fun formatScheduledDue(context: Context, millis: Long): String = DateUtils.formatDateTime(
  context,
  millis,
  DateUtils.FORMAT_SHOW_DATE or DateUtils.FORMAT_SHOW_TIME or DateUtils.FORMAT_SHOW_YEAR,
)

internal fun formatScheduledTime(context: Context, millis: Long): String =
  android.text.format.DateFormat.getTimeFormat(context).format(Date(millis))

internal val ScheduledTaskRecurrence.label: Int
  get() = when (this) {
    ScheduledTaskRecurrence.ONCE -> R.string.scheduled_once
    ScheduledTaskRecurrence.DAILY -> R.string.scheduled_daily
    ScheduledTaskRecurrence.WEEKLY -> R.string.scheduled_weekly
    ScheduledTaskRecurrence.MONTHLY -> R.string.scheduled_monthly
  }
