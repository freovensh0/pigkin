package io.pigkin.data

import java.util.Calendar

internal enum class ScheduledTaskRecurrence { ONCE, DAILY, WEEKLY, MONTHLY }

/**
 * The next occurrence after [now], stepped from [previousDue] in local time; null for a task that runs once. Slots
 * already past are skipped, so a task missed while Pigkin was closed fires at its next future slot, not once per miss.
 */
internal fun nextDueAtMillis(previousDue: Long, recurrence: ScheduledTaskRecurrence, now: Long): Long? {
  val (field, amount) = when (recurrence) {
    ScheduledTaskRecurrence.ONCE -> return null
    ScheduledTaskRecurrence.DAILY -> Calendar.DATE to 1
    ScheduledTaskRecurrence.WEEKLY -> Calendar.DATE to 7
    ScheduledTaskRecurrence.MONTHLY -> Calendar.MONTH to 1
  }
  val calendar = Calendar.getInstance().apply { timeInMillis = previousDue }
  var next = previousDue
  repeat(MAX_RELAPSED_STEPS) {
    calendar.add(field, amount)
    next = calendar.timeInMillis
    if (next > now) return next
  }
  return next
}

private const val MAX_RELAPSED_STEPS = 10_000
