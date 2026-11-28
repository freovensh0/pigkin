package io.pigkin.ui.archive

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedContent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.pigkin.R
import io.pigkin.agent.AgentProcessState
import io.pigkin.agent.ChatLocation
import io.pigkin.agent.ChatTimeline
import io.pigkin.agent.TimelineState
import io.pigkin.appGraph
import io.pigkin.host.HostRejected
import io.pigkin.host.SessionInfo
import io.pigkin.ui.ShellActions
import io.pigkin.ui.chat.ChatStream
import io.pigkin.ui.chat.ChatView
import io.pigkin.ui.chat.StreamSubagents
import io.pigkin.ui.chat.ToolCallSheet
import io.pigkin.ui.chat.TurnStatus
import io.pigkin.ui.components.HeaderBar
import io.pigkin.ui.components.HeaderButton
import io.pigkin.ui.components.LucideIcon
import io.pigkin.ui.components.PillButton
import io.pigkin.ui.components.SimmeringPot
import io.pigkin.ui.components.SnackbarStack
import io.pigkin.ui.components.SnackbarStackState
import io.pigkin.ui.sidebar.rememberFolderNames
import io.pigkin.ui.stackTransition
import io.pigkin.ui.subagents.ArchivedSubagentScreen
import io.pigkin.ui.subagents.ChatSubagents
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import io.pigkin.ui.workspace.sessionTitle
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONException

private sealed interface Transcript {
  data object Loading : Transcript

  data class Loaded(val state: TimelineState) : Transcript

  data class Failed(val message: String) : Transcript
}

/**
 * The archived chat in session file [path], read from its file without starting Pi: the chat's stream with no
 * composer. Restore takes it out of the archive and reopens it as a live chat through [onRestored]; Delete removes
 * its file. A subagent's tool row opens the subagent through [onOpenSubagent], with its tool call's id. Leaves on its
 * own once the chat is no longer archived.
 */
@Composable
internal fun ArchivedChatScreen(
  path: String,
  actions: ShellActions,
  onBack: () -> Unit,
  onRestored: (ChatLocation) -> Unit,
) {
  val graph = LocalContext.current.appGraph
  val archived by graph.sessionArchive.archived.collectAsStateWithLifecycle()
  val marks by graph.preferences.archivedSessions.collectAsStateWithLifecycle()
  val session = archived.firstOrNull { it.session.path == path }?.session
  val snackbar = remember { SnackbarStackState() }
  // One view per transcript, kept through a failed read and Retry so the scroll position survives.
  val view = remember(path) { ChatView() }
  var attempt by remember { mutableIntStateOf(0) }
  val transcript by rememberTranscript(path, attempt)
  var toolKey by remember { mutableStateOf<String?>(null) }
  var subagent by rememberSaveable { mutableStateOf<String?>(null) }
  BackHandler(enabled = subagent != null) { subagent = null }
  var confirmingDelete by remember { mutableStateOf(false) }
  var busy by remember { mutableStateOf(false) }
  LaunchedEffect(marks) { if (path !in marks) onBack() }
  AnimatedContent(
    targetState = subagent,
    transitionSpec = { stackTransition(from = archivedDepth(initialState), to = archivedDepth(targetState)) },
    label = "archived subagent",
  ) { shownSubagent ->
    if (shownSubagent != null) {
      ArchivedSubagentScreen(path, shownSubagent, onBack = { subagent = null })
    } else {
      ArchivedChatBody(
        session,
        transcript,
        busy,
        snackbar,
        actions,
        onBack,
        onRestored,
        ArchivedChatCallbacks(
          view = view,
          onRetry = { attempt += 1 },
          onTool = { toolKey = it },
          onSubagent = { subagent = it },
          onDelete = { confirmingDelete = true },
        ),
      )
    }
  }
  val tool = toolKey?.let { key -> (transcript as? Transcript.Loaded)?.state?.items?.firstOrNull { it.key == key } }
  // A transcript is settled history, so no tool call has arguments still streaming.
  tool?.let { ToolCallSheet(it, streamingArguments = { null }, onDismiss = { toolKey = null }) }
  if (confirmingDelete) {
    ArchivedDeleteDialog(
      path,
      actions,
      snackbar,
      session?.subagents ?: 0,
      onDismiss = { confirmingDelete = false },
      onBusy = { busy = it },
    )
  }
}

