# claude-mux

One web page for many Claude Code sessions: tabs on the left, the live
terminal on the right, and a dot per session that says whether it is
working, ready for input, or waiting on you.

Two programs: the **hub** (`cmd/hub`, the page) and the **backend**
(`cmd/backend`, the tabs), which the hub runs as its child and talks to
over the backend's stdin and stdout: on this machine, over SSH, in a Docker
container or in a WSL distro. See [docs/design.md](docs/design.md).

## Install

Download the hub for your machine from the
[latest release](https://github.com/askolesov/claude-mux/releases/latest).
It shows the page in a window of its own (the system's web view); closing
the window quits it, and the tabs resume on the next start.

- **Mac:** `claude-mux-mac.zip`, a universal `claude-mux.app`. Unzip it into
  Applications. It isn't signed yet: the first start needs right-click →
  Open (or System Settings → Privacy & Security → Open Anyway).
- **Windows:** `claude-mux-windows-amd64.exe`. Unsigned too: SmartScreen
  wants "More info → Run anyway". It needs the WebView2 runtime, which
  Windows 11 has. Claude Code on Windows needs Git for Windows, as its own
  setup says.
- **Linux:** `claude-mux-linux-amd64` / `-arm64`, which needs WebKitGTK 4.1
  (`libwebkit2gtk-4.1-0`).

`-headless` serves the page without a window, for the browser, until
interrupted (`-open` opens it). Started from Finder, the hub takes the
login shell's environment, so tabs see the PATH a terminal would, and logs
to `~/.claude-mux/hub.log`. A second start opens a window onto the hub
already running.

The hub downloads the
backend of its own release for each environment it connects to, this
machine included, checks it against the release's `checksums.txt`, keeps it
in `~/.claude-mux/cache/<version>/`, and installs it in
`~/.claude-mux/bin/<version>/` there. Older versions are removed.
Releases are made by pushing a `v*` tag.

From a checkout, give the hub the backend builds yourself:

```sh
mkdir -p /tmp/backends
go build -o /tmp/backends/claude-mux-backend-$(go env GOOS)-$(go env GOARCH) ./cmd/backend
go run ./cmd/hub -backend-dir /tmp/backends -cwd ~/projects
```

`./restart.sh` rebuilds both (backends for every platform), restarts a
headless hub and resumes the tabs. `scripts/macapp.sh <version> <dir>`
builds the `.app`. Run it from a regular terminal, because a tab inside
claude-mux is hung up by the restart.

Start it from a normal terminal. Starting it from inside a claude session also
works, because the server strips the session markers from its children's env.

## What it does

Tabs for claude, like a browser's. Each tab is one `claude` in its own pty
with the live terminal in the page. A tab stays until you close it:
quitting claude-mux, a reboot or a crashed backend just restarts it, and
every start continues the tab's conversation (`claude --resume`). **Close**
ends its claude and removes it for good. Only claudes started here are
shown; claude running in other terminals is left alone.

The design is in [docs/redesign/](docs/redesign/README.md): tabs on the
left (or on top, in Settings), and nothing that says the same thing twice.

- **A tab row** is its status dot and name, plus ↻ when it has its own
  message for when it starts again. Above the terminal: the name, then
  `environment / group · directory · git branch`, and **…** for Tab settings.
- **Tab settings** (…, or right-click): group, directory, the message for
  when it starts again, extra arguments, **Restart** and **Close tab**.
  Extra arguments are one text; the frequent flags
  (`--dangerously-skip-permissions`, `--chrome`, `--model opus`,
  `--permission-mode acceptEdits`) are checkboxes, each gone once the text
  has that flag, and one line shows exactly what runs. Directory and
  arguments apply on the next start. **New** (+, ⌥N) has the same fields,
  with the environment first, from the defaults in Settings › New tabs.
- **Names** belong to claude: type `/rename <name>` in the tab and the
  sidebar follows (from `claude agents --json`, polled every 5 s). The
  name is passed as `--name` on the next start.
- **When tabs start again** (Settings): a tab's claude starts again when
  claude-mux restarts or updates, on Restart and on Resume. It continues
  its conversation, but what ran in the old process (background tasks,
  watchers, servers) is gone. A message sent to every tab as its first
  prompt then, `claude --resume <id> -- "<message>"`, tells it so (off by
  default, with a suggested text). In Tab settings each tab follows it
  (Default), has its own (Custom: say, restart a watcher) or gets none.
  It's never sent on a new tab's first start.
- **Groups**: a plain label above their tabs; click it to fold it (it then
  shows the most urgent status inside). Drag a tab onto a label to put it
  in that group, or next to a tab to take that tab's group; drag to
  reorder. Right-click a label to start a new tab in it, rename it (an
  existing name merges the two) or ungroup it.
- **Jump to tab** (⌘K): fuzzy search over the tabs, waiting ones first,
  and a few actions.
- An exited tab (`/exit`, a crash) says so in its terminal: Enter resumes
  it.
- **Environments**: Settings › Environments › **Add environment** adds one of a kind:
  - an **SSH host**, as ssh takes it: host, user@host or a `~/.ssh/config`
    alias (offered from there); it must take your key without a password
    prompt;
  - a **Docker container**, running, with `sh` in it (running ones are
    offered); its tabs live as long as the container;
  - a **WSL distro** (on Windows; installed ones are offered).

  The sidebar then shows a block per environment, each with its own tabs
  and groups; New asks where. On connect the hub checks the platform
  (`uname -sm`) and, if the right backend isn't in
  `~/.claude-mux/bin/<version>/` there yet, copies it over the same
  connection, so the environment needs no internet for it. The backend
  lives as long as the connection: when the hub quits or the connection
  drops, it hangs up its tabs, and the next connect resumes them.
  Its header has a health dot (green connected, amber a problem); hover
  for the kind, platform and latency. Settings › Environments (or
  right-click the header) reconnects one, installs claude there or removes
  it (its tabs stay there for when it's added again). Drag a header to
  reorder the environments; this machine (`local`) is one of them, and
  can't be removed.
- **No claude in an environment**: its block says so, with **Install…**
  (runs Anthropic's official installer there, `curl -fsSL
  https://claude.ai/install.sh | bash`, or `irm https://claude.ai/install.ps1
  | iex` on Windows, and shows its output) and **Retry**
  (looks again, after you installed it yourself). The backend also finds
  claude in `~/.local/bin`, which a non-interactive ssh usually leaves off
  the PATH. The first tab in a fresh environment walks through claude's
  setup and login.
- **Status dot**: blue = working, green = ready for input, amber with a
  ring = needs you (a permission prompt, or a prompt shown before startup
  such as trusting the folder), hollow = exited. A tab that finished while
  you looked elsewhere has a bold name until you open it. The page title,
  icon and Dock count what waits: `(2) claude-mux`.
- **Notifications** (Settings): a tab needs you, finished or exited; only
  while the window is in the background, if you like.
- **Updates**: Settings › Updates has "Check for updates" (on, every 1 day
  by default) and Check now. When a newer release is out, the sidebar says so
  (with a notification once per version): **Update and restart**
  downloads it, checks it against the release's checksums, puts it in
  place of the running hub (the whole `.app` on a Mac), and restarts into
  it; every tab resumes, and each environment gets the new backend when it
  connects. **Skip this version** stays quiet until a newer one. A Mac app
  still running from its download (macOS's read-only copy) has to be moved
  to Applications first. Development builds don't check.
- **Appearance** (Settings): page theme (Auto follows the system, Light,
  Dark), terminal theme (Match page, Tokyo Night, Dracula, Solarized, …),
  terminal font and size. Settings live in `hub.json`, so the app's window
  and a browser share them.
- Keys: ⌘K jump, ⌥1–9 tab, ⌥↑/↓ prev/next, ⌥J next tab that waits for you,
  ⌥N new, ⌘, settings, ⇧↵ newline in the prompt.

## How it works

- The page talks to the hub, which forwards `/env/<name>/…` to that
  environment's backend over one pipe: the backend's stdin and stdout,
  directly for local, through `ssh -T`, `docker exec -i` or `wsl.exe
  --exec`, split into streams by yamux. Tabs run in a pty (ConPTY on
  Windows). A backend's API listens on no port. The hub's own API is `/hub/`
  (the environments, as server-sent events; add, remove).
- Files, all in `~/.claude-mux/`:
  - `hub.json`, the hub's: settings (the page's too, and the message for
    when tabs start again), the environments in sidebar order, the last
    update check and a skipped version. `hub.log` when it runs
    from Finder or Explorer (or `restart.sh`).
  - `backend.json`, each environment's own (on this machine and on every
    host and container): the tabs in sidebar order, each with its name,
    group, directory, args, claude session id and its resume message mode.
    `backend-hooks.json` (the hooks, rewritten every start),
    `backend.lock` (one backend per environment), `bin/<version>/`.
  - `hub.lock`: one hub per state directory.
- Status comes from Claude Code hooks. Each tab's claude gets
  `--settings ~/.claude-mux/backend-hooks.json`, whose hooks run
  `claude-mux-backend hook`; it posts the payload to the backend's hook-only
  loopback port, given to the session in `CLAUDE_MUX_HOOK` with a token
  that changes every start: SessionStart/Stop → ready,
  UserPromptSubmit/Pre/PostToolUse → working, a permission Notification →
  needs you, SessionEnd → exited. The hook also reports the claude session
  id the tab resumes.
- The hub only listens on loopback. It rejects foreign Host/Origin headers
  and non-JSON writes, because anything that reaches a backend can start
  processes.

## Known rough edges

- **One hub per state directory, one backend per environment.** The hub
  locks `~/.claude-mux/hub.lock` (its pid and address): a second start
  with the same state opens a window onto the running hub, or with
  `-headless` says it's running and where. A backend locks
  `backend.lock`: a hub with other state reaching the same machine,
  container or host shows it as in use, naming the pid, and connects by
  itself once the first lets go. Two would resume every tab twice.
- The processes live in the backend: stopping it hangs up every tab.
  Resuming brings back the conversations, but not what was running in them
  (background tasks, watchers); a startup message can restart those.
- The xterm.js scripts load from jsdelivr, so the page needs internet.
- Claude Code picks its own colours (`/theme`); with a light terminal theme
  its light theme reads better.
