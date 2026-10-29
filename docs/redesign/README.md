# UI design

**agent-mux** (formerly claude-mux): a terminal for the agent era, with
tabs like a browser, one per agent or shell. A tab runs **Claude**,
**Codex** or a plain **terminal**. The page stays a simple tool: tabs, the
live terminal, and nothing that says the same thing twice. This is the
concept behind the mockups in
[`mockups/`](mockups/) and on the design canvas
(https://claude.ai/artifact/6b129ec8-cb4b-4863-8820-b70f02942f5a).

![agent-mux, dark](screenshots/main-dark.png)

![agent-mux, light](screenshots/main-light.png)

## Principles

- **One signal per fact.** A tab's glyph says what runs in it (its shape)
  and how it is (its colour). No pill, badge or label repeats it.
- **Details live where they are asked for.** Branch, directory, session id,
  args are in the header line or in Tab settings, not on every row.
- **Settings for the window, Tab settings for a tab.** No right-click-only
  features: everything in the context menu also has a visible place.
- Two themes, dark and light (Auto follows the system); one accent; Inter
  for the UI, JetBrains Mono for the terminal and paths.

## Icon

Stacked terminals: three windows, the front one with a `>_` prompt and an
accent outline. Files in [`assets/icon/svg/`](../../assets/icon/svg/):

- `icon.svg`: the app icon (`.app`, `.exe`, release page), dark tile. Used
  in both themes, so the app looks the same everywhere.
- `icon-light.svg`: light tile, for places that need one (light docs,
  README badges).
- `icon-small.svg`: 16–24 px (favicon, sidebar mark, window title): two
  layers and heavier strokes so it stays readable.

The favicon gets a dot when something waits: amber when a tab needs you,
green when one finished unseen (the same rule as the tab dots). Mockup
`Icon.dc.html`.

## Themes

The same names in both; mockup `Main.dc.html` takes `theme`
(`dark` / `light`), `Light.dc.html` shows light.

| Token | Dark | Light | Used for |
|---|---|---|---|
| `bg` | `#0F1115` | `#FFFFFF` | page, header |
| `panel` | `#14171C` | `#F6F7F9` | sidebar |
| `line` | `#252A33` | `#E3E6EB` | borders |
| `field` | `#1B1F26` | `#ECEEF2` | inputs, Jump field |
| `sel` | `#232834` | `#E2E7F0` | selected tab |
| `term` | `#0B0D10` | `#FBFBFC` | terminal background |
| `text` | `#E6E8EC` | `#1D2129` | main text |
| `text2` | `#C4C9D3` | `#3D4452` | secondary text |
| `muted` | `#8A92A2` | `#626A7A` | labels, paths |
| `accent` | `#7AA2F7` | `#2F5FD0` | links, primary button, focus |
| `accentSoft` | `#1B2233` | `#E8EEFB` | update notice |
| `working` | `#7AA2F7` | `#2F5FD0` | tab glyph |
| `ready` | `#4FD18B` | `#1F9D5C` | tab glyph |
| `needs` | `#F5B14C` | `#B86F0A` | tab glyph, env problem |
| `exited` | `#6B7383` | `#9AA1AD` | hollow ring |

The light status colours are darker so the glyphs stay readable on white.
An idle terminal uses `muted`. The
terminal theme is separate (Settings › Appearance) and defaults to matching
the page.

## Main window

Mockups: `Main.dc.html` (compact rows, the default), `Detailed.dc.html`
(detailed rows), `Light.dc.html` (light theme).

Tabs live in the left sidebar only. There is no tabs-on-top layout: a
terminal for claude rarely needs the full width, the height matters more,
and the tree (environments, nested groups, two-line rows) doesn't fit a
horizontal strip. One layout, done well, instead of two.

**Sidebar**

- Top row: logo mark, `agent-mux` (with `2/3` when several windows are
  open), **+**, settings. **+** opens a small menu: *New Claude* (⌥N),
  *New Codex*, *New terminal*, each with its glyph; ⌥N repeats the last
  kind without the menu. Mockup `PlusMenu.dc.html`. The
  sidebar can be dragged wider or narrower; it never collapses to icons,
  because without names you can't tell the sessions apart.
- **Jump to tab ⌘K**: the only search; opens the switcher.
- **Environment header**: a collapse chevron, the name (semibold, main
  text colour; details and latency on hover), the kind in muted text
  (`this Mac`, `ssh`, `docker`). **Calm when fine**: nothing else while it
  is connected. A missing agent isn't shown here: it's asked about the
  first time you start one (see *Agent not installed*). Only when the
  connection is wrong an amber dot appears on the right, and a line under
  the header says what and what to do: the error · *Reconnect*. Folded,
  the line is hidden, so the header shows `disconnected` instead.
  Connecting: a grey dot that pulses.
- **The tree** (see *Tab tree* below): environments at the top, then
  groups and tabs, groups nesting to any depth. Groups show a chevron and
  their name; tabs a glyph.
- **Tab row**: the glyph and the name (plus ↻ when it has a startup
  message). Nothing else. Settings › Appearance › *Tab rows* switches
  between **Compact** (one line, the default) and **Detailed** (a second
  line with the directory in small mono); mockup `Detailed.dc.html`.
- **Connect environment** is a plain muted row (plug icon) after the last
  environment: adding an environment extends the list it belongs to.
- At the bottom, only when a release is out: *0.9.3 is out · Update and
  restart*.

**Above the terminal**: one compact line, 36 px (the sidebar's top row
keeps its own, roomier height; the two don't need to line up): the tab's
name, then
`env / group / subgroup · directory · branch` in mono, and **…**, which opens Tab
settings. No status pill: the glyph already says it.

**Terminal**: fills the whole area under that line, edge to edge, on the
`term` background. No inset, border or rounded frame.

## Tab tree

Mockups: `Main.dc.html`, `ContextMenu.dc.html` (right-click menu on a
group) and `NewGroup.dc.html` (a new group being named).

**Model.** Three kinds of node:

| Node | Lives in | Holds |
|---|---|---|
| Environment | the root | groups and tabs |
| Group | an environment or a group | groups and tabs |
| Tab | an environment or a group | nothing (a leaf) |

Groups and tabs mix in one user-defined order (a group isn't forced above
or below the tabs). Stored flat, one record per node:
`{id, kind, parent, order, name, …}`, with `order` a sortable key
(fractional index, e.g. `"a0"`, `"a0V"`), so a move or reorder rewrites one
record. The sidebar builds the tree from `parent`. Moving a group moves
everything in it. Today's `group` string on a tab becomes a group node
once, on upgrade.

**Rows.** One shape per kind, the same at every depth:

- Environment: chevron · name · kind (an amber dot and a state only when
  the connection is wrong)
- Group: chevron · name (collapsed: also the most urgent state inside, as
  a dot, and the tab count)
- Tab: glyph · name / directory

Each level indents 16 px, with a thin guide line under each group's
chevron so deep tabs still show where they belong.

**Adding.** No + on rows: the tree stays a list of names. New nodes come
from the same three places desktop trees use (Finder, VS Code, Xcode):

- **The sidebar's +** (its menu, or ⌥N for the last kind) creates a new
  tab right where you are: in the same environment and group as the
  current tab, just below it. It doesn't pick a place on purpose; this is
  the everyday case and needs no aiming. To create somewhere else,
  right-click that environment or group.
- **Right-click** (or the Menu key / ⇧F10) on any row opens a context menu:
  - environment: *New Claude (⌥N), New Codex, New terminal* (with their
    glyphs) · *New group* · *Reconnect, Environment settings…* · *Remove
    environment* (not for this machine)
  - group: *New Claude, New Codex, New terminal* · *New group* · *Rename
    (F2), Collapse / Expand* · *Delete group (⌫)*
  - tab: *Tab settings… (⌘I), Rename (F2), Duplicate, Move to…* · *Restart*
    (*Resume* once it has exited), *Close tab (⌘W)*

  *New …* there create inside the row you clicked (for a tab: next to it,
  in its group). ⇧⌥N is *New group* next to the selection.
- **New Claude / Codex / terminal** opens the New tab dialog already aimed
  at that place and that kind:
  environment and group filled in, directory from the last tab started
  there (else the environment's default), arguments from the defaults.
  Enter starts it; the tab appears selected, its terminal open.
- **New group** doesn't open a dialog: a group with only a name is named in
  place, like a new folder in Finder or VS Code. The row appears at the end
  of that container with *New group* selected in an inline field; Enter
  keeps it, Esc removes it. Mockup `NewGroup.dc.html`; the context menu
  is in `ContextMenu.dc.html`.

**Renaming.** Every tab can be renamed, whatever runs in it, the same way
as a group: F2, a double-click or *Rename* turns its name into a field;
Enter keeps it, Esc cancels. Mockup `RenameTab.dc.html`. agent-mux keeps
every tab's name. The new name shows at once; where it goes depends on the
kind:

- **Claude**: agent-mux types `/rename <name>` into the session, then reads
  the name back from the session's properties, so Claude's own name and
  ours stay one. If Claude is in the middle of a turn, the command waits
  until it's free (the tree already shows the new name). If the name read
  back differs, it wins.
- **Codex** and **terminal**: the name is agent-mux's alone; nothing is
  sent.

The Name field in Tab settings is the same rename.

**Collapsing.** The chevron (or ←/→) collapses a container. A collapsed
group keeps one summary: the most urgent status inside (needs you >
finished unseen > working > ready > exited) and how many tabs it holds,
so nothing waiting gets hidden. Collapse state is kept per browser.

**Moving.** Drag any tab or group:

- onto a group or environment: into it, at the end;
- between rows: a line shows the drop point, and its left edge shows the
  depth it lands at (drag left or right to go up or down a level);
- a group can't go into itself or its own descendants.

Without a mouse: *Move to…* (pick any group in the tab's environment, the
Jump list's way), and ⌘⇧↑/↓ to move within the parent.

**Other actions** (right-click, or the same keys in the tree):
rename a group or an environment (F2, or double-click), delete group (its contents move up to
the parent; no tab is ever closed by deleting a group), close tab (⌘W;
asks first while Settings › *Ask before closing a tab* is on, and always
when the tab is working).

**Keyboard and accessibility.** The sidebar is an ARIA `tree`; rows are
`treeitem`s with `aria-level` and, for containers, `aria-expanded`. ↑/↓
move, → expands or steps into, ← collapses or steps out to the parent,
Enter opens a tab, Home/End jump, typing a name jumps to it.

**Depth.** Any depth is allowed; past four levels the indent stops
growing and only the guides show the depth, so names keep their width.

## Status

Every tab shows one glyph, 14 px: its **shape** is what runs in it, its
**colour** is its state. Mockup `Glyphs.dc.html` (real size and 4×).

| Kind | Shape |
|---|---|
| Claude | a slightly wide rounded rectangle with two eyes |
| Codex | a circle |
| Terminal | a triangle pointing right |

| State | Colour |
|---|---|
| Working (a terminal: a command is running) | blue `#7AA2F7` |
| Ready, seen | green `#4FD18B` |
| A terminal, idle at its prompt | grey (`muted`) |
| Ready, **not seen yet** (finished while you looked elsewhere; for a terminal, a command finished) | green with a soft halo |
| Needs you (permission, question; agents only) | amber `#F5B14C` with a soft halo |
| Exited | the shape as a grey outline (no eyes), grey name |

One rule: **a halo means "look at me"**. Amber when an agent waits for you,
green when something finished and you haven't seen it. The halo follows
the shape, 3.5 px wide, so it reads at a glance. Opening the tab clears
the green one. Names are not bolded for status; only the selected tab is.
A collapsed group shows its most urgent state inside as a dot.

The shapes are our own; they don't use the agents' logos.

The selected tab has a filled row background. The window title still counts
what waits: `(2) agent-mux`.

## Dialogs

New tab, Tab settings, Environment settings, Connect environment, Agent
not installed and Settings are one family: a 440 px modal over the window with the same
header (title, muted subtitle), the same fields, and a footer that has, on
the left, context or a quiet red action, and on the right *Cancel* and the
one primary button (Settings applies each change at once, so it has only
×). Folded rows inside a form are split by thin lines; nothing that isn't
needed is shown by default. No separate screens.

The title names the dialog, not the thing: a tab's or an environment's name
is the first field, so it isn't said twice.

### New tab

Mockup: `NewTab.dc.html`. From the sidebar's + menu (⌥N) or *New Claude /
Codex / terminal* in a context menu. Its title follows the kind (*New
Claude*, *New Codex*, *New terminal*), and a small switch at the top
(Claude · Codex · Terminal, with their glyphs) lets you change your mind.
It asks for two things:

- **Name**, first and focused; empty, with the folder name as placeholder;
  left empty, the tab is named after the folder (claude can rename it
  later, `/rename`).
- **Directory**, prefilled with the last directory a tab was started in
  **in that environment** (most people start every claude in one folder,
  locally or in a codespace). Typing completes paths from that
  environment's backend, like Tab in a shell, so it works the same over
  SSH, Docker and WSL. The folder button opens the system folder picker,
  shown for *this Mac* (or Windows/Linux) only.

The **group isn't asked**: it comes from where you clicked (the header
says *in local / api / payments*). The sidebar + puts it next to the
current tab, in the same environment and group.

Below, for an agent only, two folded rows, both closed by default (a
terminal has neither; its footer says *Opens your default shell*):

- **Startup message**, a switch (the switch alone says off; no "none"
  beside it); on, a text box appears, sent after the global one from
  Settings.
- **Arguments**, a disclosure whose summary shows what will be used
  (`skip permissions · chrome`) while it's closed; open, the summary hides
  and the fields say it: one text field for anything extra, and under it
  two checkboxes for that agent's common flags: Claude
  `--dangerously-skip-permissions` and `--chrome`; Codex `--approve-for-me` and
  `--search`. A checkbox disappears if its flag is typed in the field.

**Everything below Name repeats the last tab of that kind you started**: same switches,
same text, same checkboxes, custom or not. The very first time: no startup
message, both checkboxes on. The footer says *Same options as your last
Claude tab* (or Codex); *Cancel*, *Start* (Enter).

### Tab settings

Mockup: `TabSettings.dc.html`. From **…** in the header, *Tab settings…*
in the context menu, or ⌘I. The same form with the tab's values; header:
*Tab settings*, then `env / group · session id`.

Name changes apply at once (see *Renaming*). Directory and arguments only
take effect when the agent restarts, so changing either shows a notice
(*Saving restarts claude in this tab. The conversation continues where it
was.*) and the button becomes *Save and restart*. The startup message is
read each time the agent starts, so changing it just saves: it is sent at
the next start, with no restart now. *Close tab* sits on the
left, in red. Moving to another group is done in the tree (drag, or *Move
to…*), not here.

### Settings

Mockup: `Settings.dc.html`. The gear in the sidebar, or ⌘,. A modal
with short sections: a small heading each and a thin line between
sections, none between rows. One row per setting:

- **Appearance**: Theme (Auto / Light / Dark), Terminal colours, Terminal
  scroll bar (off by default), Terminal font size, Tab rows (Detailed /
  Compact).
- **Tabs**: Ask before closing a tab; Startup message for every agent tab
  (a switch; on, a text box, sent before each tab's own).
- **Notifications**: When a tab needs me, When a tab finishes, Only while I
  am in another window. If the browser hasn't been asked yet: *This browser
  asks first · Allow*.
- **Updates**, two rows: *Check every [N] days* with the on/off switch at
  the end (the field greys out when off); then one line that reads as a
  sentence: *0.9.2 · checked today, 14:20 · Check now*, with *Check now* a
  text link (while it runs: *checking…*, and the link hides), and *Update
  to 0.9.3* on the right only when a release is out (it restarts the app).
- A fixed footer: state folder and *Open log*.

Environments are managed in the tree (right-click an environment; *Connect
environment* under the last one), not here. New-tab defaults aren't here
either: new tabs repeat the last one.

### Connect environment

Mockup: `ConnectEnv.dc.html`. *Connect environment* under the last
environment. Kind as a segmented control (*SSH host*, *Docker container*,
and *WSL distro* on Windows), then the target with a short label and its
hint under it (for SSH: *host, user@host or an alias from ~/.ssh/config; it
must take your key without a password prompt*), suggestions from the
system, and Name (defaults to the target). *Cancel*, *Connect*.

### Environment settings

Mockup: `EnvSettings.dc.html`. *Environment settings…* in an environment's
menu. Name (this machine keeps its own), then facts: Status (with latency),
Platform, then one line per agent: Claude, Codex (version and path, or
*not installed*), Tabs. *Remove* on the left in red (not for this machine),
*Cancel*, *Save*.

### Agent not installed

Mockup: `MissingAgent.dc.html`. Environments don't warn about a missing
agent up front. The first time you start one where it isn't installed
(*New Codex* on build-box, say), this opens instead of New tab: *Codex
isn't installed on build-box*, the environment as subtitle, one line, the
official install command it will run, and *Installed it yourself just now?
Check again.* Footer: *Check again* (a quiet link) on the left, *Cancel*,
*Install*. Install shows its progress here, then goes on to New tab. A
terminal never needs this.

## Jump (⌘K)

Mockup: `Jump.dc.html`. Fuzzy search over tabs, waiting tabs first, with
the same glyphs as the sidebar and the state in words (*Needs you*, *Done,
unseen*, *Working*, *Running*, *Idle*, *Exited*); typing `claude`, `codex`
or `term` keeps that kind. Then a few actions (New Claude, New Codex, New
terminal, New group, New window, Tab settings, Settings). ↑↓ move, ↵ open,
⌘⌫ closes the tab, esc closes the list. *Move to…* uses the same list to
pick a group.

## Not now

- **Split view** (two terminals side by side): later.
- **Tabs on top**: dropped on purpose (see *Main window*). If it ever
  comes back, it would be a flat strip with one level of Chrome-style
  group chips; the data model doesn't change.
- Details side panel, filter chips, per-row branch lines, ⌥ number hints and
  the bottom status bar were tried and dropped as noise.
- Versions 2 (Photoshop-style) and 3 (VS Code-style) were tried and
  dropped; this design is v1 with the noise taken out.
- For unread, a bold name, dimmed read tabs, a faded dot, a blue marker, a
  strip, a square, a new colour, a check mark and a count were tried; the
  ring (now a halo) won.
- For the kinds, line glyphs (a spark, braces, `>`, `>_`, a raised hand for
  *needs you*) were tried; a grey `>` looked like a folded group's chevron.
  Plain filled shapes won; a cut-in asterisk or pinwheel on Codex was too
  fine at 14 px.

## The mockup files

`mockups/*.dc.html` are artboards of the design canvas; they need the
canvas runtime (`support.js`), so open them on the canvas
(https://claude.ai/artifact/6b129ec8-cb4b-4863-8820-b70f02942f5a) rather than
in a browser. `Main` is the window; the others import it with props
(`theme`, `rows`, `contextMenu`, `newGroupIn`, `activeTab`) or draw a dialog
over it. `Icon` shows the icon files in `assets/icon/svg/`.

| File | Shows |
|---|---|
| `Main.dc.html` | the window, dark, compact rows, Claude, Codex and terminal tabs |
| `Detailed.dc.html` | detailed rows |
| `Light.dc.html` | light theme |
| `ContextMenu.dc.html` | right-click on a group: New Claude / Codex / terminal |
| `NewGroup.dc.html` | a new group named in place |
| `PlusMenu.dc.html` | the + menu |
| `RenameTab.dc.html` | a tab renamed in place |
| `NewTab.dc.html` | New Claude (the kind switch on top) |
| `TabSettings.dc.html` | Tab settings, edited, with the restart notice |
| `Settings.dc.html` | Settings |
| `Jump.dc.html` | Jump (⌘K) |
| `ConnectEnv.dc.html` | Connect environment |
| `EnvSettings.dc.html` | Environment settings |
| `MissingAgent.dc.html` | an agent not installed, asked on first start |
| `Glyphs.dc.html` | every kind in every state, real size and 4× |
| `Icon.dc.html` | the icon, small sizes, favicon dots |
