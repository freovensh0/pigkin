package io.pigkin.host

/** Wire constants shared with `host/src` (see `host.mjs` modes). */
internal object HostProtocol {
  const val VERSION = 1

  /** Images arrive base64-encoded inside `prompt`, so lines can be large. */
  const val MAX_LINE_BYTES = 16 * 1024 * 1024

  /** The hello comes from an unauthenticated peer; bound it far below [MAX_LINE_BYTES]. */
  const val MAX_HELLO_BYTES = 64 * 1024

  const val HELLO = "pigkin_hello"
  const val REQUEST = "pigkin_request"
  const val RESULT = "pigkin_result"
  const val PROGRESS = "pigkin_progress"
  const val EXIT = "pigkin_exit"

  /**
   * A line on Pigkin's channel between the app and the extensions it bundles, `{"type":…,"topic":…,"data":…}`. The
   * host routes the app's lines by their first bytes, so [type] must come first (see `host/src/channel.ts`).
   */
  const val CHANNEL = "pigkin_channel"
  const val AUTH_EVENT = "pigkin_auth_event"
  const val AUTH_PROMPT = "pigkin_auth_prompt"
  const val AUTH_RESPONSE = "pigkin_auth_response"
  const val AUTH_CANCEL = "pigkin_auth_cancel"
  const val AUTH_PROMPT_DISMISS = "pigkin_auth_prompt_dismiss"
  const val REMOTE_PROMPT = "pigkin_remote_prompt"
  const val REMOTE_ANSWER = "pigkin_remote_answer"
}
