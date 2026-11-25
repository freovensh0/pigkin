package io.pigkin.ui.settings

import androidx.activity.compose.BackHandler
import androidx.compose.runtime.Composable

/** A model list or editor shown over its provider page instead of a third destination. */
internal sealed interface ModelPage {
  data object Add : ModelPage

  data class Edit(val id: String) : ModelPage
}

/** A plain String keeps the open model page through rotation and process death. */
internal fun ModelPage.toSavedKey(): String = when (this) {
  ModelPage.Add -> "add"
  is ModelPage.Edit -> "edit:$id"
}

internal fun String?.toModelPage(): ModelPage? = when (this) {
  null -> null
  "add" -> ModelPage.Add
  else -> takeIf { it.startsWith("edit:") }?.let { ModelPage.Edit(it.removePrefix("edit:")) }
}

/** Shows the model page over its provider; true when one was shown. */
@Composable
internal fun ModelPageScreen(page: ModelPage, form: ModelDraft, onBack: () -> Unit) {
  BackHandler { onBack() }
  when (page) {
    ModelPage.Add -> AddModelsScreen(form, onBack)
    is ModelPage.Edit -> ModelEditorScreen(form, page.id, onBack)
  }
}
