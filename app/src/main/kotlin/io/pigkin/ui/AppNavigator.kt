package io.pigkin.ui

import androidx.compose.runtime.saveable.Saver
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** A full screen of the app; [AppNavigator] stacks them. */
sealed interface Destination {
  data object Main : Destination

  data object Settings : Destination

  /** Downloading and running models on the phone, reached from Providers and models › Experimental. */
  data object LocalModels : Destination

  /** The list of added providers. */
  data object Providers : Destination

  /** Picks which kind of provider to add. */
  data object AddProvider : Destination

  /**
   * A subscription Pigkin signs in to (`anthropic` or `openai-codex`); [openModels] starts on the model picker.
   */
  data class Subscription(val id: String, val openModels: Boolean = false) : Destination

  /** [id] null adds a provider; otherwise edits the custom provider with that id. */
  data class ProviderEditor(val id: String?) : Destination

  data object Onboarding : Destination

  /** Archived chats, with search, restore and delete. */
  data object Archive : Destination

  /** The read-only transcript of the archived chat in session file [path]. */
  data class ArchivedChat(val path: String) : Destination

  /** The prompts Pigkin runs at a chosen time, reached from the sidebar. */
  data object ScheduledTasks : Destination

  /** [id] null adds a scheduled task; otherwise edits the one with that id. */
  data class ScheduledTaskEditor(val id: String?) : Destination

  /** Picks a folder to add as a workspace, reached from the sidebar. */
  data object AddWorkspace : Destination

  /**
   * [id] null adds a remote host; otherwise edits the host with that id. [fromPicker] when the folder picker opened
   * it, which a new host then returns to with that host chosen.
   */
  data class RemoteHostEditor(val id: String?, val fromPicker: Boolean = false) : Destination
}

/** The screen stack; never empty. Main thread only. */
class AppNavigator(initial: List<Destination>) {
  private val stackState = MutableStateFlow(initial.ifEmpty { listOf(Destination.Main) })

  val stack: StateFlow<List<Destination>> = stackState.asStateFlow()

  fun push(d: Destination) {
    stackState.value += d
  }

  /** Removes the top screen; false when only one is left, so the caller lets the system handle back. */
  fun pop(): Boolean {
    val current = stackState.value
    if (current.size <= 1) return false
    stackState.value = current.dropLast(1)
    return true
  }

  /** Pops back to the topmost [d]; pops one screen when [d] is not on the stack. */
  fun popTo(d: Destination) {
    val current = stackState.value
    val index = current.lastIndexOf(d)
    if (index < 0) pop() else stackState.value = current.take(index + 1)
  }

  fun replaceAll(d: Destination) {
    stackState.value = listOf(d)
  }

  companion object {
    /** Keeps the stack across activity recreation as [key] strings. */
    val Saver: Saver<AppNavigator, List<String>> = Saver(
      save = { navigator -> navigator.stack.value.map(::key) },
      restore = { keys -> AppNavigator(keys.mapNotNull(::destination)) },
    )

    /** A stable string per destination, also used as its saveable-state key. */
    fun key(d: Destination): String = when (d) {
      Destination.Main -> MAIN
      Destination.Settings -> SETTINGS
      Destination.LocalModels -> LOCAL_MODELS
      Destination.Onboarding -> ONBOARDING
      Destination.Providers -> PROVIDERS
      Destination.AddProvider -> ADD_PROVIDER
      is Destination.Subscription -> SUBSCRIPTION + d.id + if (d.openModels) "!models" else ""
      is Destination.ProviderEditor -> PROVIDER + d.id.orEmpty()
      Destination.Archive, is Destination.ArchivedChat -> archiveKey(d)
      Destination.AddWorkspace -> ADD_WORKSPACE
      is Destination.RemoteHostEditor -> remoteHostKey(d)
      Destination.ScheduledTasks, is Destination.ScheduledTaskEditor -> scheduledTaskKey(d)
    }

    private fun archiveKey(d: Destination): String = when (d) {
      is Destination.ArchivedChat -> ARCHIVED_CHAT + d.path
      else -> ARCHIVE
    }

    private fun remoteHostKey(d: Destination.RemoteHostEditor): String =
      (if (d.fromPicker) REMOTE_HOST_PICKER else REMOTE_HOST) + d.id.orEmpty()

    private fun scheduledTaskKey(d: Destination): String = when (d) {
      is Destination.ScheduledTaskEditor -> SCHEDULED_TASK + d.id.orEmpty()
      else -> SCHEDULED_TASKS
    }

    /** The destinations that carry nothing but their key. */
    private val SCREENS = listOf(
      Destination.Main, Destination.Settings, Destination.LocalModels, Destination.Onboarding, Destination.Providers,
      Destination.AddProvider, Destination.Archive, Destination.AddWorkspace, Destination.ScheduledTasks,
    )

    private fun destination(key: String): Destination? = when {
      SCREENS.any { key(it) == key } -> SCREENS.first { key(it) == key }

      key.startsWith(ARCHIVED_CHAT) -> Destination.ArchivedChat(key.removePrefix(ARCHIVED_CHAT))

      key.startsWith(SUBSCRIPTION) -> key.removePrefix(SUBSCRIPTION).let { rest ->
        Destination.Subscription(rest.removeSuffix("!models"), rest.endsWith("!models"))
      }

      key.startsWith(PROVIDER) -> Destination.ProviderEditor(key.removePrefix(PROVIDER).ifEmpty { null })

      key.startsWith(REMOTE_HOST) -> Destination.RemoteHostEditor(key.removePrefix(REMOTE_HOST).ifEmpty { null })

      key.startsWith(REMOTE_HOST_PICKER) ->
        Destination.RemoteHostEditor(key.removePrefix(REMOTE_HOST_PICKER).ifEmpty { null }, fromPicker = true)

      key.startsWith(SCHEDULED_TASK) ->
        Destination.ScheduledTaskEditor(key.removePrefix(SCHEDULED_TASK).ifEmpty { null })

      else -> null
    }

    private const val MAIN = "main"
    private const val SETTINGS = "settings"
    private const val LOCAL_MODELS = "local-models"
    private const val ONBOARDING = "onboarding"
    private const val PROVIDER = "provider:"
    private const val PROVIDERS = "providers"
    private const val ADD_PROVIDER = "add-provider"
    private const val SUBSCRIPTION = "subscription:"
    private const val ARCHIVE = "archive"
    private const val ARCHIVED_CHAT = "archived-chat:"
    private const val ADD_WORKSPACE = "add-workspace"
    private const val REMOTE_HOST = "remote-host:"
    private const val REMOTE_HOST_PICKER = "remote-host-picker:"
    private const val SCHEDULED_TASKS = "scheduled-tasks"
    private const val SCHEDULED_TASK = "scheduled-task:"
  }
}
