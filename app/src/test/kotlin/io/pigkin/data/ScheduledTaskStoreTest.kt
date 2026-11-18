package io.pigkin.data

import java.util.Calendar
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ScheduledTaskStoreTest {
  private fun at(year: Int, month: Int, day: Int, hour: Int = 9) = Calendar.getInstance().apply {
    clear()
    set(year, month, day, hour, 0, 0)
  }.timeInMillis

  @Test
  fun onceHasNoNextRun() {
    assertNull(nextDueAtMillis(at(2025, Calendar.JANUARY, 1), ScheduledTaskRecurrence.ONCE, 0L))
  }

  @Test
  fun aMissedDailyRunResumesTomorrow() {
    val due = at(2025, Calendar.JANUARY, 20)
    val now = at(2025, Calendar.JANUARY, 23, 10)

    assertEquals(at(2025, Calendar.JANUARY, 24), nextDueAtMillis(due, ScheduledTaskRecurrence.DAILY, now))
  }

  @Test
  fun weeklyAddsSevenDays() {
    val due = at(2025, Calendar.JANUARY, 20)
    val now = at(2025, Calendar.JANUARY, 21)

    assertEquals(at(2025, Calendar.JANUARY, 27), nextDueAtMillis(due, ScheduledTaskRecurrence.WEEKLY, now))
  }

  @Test
  fun aLongOverdueDailyRunStillCatchesUp() {
    val due = at(2010, Calendar.JANUARY, 1)
    val now = at(2025, Calendar.JANUARY, 1)

    assertEquals(at(2025, Calendar.JANUARY, 2), nextDueAtMillis(due, ScheduledTaskRecurrence.DAILY, now))
  }

  @Test
  fun monthEndClampsToTheShortMonth() {
    val due = at(2025, Calendar.JANUARY, 31)
    val now = at(2025, Calendar.FEBRUARY, 1)

    assertEquals(at(2025, Calendar.FEBRUARY, 28), nextDueAtMillis(due, ScheduledTaskRecurrence.MONTHLY, now))
  }

  @Test
  fun monthEndClampsToALeapDay() {
    val due = at(2024, Calendar.JANUARY, 31)
    val now = at(2024, Calendar.FEBRUARY, 1)

    assertEquals(at(2024, Calendar.FEBRUARY, 29), nextDueAtMillis(due, ScheduledTaskRecurrence.MONTHLY, now))
  }

  @Test
  fun recurrenceStepsKeepTheirOrder() {
    val due = at(2025, Calendar.JANUARY, 20)
    val now = at(2025, Calendar.JANUARY, 20, 10)

    assertEquals(at(2025, Calendar.JANUARY, 21), nextDueAtMillis(due, ScheduledTaskRecurrence.DAILY, now))
    assertEquals(at(2025, Calendar.JANUARY, 27), nextDueAtMillis(due, ScheduledTaskRecurrence.WEEKLY, now))
    assertEquals(at(2025, Calendar.FEBRUARY, 20), nextDueAtMillis(due, ScheduledTaskRecurrence.MONTHLY, now))
  }

  @Test
  fun weeklyPicksTheNextChosenWeekday() {
    val due = at(2025, Calendar.JANUARY, 20)
    val days = listOf(Calendar.MONDAY, Calendar.WEDNESDAY)

    assertEquals(
      at(2025, Calendar.JANUARY, 22),
      nextDueAtMillis(due, ScheduledTaskRecurrence.WEEKLY, at(2025, Calendar.JANUARY, 21, 8), days),
    )
    assertEquals(
      at(2025, Calendar.JANUARY, 27),
      nextDueAtMillis(due, ScheduledTaskRecurrence.WEEKLY, at(2025, Calendar.JANUARY, 22, 10), days),
    )
  }

  @Test
  fun anOverdueWeeklyRunSkipsToTheNextChosenWeekday() {
    val due = at(2020, Calendar.JANUARY, 6)
    val now = at(2025, Calendar.JANUARY, 23, 12)

    assertEquals(
      at(2025, Calendar.JANUARY, 27),
      nextDueAtMillis(due, ScheduledTaskRecurrence.WEEKLY, now, listOf(Calendar.MONDAY)),
    )
  }

  @Test
  fun monthlyUsesTheChosenDaysAndSkipsInvalidOnes() {
    val due = at(2025, Calendar.JANUARY, 1)
    val days = listOf(15, 31)

    assertEquals(
      at(2025, Calendar.JANUARY, 31),
      nextDueAtMillis(due, ScheduledTaskRecurrence.MONTHLY, at(2025, Calendar.JANUARY, 20), days),
    )
    assertEquals(
      at(2025, Calendar.FEBRUARY, 15),
      nextDueAtMillis(due, ScheduledTaskRecurrence.MONTHLY, at(2025, Calendar.JANUARY, 31, 10), days),
    )
    assertEquals(
      at(2025, Calendar.MARCH, 31),
      nextDueAtMillis(due, ScheduledTaskRecurrence.MONTHLY, at(2025, Calendar.FEBRUARY, 20), listOf(31)),
    )
  }

  @Test
  fun aFailedRunPausesTheTaskWithoutAdvancingIt() {
    val task = ScheduledTask(
      id = "task",
      prompt = "Check in",
      dueAtMillis = at(2025, Calendar.JANUARY, 20),
      recurrence = ScheduledTaskRecurrence.WEEKLY,
      icon = "bell",
      reminder = true,
      enabled = true,
      days = listOf(Calendar.MONDAY),
    )
    val now = at(2025, Calendar.JANUARY, 20, 10)

    val failed = afterRun(task, failed = true, now = now)
    assertEquals(false, failed.enabled)
    assertEquals(true, failed.failed)
    assertEquals(task.dueAtMillis, failed.dueAtMillis)
    assertEquals(now, failed.lastRunAtMillis)

    val retried = failed.copy(enabled = true, failed = false)
    assertEquals(true, retried.enabled)
    assertEquals(false, retried.failed)
    assertEquals(
      at(2025, Calendar.JANUARY, 27),
      afterRun(retried, failed = false, now = now).dueAtMillis,
    )
  }
}
