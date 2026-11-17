package io.pigkin.host

import android.app.PendingIntent
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import androidx.core.net.toUri
import io.pigkin.termux.TermuxIdentity
import io.pigkin.termux.TermuxInstall
import java.io.BufferedReader
import java.io.IOException
import java.io.InputStreamReader
import java.net.InetAddress
import java.net.ServerSocket
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.UUID
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/**
 * Starts one short-lived Go host in Termux and waits for it to connect back to [HostServer]: through Termux's
 * RUN_COMMAND service in the F-Droid build, through Pigkin's helper ([HelperBridge]) in the Play build. The bundled
 * binary is served to the bootstrap over loopback and installed under PIGKIN_ROOT; the token travels only on stdin.
 */
internal object HostLauncher {
  @Suppress("SdCardPath") // Termux's own prefix, not this app's storage.
  private const val BASH = "/data/data/com.termux/files/usr/bin/bash"
  private val SERVICE = ComponentName("com.termux", "com.termux.app.RunCommandService")
  private const val HOST_ASSET = "pigkin-host"
  private const val CONNECT_TIMEOUT_MILLIS = 30_000L
  private const val TOKEN_BYTES = 32
  private const val INTERRUPTED_RETRIES = 2
  private const val RETRY_DELAY_MILLIS = 300L

  private val random = SecureRandom()

  /** The binary and its SHA-256, read from the APK once and reused by every launch. */
  @Volatile
  private var hostBinary: Pair<String, ByteArray>? = null

  private fun binary(context: Context): Pair<String, ByteArray> = hostBinary ?: run {
    val bytes = context.assets.open(HOST_ASSET).use { it.readBytes() }
    val sha = MessageDigest.getInstance("SHA-256").digest(bytes).toHexString()
    (sha to bytes).also { hostBinary = it }
  }

  /**
   * Retries a launch that Termux cancelled while stopping its service (its last command just exited);
   * the retry starts a fresh Termux service. A host the helper's child was killed in is retried too.
   */
  suspend fun launch(context: Context, mode: String): HostChannel {
    repeat(INTERRUPTED_RETRIES) {
      try {
        return launchOnce(context, mode)
      } catch (_: HostFailure.Interrupted) {
        delay(RETRY_DELAY_MILLIS)
      }
    }
    return launchOnce(context, mode)
  }

  private suspend fun launchOnce(context: Context, mode: String): HostChannel = withContext(Dispatchers.IO) {
    // Checked on every launch, not only during onboarding: Termux can be swapped for another build after setup.
    val status = TermuxInstall.status(context)
    if (status == TermuxInstall.Status.NotInstalled) throw HostFailure.MissingTermux()
    if (!status.isTrusted) throw HostFailure.UntrustedTermux()
    TermuxIdentity.verify(context)
    val server = HostServer.shared
    val token = ByteArray(TOKEN_BYTES).also(random::nextBytes).toHexString()
    val (sha, bytes) = binary(context)
    val download = BinaryServer(bytes, token)
    val url = "http://127.0.0.1:${download.port}/pigkin-host/$token"
    val arguments = arguments(mode, server.port, sha, url)
    val stdin = token + "\n"
    val connected = server.register(token)
    val onFailure: (HostFailure) -> Unit = { connected.completeExceptionally(it) }
    var started: StartedHost? = null
    var channel: HostChannel? = null
    try {
      started = if (status.isPlay) {
        HelperBridge.start(context, mode, arguments, stdin, onFailure)
      } else {
        runCommand(context, mode, arguments, stdin, onFailure)
      }
      val result = withTimeoutOrNull(CONNECT_TIMEOUT_MILLIS) { connected.await() } ?: throw HostFailure.Timeout()
      channel = result
      result
    } finally {
      download.close()
      server.unregister(token)
      // Once connected, the channel reports the host's end; Termux's result only adds the host's diagnostics.
      started?.settle(connected = channel != null)
      // If the host matched in the same instant, it gets no request and exits on its own after 10 s.
      if (channel == null) connected.cancel()
    }
  }

  /** `bash` arguments for one host: [BOOTSTRAP], `<mode> <port>`, the binary's SHA-256 and its download URL. */
  private fun arguments(mode: String, port: Int, sha: String, url: String): List<String> =
    listOf("--noprofile", "--norc", "-c", BOOTSTRAP, "pigkin-host", mode, port.toString(), sha, url)

  /** Termux reports the command's exit to [HostResultReceiver] through a one-shot PendingIntent. */
  private fun runCommand(
    context: Context,
    mode: String,
    arguments: List<String>,
    stdin: String,
    onFailure: (HostFailure) -> Unit,
  ): StartedHost {
    val id = UUID.randomUUID().toString()
    HostResultReceiver.register(id, onFailure)
    var sent = false
    try {
      start(context, mode, arguments, stdin, resultIntent(context, id))
      sent = true
    } finally {
      if (!sent) HostResultReceiver.unregister(id)
    }
    return StartedHost { connected ->
      if (connected) HostResultReceiver.logExit(id, mode) else HostResultReceiver.unregister(id)
    }
  }

  private fun resultIntent(context: Context, id: String): PendingIntent = PendingIntent.getBroadcast(
    context,
    0,
    Intent(context, HostResultReceiver::class.java).setData("pigkin://termux-result/$id".toUri()),
    PendingIntent.FLAG_CANCEL_CURRENT or PendingIntent.FLAG_ONE_SHOT or PendingIntent.FLAG_MUTABLE,
  )

