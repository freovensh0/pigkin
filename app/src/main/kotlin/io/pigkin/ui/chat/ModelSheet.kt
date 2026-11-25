package io.pigkin.ui.chat

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
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
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import io.pigkin.R
import io.pigkin.appGraph
import io.pigkin.ui.components.EvenRows
import io.pigkin.ui.components.HeaderBar
import io.pigkin.ui.components.HeaderButton
import io.pigkin.ui.components.LucideIcon
import io.pigkin.ui.components.SearchField
import io.pigkin.ui.components.SheetRow
import io.pigkin.ui.components.SimmeringPot
import io.pigkin.ui.theme.PigkinTokens
import io.pigkin.ui.theme.pigkinColors
import org.json.JSONObject

/** A thinking level's pill, and the space between pills. */
private val LEVEL_SHAPE = RoundedCornerShape(12.dp)
private val LEVEL_GAP = 6.dp

/** How long a pick takes to fill its pill or show its check. */
private const val PICK_MILLIS = 100

/** A tap's pick, kept while switching the model so a second tap doesn't start another switch. */
private class Pick(val model: JSONObject? = null, val level: String? = null)

/**
 * The model picker: a search field over the models grouped by provider, and under them, for a reasoning model, the
 * thinking levels Pi offers. Picking one switches Pi and returns to the chat.
 */
@Composable
internal fun ModelScreen(
  controller: ChatController,
  model: JSONObject?,
  thinkingLevel: String?,
  initialQuery: String,
  onBack: () -> Unit,
) {
  var models by remember { mutableStateOf<List<JSONObject>?>(null) }
  var query by rememberSaveable { mutableStateOf(initialQuery) }
  var pick by remember { mutableStateOf<Pick?>(null) }
  val accounts = LocalContext.current.appGraph.accountRepository
  val status by accounts.status.collectAsStateWithLifecycle()
  val refreshError by accounts.refreshError.collectAsStateWithLifecycle()
  val keyboard = LocalSoftwareKeyboardController.current
  // The picker opens over the chat, whose composer may still own the IME; hide it so the list gets the full window.
  LaunchedEffect(Unit) { keyboard?.hide() }
  LaunchedEffect(controller) { models = controller.query { availableModels() }.orEmpty() }
  LaunchedEffect(accounts) { accounts.refresh() }
  // Pi's RPC lists every model with credentials; settings.json `enabledModels` picks the ones shown.
  val scoped = remember(models, status, refreshError) {
    val scope = status?.scope
    when {
      models == null || (status == null && refreshError == null) -> null
      scope == null -> models
      else -> models?.filter { "${it.optString("provider")}/${it.optString("id")}" in scope }
    }
  }
  Column(
    modifier = Modifier
      .fillMaxSize()
      .background(pigkinColors.surface0)
      .windowInsetsPadding(WindowInsets.navigationBars)
      .imePadding(),
  ) {
    HeaderBar(
      title = stringResource(R.string.chat_model),
      leading = {
        HeaderButton(
          icon = R.drawable.ic_lucide_chevron_left,
          contentDescription = stringResource(R.string.settings_back),
          onClick = onBack,
        )
      },
    )
    SearchField(query, onQueryChange = { query = it }, placeholder = stringResource(R.string.chat_model_search))
    fun pickAndSwitch(picked: Pick, switch: () -> Unit) {
      if (pick != null) return
      pick = picked
      switch()
      onBack()
    }
    ModelList(scoped, query.trim(), pick?.model ?: model, Modifier.weight(1f)) { selected ->
      pickAndSwitch(Pick(model = selected)) {
        controller.setModel(selected.optString("provider"), selected.optString("id"))
      }
    }
    if (supportsThinking(model)) {
      ThinkingLevels(controller, model, pick?.level ?: thinkingLevel) { level ->
        pickAndSwitch(Pick(level = level)) { controller.setThinkingLevel(level) }
      }
    }
  }
}

/**
 * The reasoning-effort levels Pi offers for [model], as pills of one width with [current] filled in cobalt. As many
 * share a row as fit the widest name, so on a narrow screen or a large font they wrap instead of cutting it.
 */
