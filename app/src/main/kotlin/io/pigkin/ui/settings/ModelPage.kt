package io.pigkin.ui.settings

import androidx.compose.runtime.Composable

/** A model list or editor shown over its provider page instead of a third destination. */
internal sealed interface ModelPage {
  data object Add : ModelPage

  data class Edit(val id: String) : ModelPage
}

/** Shows the model page over its provider; true when one was shown. */
@Composable
internal fun ModelPageScreen(page: ModelPage, form: ModelDraft, onBack: () -> Unit) {
  when (page) {
    ModelPage.Add -> AddModelsScreen(form, onBack)
    is ModelPage.Edit -> ModelEditorScreen(form, page.id, onBack)
  }
}
