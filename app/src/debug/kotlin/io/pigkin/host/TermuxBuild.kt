package io.pigkin.host

/**
 * Pigkin Dev's names in Termux, apart from the release's (src/release) so both builds can share one Termux: Pi's
 * config and the helper under `~/.pigkin-dev`, the host under `~/.local/share/pigkin-dev`, `pigkin-dev-start`.
 */
internal object TermuxBuild {
  const val NAME = "pigkin-dev"

  /** The first of the five loopback ports the Play-Termux helper may listen on. */
  const val HELPER_FIRST_PORT = 47320

  /** The loopback port Pi reaches Pigkin's local models on, just past the helper's. */
  const val LOCAL_MODEL_PORT = 47325
}
