package io.pigkin.ui.workspace

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyItemScope
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.PlatformTextStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.LineHeightStyle
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.pigkin.R
import io.pigkin.agent.AgentProcess
import io.pigkin.agent.ChatKey
import io.pigkin.appGraph
import io.pigkin.host.SessionInfo
import io.pigkin.ui.ShellActions
import io.pigkin.ui.chat.ConfirmDialog
import io.pigkin.ui.components.HeaderBar
import io.pigkin.ui.components.HeaderButton
import io.pigkin.ui.components.LucideIcon
import io.pigkin.ui.components.PigkinSheet
import io.pigkin.ui.components.SearchField
import io.pigkin.ui.components.SnackbarStackOverlay
import io.pigkin.ui.components.SnackbarStackState
import io.pigkin.ui.components.SwipeAway
import io.pigkin.ui.components.scrollFades
import io.pigkin.ui.sidebar.pathTextStyle
import io.pigkin.ui.sidebar.rememberFolderNames
import io.pigkin.ui.terminal.terminalTitle
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import io.pigkin.workspace.Pane
import io.pigkin.workspace.WorkspaceTab

/**
 * The tabs screen: the workspace's name and path over the tabs in the workspace folder [cwd] of what [pane] shows,
 * its terminals, its open files, its open commits and then its chats, the shown one highlighted, and under them what
 * the shown tab can do, from [shownTab]. Tapping a tab shows it in [pane] instead and closes the screen; tapping one
 * the other pane shows calls [onSwapPanes]. A chat is archived by swiping it right or from its actions, which also
 * renames it and, when [onShownInSplit] is given, opens it in split screen.
 */
@Composable
internal fun WorkspaceTabsScreen(
  pane: Pane,
  cwd: String,
  actions: ShellActions,
  snackbar: SnackbarStackState,
  shownTab: ShownTabActions,
  onShownInSplit: (() -> Unit)?,
  onSwapPanes: () -> Unit,
  onBack: () -> Unit,
) {
  val graph = LocalContext.current.appGraph
  val tabs = rememberWorkspaceTabs(pane, cwd)
  val names = rememberFolderNames()
  val archiving = remember(actions) { TabArchiving(actions) }
  val closing = remember(actions) { TabClosing(actions) }
  var menu by remember { mutableStateOf<TabMenu?>(null) }
  var query by rememberSaveable { mutableStateOf("") }
  LaunchedEffect(Unit) { graph.sessionRepository.refresh() }
  LaunchedEffect(tabs.chats) { archiving.forgetGone(tabs.chats) }
  // A short window, such as a phone on its side, has no room for the tiles under the tabs without squeezing the tabs
  // out, so there the tiles scroll at the end of the list instead.
  val pinActions = LocalWindowInfo.current.containerDpSize.height >= PINNED_ACTIONS_MIN_HEIGHT
  Box(
    modifier = Modifier
      .fillMaxSize()
      .background(pigkinColors.surface0)
      .windowInsetsPadding(WindowInsets.navigationBars)
      .imePadding(),
  ) {
    Column {
      HeaderBar(
        title = names.name(cwd),
        folder = names.path(cwd).takeIf { it != names.name(cwd) },
        leading = {
          HeaderButton(
            icon = R.drawable.ic_lucide_chevron_left,
            contentDescription = stringResource(R.string.settings_back),
            onClick = onBack,
          )
        },
      )
      TabsLabels()
      SearchField(query, { query = it }, stringResource(R.string.shell_tabs_search))
      TabList(
        tabs = tabs,
        archiving = archiving,
        closing = closing,
        query = query.trim(),
        callbacks = TabListCallbacks(
          onOpenChat = { session -> if (actions.openChat(pane, session.cwd, session.path)) onBack() },
          onShow = { tab -> if (!graph.tabs.show(pane, tab)) onSwapPanes() },
          onSwapPanes = {
            onSwapPanes()
            onBack()
          },
          onActions = { menu = it },
        ),
        footer = shownTab.current?.takeUnless { pinActions }?.let { { TabActionGrid(it) } },
        modifier = Modifier.weight(1f),
      )
      if (pinActions) shownTab.current?.let { TabActionGrid(it) }
      TabMenuOverlay(menu, actions, archiving, tabs, onShownInSplit, onBack) { menu = null }
    }
    SnackbarStackOverlay(
      snackbar,
      modifier = Modifier.align(Alignment.BottomCenter).padding(PigkinTokens.Spacing.s16),
    )
  }
  ConfirmArchive(archiving)
  ConfirmCloseTab(closing)
}

