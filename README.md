<p align="center">
  <img src="site/public/favicon.svg" alt="Pigkin logo" width="128" height="128">
</p>

<h1 align="center">Pigkin</h1>

<p align="center">
  <a href="https://github.com/freovensh0/pigkin/actions/workflows/release.yml"><img src="https://github.com/freovensh0/pigkin/actions/workflows/release.yml/badge.svg" alt="Release"></a>
  <a href="https://github.com/freovensh0/pigkin/releases/latest"><img src="https://img.shields.io/github/v/release/freovensh0/pigkin" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/freovensh0/pigkin" alt="License"></a>
</p>

<p align="center">
  <a href="https://pipkin.jeph.io">Website</a> · <a href="https://pipkin.jeph.io/docs/">Docs</a>
</p>

**Pigkin** is a native Android app for agentic coding. Tell it what to build or fix, and it reads your
code, runs commands, edits files, and checks its own work, all on your phone. It's built on the
[Pi](https://pi.dev) agent harness and runs in [Termux](https://termux.dev), so there's no laptop or
cloud server doing the work somewhere else.

It is a fork of [jeph/pipkin](https://github.com/jeph/pipkin). The Termux backend is being ported to
Go on top of [PiG](https://github.com/MichaelKinsy/PiG) (Pi in Go); the app is unchanged.

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="site/src/assets/screens/stone/photos.png">
    <img src="site/src/assets/screens/porcelain/photos.png" alt="A finished chat in which Pigkin found and moved duplicate photos" height="320">
  </picture>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="site/src/assets/screens/stone/git-changes.png">
    <img src="site/src/assets/screens/porcelain/git-changes.png" alt="The Changes view showing an uncommitted diff" height="320">
  </picture>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="site/src/assets/screens/stone/file-view.png">
    <img src="site/src/assets/screens/porcelain/file-view.png" alt="A Python file open with syntax highlighting" height="320">
  </picture>
</p>

## Contents

- [Features](#features)
- [Requirements](#requirements)
- [Installation](#installation)
- [Usage](#usage)
- [Development](#development)
- [Contributing](#contributing)
- [Acknowledgements](#acknowledgements)
- [License](#license)

## Features

Most agents you use from a phone are remotes for a computer somewhere else. Pigkin runs the agent,
your code, and every command it runs on the phone itself:

- **No computer required.** The agent, your files, git, and terminals all live in Termux on your
  phone. Install compilers, runtimes, and databases with `pkg`, or run Debian or Ubuntu with
  `proot-distro`, no root required.
- **Built on Pi.** Pigkin is compatible with Pi's ecosystem of skills, tools, prompt templates, and
  extensions. Install extensions the usual Pi way and Pigkin picks them up.
- **Native extension UI.** Extensions that use Pi's RPC UI protocol render with native controls:
  choices, confirmations, and text fields appear as cards above the composer, and status lines,
  notices, and widgets each get a home.
- **Android native.** Pigkin is written in Jetpack Compose with no web view, so scrolling, typing, and
  streaming replies stay smooth in long chats. On foldables and tablets, split panes put chats,
  files, diffs, and terminals side by side.
- **A real workbench.** Review changes in the built-in git diff viewer, browse and read files with
  tree-sitter syntax highlighting for more than twenty languages, look back through commits, and
  open terminals without leaving the app.
- **Your model, your plan.** Sign in with a Claude Pro or Max subscription or an OpenAI Codex
  subscription, add a Gemini, Z.ai Coding Plan, Kimi Code or OpenRouter API key, or add any OpenAI-compatible
  API, including hosted gateways and servers you run yourself. Add several and switch per chat.
- **Runs in the background.** Lock your phone or switch apps; a notification tells you when the chat
  is done or needs your answer. While you're in Pigkin, a small pill under the header tells you the
  same about your other chats.
- **Guided setup.** A step-by-step guide sets up Termux and installs Pi for you, explaining each
  permission it asks for.
- **Bundled extensions.** Pigkin ships web search and page fetching (a vendored copy of
  [pi-web-access](https://github.com/nicobailon/pi-web-access)) and a to-do list widget.
- **Backups.** Save your settings, sign-ins, models, and extensions in one tap and restore them
  later.

## Requirements

- Android 10 or newer on a 64-bit (arm64 or x86_64) device.
- [Termux](https://termux.dev) from [F-Droid](https://f-droid.org/packages/com.termux/) (0.118 or
  newer, recommended) or [Google Play](https://play.google.com/store/apps/details?id=com.termux)
  (Android 11 or newer).
- A network connection while a chat is working. The agent runs on your phone, but the model runs at
  the provider you connect.

## Installation

### Download a release

[Download the latest APK](https://github.com/freovensh0/pigkin/releases/latest) from the releases page on
your phone and open it to install. Android asks you to allow installs from your browser or file
manager the first time. Later releases install over it as updates and keep your chats and settings.

Each release also lists the APK's SHA-256 checksum in `SHA256SUMS`, and you can confirm that GitHub
Actions built it from this repository with the [GitHub CLI](https://cli.github.com):

```sh
sha256sum --check SHA256SUMS
gh attestation verify pigkin-v<VERSION>.apk --repo freovensh0/pigkin
```

### Build from source

The build needs a JDK, the Android SDK, and Node.js and pnpm on your `PATH`; see
[Development](#development) for the exact toolchain.

```sh
git clone https://github.com/freovensh0/pigkin.git
cd pigkin
./gradlew :app:installDebug
```

`installDebug` installs on the device or emulator that `adb` is connected to. To build an APK
without installing it, run `./gradlew :app:assembleDebug` and find it in
`app/build/outputs/apk/debug/`.

A build from source is Pigkin Dev, a separate app that installs beside a downloaded release rather
than over it. Both can use the same Termux: Pigkin Dev keeps its own state there, in `~/.pigkin-dev`
and `~/.local/share/pigkin-dev` instead of the release's `~/.pigkin` and `~/.local/share/pigkin`, so
it needs its own setup and sign-ins, and the two don't see each other's chats.

## Usage

The [Pigkin guide](https://pipkin.jeph.io/docs/) covers using the app:

- [Get started](https://pipkin.jeph.io/docs/get-started/): install Termux, walk through setup, and
  start your first chat.
- [Connect a model](https://pipkin.jeph.io/docs/providers/): sign in with a Claude or OpenAI
  subscription, add an API key or an OpenAI-compatible API, and choose models per chat.
- [Configure the agent](https://pipkin.jeph.io/docs/configure/): edit Pi's configuration, give the
  agent standing instructions, and use backups.
- [Extensions](https://pipkin.jeph.io/docs/extensions/): install Pi packages, write your own
  extensions, and add skills and prompt templates.
- [Run in the background](https://pipkin.jeph.io/docs/background/): keep chats and terminal commands
  running when you leave the app or lock the screen.

## Development

Building Pigkin needs:

- A JDK 17 or newer, through `JAVA_HOME` or `java` on your `PATH`, to start Gradle.
- The Android SDK with API level 37, NDK `30.0.16248370`, and CMake `4.1.2`. Point Gradle at the SDK
  with `ANDROID_HOME` or `sdk.dir` in `local.properties`.
- Node.js 22.19 or newer and [pnpm](https://pnpm.io), which build the host bundle.
- A network connection on the first build, which downloads the pinned tree-sitter grammars and the
  JDKs Gradle provisions for itself.

Run every check, including ktlint, detekt, Android lint, and the host's type check, lint, and
tests:

```sh
./gradlew check
```

Run only the host's checks:

```sh
cd host
pnpm install --frozen-lockfile
pnpm run check
```

Run the Go backend's checks (it is not wired into the app yet; see
[plans/go-backend.md](plans/go-backend.md)):

```sh
cd backend
go test ./...
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/pigkin-host
```

Run the website locally:

```sh
cd site
pnpm install --frozen-lockfile
pnpm run dev
```

## Contributing

Pull requests are welcome. If you run into a bug, unexpected behavior, or a device-specific
problem, please [open an issue](https://github.com/freovensh0/pigkin/issues) with your phone model,
Android version, and Termux build. Reports from real-world use are especially helpful while the
project is young.

Do not report suspected security vulnerabilities in public issues. Follow the
[security policy](.github/SECURITY.md) to submit a private vulnerability report.

## Acknowledgements

Pigkin's interface started out modelled on [Paseo](https://paseo.sh), an open-source app for running
coding agents from your desktop and phone, and some of its code is adapted from
[Paseo's](https://github.com/getpaseo/paseo): the folder search, and the file-type icon table and icon
colours.

Pigkin is built on [Pi](https://pi.dev) and [Termux](https://termux.dev), with Jetpack Compose,
[libghostty-android](https://github.com/SagerNet/libghostty-android) and
[Ghostty](https://github.com/ghostty-org/ghostty) for the terminal,
[tree-sitter](https://github.com/tree-sitter/tree-sitter) for syntax highlighting,
[Lucide](https://lucide.dev) and
[Material Icon Theme](https://github.com/material-extensions/vscode-material-icon-theme) for icons,
and [pi-web-access](https://github.com/nicobailon/pi-web-access) for web search. The
[acknowledgements page](https://pipkin.jeph.io/docs/acknowledgements/) credits the main open-source
projects Pigkin uses.

## License

Pigkin is released under the [MIT License](LICENSE). Third-party code, fonts, and icons that Pigkin
includes keep their own licenses. The app lists them under **Settings → About → Open-source
licenses**. Their texts are in [`app/src/main/assets/licenses/`](app/src/main/assets/licenses/), the
bundled Pi extensions' folders, and the files they were copied into, and the website serves its own
at [`/licenses.txt`](site/public/licenses.txt).
