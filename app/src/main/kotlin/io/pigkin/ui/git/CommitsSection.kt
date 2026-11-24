package io.pigkin.ui.git

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.asPaddingValues
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.pigkin.R
import io.pigkin.host.GitCommit
import io.pigkin.host.RemoteFailure
import io.pigkin.ui.components.LucideIcon
import io.pigkin.ui.components.PillButton
import io.pigkin.ui.components.PillTone
import io.pigkin.ui.components.SearchField
import io.pigkin.ui.theme.PigkinFonts
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import io.pigkin.ui.workspace.relativeTime

/**
 * The Commits section, docked under the diff: a header that folds and unfolds it with the number of commits once
 * loaded, and while unfolded the commits ahead of the base, newest first, in a list up to [listMaxHeight] tall that
 * ends by saying so when the host listed only the first of them. A failure only connecting the remote host fixes
 * offers [onConnect].
 */
@Composable
internal fun CommitsSection(
  content: CommitsContent,
  collapsed: Boolean,
  listMaxHeight: Dp,
  onToggle: () -> Unit,
  onOpenCommit: (GitCommit) -> Unit,
  onConnect: (() -> Unit)?,
) {
  val colors = pigkinColors
  val navigationBar = WindowInsets.navigationBars.asPaddingValues()
  Column(modifier = Modifier.fillMaxWidth().padding(bottom = navigationBar.calculateBottomPadding())) {
    HorizontalDivider(thickness = 1.dp, color = colors.border)
    Row(
      modifier = Modifier
        .fillMaxWidth()
        .clickable(onClick = onToggle)
        .heightIn(min = PigkinTokens.minTouchTarget)
        .padding(horizontal = PigkinTokens.Spacing.s16, vertical = PigkinTokens.Spacing.s8),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s8),
    ) {
      LucideIcon(
        icon = R.drawable.ic_lucide_chevron_right,
        contentDescription = null,
        modifier = Modifier.rotate(if (collapsed) 0f else 90f),
        size = PigkinTokens.IconSize.sm,
        tint = colors.foregroundMuted,
      )
      Text(
        text = stringResource(R.string.git_commits),
        color = colors.foreground,
        fontSize = PigkinTokens.FontSize.base,
        fontWeight = FontWeight.Bold,
      )
      if (content is CommitsContent.Loaded) CommitCount(content.commits.commits.size)
    }
    if (!collapsed) CommitsContentRows(content, listMaxHeight, onOpenCommit, onConnect)
  }
}

/** The number of commits as a small accent badge, read out in words. */
@Composable
private fun CommitCount(count: Int) {
  val colors = pigkinColors
  val description = pluralStringResource(R.plurals.git_commits_count, count, count)
  Text(
    text = count.toString(),
    modifier = Modifier
      .semantics { contentDescription = description }
      .background(colors.accentSoft, RoundedCornerShape(4.dp))
      .padding(horizontal = PigkinTokens.Spacing.s6, vertical = PigkinTokens.Spacing.s2),
    color = colors.foreground,
    fontSize = 12.sp,
    fontWeight = FontWeight.Bold,
    style = LocalTextStyle.current.copy(fontFeatureSettings = PigkinFonts.TABULAR),
    maxLines = 1,
  )
}

@Composable
private fun CommitsContentRows(
  content: CommitsContent,
  listMaxHeight: Dp,
  onOpenCommit: (GitCommit) -> Unit,
  onConnect: (() -> Unit)?,
) {
  val colors = pigkinColors
  var query by rememberSaveable { mutableStateOf("") }
  val notePadding = Modifier.padding(
    start = PigkinTokens.Spacing.s16,
    end = PigkinTokens.Spacing.s16,
    top = PigkinTokens.Spacing.s4,
    bottom = PigkinTokens.Spacing.s12,
  )
  when (content) {
    CommitsContent.Loading -> CommitsNote(
      stringResource(R.string.git_commits_loading),
      colors.foregroundMuted,
      notePadding,
    )

    is CommitsContent.Failed -> CommitsFailed(content.remote, onConnect, notePadding)

    is CommitsContent.Loaded -> {
      val commits = content.commits.commits
      if (commits.isEmpty()) {
        val base = branchLabel(content.commits.baseRef) ?: stringResource(R.string.git_base)
        CommitsNote(stringResource(R.string.git_commits_none_ahead, base), colors.foregroundMuted, notePadding)
        return
      }
      SearchField(
        query = query,
        onQueryChange = { query = it },
        placeholder = stringResource(R.string.git_commits_search),
      )
      val shown = commits.filter {
        it.subject.contains(query, ignoreCase = true) || it.sha.startsWith(query, ignoreCase = true)
      }
      if (shown.isEmpty()) {
        CommitsNote(stringResource(R.string.git_commits_no_match), colors.foregroundMuted, notePadding)
        return
      }
      LazyColumn(
        modifier = Modifier.fillMaxWidth().heightIn(max = listMaxHeight),
        contentPadding = PaddingValues(bottom = PigkinTokens.Spacing.s8),
      ) {
        itemsIndexed(shown, key = { _, commit -> commit.sha }) { index, commit ->
          CommitRow(commit, isFirst = index == 0, isLast = index == shown.lastIndex, onOpen = {
            onOpenCommit(commit)
          })
        }
        val total = content.commits.total
        if (total > commits.size) {
          item(key = TRUNCATED_KEY) {
            CommitsNote(
              pluralStringResource(R.plurals.git_commits_truncated, total, commits.size, total),
              colors.foregroundMuted,
              notePadding,
            )
          }
        }
      }
    }
  }
}

