package io.pigkin.ui.chat

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.pigkin.agent.AgentProcess
import io.pigkin.ui.subagents.SubagentsScreen
import io.pigkin.ui.subagents.rememberChatSubagents
import kotlinx.coroutines.launch
import org.json.JSONObject

/** A list that used to live in a sheet, now a screen over its chat. */
internal sealed interface ChatDrill {
  data class Model(val query: String) : ChatDrill

  data class Fork(val messages: List<JSONObject>) : ChatDrill

  data object Subagents : ChatDrill
}

@Composable
internal fun ChatDrillScreen(
  drill: ChatDrill?,
  controller: ChatController,
  onOpenSubagent: (String) -> Unit,
  onBack: () -> Unit,
) {
  val process = controller.process
  val timeline by process.screenTimeline.collectAsStateWithLifecycle()
  when (drill) {
    is ChatDrill.Model -> ModelScreen(
      controller,
      timeline.model,
      timeline.thinkingLevel,
      drill.query,
      onBack,
    )

    is ChatDrill.Fork -> ForkScreen(drill.messages, { controller.builtins.fork(it) }, onBack)

    ChatDrill.Subagents -> SubagentsScreen(
      subagents = rememberChatSubagents(process, timeline),
      question = timeline.pendingUi?.title,
      onOpen = {
        onOpenSubagent(it)
        onBack()
      },
      onStopAll = { controller.scope.launch { process.stopAllSubagents() } },
      onBack = onBack,
    )

    null -> Unit
  }
}

/** Sends a chat's sheet either to a screen or to a modal sheet. */
internal fun routeChatSheet(asked: ChatSheet, onDrill: (ChatDrill) -> Unit, onSheet: (ChatSheet) -> Unit) {
  when (asked) {
    is ChatSheet.Model -> onDrill(ChatDrill.Model(asked.query))
    is ChatSheet.Fork -> onDrill(ChatDrill.Fork(asked.messages))
    ChatSheet.Subagents -> onDrill(ChatDrill.Subagents)
    else -> onSheet(asked)
  }
}