/** A subagent's depth over its archived chat. */
private fun archivedDepth(id: String?): Int = if (id == null) 0 else 1

/** The callbacks the archived chat's body needs from its screen. */
private data class ArchivedChatCallbacks(
  val view: ChatView,
  val onRetry: () -> Unit,
  val onTool: (String) -> Unit,
  val onSubagent: (String) -> Unit,
  val onDelete: () -> Unit,
)

@Composable
private fun ArchivedChatBody(
  session: SessionInfo?,
  transcript: Transcript,
  busy: Boolean,
  snackbar: SnackbarStackState,
  actions: ShellActions,
  onBack: () -> Unit,
  onRestored: (ChatLocation) -> Unit,
  callbacks: ArchivedChatCallbacks,
) {
  Column(modifier = Modifier.fillMaxSize().background(pigkinColors.surface0)) {
    TranscriptHeader(session, deleteEnabled = !busy, onBack, onDelete = callbacks.onDelete)
    Box(modifier = Modifier.weight(1f).fillMaxWidth()) {
      TranscriptBody(
        transcript,
        callbacks.view,
        onRetry = callbacks.onRetry,
        onOpenTool = callbacks.onTool,
        onOpenSubagent = callbacks.onSubagent,
      )
      SnackbarStack(snackbar, Modifier.align(Alignment.BottomCenter).padding(PigkinTokens.Spacing.s16))
    }
    ReadOnlyBar(
      session = session,
      enabled = !busy,
      onRestore = {
        val restored = session?.let { actions.restore(listOf(it)) }.orEmpty()
        restored.firstOrNull()?.let { onRestored(ChatLocation(it.cwd, it.path)) }
      },
    )
  }
}

/** The delete confirmation, which keeps working after this screen leaves the archive. */
@Composable
private fun ArchivedDeleteDialog(
  path: String,
  actions: ShellActions,
  snackbar: SnackbarStackState,
  subagents: Int,
  onDismiss: () -> Unit,
  onBusy: (Boolean) -> Unit,
) {
  val scope = rememberCoroutineScope()
  val context = LocalContext.current
  val genericError = stringResource(R.string.shell_error_generic)
  DeleteDialog(
    count = 1,
    onConfirm = {
      onDismiss()
      onBusy(true)
      val result = context.appGraph.scope.async { actions.deleteArchived(listOf(path)) }
      scope.launch {
        val failure = result.await() ?: return@launch
        onBusy(false)
        snackbar.show(failure.ifBlank { genericError })
      }
    },
    onDismiss = onDismiss,
    subagents = subagents,
  )
}

/** The chat's messages from its session file; a new [attempt] reads them again. */
@Composable
private fun rememberTranscript(path: String, attempt: Int): State<Transcript> {
  val repository = LocalContext.current.appGraph.sessionRepository
  val failedToLoad = stringResource(R.string.archive_transcript_failed)
  return produceState<Transcript>(Transcript.Loading, path, attempt) {
    value = Transcript.Loading
    value = try {
      val timeline = ChatTimeline()
      val messages = repository.messages(path)
      val saved = withContext(Dispatchers.IO) { repository.notices.load(path) }
      timeline.restoreNotices { saved }
      // Off the main thread: a long history takes a while to turn into rows.
      val state = withContext(Dispatchers.Default) {
        timeline.seed(messages)
        timeline.state.value.withStream()
      }
      Transcript.Loaded(state)
    } catch (error: IOException) {
      Transcript.Failed(error.message ?: failedToLoad)
    } catch (error: HostRejected) {
      Transcript.Failed(error.error)
    } catch (_: JSONException) {
      Transcript.Failed(failedToLoad)
    }
  }
}

