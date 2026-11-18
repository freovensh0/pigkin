package io.pigkin.scheduler

import android.app.AlarmManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import io.pigkin.data.ScheduledTask
import io.pigkin.data.ScheduledTaskStore

internal const val ACTION_RUN_TASK = "io.pigkin.scheduler.RUN_TASK"
internal const val EXTRA_TASK_ID = "io.pigkin.scheduler.TASK_ID"
internal const val EXTRA_RETRY = "io.pigkin.scheduler.RETRY"

/** Inexact alarms that survive Doze without needing the exact-alarm permission; re-armed on boot and on foreground. */
internal object ScheduledTaskAlarms {
  fun rescheduleAll() {
    for (task in ScheduledTaskStore.tasks.value) {
      if (task.enabled && !task.failed) schedule(task) else cancel(task.id)
    }
  }

  fun cancel(id: String) {
    val context = ScheduledTaskStore.applicationContext
    context.alarmManager().cancel(pendingIntent(context, id))
  }

  fun schedule(task: ScheduledTask) {
    val context = ScheduledTaskStore.applicationContext
    context.alarmManager().setAndAllowWhileIdle(
      AlarmManager.RTC_WAKEUP,
      task.dueAtMillis,
      pendingIntent(context, task.id),
    )
  }

  private fun Context.alarmManager(): AlarmManager = getSystemService(AlarmManager::class.java)

  private fun pendingIntent(context: Context, id: String): PendingIntent {
    val intent = Intent(context, ScheduledTaskReceiver::class.java)
      .setAction(ACTION_RUN_TASK)
      .putExtra(EXTRA_TASK_ID, id)
    return PendingIntent.getBroadcast(
      context,
      id.hashCode(),
      intent,
      PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
    )
  }
}