@Composable
private fun ThinkingLevels(controller: ChatController, model: JSONObject?, current: String?, onPick: (String) -> Unit) {
  var levels by remember { mutableStateOf<List<String>?>(null) }
  // Pi clamps the available levels to the model, so they reload whenever it changes.
  LaunchedEffect(controller, model) { levels = controller.query { availableThinkingLevels() }.orEmpty() }
  val colors = pigkinColors
  SectionLabel(stringResource(R.string.chat_reasoning))
  val shown = levels
  if (shown == null) {
    Loading()
    return
  }
  EvenRows(
    gap = LEVEL_GAP,
    modifier = Modifier
      .fillMaxWidth()
      .padding(horizontal = PigkinTokens.Spacing.s12, vertical = PigkinTokens.Spacing.s4)
      .selectableGroup(),
  ) {
    shown.forEach { level ->
      val picked = level == current
      val fill by animateColorAsState(if (picked) colors.accent else colors.surface0, tween(PICK_MILLIS))
      val textColor by animateColorAsState(
        if (picked) colors.accentForeground else colors.foregroundMuted,
        tween(PICK_MILLIS),
      )
      Box(
        modifier = Modifier
          .heightIn(min = 36.dp)
          .clip(LEVEL_SHAPE)
          .background(fill)
          .selectable(selected = picked, role = Role.RadioButton, onClick = { onPick(level) })
          .padding(horizontal = PigkinTokens.Spacing.s12, vertical = PigkinTokens.Spacing.s8),
        contentAlignment = Alignment.Center,
      ) {
        Text(
          text = thinkingLevelLabel(level),
          color = textColor,
          fontSize = 12.sp,
          fontWeight = FontWeight.Bold,
          maxLines = 1,
        )
      }
    }
  }
}

/** Models matching [query], grouped by provider under a muted label; the current model is checked. */
@Composable
private fun ModelList(
  models: List<JSONObject>?,
  query: String,
  current: JSONObject?,
  modifier: Modifier,
  onSelect: (JSONObject) -> Unit,
) {
  if (models == null) {
    Loading()
    return
  }
  val groups = remember(models, query) {
    models.filter { matches(it, query) }.groupBy { it.optString("provider") }.toList()
  }
  if (groups.isEmpty()) {
    val empty = if (models.isEmpty()) R.string.chat_no_model else R.string.chat_model_search_empty
    SheetRow(label = stringResource(empty), labelColor = pigkinColors.foregroundMuted)
    return
  }
  LazyColumn(modifier = modifier.fillMaxWidth().padding(top = PigkinTokens.Spacing.s4)) {
    groups.forEach { (provider, providerModels) ->
      item(key = "provider:$provider") { SectionLabel(provider) }
      items(providerModels, key = { "model:$provider/${it.optString("id")}" }) { entry ->
        SheetRow(
          label = modelLabel(entry).orEmpty(),
          onClick = { onSelect(entry) },
          trailing = {
            AnimatedVisibility(
              visible = isSameModel(entry, current),
              enter = fadeIn(tween(PICK_MILLIS)) + scaleIn(tween(PICK_MILLIS)),
              exit = fadeOut(tween(PICK_MILLIS)),
            ) { CheckIcon() }
          },
        )
      }
    }
  }
}

/** Case-insensitive match on the model's name, id or provider. */
private fun matches(model: JSONObject, query: String): Boolean = query.isEmpty() ||
  listOf(modelLabel(model).orEmpty(), model.optString("id"), model.optString("provider"))
    .any { it.contains(query, ignoreCase = true) }

/** A muted heading over a group of the sheet's rows: a provider's models, or the thinking levels. */
@Composable
private fun SectionLabel(text: String) {
  Text(
    text = text,
    modifier = Modifier.fillMaxWidth().padding(PigkinTokens.Spacing.s16, PigkinTokens.Spacing.s8),
    color = pigkinColors.foregroundMuted,
    fontSize = PigkinTokens.FontSize.sm,
  )
}

@Composable
private fun Loading() {
  Box(modifier = Modifier.fillMaxWidth().height(44.dp), contentAlignment = Alignment.Center) { SimmeringPot() }
}

@Composable
private fun CheckIcon() {
  LucideIcon(icon = R.drawable.ic_lucide_check, contentDescription = null, tint = pigkinColors.accent)
}
