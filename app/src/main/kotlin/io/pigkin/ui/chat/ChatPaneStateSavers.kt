package io.pigkin.ui.chat

import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.Saver
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject

/** Sheets and drills as `tag:payload` strings, so rotation keeps them without saving live JSON objects. */
internal val ChatSheetSaver = Saver<MutableState<ChatSheet?>, String>(
  save = { state ->
    when (val sheet = state.value) {
      null -> ""
      is ChatSheet.Model -> "model:${sheet.query}"
      ChatSheet.Rename -> "rename"
      ChatSheet.ConfirmReload -> "reload"
      ChatSheet.ConfirmArchive -> "archive"
      is ChatSheet.Tool -> "tool:${sheet.key}"
      is ChatSheet.SessionInfo -> "stats:${sheet.stats}"
      is ChatSheet.Widget -> "widget:${sheet.key}"
      ChatSheet.Subagents -> "subagents"
      is ChatSheet.Fork -> "fork:${JSONArray(sheet.messages)}"
    }
  },
  restore = { saved ->
    mutableStateOf(
      try {
        when {
          saved.isEmpty() -> null
          saved == "rename" -> ChatSheet.Rename
          saved == "reload" -> ChatSheet.ConfirmReload
          saved == "archive" -> ChatSheet.ConfirmArchive
          saved == "subagents" -> ChatSheet.Subagents
          saved.startsWith("model:") -> ChatSheet.Model(saved.removePrefix("model:"))
          saved.startsWith("tool:") -> ChatSheet.Tool(saved.removePrefix("tool:"))
          saved.startsWith("widget:") -> ChatSheet.Widget(saved.removePrefix("widget:"))
          saved.startsWith("stats:") -> ChatSheet.SessionInfo(JSONObject(saved.removePrefix("stats:")))
          saved.startsWith("fork:") -> ChatSheet.Fork(saved.removePrefix("fork:").savedMessages())
          else -> null
        }
      } catch (_: JSONException) {
        null
      },
    )
  },
)

internal val ChatDrillSaver = Saver<MutableState<ChatDrill?>, String>(
  save = { state ->
    when (val drill = state.value) {
      null -> ""
      is ChatDrill.Model -> "model:${drill.query}"
      is ChatDrill.Fork -> "fork:${JSONArray(drill.messages)}"
      ChatDrill.Subagents -> "subagents"
    }
  },
  restore = { saved ->
    mutableStateOf(
      try {
        when {
          saved.isEmpty() -> null
          saved == "subagents" -> ChatDrill.Subagents
          saved.startsWith("model:") -> ChatDrill.Model(saved.removePrefix("model:"))
          saved.startsWith("fork:") -> ChatDrill.Fork(saved.removePrefix("fork:").savedMessages())
          else -> null
        }
      } catch (_: JSONException) {
        null
      },
    )
  },
)

internal fun String.savedMessages(): List<JSONObject> {
  val array = JSONArray(this)
  return List(array.length()) { array.getJSONObject(it) }
}
