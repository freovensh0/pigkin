package io.pigkin.scheduler

import android.content.Context
import io.pigkin.data.ScheduledTaskStore
import io.pigkin.host.HostClient
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.flowOf

/** Runs scheduled tasks off the receiver's main thread. flowOf(true) bypasses the app's foreground-only host gate. */
internal object ScheduledTaskRunner {
  val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
  private val hostClient: HostClient by lazy {
    HostClient(ScheduledTaskStore.applicationContext, flowOf(true), scope)
  }

  @Suppress("TooGenericExceptionCaught") // Any failure must reach the user as a notification, never crash the receiver.
  suspend fun run(context: Context, id: String) {
    val task = ScheduledTaskStore.tasks.value.firstOrNull { it.id == id } ?: return
    if (!ScheduledTaskStore.beginRun(id)) return
    try {
      val run = try {
        hostClient.scheduledTaskRun(task.prompt, task.modelId, task.thinking, task.id, task.sessionPath)
      } catch (ignored: Exception) {
        null
      }
      val failed = run == null
      if (task.reminder) ScheduledTaskNotifications.post(context.applicationContext, task, run?.result, failed)
      ScheduledTaskStore.markRun(id, failed, run?.sessionPath, run?.cwd)
    } finally {
      ScheduledTaskStore.endRun(id)
    }
  }

  /** Runs a task now, after the caller has retried a failed one; used by the UI. */
  suspend fun runNow(context: Context, id: String) = run(context, id)
}
