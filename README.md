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

- **New** (⌥N): name, group, directory, startup message, extra args and the
  `--dangerously-skip-permissions` / `--chrome` switches. The name is
  passed as `--name`.
- **Names** belong to claude: type `/rename <name>` in the tab and the
  sidebar follows (from `claude agents --json`, polled every 5 s).
- **Startup message** (right-click → Startup message…, marked ↻): the first
  prompt whenever the tab's claude starts, passed as
  `claude [--resume <id>] -- "<message>"`. Use it to bring back what dies
  with the old process, like a watcher.
- **Groups**: right-click → Group… (or the Group field in New) puts a tab
  under a collapsible header. Drag a tab onto a header to put it in that
  group, or next to a tab to take that tab's group; drag to reorder. A
  collapsed header shows the most urgent status inside it. Right-click a
  header to start a new tab in it, rename it (an existing name merges the
  two) or ungroup it. Collapsing is kept per browser.
- **Resume** restarts a tab whose claude exited (`/exit`, a crash).
- **Environments**: **+ Env** adds one of a kind:
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
  Right-click an environment's header to open a tab there or remove it
  (its tabs stay there for when it's added again). Environments are kept
  in `~/.claude-mux/environments.json`.
- **No claude in an environment**: its block says so, with **Install…**
  (runs Anthropic's official installer there, `curl -fsSL
  https://claude.ai/install.sh | bash`, or `irm https://claude.ai/install.ps1
  | iex` on Windows, and shows its output) and **Retry**
  (looks again, after you installed it yourself). The backend also finds
  claude in `~/.local/bin`, which a non-interactive ssh usually leaves off
  the PATH. The first tab in a fresh environment walks through claude's
  setup and login.
- **Status dot**: blue pulsing = working, green = ready for input, amber
  pulsing = needs you (a permission prompt, or a prompt shown before startup
  such as trusting the folder), hollow = exited. A tab that finished while
  you looked elsewhere gets a bold name and a green edge until you open it.
  The page title and icon count what waits: `(2) claude-mux`.
- **Notifications** when a tab becomes ready or needs you and you aren't
  looking at it (press "Enable notifications" once).
- **Themes**: page theme (Auto follows macOS, Light, Dark) and terminal theme
  (Auto matches the page, or Tokyo Night, Dracula, Solarized) in the footer,
  kept per browser.
- Keys: ⌥1–9 tab, ⌥↑/↓ prev/next, ⌥J next tab that waits for you, ⌥N new,
  ⇧↵ newline in the prompt.

## How it works

- The page talks to the hub, which forwards `/env/<name>/…` to that
  environment's backend over one pipe: the backend's stdin and stdout,
  directly for local, through `ssh -T`, `docker exec -i` or `wsl.exe
  --exec`, split into streams by yamux. Tabs run in a pty (ConPTY on
  Windows). A backend's API listens on no port. The hub's own API is `/hub/`
  (the environments, as server-sent events; add, remove).
- State is one file, `~/.claude-mux/state.json`: the tabs in sidebar order,
  each with its name, group, directory, args, claude session id and startup
  message. (Older versions' `sessions.json` and `favourites.json` are
  migrated into it once.)
- Status comes from Claude Code hooks. Each tab's claude gets
  `--settings ~/.claude-mux/hooks.json`, whose hooks run
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

- **One backend per environment.** A backend locks its state directory
  (`~/.claude-mux/backend.lock`): a second hub reaching the same machine,
  container or host shows it as in use, naming the pid, and connects by
  itself once the first lets go. Two would resume every tab twice.
- The processes live in the backend: stopping it hangs up every tab.
  Resuming brings back the conversations, but not what was running in them
  (background tasks, watchers); a startup message can restart those.
- The xterm.js scripts load from jsdelivr, so the page needs internet.
- Claude Code picks its own colours (`/theme`); with a light terminal theme
  its light theme reads better.
