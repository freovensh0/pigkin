package io.pigkin.ui.auth

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import io.pigkin.data.AccountRepository
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** One sign-in attempt's flow, kept in the activity's ViewModel store so rotation doesn't restart it. */
internal class LoginViewModel : ViewModel() {
  var flow by mutableStateOf(LoginFlowState())
    private set
  var offerClaudePlugin by mutableStateOf(false)
    private set
  var finished by mutableStateOf(false)
    private set

  private var job: Job? = null
  private var startedFlow: LoginFlowState? = null

  /** Starts the attempt once, even when the sheet recomposes after rotation. */
  fun start(accounts: AccountRepository, providerId: String) {
    if (startedFlow === flow) return
    startedFlow = flow
    job = viewModelScope.launch {
      flow.run(accounts, providerId)
      if (flow.outcome != LoginOutcome.SignedIn) return@launch
      // Signing in refreshed the status, so it shows whether Pi already installs the plugin.
      val installed = accounts.status.value?.npmPackages.orEmpty()
      if (providerId == CLAUDE_PROVIDER && CLAUDE_PLUGIN_PACKAGE !in installed) {
        offerClaudePlugin = true
        return@launch
      }
      delay(1_000)
      finished = true
    }
  }

  /** Cancels the attempt and clears its state, also when the sheet is dismissed. */
  fun reset() {
    job?.cancel()
    job = null
    startedFlow = null
    flow = LoginFlowState()
    offerClaudePlugin = false
    finished = false
  }

  override fun onCleared() {
    job?.cancel()
  }
}
