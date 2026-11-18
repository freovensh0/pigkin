package io.pigkin.data

import android.content.Context
import android.content.SharedPreferences
import android.util.Log
import androidx.core.content.edit
import io.pigkin.scheduler.ScheduledTaskAlarms
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

internal data class ScheduledTask(
  val id: String,
  val prompt: String,
  val dueAtMillis: Long,
  val recurrence: ScheduledTaskRecurrence,
  val icon: String,
  val reminder: Boolean,
  val enabled: Boolean,
  val lastRunAtMillis: Long? = null,
  val days: List<Int> = emptyList(),
  val modelId: String? = null,
  val thinking: String? = null,
  val failed: Boolean = false,
)

/** A failed run pauses its task and leaves it unscheduled until the user retries it. */
internal fun afterRun(task: ScheduledTask, failed: Boolean, now: Long): ScheduledTask {
  if (failed) return task.copy(enabled = false, failed = true, lastRunAtMillis = now)
  val updated = task.copy(
    dueAtMillis = nextDueAtMillis(task.dueAtMillis, task.recurrence, now, task.days) ?: task.dueAtMillis,
    lastRunAtMillis = now,
    failed = false,
  )
  return if (task.recurrence == ScheduledTaskRecurrence.ONCE) updated.copy(enabled = false) else updated
}

/** Scheduled tasks in the `scheduled_tasks` SharedPreferences file. The [tasks] flow is the source of truth. */
internal object ScheduledTaskStore {
  private val state = MutableStateFlow<List<ScheduledTask>>(emptyList())
  val tasks: StateFlow<List<ScheduledTask>> = state.asStateFlow()

  internal lateinit var applicationContext: Context
    private set

  private lateinit var prefs: SharedPreferences

  /** Loads the saved tasks once; safe to call from the app and from a receiver in the same process. */
  fun init(context: Context) {
    if (this::applicationContext.isInitialized) return
    synchronized(this) {
      if (this::applicationContext.isInitialized) return
      applicationContext = context.applicationContext
      prefs = applicationContext.getSharedPreferences(FILE, Context.MODE_PRIVATE)
      state.value = load()
    }
  }

  fun refresh() {
    state.value = load()
  }

  fun save(task: ScheduledTask) {
    write(state.value.filterNot { it.id == task.id } + task)
    if (task.enabled && !task.failed) ScheduledTaskAlarms.schedule(task) else ScheduledTaskAlarms.cancel(task.id)
  }

  fun remove(id: String) {
    write(state.value.filterNot { it.id == id })
    ScheduledTaskAlarms.cancel(id)
  }

  fun markRun(id: String, failed: Boolean) {
    val task = state.value.firstOrNull { it.id == id } ?: return
    val updated = afterRun(task, failed, System.currentTimeMillis())
    write(state.value.map { if (it.id == id) updated else it })
    // A failed run consumed its alarm: leave it unscheduled so an explicit retry is the only way to run it again.
    if (!updated.enabled || updated.failed) ScheduledTaskAlarms.cancel(id) else ScheduledTaskAlarms.schedule(updated)
  }

  /** Enables a failed task without scheduling or running it; the caller runs it explicitly. */
  fun retry(id: String) {
    val task = state.value.firstOrNull { it.id == id } ?: return
    write(state.value.map { if (it.id == id) task.copy(failed = false, enabled = true) else it })
  }

  private fun write(tasks: List<ScheduledTask>) {
    state.value = tasks
    prefs.edit { putString(KEY_TASKS, JSONArray(tasks.map { it.toTaskJson() }).toString()) }
  }

  private fun load(): List<ScheduledTask> {
    val raw = prefs.getString(KEY_TASKS, null) ?: return emptyList()
    val array = try {
      JSONArray(raw)
    } catch (error: JSONException) {
      Log.w(TAG, "Discarding unreadable scheduled tasks", error)
      return emptyList()
    }
    return (0 until array.length()).mapNotNull { index -> array.optJSONObject(index)?.toTask() }
  }

  private fun JSONObject.toTask(): ScheduledTask? = try {
    val recurrence = ScheduledTaskRecurrence.entries
      .firstOrNull { it.name == optString("recurrence") } ?: return null
    val savedDays = optJSONArray("days")
    ScheduledTask(
      id = getString("id"),
      prompt = getString("prompt"),
      dueAtMillis = getLong("dueAtMillis"),
      recurrence = recurrence,
      icon = optString("icon"),
      reminder = optBoolean("reminder"),
      enabled = optBoolean("enabled", true),
      lastRunAtMillis = if (isNull("lastRunAtMillis")) null else getLong("lastRunAtMillis"),
      days = if (savedDays == null) {
        emptyList()
      } else {
        (0 until savedDays.length()).mapNotNull { index ->
          savedDays.optInt(index).takeIf { it > 0 }
        }
      },
      modelId = if (isNull("modelId")) null else optString("modelId"),
      thinking = if (isNull("thinking")) null else optString("thinking"),
      failed = optBoolean("failed", false),
    )
  } catch (error: JSONException) {
    Log.w(TAG, "Discarding a malformed scheduled task", error)
    null
  }

  private fun ScheduledTask.toTaskJson(): JSONObject = JSONObject()
    .put("id", id)
    .put("prompt", prompt)
    .put("dueAtMillis", dueAtMillis)
    .put("recurrence", recurrence.name)
    .put("icon", icon)
    .put("reminder", reminder)
    .put("enabled", enabled)
    .putOpt("lastRunAtMillis", lastRunAtMillis)
    .put("days", JSONArray(days))
    .putOpt("modelId", modelId)
    .putOpt("thinking", thinking)
    .put("failed", failed)

  private const val FILE = "scheduled_tasks"
  private const val KEY_TASKS = "tasks"
  private const val TAG = "PigkinScheduled"
}
