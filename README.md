# claude-mux

One web page for many Claude Code sessions: tabs on the left, the live
terminal on the right, and a dot per session that says whether it is
working, ready for input, or waiting on you.

```sh
go -C tools/claude-mux run . -cwd ~/Documents/@Ankr/codespace
# → http://127.0.0.1:7777
```

`tools/claude-mux/restart.sh` rebuilds it, restarts it and resumes the
sessions that were running. Run it from a regular terminal, because a
session inside claude-mux is hung up by the restart.

Start it from a normal terminal. Starting it from inside a claude session also
works, because the server strips the session markers from its children's env.

## What it does

- **Sessions it starts** (`+ New`, ⌥N): each one is `claude` in its own pty,
  with the live terminal in the browser. Closing the tab doesn't stop
  anything, and reopening replays the last 1 MB of output.
- **Status dot**: blue pulsing = working, green = ready for input, amber
  pulsing = needs you (a permission prompt, or a prompt shown before startup
  such as trusting the folder), hollow = exited/stopped. A session that
  finished while you looked elsewhere gets a bold name and a green edge until
  you open it. The page title counts what waits: `(2) claude-mux`.
- **Notifications** when a session becomes ready or needs you and you aren't
  looking at it (press "Enable notifications" once).
- **Other terminals**: the claude sessions running outside it (from
  `claude agents --json`) are listed with their last 40 messages from the
  transcript, Markdown rendered. The box at the bottom sends a message: it
  is typed into that session's Terminal.app or iTerm2 window over
  AppleScript, line breaks included. Claude reads a write of 64 bytes or
  more as a paste, where Enter turns into a newline, so the text goes in
  chunks of at most 48 bytes, joined with Alt+Enter and Backspace (see
  `keystrokeChunks`). The window doesn't come to the front. **Take over**
  hangs up that session's claude process (SIGHUP to its pid only, so the
  window and its shell stay open), waits for it to exit, and continues the
  session here.
- **Open in Terminal** is Take over in reverse: it ends the session here,
  waits for the process to exit, opens a Terminal.app window running
  `claude --resume <id>` in the same directory, and removes the tab. The
  window then appears under Other terminals.
- **Kill / Resume**: kill ends the process and keeps the tab. Resume runs
  `claude --resume <id>` in it. After a server restart every session shows
  as stopped and can be resumed.
- **Names**: a tab follows claude's own name, so `/rename` in the session
  renames the tab. A rename in the page sticks until claude's name changes
  again.
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
  Resume brings back the conversations, but not what was running.
- For a moment, claude's status line shows "running … hook" while a hook
  `curl`s.
- The xterm.js, marked and DOMPurify scripts load from jsdelivr, so the page
  needs internet.
- Sending to other terminals: the first send makes macOS ask whether
  claude-mux may control Terminal. The iTerm2 path is written but untested.
