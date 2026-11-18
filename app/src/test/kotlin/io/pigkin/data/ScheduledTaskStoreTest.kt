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
}