/** One layer of actions over the tabs screen; only chats no pane shows can open in split screen. */
@Composable
private fun TabMenuOverlay(
  menu: TabMenu?,
  actions: ShellActions,
  archiving: TabArchiving,
  tabs: WorkspaceTabs,
  onShownInSplit: (() -> Unit)?,
  onBack: () -> Unit,
  onDismiss: () -> Unit,
) {
  val shownMenu = menu ?: return
  val path = (shownMenu as? TabMenu.Chat)?.session?.path
  val openInSplit =
    onShownInSplit?.let { shown ->
      { session: SessionInfo ->
        if (actions.openChat(Pane.Secondary, session.cwd, session.path)) {
          shown()
          onBack()
        }
      }
    }?.takeIf { path != tabs.shown.chat && path != tabs.shown.elsewhereChat }
  TabMenuSheet(shownMenu, actions, archiving, openInSplit, onDismiss = onDismiss)
}

/** The labels over the tabs: what they are, and that a swipe closes one. */
@Composable
private fun TabsLabels() {
  Row(
    modifier = Modifier
      .fillMaxWidth()
      .padding(horizontal = 20.dp)
      .padding(bottom = PigkinTokens.Spacing.s6),
    verticalAlignment = Alignment.CenterVertically,
  ) {
    SectionLabel(stringResource(R.string.shell_tabs_label), Modifier.weight(1f))
    SectionLabel(stringResource(R.string.shell_tabs_swipe_hint))
  }
}

/** "Stop and archive" for a slid-out chat Pi is working in. */
@Composable
private fun ConfirmArchive(archiving: TabArchiving) {
  if (archiving.confirming == null) return
  ConfirmDialog(
    title = stringResource(R.string.archive_stop_title),
    text = stringResource(R.string.archive_stop_text),
    confirmLabel = stringResource(R.string.archive_stop_confirm),
    onConfirm = archiving::confirm,
    onDismiss = archiving::cancel,
    destructive = true,
    icon = R.drawable.ic_lucide_archive,
  )
}

/** The [CloseWarning] of a slid-out tab, such as a terminal that may have something running. */
@Composable
private fun ConfirmCloseTab(closing: TabClosing) {
  val warning = closing.confirming?.warning ?: return
  ConfirmDialog(
    title = stringResource(warning.title),
    text = stringResource(warning.text),
    confirmLabel = stringResource(warning.confirm),
    onConfirm = closing::confirm,
    onDismiss = closing::cancel,
    destructive = true,
    icon = warning.icon,
  )
}

/** A tab whose actions sheet is open. */
private sealed interface TabMenu {
  data class Chat(val session: SessionInfo) : TabMenu

  data class Tab(val tab: WorkspaceTab) : TabMenu
}

/**
 * [menu]'s actions sheet: a chat's renames or archives it, and opens it in split screen through [openInSplit] when
 * given; a tab's are its kind's, with its close.
 */
@Composable
private fun TabMenuSheet(
  menu: TabMenu,
  actions: ShellActions,
  archiving: TabArchiving,
  openInSplit: ((SessionInfo) -> Unit)?,
  onDismiss: () -> Unit,
) {
  when (menu) {
    is TabMenu.Chat -> SessionActions(
      session = menu.session,
      onDismiss = onDismiss,
      onRename = { actions.rename(menu.session, it) },
      onArchive = { archiving.archive(menu.session) },
      onOpenInSplit = openInSplit?.let { open -> { open(menu.session) } },
    )

    is TabMenu.Tab -> WorkspaceTabActionsSheet(menu.tab, onClose = {
      actions.closeTab(menu.tab)
    }, onDismiss = onDismiss)
  }
}

/** What the tab menu's rows do. */
private class TabListCallbacks(
  val onOpenChat: (SessionInfo) -> Unit,
  val onShow: (WorkspaceTab) -> Unit,
  val onSwapPanes: () -> Unit,
  val onActions: (TabMenu) -> Unit,
)

