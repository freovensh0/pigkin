package io.pigkin.ui.settings

import java.util.Calendar

internal fun nextScheduledHour(): Long = Calendar.getInstance().apply {
  add(Calendar.HOUR_OF_DAY, 1)
  set(Calendar.MINUTE, 0)
  set(Calendar.SECOND, 0)
  set(Calendar.MILLISECOND, 0)
}.timeInMillis

/** Keeps the time the user already chose when they change the date. */
internal fun withScheduledDay(dayMillis: Long, current: Long): Long {
  val time = Calendar.getInstance().apply { timeInMillis = current }
  return Calendar.getInstance().apply {
    timeInMillis = dayMillis
    set(Calendar.HOUR_OF_DAY, time.get(Calendar.HOUR_OF_DAY))
    set(Calendar.MINUTE, time.get(Calendar.MINUTE))
    set(Calendar.SECOND, 0)
    set(Calendar.MILLISECOND, 0)
  }.timeInMillis
}

internal fun withScheduledTime(current: Long, hour: Int, minute: Int): Long = Calendar.getInstance().apply {
  timeInMillis = current
  set(Calendar.HOUR_OF_DAY, hour)
  set(Calendar.MINUTE, minute)
  set(Calendar.SECOND, 0)
  set(Calendar.MILLISECOND, 0)
}.timeInMillis
