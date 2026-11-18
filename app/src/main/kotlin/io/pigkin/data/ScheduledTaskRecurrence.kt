package io.pigkin.data

import java.util.Calendar

internal enum class ScheduledTaskRecurrence { ONCE, DAILY, WEEKLY, MONTHLY }

/**
 * The next occurrence after [now] in local time; null for a task that runs once. Slots already past are skipped, so a
 * task missed while Pigkin was closed fires at its next future slot, not once per miss. [days] selects weekdays (1..7)
 * for WEEKLY or days of month (1..31) for MONTHLY; when empty, the day in [dueAt] is used. Invalid days are skipped.
 */
internal fun nextDueAtMillis(
  dueAt: Long,
  recurrence: ScheduledTaskRecurrence,
  now: Long,
  days: List<Int> = emptyList(),
): Long? {
  if (recurrence == ScheduledTaskRecurrence.ONCE) return null
  val selected = days.filter {
    if (recurrence == ScheduledTaskRecurrence.WEEKLY) it in 1..7 else it in 1..31
  }.toSet()
  return when {
    recurrence == ScheduledTaskRecurrence.DAILY || selected.isEmpty() -> steppedDueAt(dueAt, recurrence, now)
    recurrence == ScheduledTaskRecurrence.WEEKLY -> nextWeekday(now, dueAt, selected)
    else -> nextMonthDay(now, dueAt, selected)
  }
}

private fun steppedDueAt(dueAt: Long, recurrence: ScheduledTaskRecurrence, now: Long): Long {
  val (field, amount) = when (recurrence) {
    ScheduledTaskRecurrence.DAILY -> Calendar.DATE to 1
    ScheduledTaskRecurrence.WEEKLY -> Calendar.DATE to 7
    ScheduledTaskRecurrence.MONTHLY -> Calendar.MONTH to 1
    ScheduledTaskRecurrence.ONCE -> return dueAt
  }
  val calendar = Calendar.getInstance().apply { timeInMillis = dueAt }
  var next = dueAt
  repeat(MAX_RELAPSED_STEPS) {
    calendar.add(field, amount)
    next = calendar.timeInMillis
    if (next > now) return next
  }
  return next
}

private fun nextWeekday(now: Long, dueAt: Long, weekdays: Set<Int>): Long {
  val calendar = Calendar.getInstance().apply {
    timeInMillis = now
    setTimeOfDay(dueAt)
  }
  repeat(MAX_RELAPSED_STEPS) {
    if (calendar.timeInMillis > now && calendar.get(Calendar.DAY_OF_WEEK) in weekdays) return calendar.timeInMillis
    calendar.add(Calendar.DATE, 1)
  }
  return calendar.timeInMillis
}

private fun nextMonthDay(now: Long, dueAt: Long, monthDays: Set<Int>): Long {
  val start = Calendar.getInstance().apply { timeInMillis = now }
  val year = start.get(Calendar.YEAR)
  val month = start.get(Calendar.MONTH)
  val time = Calendar.getInstance().apply { timeInMillis = dueAt }
  val hour = time.get(Calendar.HOUR_OF_DAY)
  val minute = time.get(Calendar.MINUTE)
  val second = time.get(Calendar.SECOND)
  val millisecond = time.get(Calendar.MILLISECOND)
  var latest = now
  repeat(MAX_MONTHS) { offset ->
    for (day in monthDays.sorted()) {
      val candidate = Calendar.getInstance().apply {
        clear()
        set(year, month + offset, day, hour, minute, second)
        set(Calendar.MILLISECOND, millisecond)
      }
      // A lenient Calendar rolls Feb 31 into March; only keep days that landed on the requested date.
      val requestedMonth = year * 12 + month + offset
      if (candidate.get(Calendar.DAY_OF_MONTH) != day ||
        candidate.get(Calendar.YEAR) * 12 + candidate.get(Calendar.MONTH) != requestedMonth
      ) {
        continue
      }
      val millis = candidate.timeInMillis
      if (millis > now) return millis
      latest = millis
    }
  }
  return latest
}

private fun Calendar.setTimeOfDay(dueAt: Long) {
  val time = Calendar.getInstance().apply { timeInMillis = dueAt }
  set(Calendar.HOUR_OF_DAY, time.get(Calendar.HOUR_OF_DAY))
  set(Calendar.MINUTE, time.get(Calendar.MINUTE))
  set(Calendar.SECOND, time.get(Calendar.SECOND))
  set(Calendar.MILLISECOND, time.get(Calendar.MILLISECOND))
}

private const val MAX_RELAPSED_STEPS = 10_000
private const val MAX_MONTHS = 24