@Composable
private fun CommitsNote(text: String, color: Color, modifier: Modifier) {
  Text(text = text, modifier = modifier, color = color, fontSize = PigkinTokens.FontSize.meta)
}

/**
 * Why the commits didn't load: the remote host's reason when it couldn't be reached, with Connect when only connecting
 * fixes it.
 */
@Composable
private fun CommitsFailed(remote: RemoteFailure?, onConnect: (() -> Unit)?, modifier: Modifier) {
  val colors = pigkinColors
  val text = remote?.text?.localized() ?: stringResource(R.string.git_commits_failed)
  val connect = onConnect?.takeIf { remote?.needsConnect == true }
  if (connect == null) {
    CommitsNote(text, colors.dotDanger, modifier)
    return
  }
  Row(
    modifier = modifier.fillMaxWidth(),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s12),
  ) {
    Text(text = text, modifier = Modifier.weight(1f), color = colors.dotDanger, fontSize = PigkinTokens.FontSize.meta)
    PillButton(label = stringResource(R.string.remote_connect), onClick = connect, tone = PillTone.Tonal)
  }
}

/** A commit row: the graph node, the short SHA, the subject, how long ago it was written, a chevron. */
@Composable
private fun CommitRow(commit: GitCommit, isFirst: Boolean, isLast: Boolean, onOpen: () -> Unit) {
  val colors = pigkinColors
  Row(
    modifier = Modifier
      .fillMaxWidth()
      .height(IntrinsicSize.Min)
      .padding(horizontal = PigkinTokens.Spacing.s4)
      // Rounded less than the rail is inset, so the clip never bites into the rail where rows meet.
      .clip(RoundedCornerShape(PigkinTokens.Radius.lg))
      .clickable(onClick = onOpen)
      .padding(horizontal = PigkinTokens.Spacing.s12),
    verticalAlignment = Alignment.CenterVertically,
    horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s8),
  ) {
    CommitGraphNode(isFirst, isLast)
    Row(
      modifier = Modifier.weight(1f).padding(vertical = PigkinTokens.Spacing.s8),
      verticalAlignment = Alignment.CenterVertically,
      horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s4),
    ) {
      Text(
        text = commit.shortSha,
        modifier = Modifier.width(70.dp),
        color = colors.foregroundMuted,
        fontFamily = PigkinFonts.mono,
        fontSize = PigkinTokens.FontSize.sm,
        maxLines = 1,
      )
      Text(
        text = commit.subject,
        modifier = Modifier.weight(1f),
        color = colors.foreground,
        fontSize = PigkinTokens.FontSize.base,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
      )
    }
    Text(
      text = relativeTime(commit.authorTime * 1_000),
      color = colors.foregroundMuted,
      fontSize = PigkinTokens.FontSize.sm,
      maxLines = 1,
    )
    LucideIcon(
      icon = R.drawable.ic_lucide_chevron_right,
      contentDescription = null,
      size = PigkinTokens.IconSize.sm,
      tint = colors.foregroundExtraMuted,
    )
  }
}

/**
 * The commit graph node: an accent dot on an accent rail that joins it to the rows above and below, starting at the
 * first commit's dot and ending at the last one's.
 */
@Composable
private fun CommitGraphNode(isFirst: Boolean, isLast: Boolean) {
  val accent = pigkinColors.accent
  Canvas(modifier = Modifier.width(GRAPH_MARKER_SIZE).fillMaxHeight()) {
    val center = Offset(size.width / 2, size.height / 2)
    if (!(isFirst && isLast)) {
      drawLine(
        color = accent,
        start = Offset(center.x, if (isFirst) center.y else 0f),
        end = Offset(center.x, if (isLast) center.y else size.height),
        strokeWidth = GRAPH_RAIL_WIDTH.toPx(),
      )
    }
    drawCircle(color = accent, radius = GRAPH_MARKER_SIZE.toPx() / 2, center = center)
  }
}

/** The lazy-list key of the row saying the host listed only the first commits, which can't clash with a SHA. */
private const val TRUNCATED_KEY = "/truncated"

private val GRAPH_MARKER_SIZE = 8.dp
private val GRAPH_RAIL_WIDTH = 2.dp
