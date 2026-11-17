package io.pigkin.host

/**
 * The release's names in Termux, apart from Pigkin Dev's (src/debug) so both builds can share one Termux: Pi's config
 * and the helper under `~/.pigkin`, the host under `~/.local/share/pigkin`, `pigkin-start`.
 */
internal object TermuxBuild {
  const val NAME = "pigkin"

  /** The first of the five loopback ports the Play-Termux helper may listen on. */
  const val HELPER_FIRST_PORT = 47310

  /** The loopback port Pi reaches Pigkin's local models on, just past the helper's. */
  const val LOCAL_MODEL_PORT = 47315
}
