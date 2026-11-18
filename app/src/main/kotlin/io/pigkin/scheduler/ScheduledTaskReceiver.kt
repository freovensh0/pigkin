package io.pigkin.scheduler

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import io.pigkin.data.ScheduledTaskStore
import kotlinx.coroutines.launch

/** Re-arms every enabled task after a reboot and runs a task when its alarm fires. */
class ScheduledTaskReceiver : BroadcastReceiver() {
  override fun onReceive(context: Context, intent: Intent) {
    when (intent.action) {
      Intent.ACTION_BOOT_COMPLETED -> {
        ScheduledTaskStore.init(context)
        ScheduledTaskAlarms.rescheduleAll()
      }

      ACTION_RUN_TASK -> {
        val id = intent.getStringExtra(EXTRA_TASK_ID) ?: return
        val retry = intent.getBooleanExtra(EXTRA_RETRY, false)
        val pending = goAsync()
        val app = context.applicationContext
        ScheduledTaskRunner.scope.launch {
          try {
            ScheduledTaskStore.init(app)
            if (retry) ScheduledTaskStore.retry(id)
            ScheduledTaskRunner.run(app, id)
          } finally {
            pending.finish()
          }
        }
      }
    }
  }
}
