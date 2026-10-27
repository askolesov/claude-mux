# claude-mux

One web page for many Claude Code sessions: tabs on the left, the live
terminal on the right, and a dot per session that says whether it is
working, ready for input, or waiting on you.

Two programs: the **hub** (`cmd/hub`, the page) and the **backend**
(`cmd/backend`, the sessions), which the hub runs as its child and talks to
over the backend's stdin and stdout. See [docs/design.md](docs/design.md)
for where this is going: backends over SSH, in Docker, in WSL and on
Windows.

```sh
mkdir -p /tmp/backends
go build -o /tmp/backends/claude-mux-backend-$(go env GOOS)-$(go env GOARCH) ./cmd/backend
go run ./cmd/hub -backend-dir /tmp/backends -cwd ~/Documents/@Ankr/codespace
# → http://127.0.0.1:7777
```

`./restart.sh` rebuilds both, restarts them and resumes the sessions that
were running. Run it from a regular terminal, because a session inside
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
- **Environments**: **+ Host** adds an SSH host (as ssh takes it: host,
  user@host or a `~/.ssh/config` alias; it must take your key without a
  password prompt). The sidebar then shows a block per environment, each
  with its own tabs and groups; New asks where. On connect the hub checks
  the host's platform (`uname -sm`) and, if the right backend build isn't
  in `~/.claude-mux/bin/<version>/` there yet, copies it over the same ssh.
  The remote backend lives as long as the connection: when the hub quits or
  ssh drops, it hangs up its tabs, and the next connect resumes them. Right-
  click an environment's header to open a tab there or remove it (its tabs
  stay on the host for when it's added again). Hosts are kept in
  `~/.claude-mux/environments.json`.
- **No claude in an environment**: its block says so, with **Install…**
  (runs Anthropic's official installer there, `curl -fsSL
  https://claude.ai/install.sh | bash`, and shows its output) and **Retry**
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
  directly for local and through `ssh -T` for a host, split into streams by
  yamux. A backend's API listens on no port. The hub's own API is `/hub/`
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

- The processes live in the backend: stopping it hangs up every tab.
  Resuming brings back the conversations, but not what was running in them
  (background tasks, watchers); a startup message can restart those.
- The xterm.js scripts load from jsdelivr, so the page needs internet.
- Claude Code picks its own colours (`/theme`); with a light terminal theme
  its light theme reads better.
