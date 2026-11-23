package io.pigkin.ui.settings

/** Where each Settings row leads, kept together so the screen and its content stay small. */
internal data class SettingsNav(
  val onBack: () -> Unit,
  val onOpenProviders: () -> Unit,
  val onOpenRunningAgents: () -> Unit,
  val onOpenPermissions: () -> Unit,
  val onOpenRemoteHosts: () -> Unit,
  val onOpenBackups: () -> Unit,
  val onOpenLicenses: () -> Unit,
)
