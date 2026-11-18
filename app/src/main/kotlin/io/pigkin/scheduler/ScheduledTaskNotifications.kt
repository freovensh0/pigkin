package io.pigkin.scheduler

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import io.pigkin.MainActivity
import io.pigkin.R
import io.pigkin.data.ScheduledTask

internal object ScheduledTaskNotifications {
  fun post(context: Context, task: ScheduledTask, result: String?, failed: Boolean) {
    val manager = context.getSystemService(NotificationManager::class.java)
    manager.createNotificationChannel(
      NotificationChannel(
        CHANNEL,
        context.getString(R.string.scheduled_task_notification_channel),
        NotificationManager.IMPORTANCE_HIGH,
      ),
    )
    val text = if (failed) {
      context.getString(R.string.scheduled_task_notification_failed)
    } else {
      result?.take(RESULT_LIMIT).orEmpty()
    }
    val title = task.prompt.take(TITLE_LIMIT).ifBlank { context.getString(R.string.scheduled_task_notification_title) }
    val open = Intent(context, MainActivity::class.java)
      .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP)
    val notification = Notification.Builder(context, CHANNEL)
      .setSmallIcon(R.drawable.ic_launcher_foreground)
      .setContentTitle(title)
      .setContentText(text)
      .setStyle(Notification.BigTextStyle().bigText(text))
      .setContentIntent(
        PendingIntent.getActivity(
          context,
          task.id.hashCode(),
          open,
          PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        ),
      )
      .setAutoCancel(true)
      .build()
    manager.notify(task.id.hashCode(), notification)
  }

  private const val CHANNEL = "scheduled_tasks"
  private const val RESULT_LIMIT = 200
  private const val TITLE_LIMIT = 100
}
