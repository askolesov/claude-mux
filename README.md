# claude-mux

One window for all your Claude Code sessions. Tabs on the left, the live
terminal on the right, and a dot on every tab that tells you whether that
claude is working, done, or waiting for you.

The tabs can run on this machine, on SSH hosts, in Docker containers and in
WSL, all in the same window. A tab stays until you close it: quit the app,
reboot, update, and every tab comes back with its conversation.

```
┌──────────────┬──────────────────────────────────────────────┐
│ claude-mux   │ api-fix   local / backend · ~/src/api · main │
│              ├──────────────────────────────────────────────┤
│ local      ● │                                              │
│  backend     │  > fix the flaky test in store_test.go       │
│   ● api-fix  │                                              │
│   ● migrate  │  ● Reading store_test.go…                    │
│  ● notes     │                                              │
│ devbox     ● │                                              │
│   ● deploy   │            (the tab's real terminal)         │
│              │                                              │
└──────────────┴──────────────────────────────────────────────┘
  ● blue: working   ● green: done, ready for you   ● amber: needs you
```

## How it works, in short

claude-mux is two programs.

- The **hub** is the app you start. It shows the window and remembers your
  environments and settings.
- A **backend** runs your tabs: one `claude` per tab, each in its own
  terminal. There is one backend per environment, and the hub starts it
  there: as its own child on this machine, or through `ssh`, `docker exec`
  or `wsl.exe` elsewhere. It talks to it over that one connection, with no
  ports opened anywhere.

```mermaid
flowchart LR
    page["The window<br/>(the page)"] --> hub["hub<br/>claude-mux"]
    hub -- "stdin/stdout" --> local["backend<br/>this machine"]
    hub -- "ssh host" --> ssh["backend<br/>SSH host"]
    hub -- "docker exec" --> docker["backend<br/>container"]
    hub -- "wsl.exe" --> wsl["backend<br/>WSL distro"]
    local --> c1["claude"] & c2["claude"]
    ssh --> c3["claude"]
```

You never install anything in an environment yourself. On connecting, the
hub checks what the machine is (`uname`), and copies the matching backend
there if it is missing.

**How the dots know.** claude-mux starts each claude with a small set of
[hooks](https://docs.claude.com/en/docs/claude-code/hooks). Claude Code runs
them on every event: a prompt sent, a tool used, a permission asked, a turn
finished. Each hook tells the backend, the backend tells the page, and the
dot changes.

```mermaid
flowchart LR
    claude -- "hook: Stop" --> backend -- "tab is ready" --> hub --> page["dot turns green"]
```

**How tabs come back.** A backend lives only as long as its connection.
When the hub quits, or an SSH connection drops, the backend hangs up its
claudes, the way closing a terminal window does. It keeps the list of tabs.
On the next start it opens each tab again with `claude --resume`, so the
conversation continues. What was running inside the old process
(background tasks, watchers, dev servers) is gone. claude-mux can send each
tab a message saying so as its first prompt (Settings › When tabs start
again).

The details are in [docs/how-it-works.md](docs/how-it-works.md).

## Install

Download the hub for your machine from the
[latest release](https://github.com/askolesov/claude-mux/releases/latest).

| System | File | Notes |
|---|---|---|
| Mac (Apple silicon and Intel) | `claude-mux-mac.zip` | Unzip `claude-mux.app` into Applications. It isn't signed: the first time, right-click → Open, or System Settings › Privacy & Security › Open Anyway. |
| Windows | `claude-mux-windows-amd64.exe` | Unsigned too: SmartScreen wants More info → Run anyway. Needs WebView2, which Windows 11 has. Claude Code on Windows needs Git for Windows. |
| Linux | `claude-mux-linux-amd64`, `-arm64` | Needs WebKitGTK 4.1 (`libwebkit2gtk-4.1-0`). |

You need [Claude Code](https://docs.claude.com/en/docs/claude-code) in
each environment you use. If it isn't there, claude-mux offers to run the
official installer for you.

claude-mux updates itself. It checks GitHub once a day (Settings ›
Updates); **Update and restart** replaces the app and brings every tab
back.

## Using it

- **New tab** (+ or ⌥N): pick the environment and the directory. Optional:
  a group and extra `claude` arguments. Frequent flags such as
  `--dangerously-skip-permissions` are checkboxes.
- **The dot**:
  - blue: working
  - green: ready for your next prompt
  - amber with a ring: needs you (a permission prompt or a question)
  - hollow: exited, and Enter in its terminal resumes it

  A tab that finished while you weren't looking stays bold until you open
  it. The window title and the Dock icon count the tabs that wait.
- **Tab settings** (… above the terminal, or right-click a tab): its
  group, directory, arguments and resume message, plus Restart and Close.
  The name belongs to claude: type `/rename <name>` in the tab.
- **Groups**: drag a tab onto a group's label or next to its tabs. Click a
  label to fold it, right-click it to rename it.
- **Environments**: Settings › Environments › Add environment. You can add:
  - an SSH host (it must accept your key without a password prompt)
  - a running Docker container
  - a WSL distro

  Drag their headers in the sidebar to reorder them.
- **Keys**:

  | Key | Does |
  |---|---|
  | ⌘K (Ctrl+K) | jump to a tab |
  | ⌥1–9 | go to tab 1–9 |
  | ⌥↑ / ⌥↓ | previous / next tab |
  | ⌥J | next tab that waits for you |
  | ⌥N | new tab |
  | ⌘, | Settings |
  | ⇧↵ | newline in the prompt |

- **No window**: `claude-mux -headless -open` serves the page in your
  browser instead, until you stop it.

Everything claude-mux keeps is in `~/.claude-mux/`, in each environment:
`hub.json` on your machine, `backend.json` (the tabs) wherever tabs run.

## Building from source

Go 1.27 or newer. The hub's window needs cgo and the system web view.

```sh
scripts/restart.sh        # build the hub and every backend, restart a dev hub on :7777
go test ./...
scripts/macapp.sh v0.0.0 out   # build claude-mux.app
```

A development build installs the backends you built (`-backend-dir`)
instead of downloading them, and doesn't update itself. Releases are made by
pushing a `v*` tag; CI builds everything and publishes it.
