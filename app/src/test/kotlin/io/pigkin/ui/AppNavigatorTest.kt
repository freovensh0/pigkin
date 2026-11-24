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

  /** 0.0.30 and 0.0.31 pushed the folder picker on every sidebar recomposition, so their saved stacks repeat it. */
  @Test
  fun aRestoredStackKeepsOneCopyOfARepeatedScreen() {
    val saved = List(4) { Destination.Main } + List(200) { Destination.AddWorkspace }

    val restored = AppNavigator.Saver.restore(saved.map(AppNavigator::key))

    assertEquals(listOf(Destination.Main, Destination.AddWorkspace), restored?.stack?.value)
  }
}
