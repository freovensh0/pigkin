package io.pigkin.ui.sidebar

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.pigkin.R
import io.pigkin.appGraph
import io.pigkin.data.sameFolder
import io.pigkin.ui.components.HeaderBar
import io.pigkin.ui.components.HeaderButton
import io.pigkin.ui.theme.pigkinColors

/**
 * Picks a folder to add as a sidebar workspace with [FolderBrowserPane], on the machine last browsed. Folders [isWorkspace]
 * already accepts can't be added again. Add host and Edit host push the host's editor, which returns here with that host
 * chosen; Add host for null adds a new one.
 */
@Composable
internal fun AddWorkspaceScreen(
  onAdd: (cwd: String) -> Unit,
  onOpenHost: (hostId: String?) -> Unit,
  onBack: () -> Unit,
) {
  val workspaces by LocalContext.current.appGraph.preferences.workspaces.collectAsStateWithLifecycle()
  Column(modifier = Modifier.fillMaxSize().background(pigkinColors.surface0)) {
    HeaderBar(
      title = stringResource(R.string.shell_add_workspace),
      leading = {
        HeaderButton(
          icon = R.drawable.ic_lucide_chevron_left,
          contentDescription = stringResource(R.string.settings_back),
          onClick = onBack,
        )
      },
    )
    FolderBrowserPane(
      isWorkspace = { cwd -> workspaces.any { sameFolder(it, cwd) } },
      onOpenHost = onOpenHost,
      modifier = Modifier.weight(1f).windowInsetsPadding(WindowInsets.navigationBars).imePadding(),
      onChoose = onAdd,
    )
  }
}