/** Back, the chat's title over its folder (on its host, for a remote one), and Delete. */
@Composable
private fun TranscriptHeader(session: SessionInfo?, deleteEnabled: Boolean, onBack: () -> Unit, onDelete: () -> Unit) {
  val names = rememberFolderNames()
  HeaderBar(
    title = session?.let { sessionTitle(it) } ?: stringResource(R.string.archive_title),
    folder = session?.let { names.path(it.cwd) },
    folderIcon = if (session != null && names.isRemote(session.cwd)) {
      R.drawable.ic_lucide_server
    } else {
      R.drawable.ic_lucide_folder
    },
    leading = {
      HeaderButton(
        icon = R.drawable.ic_lucide_chevron_left,
        contentDescription = stringResource(R.string.archive_back),
        onClick = onBack,
      )
    },
    trailing = {
      HeaderButton(
        icon = R.drawable.ic_lucide_trash_2,
        contentDescription = stringResource(R.string.archive_delete),
        onClick = onDelete,
        enabled = deleteEnabled,
        tint = pigkinColors.destructive,
      )
    },
  )
}

@Composable
private fun TranscriptBody(
  transcript: Transcript,
  view: ChatView,
  onRetry: () -> Unit,
  onOpenTool: (String) -> Unit,
  onOpenSubagent: (String) -> Unit,
) {
  val colors = pigkinColors
  when (transcript) {
    Transcript.Loading -> Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { SimmeringPot() }

    is Transcript.Failed -> Column(
      modifier = Modifier.fillMaxSize().padding(PigkinTokens.Spacing.s24),
      verticalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s8, Alignment.CenterVertically),
      horizontalAlignment = Alignment.CenterHorizontally,
    ) {
      Text(text = transcript.message, color = colors.foregroundMuted, fontSize = PigkinTokens.FontSize.base)
      PillButton(label = stringResource(R.string.archive_retry), onClick = onRetry)
    }

    is Transcript.Loaded -> {
      if (transcript.state.items.isEmpty()) {
        Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
          Text(
            text = stringResource(R.string.archive_transcript_empty),
            color = colors.foregroundMuted,
            fontSize = PigkinTokens.FontSize.base,
          )
        }
        return
      }
      ChatStream(
        timeline = transcript.state,
        processState = AgentProcessState.Ready,
        view = view,
        onOpenTool = onOpenTool,
        onOpenProviders = {},
        onOpenOnboarding = {},
        onRestart = {},
        lastTurn = TurnStatus.None,
        // No Pi runs an archived chat, so every subagent it shows has finished, or was interrupted.
        subagents = remember(onOpenSubagent) { StreamSubagents(ChatSubagents.None, onOpenSubagent) },
      )
    }
  }
}

/**
 * In place of the composer: a card saying the chat is archived and read only, with Restore unless its folder is
 * gone.
 */
@Composable
private fun ReadOnlyBar(session: SessionInfo?, enabled: Boolean, onRestore: () -> Unit) {
  val colors = pigkinColors
  val restorable = session?.cwdExists == true
  Row(
    modifier = Modifier
      .fillMaxWidth()
      .background(colors.surface0)
      .navigationBarsPadding()
      .padding(horizontal = PigkinTokens.Spacing.s12, vertical = PigkinTokens.Spacing.s8)
      .clip(RoundedCornerShape(PigkinTokens.Radius.xxl))
      .background(colors.surface1)
      .padding(start = PigkinTokens.Spacing.s12, end = PigkinTokens.Spacing.s8, top = 10.dp, bottom = 10.dp),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s12),
  ) {
    Box(
      modifier = Modifier.size(32.dp).clip(CircleShape).background(colors.surface2),
      contentAlignment = Alignment.Center,
    ) {
      LucideIcon(icon = R.drawable.ic_lucide_archive, contentDescription = null, tint = colors.foregroundMuted)
    }
    Text(
      text = stringResource(
        if (session != null && !restorable) R.string.archive_read_only_missing else R.string.archive_read_only,
      ),
      modifier = Modifier.weight(1f),
      color = if (session != null && !restorable) colors.destructive else colors.foregroundMuted,
      fontSize = PigkinTokens.FontSize.meta,
    )
    PillButton(
      label = stringResource(R.string.archive_restore),
      onClick = onRestore,
      enabled = enabled && restorable,
      icon = R.drawable.ic_lucide_archive_restore,
    )
  }
}