  private fun start(
    context: Context,
    mode: String,
    arguments: List<String>,
    stdin: String,
    resultIntent: PendingIntent,
  ) = send(
    context,
    Intent("com.termux.RUN_COMMAND").setComponent(SERVICE)
      .putExtra("com.termux.RUN_COMMAND_PATH", BASH)
      .putExtra("com.termux.RUN_COMMAND_ARGUMENTS", arguments.toTypedArray())
      .putExtra("com.termux.RUN_COMMAND_STDIN", stdin)
      .putExtra("com.termux.RUN_COMMAND_BACKGROUND", true)
      .putExtra("com.termux.RUN_COMMAND_BACKGROUND_CUSTOM_LOG_LEVEL", "0")
      .putExtra("com.termux.RUN_COMMAND_COMMAND_LABEL", "Pigkin: $mode")
      .putExtra("com.termux.RUN_COMMAND_PENDING_INTENT", resultIntent),
  )

  /**
   * Runs [command] in a new terminal session that Termux switches to, for commands that need Termux on screen, such as
   * its storage prompt. The caller opens Termux, since Android may not let Termux bring itself forward.
   */
  fun runInTerminal(context: Context, label: String, command: String) = send(
    context,
    Intent("com.termux.RUN_COMMAND").setComponent(SERVICE)
      .putExtra("com.termux.RUN_COMMAND_PATH", BASH)
      .putExtra("com.termux.RUN_COMMAND_ARGUMENTS", arrayOf("-c", command))
      .putExtra("com.termux.RUN_COMMAND_BACKGROUND", false)
      .putExtra("com.termux.RUN_COMMAND_COMMAND_LABEL", "Pigkin: $label"),
  )

  private fun send(context: Context, intent: Intent) {
    try {
      // A plain startService is refused while Termux is idle in the background. Termux's
      // RunCommandService calls startForeground() on every start, so this path is always allowed.
      if (context.startForegroundService(intent) == null) throw HostFailure.MissingTermux()
    } catch (_: SecurityException) {
      throw HostFailure.Permission()
    } catch (_: IllegalStateException) {
      throw HostFailure.Background()
    }
  }

  /**
   * `<mode> <port>` for the Go host, the binary's SHA-256 and its download URL; the token is the one stdin line.
   * Exit codes: 70 Termux env, 75 download, 76 checksum, 80 token missing.
   */
  private val BOOTSTRAP = $$$"""
    set -eu
    [ -n "${PREFIX:-}" ] && [ -n "${HOME:-}" ] || exit 70
    IFS= read -r PIGKIN_TOKEN || exit 80
    [ -n "$PIGKIN_TOKEN" ] || exit 80
    export PIGKIN_TOKEN
    exec </dev/null
    export PIGKIN_ROOT="$HOME/.local/share/$$${TermuxBuild.NAME}"
    export PI_CODING_AGENT_DIR="$HOME/.$$${TermuxBuild.NAME}/pi"
    mkdir -p "$PIGKIN_ROOT" "$PI_CODING_AGENT_DIR"
    mode=$1
    port=$2
    sha=$3
    url=$4
    host="$PIGKIN_ROOT/pigkin-host"
    # Skip the download when the installed binary is already the bundled one.
    if [ ! -x "$host" ] || [ "$(sha256sum "$host" | cut -d' ' -f1)" != "$sha" ]; then
      tmp="$host.$$.tmp"
      curl -fsS -m 300 "$url" -o "$tmp" || exit 75
      chmod +x "$tmp"
      [ "$(sha256sum "$tmp" | cut -d' ' -f1)" = "$sha" ] || exit 76
      mv -f "$tmp" "$host"
    fi
    exec "$host" "$mode" "$port"
  """.trimIndent()
}

/** Serves the bundled Go binary to the bootstrap over loopback for the length of one launch. */
private class BinaryServer(bytes: ByteArray, token: String) : AutoCloseable {
  private val socket = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))
  private val path = "/pigkin-host/$token"
  val port: Int = socket.localPort

  init {
    Thread {
      while (!socket.isClosed) {
        val client = try {
          socket.accept()
        } catch (_: IOException) {
          return@Thread
        }
        client.use { connection ->
          val reader = BufferedReader(InputStreamReader(connection.getInputStream(), Charsets.US_ASCII))
          val request = reader.readLine() ?: return@use
          var header = reader.readLine()
          while (header?.isNotEmpty() == true) header = reader.readLine()
          val ok = request.split(' ').getOrNull(1) == path
          val head = if (ok) {
            "HTTP/1.1 200 OK\r\nContent-Length: ${bytes.size}\r\n"
          } else {
            "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n"
          }
          val output = connection.getOutputStream()
          output.write((head + "Connection: close\r\n\r\n").toByteArray(Charsets.US_ASCII))
          if (ok) output.write(bytes)
          output.flush()
        }
      }
    }.apply { isDaemon = true }.start()
  }

  override fun close() {
    socket.close()
  }
}

/** A host command on its way; [settle] tells the bridge whether the host connected, once the launch is decided. */
internal fun interface StartedHost {
  fun settle(connected: Boolean)
}