/**
 * The tab menu's rows: what the pane shows first, then the workspace's other open tabs, then its other saved chats,
 * then [footer] when given. What the pane shows is highlighted; opening the chat the other pane shows swaps the panes.
 * An edge with more rows past it fades into the sheet, so a list cut short reads as one that scrolls.
 */
@Composable
private fun TabList(
  tabs: WorkspaceTabs,
  query: String,
  archiving: TabArchiving,
  closing: TabClosing,
  callbacks: TabListCallbacks,
  footer: (@Composable () -> Unit)?,
  modifier: Modifier,
) {
  val graph = LocalContext.current.appGraph
  val processes by graph.registry.processes.collectAsStateWithLifecycle()
  val keptTitles by graph.sessionRepository.extensionTitles.bySessionFile.collectAsStateWithLifecycle()
  val keptStatuses by graph.sessionRepository.extensionStatuses.bySessionFile.collectAsStateWithLifecycle()
  val listState = rememberLazyListState()
  // The list keeps its first visible row in place, so a shown tab that changes while the sheet is open, as when the
  // shown terminal closes, would land just above the view; going back to the top keeps it in sight.
  val shownKey = tabs.shownChat?.path ?: tabs.shown.tab?.key
  LaunchedEffect(shownKey) { listState.requestScrollToItem(0) }
  val fadeTo = pigkinColors.surface1
  val chatRow: @Composable LazyItemScope.(SessionInfo) -> Unit = { session ->
    val elsewhere = session.path == tabs.shown.elsewhereChat
    SwipeAway(
      leaving = session.path in archiving.leaving,
      onSwiped = { archiving.archive(session) },
      onLeft = { archiving.onLeft(session) },
      containerColor = pigkinColors.surface1,
      modifier = Modifier.animateItem(),
    ) {
      val process = processes[ChatKey.session(session.path)]
      ChatTabRow(
        title = chatTitle(session, process, keptTitles[session.path]),
        modified = session.modified,
        statuses = chatStatuses(process, keptStatuses[session.path]),
        process = process,
        current = session.path == tabs.shown.chat,
        onOpen = { if (elsewhere) callbacks.onSwapPanes() else callbacks.onOpenChat(session) },
        onActions = { callbacks.onActions(TabMenu.Chat(session)) },
      )
    }
  }
  val matchingTabs = titledTabs(tabs.tabs)
    .filter { (_, title) -> title.contains(query, ignoreCase = true) }
    .map { (tab, _) -> tab }
  val shownChat = tabs.shownChat?.takeIf { it.matchesQuery(query) }
  val matchingChats = tabs.otherChats.filter { it.matchesQuery(query) }
  val hasResults = shownChat != null || matchingTabs.isNotEmpty() || matchingChats.isNotEmpty()
  val emptyMessage = emptyLine(tabs, hasResults)
  LazyColumn(modifier = modifier.scrollFades(listState, fadeTo), state = listState) {
    emptyMessage?.let { message ->
      item(key = EMPTY_KEY) {
        Text(
          text = stringResource(message),
          modifier = Modifier.fillMaxWidth().padding(PigkinTokens.Spacing.s16),
          color = pigkinColors.foregroundMuted,
          fontSize = PigkinTokens.FontSize.base,
        )
      }
    }
    shownChat?.let { shown -> item(key = shown.path) { chatRow(shown) } }
    tabRows(tabs, matchingTabs, closing, callbacks)
    items(matchingChats, key = { it.path }) { chatRow(it) }
    footer?.takeIf { query.isEmpty() }?.let { item(key = FOOTER_KEY) { it() } }
  }
}

/** [tabs] with the titles their rows show, so search also finds an unnamed terminal's "Terminal N". */
@Composable
private fun titledTabs(tabs: List<WorkspaceTab>): List<Pair<WorkspaceTab, String>> = tabs.map { tab ->
  val title = when (tab) {
    is WorkspaceTab.Terminal -> terminalTitle(tab.session)
    is WorkspaceTab.File -> tab.file.name
    is WorkspaceTab.Commit -> tab.commit.title
  }
  tab to title
}

