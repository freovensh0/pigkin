package io.pigkin.ui.chat

import androidx.compose.animation.AnimatedContent
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import io.pigkin.agent.AgentProcess
import io.pigkin.ui.components.SnackbarStackState
import io.pigkin.ui.stackTransition
import io.pigkin.ui.subagents.ChatSubagentScreen
import io.pigkin.ui.workspace.PaneControls

/** A subagent's depth over its chat, for the slide between them; none is the chat itself. */
private val String?.depth: Int get() = if (this == null) 0 else 1

/** The callbacks the chat body needs from its pane. */
internal data class ChatPaneCallbacks(
  val onOpenProviders: () -> Unit,
  val onOpenOnboarding: () -> Unit,
  val onSheet: (ChatSheet) -> Unit,
  val onOpenSubagent: (String) -> Unit,
  val onCloseSubagent: () -> Unit,
)

@Composable
internal fun ChatPaneBody(
  controller: ChatController,
  snackbar: SnackbarStackState,
  controls: PaneControls,
  shownSubagent: String?,
  process: AgentProcess,
  callbacks: ChatPaneCallbacks,
) {
  AnimatedContent(
    targetState = shownSubagent,
    transitionSpec = { stackTransition(from = initialState.depth, to = targetState.depth) },
    label = "subagent",
  ) { subagentId ->
    if (subagentId != null) {
      ChatSubagentScreen(
        controller = controller,
        toolCallId = subagentId,
        chatTitle = chatTitle(process),
        controls = controls,
        onBack = callbacks.onCloseSubagent,
      )
    } else {
      ChatContent(
        controller,
        snackbar,
        controls,
        callbacks.onOpenProviders,
        callbacks.onOpenOnboarding,
        onSheet = callbacks.onSheet,
        onOpenSubagent = callbacks.onOpenSubagent,
      )
    }
  }
}

/** What the chat and its drill-down screens need from the pane. */
internal data class ChatScreensState(
  val controller: ChatController,
  val snackbar: SnackbarStackState,
  val controls: PaneControls,
  val subagent: String?,
  val drill: ChatDrill?,
  val process: AgentProcess,
  val callbacks: ChatPaneCallbacks,
  val onSetSubagent: (String) -> Unit,
  val onClearDrill: () -> Unit,
)

/** The chat or subagent, with any model, fork or subagent list over it. */
@Composable
internal fun ChatPaneScreens(state: ChatScreensState) {
  Box(Modifier.fillMaxSize()) {
    ChatPaneBody(
      state.controller,
      state.snackbar,
      state.controls,
      state.subagent,
      state.process,
      state.callbacks,
    )
    ChatDrillScreen(
      drill = state.drill,
      controller = state.controller,
      onOpenSubagent = {
        state.onSetSubagent(it)
        state.onClearDrill()
      },
      onBack = state.onClearDrill,
    )
  }
}
