package io.pigkin.ui.settings

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import io.pigkin.R
import io.pigkin.data.ScheduledTaskRecurrence
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import java.text.DateFormat
import java.util.Calendar
import java.util.Date

/** Only the fields that matter for the chosen repeat: a time daily, days and a time weekly or monthly. */
@Composable
internal fun ScheduleFields(
  recurrence: ScheduledTaskRecurrence,
  dueAt: Long,
  days: List<Int>,
  onDate: () -> Unit,
  onTime: () -> Unit,
  onDays: (List<Int>) -> Unit,
) {
  when (recurrence) {
    ScheduledTaskRecurrence.ONCE -> {
      DateRow(dueAt, onDate)
      TimeRow(dueAt, onTime)
    }

    ScheduledTaskRecurrence.DAILY -> TimeRow(dueAt, onTime)

    ScheduledTaskRecurrence.WEEKLY -> {
      WeekdayChips(days, onDays)
      TimeRow(dueAt, onTime)
    }

    ScheduledTaskRecurrence.MONTHLY -> {
      MonthDayChips(days, onDays)
      TimeRow(dueAt, onTime)
    }
  }
}

private fun formatScheduledDate(millis: Long): String =
  DateFormat.getDateInstance(DateFormat.MEDIUM).format(Date(millis))

internal fun defaultDaysFor(recurrence: ScheduledTaskRecurrence, dueAt: Long): List<Int> {
  val date = Calendar.getInstance().apply { timeInMillis = dueAt }
  return when (recurrence) {
    ScheduledTaskRecurrence.WEEKLY -> listOf(date.get(Calendar.DAY_OF_WEEK))
    ScheduledTaskRecurrence.MONTHLY -> listOf(date.get(Calendar.DAY_OF_MONTH))
    else -> emptyList()
  }
}

@Composable
private fun DateRow(dueAt: Long, onDate: () -> Unit) {
  SettingsSection(title = null) {
    SettingsRow(
      title = stringResource(R.string.scheduled_task_date),
      subtitle = formatScheduledDate(dueAt),
      onClick = onDate,
    ) { Chevron() }
  }
}

@Composable
private fun TimeRow(dueAt: Long, onTime: () -> Unit) {
  SettingsSection(title = null) {
    SettingsRow(
      title = stringResource(R.string.scheduled_task_time),
      subtitle = formatScheduledTime(LocalContext.current, dueAt),
      onClick = onTime,
    ) { Chevron() }
  }
}

@Composable
private fun WeekdayChips(days: List<Int>, onDays: (List<Int>) -> Unit) {
  SettingsSection(title = stringResource(R.string.scheduled_task_days)) {
    Row(
      modifier = Modifier.fillMaxWidth().padding(PigkinTokens.Spacing.s8),
      horizontalArrangement = Arrangement.SpaceEvenly,
    ) {
      weekdays.forEach { day ->
        DayChip(
          label = stringResource(weekdayLabel(day)),
          selected = day in days,
          onClick = { onDays(toggle(days, day)) },
        )
      }
    }
  }
}

@Composable
private fun MonthDayChips(days: List<Int>, onDays: (List<Int>) -> Unit) {
  SettingsSection(title = stringResource(R.string.scheduled_task_days)) {
    Column(modifier = Modifier.fillMaxWidth().padding(PigkinTokens.Spacing.s8)) {
      for (week in (1..31).chunked(7)) {
        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
          week.forEach { day ->
            DayChip(
              label = day.toString(),
              selected = day in days,
              onClick = { onDays(toggle(days, day)) },
            )
          }
        }
      }
    }
  }
}

@Composable
private fun DayChip(label: String, selected: Boolean, onClick: () -> Unit) {
  val colors = pigkinColors
  Box(
    modifier = Modifier
      .heightIn(min = 40.dp)
      .clip(CircleShape)
      .background(if (selected) colors.accentSoft else colors.surface2)
      .selectable(selected = selected, role = Role.Checkbox, onClick = onClick)
      .padding(horizontal = PigkinTokens.Spacing.s8, vertical = PigkinTokens.Spacing.s4),
    contentAlignment = Alignment.Center,
  ) {
    Text(
      text = label,
      color = if (selected) colors.accent else colors.foregroundMuted,
      fontSize = PigkinTokens.FontSize.sm,
      fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
    )
  }
}

private fun toggle(days: List<Int>, day: Int): List<Int> = if (day in days) days - day else (days + day).sorted()

private val weekdays = listOf(
  Calendar.MONDAY,
  Calendar.TUESDAY,
  Calendar.WEDNESDAY,
  Calendar.THURSDAY,
  Calendar.FRIDAY,
  Calendar.SATURDAY,
  Calendar.SUNDAY,
)

private fun weekdayLabel(day: Int): Int = when (day) {
  Calendar.SUNDAY -> R.string.scheduled_sunday
  Calendar.MONDAY -> R.string.scheduled_monday
  Calendar.TUESDAY -> R.string.scheduled_tuesday
  Calendar.WEDNESDAY -> R.string.scheduled_wednesday
  Calendar.THURSDAY -> R.string.scheduled_thursday
  Calendar.FRIDAY -> R.string.scheduled_friday
  else -> R.string.scheduled_saturday
}