/** The one empty-state line to show, or null when a row matches. */
private fun emptyLine(tabs: WorkspaceTabs, hasResults: Boolean): Int? = when {
  tabs.tabs.isEmpty() && tabs.chats.isEmpty() -> R.string.shell_tabs_empty
  !hasResults -> R.string.shell_tabs_no_match
  else -> null
}

/** Whether [this] chat's name or session path contains [query], ignoring case. */
private fun SessionInfo.matchesQuery(query: String): Boolean =
  name.orEmpty().contains(query, ignoreCase = true) || path.contains(query, ignoreCase = true)

/** The workspace's open tabs; one closes by swiping it right, as a chat archives. */
private fun LazyListScope.tabRows(
  tabs: WorkspaceTabs,
  matchingTabs: List<WorkspaceTab>,
  closing: TabClosing,
  callbacks: TabListCallbacks,
) {
  items(matchingTabs, key = { it.key }) { tab ->
    SwipeAway(
      leaving = tab.key in closing.leaving,
      onSwiped = { closing.close(tab) },
      onLeft = { closing.onLeft(tab) },
      containerColor = pigkinColors.surface1,
      modifier = Modifier.animateItem(),
      mark = R.drawable.ic_lucide_x,
    ) {
      WorkspaceTabRow(
        tab = tab,
        current = tab == tabs.shown.tab,
        onOpen = { callbacks.onShow(tab) },
        onActions = { callbacks.onActions(TabMenu.Tab(tab)) },
      )
    }
  }
}

/**
 * Closing tabs from the tab menu. A row first slides out; once it is gone the tab closes, or, when its kind gives a
 * [CloseWarning], the user is asked first and a cancelled row slides back.
 */
private class TabClosing(private val actions: ShellActions) {
  /** A slid-out tab and the warning the user is asked to confirm, kept while asking even if the tab calms down. */
  class Confirming(val tab: WorkspaceTab, val warning: CloseWarning)

  /** Keys of tabs whose rows are sliding out, or out and waiting for the user. */
  var leaving by mutableStateOf(emptySet<String>())
    private set

  var confirming by mutableStateOf<Confirming?>(null)
    private set

  fun close(tab: WorkspaceTab) {
    leaving += tab.key
  }

  fun onLeft(tab: WorkspaceTab) {
    val warning = tab.closeWarning()
    if (warning != null) confirming = Confirming(tab, warning) else remove(tab)
  }

  fun confirm() {
    confirming?.let { remove(it.tab) }
    confirming = null
  }

  fun cancel() {
    confirming?.let { leaving -= it.tab.key }
    confirming = null
  }

  private fun remove(tab: WorkspaceTab) {
    actions.closeTab(tab)
    leaving -= tab.key
  }
}

/**
 * Archiving from the tab menu. A row first slides out; once it is gone the chat is archived, or, when Pi is working in
 * it, the user is asked first and a cancelled row slides back.
 */
private class TabArchiving(private val actions: ShellActions) {
  /** Session files of rows sliding out, or out and waiting for the user. */
  var leaving by mutableStateOf(emptySet<String>())
    private set

  /** A slid-out chat Pi is working in, waiting for "Stop and archive". */
  var confirming by mutableStateOf<SessionInfo?>(null)
    private set

  fun archive(session: SessionInfo) {
    leaving += session.path
  }

  fun onLeft(session: SessionInfo) {
    if (actions.isWorking(session.path)) confirming = session else actions.archive(session)
  }

  fun confirm() {
    confirming?.let(actions::archive)
    confirming = null
  }

  fun cancel() {
    confirming?.let { leaving -= it.path }
    confirming = null
  }

  /** Forgets rows that left [chats], so a chat Undo brings back shows instead of staying slid out. */
  fun forgetGone(chats: List<SessionInfo>) {
    val listed = chats.mapTo(HashSet()) { it.path }
    leaving = leaving.filterTo(HashSet()) { it in listed }
  }
}

/** The window height under which the shown tab's tiles scroll at the end of the tabs rather than stay under them. */
private val PINNED_ACTIONS_MIN_HEIGHT = 600.dp

/** The list key of the tiles when they scroll at the end of the tabs. */
private const val FOOTER_KEY = "pigkin:tab-actions"

/** The list key of the one empty-state line. */
private const val EMPTY_KEY = "pigkin:tabs-empty"
