# claude-mux

One window for all your Claude Code sessions, and Codex's, and a terminal
or two. Tabs on the left, the live terminal on the right, and a glyph on
every tab that tells you what runs there and whether it is working, done,
or waiting for you.

The tabs can run on this machine, on SSH hosts, in Docker containers and in
WSL, all in the same window. A tab stays until you close it: quit the app,
reboot, update, and every tab comes back with its conversation.

<picture>
  <source media="(prefers-color-scheme: light)" srcset="docs/redesign/screenshots/main-light.png">
  <img alt="claude-mux: environments, groups and tabs on the left, each with its status dot; the selected tab's terminal on the right, waiting on a permission prompt" src="docs/redesign/screenshots/main-dark.png">
</picture>

## How it works, in short

claude-mux is two programs.

- The **hub** is the app you start. It shows the window and remembers your
  environments and settings.
- A **backend** runs your tabs: one `claude`, `codex` or shell per tab,
  each in its own terminal. There is one backend per environment, and the hub starts it
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
glyph changes. Codex gets the same kind of
[hooks](https://learn.chatgpt.com/docs/hooks), for its session only; Codex
asks you once, on the first Codex tab on a machine, to trust them (choose
*Trust all and continue*). A terminal needs no hooks: whatever runs in its
foreground is running.

```mermaid
flowchart LR
    claude -- "hook: Stop" --> backend -- "tab is ready" --> hub --> page["dot turns green"]
```

**How tabs come back.** A backend lives only as long as its connection.
When the hub quits, or an SSH connection drops, the backend hangs up its
claudes, the way closing a terminal window does. It keeps the list of tabs.
On the next start it opens each tab again with `claude --resume` (or
`codex resume`), so the conversation continues; a terminal gets a new
shell, in the folder the old one was in. What was running inside the old process
(background tasks, watchers, dev servers) is gone. A startup message,
sent as the first prompt each time a claude starts again, can say so or
restart a watcher: one for every tab (Settings › Tabs), and each tab's own
(New tab and Tab settings), which comes after it. Switched on empty, it
starts with a message saying claude was restarted, which you can change.

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

- **New tab**: the **+** at the top of the sidebar offers New Claude, New
  Codex and New terminal, next to the selected tab; ⌥N makes the kind you
  made last. Right-click an environment or a group for one inside it. It
  asks for a name and a directory (Tab completes it, from that
  environment). An agent's startup message and arguments repeat your last
  tab of that kind; the frequent flags are checkboxes
  (`--dangerously-skip-permissions`, `--chrome` for Claude;
  `--approve-for-me`, `--search` for Codex). Starting an agent that isn't
  installed there offers its official installer.
- **The glyph**: its shape is what runs (Claude a rounded rectangle with
  eyes, Codex a circle, a terminal a triangle), its colour how it is:
  - blue: working (a terminal: a command is running)
  - green: ready for your next prompt
  - grey: a terminal at its prompt, or a Codex tab before its first prompt
  - a halo: look at me. Green: finished while you weren't looking, until
    you open it; amber: needs you (a permission prompt or a question)
  - an outline: exited, and Enter in its terminal starts it again

  A folded group shows its most urgent tab's state as a dot. The window
  title and the Dock icon count the agent tabs that wait; terminals never
  notify.
- **Tab settings** (… above the terminal, ⌘I, or right-click a tab): its
  directory, startup message and arguments. Saving a new directory or
  arguments restarts its claude, and the conversation continues; a new
  startup message waits for the tab's next start. A terminal has only its
  directory.
- **Renaming** a tab: F2, a double-click, Rename, or its name in Tab
  settings. A Claude tab's new name goes to claude as `/rename` once it is
  ready for a prompt (anything half-typed in its prompt is cleared).
- **Right-click** any row for what it can do. A tab: Tab settings,
  Rename, Duplicate, Move to…, Restart, Close. A group: New Claude, Codex
  or terminal, New group, Rename, Collapse, Delete. An environment: New
  Claude, Codex or terminal, New group, Reconnect, Environment settings,
  Remove. The Menu key or ⇧F10 opens it
  from the keyboard.
- **Groups** hold tabs and other groups, as deep as you like. Make one
  with ⇧⌥N (next to the selected tab) or from a right-click; it starts
  named "New group" for you to type over, and Esc takes it back. Fold
  groups and environments by their chevrons. Rename them with F2 or a
  double-click. Deleting a group moves what it held up a level; no tab is
  closed.
- **Moving**: drag a tab or a group onto a group to put it inside, or
  between rows. The line shows where it lands, and its left edge the
  level: drag left or right to change it. ⌘⇧↑/↓ moves the selected row
  within its group, and Move to… puts a tab in any group.
- **Files**: drop a file on the terminal, or paste one or a screenshot,
  and its path is typed in for claude to attach. On a local tab in the Mac
  app that is the file's own path; elsewhere the file is copied to the
  tab's environment first, so this works on SSH hosts and in containers
  too.
- **Tab rows** (Settings › Appearance): Compact, the default, shows only
  each tab's name; Detailed adds its directory under it.
- **Environments**: **Connect environment**, under the last one, adds one.
  You can add:
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
  | ⌥N | new tab of the kind you made last, next to this one |
  | ⇧⌥N | new group next to this one |
  | ⌘I | Tab settings |
  | ⌘W | close the tab (asks first, unless Settings say not to) |
  | ⌘⇧↑ / ⌘⇧↓ | move a row up / down in its group |
  | ↑↓←→, Enter, F2, ⌫ | in the sidebar: move, fold, open, rename or delete a group |
  | Menu key, ⇧F10 | in the sidebar: the row's menu |
  | ⌘N | a new window: another tab on another screen |
  | ⌘, | Settings |
  | ⇧↵ | newline in the prompt |
  | Ctrl+C with a selection, Ctrl+Shift+C | Windows, Linux: copy (⌘C on a Mac) |
  | Ctrl+V, Ctrl+Shift+V | Windows, Linux: paste (⌘V on a Mac) |

- **Windows**: ⌘N (or New window in Jump) opens another window, each with
  its own tab; they all show the same tabs live. With more than one open,
  each shows which it is beside the name: 1/2, 2/2. The app quits with the last.
- **No window**: `claude-mux -headless -open` serves the page in your
  browser instead, until you stop it.

Everything claude-mux keeps is in `~/.claude-mux/`, in each environment:
`hub.json` on your machine, `backend.json` (the tabs) wherever tabs run.

## Building from source

Go 1.27 or newer. The hub's window needs cgo and the system web view.

```sh
scripts/restart.sh        # build the hub and every backend, restart a dev hub on :7777
scripts/check.sh          # every check CI runs: gofmt, vet, golangci-lint, ESLint, tests, -race
scripts/macapp.sh v0.0.0 out   # build claude-mux.app
```

The checks need [golangci-lint](https://golangci-lint.run) 2.x and Node,
for ESLint (`npm install` once).

A development build installs the backends you built (`-backend-dir`)
instead of downloading them, and doesn't update itself. Releases are made by
pushing a `v*` tag; CI builds everything and publishes it.
