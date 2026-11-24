package io.pigkin.ui.chat

import androidx.annotation.StringRes
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.pigkin.R
import io.pigkin.ui.components.HeaderBar
import io.pigkin.ui.components.HeaderButton
import io.pigkin.ui.components.PigkinSheet
import io.pigkin.ui.sidebar.tildePath
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import java.text.NumberFormat
import java.util.Currency
import kotlin.math.roundToInt
import org.json.JSONObject

private const val SHEET_MAX_HEIGHT = 0.8f

/** `/session`: the session's name, file and id, message counts, tokens, context use and cost, as in Pi's terminal UI. */
@Composable
internal fun SessionInfoSheet(stats: JSONObject, name: String?, home: String?, onDismiss: () -> Unit) {
  val numbers = NumberFormat.getIntegerInstance()
  val count = { key: String -> numbers.format(stats.optLong(key)) }
  PigkinSheet(onDismissRequest = onDismiss, maxHeightFraction = SHEET_MAX_HEIGHT) {
    Column(modifier = Modifier.weight(1f, fill = false).verticalScroll(rememberScrollState())) {
      SectionTitle(R.string.session_info)
      name?.let { InfoRow(R.string.session_name, it) }
      val file = stats.opt("sessionFile") as? String
      InfoRow(
        R.string.session_file,
        file?.let { path -> home?.let { tildePath(path, it) } ?: path } ?: stringResource(R.string.session_in_memory),
      )
      InfoRow(R.string.session_id, stats.optString("sessionId"))
      SectionTitle(R.string.session_messages)
      InfoRow(R.string.session_total, count("totalMessages"))
      InfoRow(R.string.session_user, count("userMessages"))
      InfoRow(R.string.session_assistant, count("assistantMessages"))
      InfoRow(R.string.session_tool_calls, count("toolCalls"))
      InfoRow(R.string.session_tool_results, count("toolResults"))
      stats.optJSONObject("tokens")?.let { TokenRows(it, numbers) }
      stats.optJSONObject("contextUsage")?.let { ContextRow(it, numbers) }
      val cost = stats.optDouble("cost", 0.0)
      if (cost > 0) {
        SectionTitle(R.string.session_cost)
        InfoRow(R.string.session_total, formatCost(cost))
      }
    }
  }
}

/** A session's spend in USD to a tenth of a cent, written as the app's language writes amounts of US dollars. */
internal fun formatCost(usd: Double): String = NumberFormat.getCurrencyInstance().apply {
  currency = Currency.getInstance("USD")
  minimumFractionDigits = COST_DIGITS
  maximumFractionDigits = COST_DIGITS
}.format(usd)

private const val COST_DIGITS = 3

/** Input counts the whole prompt, cached or not, like Pi's terminal UI. */
@Composable
private fun TokenRows(tokens: JSONObject, numbers: NumberFormat) {
  val cacheRead = tokens.optLong("cacheRead")
  val input = tokens.optLong("input") + cacheRead + tokens.optLong("cacheWrite")
  SectionTitle(R.string.session_tokens)
  InfoRow(R.string.session_input, numbers.format(input))
  if (cacheRead > 0) InfoRow(R.string.session_cached, numbers.format(cacheRead))
  InfoRow(R.string.session_output, numbers.format(tokens.optLong("output")))
  InfoRow(R.string.session_total, numbers.format(tokens.optLong("total")))
}

/** Pi reports no context tokens right after compacting, until the next reply; then only the window shows. */
@Composable
private fun ContextRow(usage: JSONObject, numbers: NumberFormat) {
  val window = numbers.format(usage.optLong("contextWindow"))
  val value = if (usage.isNull("tokens")) {
    stringResource(R.string.session_context_unknown, window)
  } else {
    val percent = usage.optDouble("percent", 0.0).roundToInt()
    stringResource(R.string.session_context_value, numbers.format(usage.optLong("tokens")), window, percent)
  }
  InfoRow(R.string.session_context, value)
}

/** `/fork`: the user messages to fork from, newest first, on their own screen; picking one forks the chat. */
@Composable
internal fun ForkScreen(messages: List<JSONObject>, onFork: (entryId: String) -> Unit, onBack: () -> Unit) {
  val colors = pigkinColors
  Column(
    modifier = Modifier
      .fillMaxSize()
      .background(pigkinColors.surface0)
      .windowInsetsPadding(WindowInsets.safeDrawing),
  ) {
    HeaderBar(
      title = stringResource(R.string.chat_fork_title),
      leading = {
        HeaderButton(
          icon = R.drawable.ic_lucide_chevron_left,
          contentDescription = stringResource(R.string.settings_back),
          onClick = onBack,
        )
      },
    )
    LazyColumn(modifier = Modifier.weight(1f)) {
      items(messages.asReversed(), key = { it.optString("entryId") }) { message ->
        Text(
          text = message.optString("text").trim(),
          modifier = Modifier
            .fillMaxWidth()
            .clickable { onFork(message.optString("entryId")) }
            .padding(horizontal = PigkinTokens.Spacing.s16, vertical = PigkinTokens.Spacing.s12),
          color = colors.foreground,
          fontSize = PigkinTokens.FontSize.base,
          maxLines = 2,
          overflow = TextOverflow.Ellipsis,
        )
      }
    }
  }
}

@Composable
private fun SectionTitle(@StringRes text: Int) {
  Text(
    text = stringResource(text),
    modifier = Modifier.fillMaxWidth().padding(
      start = PigkinTokens.Spacing.s16,
      end = PigkinTokens.Spacing.s16,
      top = PigkinTokens.Spacing.s12,
      bottom = PigkinTokens.Spacing.s4,
    ),
    color = pigkinColors.foregroundMuted,
    fontSize = PigkinTokens.FontSize.sm,
  )
}

/** A label with its value right-aligned; long values such as the session file wrap instead of being cut. */
@Composable
private fun InfoRow(@StringRes label: Int, value: String) {
  val colors = pigkinColors
  Row(
    modifier = Modifier
      .fillMaxWidth()
      .heightIn(min = 36.dp)
      .padding(horizontal = PigkinTokens.Spacing.s16, vertical = PigkinTokens.Spacing.s8),
    verticalAlignment = Alignment.Top,
    horizontalArrangement = Arrangement.spacedBy(PigkinTokens.Spacing.s16),
  ) {
    Text(text = stringResource(label), color = colors.foregroundMuted, fontSize = PigkinTokens.FontSize.base)
    Text(
      text = value,
      modifier = Modifier.weight(1f),
      color = colors.foreground,
      fontSize = PigkinTokens.FontSize.base,
      textAlign = TextAlign.End,
    )
  }
}
