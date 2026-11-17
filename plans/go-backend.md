# Go backend

The app still starts the TypeScript host (`host/src/main.ts`) inside Termux. This plan replaces it
with a Go host built on [sky-valley/pi](https://github.com/sky-valley/pi), a pure-Go port of the same
Pi harness (`pi-ai`, `pi-agent-core`, `pi-coding-agent`), so the phone runs no Node.js.

Today `backend/` holds the start of that host and is not wired into the app:

- `backend/internal/host`: the JSONL framing, the connect-back connection (hello, first request,
  queued messages, hang-up), the Termux environment, and host errors with their `i18n` text.
- `backend/internal/modes`: the mode registry, `hello`, and `browse` for local folders (places,
  listing, search). Remote hosts are refused until the SSH side is ported.
- `backend/cmd/pigkin-host`: argument and environment checks, hello, request dispatch, result, exit
  codes 64 (usage), 2 (no request), 1 (hang-up before the request).

Checks:

```sh
cd backend
go vet ./...
go test ./...
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/pigkin-host
```

## Protocol

`pigkin-host <mode> <port>` dials `127.0.0.1:<port>` and writes one `pigkin_hello` with the token,
the mode, the Pi version, the runtime version, the config folder, home, and shared-storage access.
The app answers with one `pigkin_request {params}`; the host replies with one `pigkin_result` and
closes, unless the mode streams (`browse`, `utility`, `rpc`, `terminal`), in which case it keeps
answering lines until the app hangs up. Lines are LF-delimited JSON under 16 MiB. Long-lived modes
also carry `pigkin_channel` lines on fd 3 between the app and the bundled Pi extensions; that socket
has to be ported with the extensions, not with the protocol core.

## Phases

1. **Plumbing and file features** (in progress)
   - `utility` host and its one-off operations, starting with the ones that need no agent:
     `sessions`, `session_messages`, `backups`, `save_attachments`, `delete_sessions`.
   - `files`, `git`, `browse` in full, including remote folders over SSH.
   - `ensure`, `package_install`, `update_extensions`, `login`, `logout`, auth and provider modes on
     `ai`'s model/auth runtime; `ModelRuntime` replaces the Pi SDK import.
2. **Agent chats**
   - `rpc` on `coding.Session` and `agent.Agent`: streaming events, steering, subagent runs, session
     persistence at `PI_CODING_AGENT_DIR` in Pi's JSONL v3 format.
   - `terminal` and `askpass`, including raw byte piping after the first message.
   - `remote_*` on `env.ConnectSSH` / `RemoteExecutionEnv`, replacing `host/src/remote`.
3. **Pi extensions**
   The bundled extensions (`pigkin-subagents`, `pigkin-todo`, `pigkin-background`,
   `pigkin-remote`, `pigkin-web-access`) are TypeScript loaded by Pi's Node runtime. The Go host
   cannot load them; each has to become a Go tool or stay in a Node sidecar. `pigkin-web-access` is
   the largest and needs its own parity work and license upkeep. The fd-3 channel and every topic in
   `host/src/pi-extensions/pigkin-channel.ts` must be mirrored either way.
4. **Packaging and wiring**
   - Bundle the cross-compiled `pigkin-host` as an APK asset, point `HostLauncher.HOST_ASSET` and
     `TermuxSetup` at it, and stop installing Node and the pinned Pi under `PIGKIN_ROOT`.
   - Release workflow: add the Go build, and keep `./gradlew check` first.
   - Remove the TypeScript host, its pnpm lockfile, and the Node runtime packages once nothing uses
     them.

## Known gaps

- `browse` search matches folder names by substring; Paseo's path and fuzzy ranking tiers are not
  ported yet (`shortcut:` in `backend/internal/modes/browse.go`).
- Remote browsing, search, and every SSH-backed mode are unported.
- The hello's `nodeVersion` carries the Go runtime version; the app shows it as the runtime version.
- Nothing in `backend/` is reachable from the app yet, so the TypeScript host remains the only
  backend that runs.
