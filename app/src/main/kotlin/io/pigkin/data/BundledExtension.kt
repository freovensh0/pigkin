package io.pigkin.data

import androidx.annotation.StringRes
import io.pigkin.R

/**
 * The Pi extensions Pigkin bundles. Each one's source is the APK asset [asset] (`host/build.ts` bundles it), which the
 * host's `ensure` installs under [id]. [usesRuntimePackages] marks one that imports packages the bundled
 * `pi/package.json` installs, which `ensure` installs inside that runtime so Node finds them. One with a [switch] is
 * listed in Settings, which can turn it off; one without is part of how Pigkin works, so every chat's Pi gets it.
 */
internal enum class BundledExtension(
  val id: String,
  val switch: Switch?,
  val asset: String,
  val usesRuntimePackages: Boolean = false,
) {
  Todo(
    "pigkin-todo",
    Switch(R.string.settings_extension_todo, R.string.settings_extension_todo_summary),
    asset = "pigkin-extensions/pigkin-todo.js",
  ),

  /** Runs shell commands past the agent's turn; it tells the app over Pigkin's channel while they run. */
  Background(
    "pigkin-background",
    Switch(R.string.settings_extension_background, R.string.settings_extension_background_summary),
    asset = "pigkin-extensions/pigkin-background.js",
  ),

  /** pi-web-access, vendored in `host/src/pi-extensions/pigkin-web-access`. */
  WebAccess(
    "pigkin-web-access",
    Switch(R.string.settings_extension_web_access, R.string.settings_extension_web_access_summary),
    asset = "pigkin-extensions/pigkin-web-access.js",
    usesRuntimePackages = true,
  ),

  /** Hands the agent's tasks to subagents in the same Pi; it tells the app about their runs over Pigkin's channel. */
  Subagents(
    "pigkin-subagents",
    Switch(R.string.settings_extension_subagents, R.string.settings_extension_subagents_summary),
    asset = "pigkin-extensions/pigkin-subagents.js",
  ),

  /**
   * Runs Pi's file and shell tools on an SSH host. The host's `rpc` loads it only in a remote workspace, where the
   * chat can't work without it, so there's no switch.
   */
  Remote(
    "pigkin-remote",
    switch = null,
    asset = "pigkin-extensions/pigkin-remote.js",
  ),
  ;

  /** How Settings lists an extension the user can turn off. */
  class Switch(@StringRes val title: Int, @StringRes val summary: Int)
}
