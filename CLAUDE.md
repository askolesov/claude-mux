# claude-mux — rules for agents

Moved from ankr-codespace CLAUDE.md on 2026-11-24 (dates are the user's).

## Running and releasing
- the live hub is the release app `/Applications/claude-mux.app`, which updates itself (Settings › Updates); `scripts/restart.sh` runs a dev hub from the checkout and only works once the app has quit (one hub per `~/.claude-mux`), and a dev hub never updates. Releases: push a `v*` tag (CI builds everything)

## Rules
- **Every tab can be renamed from the page (F2, double-click, Rename, Tab settings' Name), but a Claude tab's rename goes through Claude**: claude-mux types `/rename <name>` into the session and reads the name back; Codex and terminal names are claude-mux's own (user 2026-10-29, replacing the 10-28 "never rename from the page" rule).
- **Always open a PR once a fix or feature branch is pushed — don't ask first** (user 2026-10-29: "always open pr"). Merging and releasing still wait for the user's word.
- **When a design in `docs/redesign` contradicts something the user asked for earlier, ask before applying that part** (user 2026-10-28, after the v4 design's tab Rename overrode "inline rename only for groups and envs").
