# Go backend

The app launches the Go host (`backend/cmd/pigkin-host`) from an APK asset through `HostLauncher`,
replacing the TypeScript host (`host/src/main.ts`) that ran inside Termux. The host is built on
[PiG](https://github.com/MichaelKinsy/PiG) (Pi in Go), a pure-Go port of the same Pi harness, pinned
to Pi 1.5.0 and MIT-licensed, so chats need no Node.js. The old TypeScript source tree stays in
`host/src` as the behavioral spec that `hostCheck` builds and tests; the app no longer installs or
launches its bundle.

Today `backend/` holds that host:

- `backend/internal/host`: the JSONL framing, the connect-back connection (hello, first request,
  queued messages, hang-up), the Termux environment, and host errors with their `i18n` text.
- `backend/internal/modes`: the mode registry and the ported modes listed below.
- `backend/cmd/pigkin-host`: argument and environment checks, hello, request dispatch, result, exit
  codes 64 (usage), 2 (no request), 1 (hang-up before the request). It also runs standalone as PiG's
  hidden RPC process and as SSH's `askpass` program.

The registered modes (`backend/internal/modes/modes.go`) are `hello`, `browse`, `files`, `login`,
`utility`, `git`, `rpc`, `terminal`, `remote_connect`, `remote_install`, `ensure`, `package_install`,
`update_extensions`, `backup_create` and `backup_restore`. Auth operations such as `logout` and
`auth_status` live under `utility`.

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
also carry `pigkin_channel` lines on fd 3 between the app and the bundled Pi extensions; that bridge
works today through Node Pi when the extensions are installed, and falls back to PiG-only otherwise.

## Phases

1. **Plumbing and file features** (done)
   - `utility` and its one-off operations: `sessions`, `session_messages`, `backups`,
     `save_attachments`, `delete_sessions`.
   - `files`, `git` and `browse` for local folders (places, listing, search), with their remote
     branches running over SSH.
   - `hello`, `login`, `logout`, `auth_status`, `provider` and `providers`, and the `remote_*` host,
     key and operation modes on PiG's `ai` model and credential runtime.
2. **Agent chats** (ported, verification remaining)
   - `rpc` on PiG's `coding` session and `agent` loop: streaming events, steering, subagent runs,
     session persistence in `PI_CODING_AGENT_DIR` in Pi's JSONL v3 format.
   - `terminal`, including raw byte piping after the first message, and `askpass` as a standalone
     program. Remote `rpc` and `terminal` run over SSH.
   - Standalone `login`, `remote_connect` and `remote_install`, plus setup and configuration backups
     (`ensure`, `package_install`, `update_extensions`, `backup_create`, `backup_restore`).
3. **Pi extensions** (wired; tool parity may remain)
   The fd-3 channel and bundled extension loading are wired. When the extensions are installed the
   host runs them through Node Pi; `ensure` still installs the pinned Node Pi for that path (restored
   after the PiG-only switch). Without them the host runs PiG-only. Each remote or native-only tool
   still needs a parity pass against Pi's extensions.
4. **Packaging and wiring** (done; verification remaining)
   - The cross-compiled `pigkin-host` ships as an APK asset; `HostLauncher.HOST_ASSET` and
     `TermuxSetup` point at it. The Go host's CI, license and release wiring are done.
   - Verify the ported modes on a device or emulator: setup, chat, files, git, terminal and backups.

## Known gaps

- `browse` search matches folder names by substring; Paseo's path and fuzzy ranking tiers are not
  ported yet (`shortcut:` in `backend/internal/modes/browse.go`).
- The remote branches of `files`, `browse`, `git`, `rpc` and `terminal` are ported over SSH but not
  verified against a real `sshd`; PiG-native tool gaps may still show up there.
- The ported modes are not yet verified on a device or emulator; that pass is still owed.
- The old TypeScript host source is kept as a spec and for `hostCheck`, but the app no longer ships
  or runs it; the bundled Pi extensions and helper still ship as APK assets.
- `ensure` installs the pinned Node Pi only for the extension path; with no extensions installed the
  host runs PiG-only and needs no Node.
- The hello's `nodeVersion` carries the Go runtime version; the app shows it as the runtime version.
- PiG is a pre-stable 0.x port pinned to Pi 1.5.0, so parity gaps are tracked upstream and the
  dependency needs a pin-and-review step on each bump.
