# claude-mux

One web page for many Claude Code sessions: tabs on the left, the live
terminal on the right, and a dot per session that says whether it is
working, ready for input, or waiting on you.

```sh
go run . -cwd ~/Documents/@Ankr/codespace
# → http://127.0.0.1:7777
```

`./restart.sh` rebuilds it, restarts it and resumes the
sessions that were running. Run it from a regular terminal, because a
session inside claude-mux is hung up by the restart.

Start it from a normal terminal. Starting it from inside a claude session also
works, because the server strips the session markers from its children's env.

## What it does

The sidebar has three sections, by where a conversation is:

- **Running here**: claude-mux's own sessions (`+ New`, ⌥N), each `claude`
  in its own pty with the live terminal in the page. Closing the browser tab
  doesn't stop anything, and reopening replays the last 1 MB of output.
  **Close** ends the session and removes its tab. **Move to Terminal** ends
  it here and continues it in a Terminal.app window. Drag tabs to reorder.
- **Running in Terminal**: claude sessions in other terminals (from
  `claude agents --json`), with their last 40 messages, Markdown rendered.
  The box at the bottom types a message into that window (see below).
  **Move here** hangs up that claude process (SIGHUP to its pid only; the
  window and its shell stay open) and continues the session here. **Close**
  just ends it.
- **Saved**: conversations you starred (right-click → ★ Save) that run
  nowhere at the moment. **Open here** or **Open in Terminal** continues
  one. Saved conversations live in `~/.claude-mux/favourites.json`; while
  one runs, its row shows ★. Unsave forgets it.
  A saved conversation can have a **restore message** (right-click →
  Restore message…, marked ↻): the first prompt whenever it starts in a new
  process (Open here, Open in Terminal, Move, Resume, a claude-mux
  restart), passed as `claude --resume <id> -- "<message>"`. Use it to bring
  back what dies with the old process, like a watcher.

Also:

- **Groups**: right-click a tab or saved conversation → Group… (or the
  Group field in New) puts it under a collapsible header. Drag a tab onto a
  header to put it in that group, or next to a tab to take that tab's group.
  A collapsed header shows the most urgent status inside it. Right-click a
  header to start a new session in it, rename it (an existing name merges
  the two) or ungroup it. A tab's group goes with it to its saved
  conversation and back; collapsing is kept per browser.
- **Status dot**: blue pulsing = working, green = ready for input, amber
  pulsing = needs you (a permission prompt, or a prompt shown before startup
  such as trusting the folder), hollow = exited. A session that finished
  while you looked elsewhere gets a bold name and a green edge until you open
  it. The page title and icon count what waits: `(2) claude-mux`.
- **Notifications** when a session becomes ready or needs you and you aren't
  looking at it (press "Enable notifications" once).
- **Sending to other terminals** types the text over AppleScript, line
  breaks included, without bringing the window to the front. Claude reads a
  write of 64 bytes or more as a paste, where Enter turns into a newline, so
  the text goes in chunks of at most 48 bytes, joined with Alt+Enter and
  Backspace (see `keystrokeChunks`).
- **Names** come from claude's own list (`claude agents --json`), for tabs,
  Terminal sessions and saved ones alike. Rename (right-click, either kind)
  goes through claude: it clears the prompt (an unsent draft is lost),
  types `/rename <name>` and Enter, waiting first while claude works or
  shows a prompt; the sidebar follows on the next poll.
- **Themes**: page theme (Auto follows macOS, Light, Dark) and terminal theme
  (Auto matches the page, or Tokyo Night, Dracula, Solarized) in the footer,
  kept per browser.
- **Restarts**: a plain start brings back only the saved conversations.
  `restart.sh` passes `-resume-running`, which also resumes the tabs that
  were running, so a redeploy keeps them.
- Keys: ⌥1–9 tab, ⌥↑/↓ prev/next, ⌥J next session that waits for you,
  ⌥N new, ⇧↵ newline in the prompt.

## How it works

- Status comes from Claude Code hooks. Each session gets
  `--settings ~/.claude-mux/hooks.json`, whose hooks `curl` their payload to
  `/hook/$CLAUDE_MUX_ID`: SessionStart/Stop → ready,
  UserPromptSubmit/Pre/PostToolUse → working, a permission Notification →
  needs you, SessionEnd → exited. The hook also reports the claude session
  id that Resume uses.
- State (names, dirs, claude session ids) is in `~/.claude-mux/sessions.json`.
- The server only listens on loopback. It rejects foreign Host/Origin headers
  and non-JSON writes, because anything that reaches the API can start
  processes.

## Known rough edges

- The processes live in the server: stopping it hangs up every session.
  Resuming brings back the conversations, but not what was running in them
  (background tasks, watchers).
- For a moment, claude's status line shows "running … hook" while a hook
  `curl`s.
- The xterm.js, marked and DOMPurify scripts load from jsdelivr, so the page
  needs internet.
- Sending to other terminals: the first send makes macOS ask whether
  claude-mux may control Terminal. The iTerm2 path is written but untested.
- Claude Code picks its own colours (`/theme`); with a light terminal theme
  its light theme reads better.
