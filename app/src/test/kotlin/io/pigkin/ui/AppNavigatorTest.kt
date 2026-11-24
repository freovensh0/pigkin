package io.pigkin.ui

import org.junit.Assert.assertEquals
import org.junit.Test

class AppNavigatorTest {
  @Test
  fun anArchivedChatSurvivesBeingSavedWhateverItsPathHolds() {
    val stack = listOf(
      Destination.Main,
      Destination.Archive,
      Destination.ArchivedChat("/s/a b:c.jsonl"),
    )

    val restored = AppNavigator.Saver.restore(stack.map(AppNavigator::key))

    assertEquals(stack, restored?.stack?.value)
  }

  @Test
  fun aScheduledTaskEditorSurvivesBeingSaved() {
    val stack = listOf(
      Destination.Main,
      Destination.Settings,
      Destination.ScheduledTasks,
      Destination.ScheduledTaskEditor("task-1"),
    )

    val restored = AppNavigator.Saver.restore(stack.map(AppNavigator::key))

    assertEquals(stack, restored?.stack?.value)
  }

  @Test
  fun aNewScheduledTaskEditorHasNoIdAfterBeingSaved() {
    val stack = listOf(Destination.Main, Destination.ScheduledTaskEditor(null))

    val restored = AppNavigator.Saver.restore(stack.map(AppNavigator::key))

    assertEquals(stack, restored?.stack?.value)
  }
}
