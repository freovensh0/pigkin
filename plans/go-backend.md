# Go backend

The app now launches the Go host (`backend/cmd/pigkin-host`) from an APK asset through `HostLauncher`,
replacing the TypeScript host (`host/src/main.ts`) that ran inside Termux. The host is built on
[PiG](https://github.com/MichaelKinsy/PiG) (Pi in Go), a pure-Go port of the same Pi harness, pinned
to Pi 1.5.0 and MIT-licensed, so chats need no Node.js.

Today `backend/` holds that host:

- `backend/internal/host`: the JSONL framing, the connect-back connection (hello, first request,
  queued messages, hang-up), the Termux environment, and host errors with their `i18n` text.
- `backend/internal/modes`: the mode registry and the ported modes below; remote branches still
  stand in where the SSH side is unported.
- `backend/cmd/pigkin-host`: argument and environment checks, hello, request dispatch, result, exit
  codes 64 (usage), 2 (no request), 1 (hang-up before the request). It also runs standalone as PiG's
  hidden RPC process and as SSH's `askpass` program.

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

1. **Plumbing and file features** (done)
   - `utility` and its one-off operations: `sessions`, `session_messages`, `backups`,
     `save_attachments`, `delete_sessions`.
   - `files`, `git` and `browse` for local folders (places, listing, search).
   - `hello`, `login`, `logout`, `auth_status`, `provider` and `providers`, and the `remote_*` host,
     key and operation modes on PiG's `ai` model and credential runtime.
2. **Agent chats** (ported, remote branches remaining)
   - `rpc` on PiG's `coding` session and `agent` loop: streaming events, steering, subagent runs,
     session persistence in `PI_CODING_AGENT_DIR` in Pi's JSONL v3 format.
   - `terminal`, including raw byte piping after the first message, and `askpass` as a standalone
     program.
   - Standalone `login`, `remote_connect` and `remote_install`; the remote branches of `files`,
     `browse`, `git`, `rpc` and `terminal` still run on PiG's `env` package, replacing `host/src/remote`.
3. **Pi extensions** (remaining)
   PiG's extension host runs Pi's TypeScript extensions through a process-based SDK alongside Go,
   Rust, and Python ones, so the bundled extensions (`pigkin-subagents`, `pigkin-todo`,
   `pigkin-background`, `pigkin-remote`, `pigkin-web-access`) can be reused where PiG's SDK matches
   Pi's. Reuse means keeping Node on the phone for the extensions even though the agent is Go, so
   each extension still needs a pass: the fd-3 channel and every topic in
   `host/src/pi-extensions/pigkin-channel.ts` must exist under PiG, and `pigkin-web-access` (the
   largest) carries its own parity and license upkeep.
4. **Packaging and wiring** (done, device verification and cleanup remaining)
   - Bundle the cross-compiled `pigkin-host` as an APK asset, point `HostLauncher.HOST_ASSET` and
     `TermuxSetup` at it, and stop installing Node and the pinned Pi under `PIGKIN_ROOT`.
   - Release workflow: add the Go build, and keep `./gradlew check` first.
   - Verify the ported modes on a device: setup, chat, files, git, terminal and backups.
   - Remove the TypeScript host, its pnpm lockfile, and the Node runtime packages once nothing uses
     them.

## Known gaps

- `browse` search matches folder names by substring; Paseo's path and fuzzy ranking tiers are not
  ported yet (`shortcut:` in `backend/internal/modes/browse.go`).
- The remote branches of `files`, `browse`, `git`, `rpc` and `terminal` are unported; remote helpers
  stand in until the SSH side lands.
- The fd-3 extensions channel is not ported, so the TypeScript extensions do not run on the Go host
  yet.
- The ported modes are not yet verified on a device; that pass is still owed.
- The TypeScript host, its pnpm lockfile and the Node runtime packages are still shipped until the
  extensions channel is ported.
- The hello's `nodeVersion` carries the Go runtime version; the app shows it as the runtime version.
- PiG is a pre-stable 0.x port pinned to Pi 1.5.0, so parity gaps are tracked upstream and the
  dependency needs a pin-and-review step on each bump.
