package io.pigkin.ui.chat

import androidx.annotation.DrawableRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import io.pigkin.R
import io.pigkin.ui.components.DialogConfirm
import io.pigkin.ui.components.DialogTextField
import io.pigkin.ui.components.PigkinDialog

/** A [PigkinDialog] with Cancel and a confirm button; back or a tap outside dismisses it. */
@Composable
internal fun ConfirmDialog(
  title: String,
  text: String?,
  confirmLabel: String,
  onConfirm: () -> Unit,
  onDismiss: () -> Unit,
  @DrawableRes icon: Int? = null,
  destructive: Boolean = false,
) {
  PigkinDialog(
    title = title,
    text = text,
    icon = icon,
    confirm = DialogConfirm(confirmLabel, onConfirm, destructive = destructive),
    dismissLabel = stringResource(R.string.chat_cancel),
    onDismiss = onDismiss,
  )
}

/** A dialog asking for one line, with the cursor after [initial] and the keyboard up; confirm is disabled while the text is blank. */
@Composable
internal fun TextInputDialog(
  title: String,
  initial: String,
  placeholder: String?,
  confirmLabel: String,
  onConfirm: (String) -> Unit,
  onDismiss: () -> Unit,
) {
  var value by rememberSaveable(initial, stateSaver = TextFieldValue.Saver) {
    mutableStateOf(TextFieldValue(initial, TextRange(initial.length)))
  }
  PigkinDialog(
    title = title,
    confirm = DialogConfirm(confirmLabel, { onConfirm(value.text) }, enabled = value.text.isNotBlank()),
    dismissLabel = stringResource(R.string.chat_cancel),
    onDismiss = onDismiss,
  ) {
    // In the dialog's own composition, so the field is attached when the effect runs.
    val focusRequester = remember { FocusRequester() }
    LaunchedEffect(Unit) { focusRequester.requestFocus() }
    DialogTextField(
      value = value,
      onValueChange = { value = it },
      modifier = Modifier.focusRequester(focusRequester),
      placeholder = placeholder,
    )
  }
}
