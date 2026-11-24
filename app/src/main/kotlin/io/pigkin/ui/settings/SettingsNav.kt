package io.pigkin.ui.settings

/** The one Settings page's subpages, shown over it instead of pushed as destinations. */
internal enum class SettingsSubpage { Permissions, RunningAgents, RemoteHosts, Backups, Licenses }

/** What Settings needs from outside: leaving it, and opening a remote host's editor. */
internal data class SettingsNav(val onBack: () -> Unit, val onOpenRemoteHost: (hostId: String?) -> Unit)
