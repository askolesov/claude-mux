# UI redesign (v4)

The page stays a simple tool: tabs, the live terminal, and nothing that
says the same thing twice. This is the concept behind the mockups in
[`mockups/`](mockups/) and on the design canvas
(https://claude.ai/artifact/6b129ec8-cb4b-4863-8820-b70f02942f5a).

## Principles

- **One signal per fact.** A tab's state is its dot. No pill, badge or
  label repeats it.
- **Details live where they are asked for.** Branch, directory, session id,
  args are in the header line or in Tab settings, not on every row.
- **Settings for the window, Tab settings for a tab.** No right-click-only
  features: everything in the context menu also has a visible place.
- Two themes, dark and light (Auto follows the system); one accent; Inter
  for the UI, JetBrains Mono for the terminal and paths.

## Icon

Stacked terminals: three windows, the front one with a `>_` prompt and an
accent outline. Files in [`icon/`](icon/):

- `icon.svg`: the app icon (`.app`, `.exe`, release page), dark tile. Used
  in both themes, so the app looks the same everywhere.
- `icon-light.svg`: light tile, for places that need one (light docs,
  README badges).
- `icon-small.svg`: 16–24 px (favicon, sidebar mark, window title): two
  layers and heavier strokes so it stays readable.

The favicon keeps the waiting count the page already draws on it.

## Themes

The same names in both; mockup `V4Main.dc.html` takes `theme`
(`dark` / `light`), `V4Light.dc.html` shows light.

| Token | Dark | Light | Used for |
|---|---|---|---|
| `bg` | `#0F1115` | `#FFFFFF` | page, header |
| `panel` | `#14171C` | `#F6F7F9` | sidebar, top strip |
| `line` | `#252A33` | `#E3E6EB` | borders |
| `field` | `#1B1F26` | `#ECEEF2` | inputs, Jump field |
| `sel` | `#232834` | `#E2E7F0` | selected tab |
| `term` | `#0B0D10` | `#FBFBFC` | terminal background |
| `text` | `#E6E8EC` | `#1D2129` | main text |
| `text2` | `#C4C9D3` | `#3D4452` | secondary text |
| `muted` | `#8A92A2` | `#626A7A` | labels, paths |
| `accent` | `#7AA2F7` | `#2F5FD0` | links, primary button, focus |
| `accentSoft` | `#1B2233` | `#E8EEFB` | update notice |
| `working` | `#7AA2F7` | `#2F5FD0` | status dot |
| `ready` | `#4FD18B` | `#1F9D5C` | status dot, healthy env |
| `needs` | `#F5B14C` | `#B86F0A` | status dot, env problem |
| `exited` | `#6B7383` | `#9AA1AD` | hollow ring |

The light status colours are darker so the dots stay readable on white. The
terminal theme is separate (Settings › Appearance) and defaults to matching
the page.

## Main window

Mockups: `V4Main.dc.html` (tabs on the left), `V4TopTabs.dc.html` (on top).

**Sidebar (tabs on the left)**

- Top row: logo mark, `claude-mux`, settings.
- **Jump to tab ⌘K**: the only search; opens the switcher.
- **Environment header**: a collapse chevron, the name (semibold, main
  text colour), the kind in a small outlined tag (`this Mac`, `ssh`,
  `docker`), then on the right the health dot (green connected, amber
  problem) and a short state (`connected`, `42 ms`, `no claude`). An
  environment without claude says *No claude here · Install*.
- **The tree** (see *Tab tree* below): environments at the top, then
  groups and tabs, groups nesting to any depth. Groups show a chevron and
  their name; tabs a status dot.
- **Tab row**: the status dot, then two lines: the name (plus ↻ when it has
  a startup message) and the directory in small mono. Nothing else.
  Settings › Appearance › *Tab rows* switches between **Detailed** (two
  lines, the default) and **Compact** (name only, one line); mockup
  `V4Compact.dc.html`.
- **Connect environment…** is a dashed row after the last environment:
  adding an environment extends the list it belongs to.
- At the bottom, only when a release is out: *0.9.3 is out · Update and
  restart*.


**Tabs on top** (Settings → Tab position): one strip for every environment,
`[env] (tab) (tab) [env] (tab) …`. An environment is a small square-cornered
chip (kind icon, name, health dot; click for a new tab there), separated
from the one before by a divider; its tabs follow as rounded pills (dot +
name), then a round **+** for a new tab in that environment. After the last
environment, a dashed **Connect** chip. Groups don't show in the strip. On
the right: *Update to …* when one is out, and settings.

**Above the terminal**: the tab's name, then
`env / group / subgroup · directory · branch` in mono, and **…**, which opens Tab
settings. No status pill: the dot already says it.

**Terminal**: fills the whole area under that line, edge to edge, on the
`term` background. No inset, border or rounded frame.

## Tab tree

Mockups: `V4Main.dc.html`, and `V4Tree.dc.html` with the add menu open on
a nested group.

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

- Environment: chevron · name · kind tag · health dot · state · **+**
- Group: chevron · name · **+** (collapsed: also the most urgent dot inside
  and the tab count)
- Tab: status dot · name / directory

Each level indents 16 px, with a thin guide line under each group's
chevron so deep tabs still show where they belong.

**Adding.** The **+** on an environment or group opens a two-item menu
right under it: *New tab* and *New group*. Both create the node inside
that container; a new group starts with its name selected for typing.
⌥N makes a new tab next to the current one (same parent).

**Collapsing.** The chevron (or ←/→) collapses a container. A collapsed
group keeps one summary: the most urgent status inside (needs you >
finished unseen > working > ready > exited) and how many tabs it holds,
so nothing waiting gets hidden. Collapse state is kept per browser.

**Moving.** Drag any tab or group:

- onto a group or environment: into it, at the end;
- between rows: a line shows the drop point, and its left edge shows the
  depth it lands at (drag left or right to go up or down a level);
- a group can't go into itself or its own descendants.

Without a mouse: Tab settings › Group (pick any container), and ⌘⇧↑/↓ to
move within the parent.

**Other actions** (right-click, or the same keys in the tree):
rename (F2, or double-click a group), delete group (its contents move up to
the parent; no tab is ever closed by deleting a group), close tab (⌘W,
asks first).

**Keyboard and accessibility.** The sidebar is an ARIA `tree`; rows are
`treeitem`s with `aria-level` and, for containers, `aria-expanded`. ↑/↓
move, → expands or steps into, ← collapses or steps out to the parent,
Enter opens a tab, Home/End jump, typing a name jumps to it.

**Depth.** Any depth is allowed; past three group levels the indent stops
growing and only the guides show the depth, so names keep their width.

## Status

| State | Dot | Name |
|---|---|---|
| Working | blue `#7AA2F7` | regular |
| Ready | green `#4FD18B` | regular |
| Finished while you looked elsewhere | green | **bold** until opened |
| Needs you (permission, question) | amber `#F5B14C` with a soft ring | **bold** |
| Exited | hollow grey ring | grey |

The selected tab has a filled row background. The window title still counts
what waits: `(2) claude-mux`.

## Tab settings

Mockup: `V4TabSettings.dc.html`. A dialog from **…** in the header (and the
context menu). Header: name, then `env / group · branch · session id`.

- Name, Group (pick one, *None*, or *New group…*), Directory.
- Startup message.
- **Extra arguments**: one plain text field, the single source of truth.
  Under it, checkboxes for frequent flags: `--dangerously-skip-permissions`,
  `--chrome`, `--model opus`, `--permission-mode acceptEdits`.
  A checkbox **disappears when the text already has that flag** (matched by
  flag name, so `--model sonnet` hides `--model opus`). A flag can't be
  both typed and checked.
- One mono line shows exactly what runs:
  `$ claude --resume <id> <checked flags> <typed args>`.
- Footer: *Close tab* (red), *Restart*, then *Cancel* / *Save*. Directory and
  arguments apply on the next restart.

The New tab dialog uses the same fields, with Environment first.

## Settings

Mockup: `Settings.dc.html` (v1). A page, not a modal, with sections on the
left; *Back to tabs* returns.

- **Tabs & layout**: tab position (left / top, as two picture cards),
  confirm before closing, sort (manual / waiting first).
- **Appearance**: page theme (Auto / Light / Dark), terminal theme,
  terminal font and size, tab rows (Compact / Detailed).
- **New tabs**: default environment, directory, extra arguments, and which
  of the frequent flags start checked.
- **Notifications**: needs you, finished, exited; only when not looking;
  count in title and Dock.
- **Environments**: list with kind, platform, tabs and status; Add,
  Reconnect, Remove, Install claude.
- **Updates**: *Check for updates every N days*, *Check now*; when a
  release is out, *Update and restart* / *Skip this version*
  (`~/.claude-mux/hub.json`).
- **Shortcuts**: the key list.

## Jump (⌘K)

Mockup: `Palette.dc.html` (v1). Fuzzy search over tabs, waiting tabs first,
then a few actions (New tab, Tab settings, Settings). ↑↓ move, ↵ open,
esc closes.

## Not now

- **Split view** (two terminals side by side): designed in v1, later.
- Details side panel, filter chips, per-row branch lines, ⌥ number hints and
  the bottom status bar were tried and dropped as noise.
- Versions 2 (Photoshop-style) and 3 (VS Code-style) were tried and
  dropped; v4 is v1 with the noise taken out.

## The mockup files

`Icons.dc.html` compares the five icon concepts; D (stacked terminals) was
picked. `*.dc.html` are artboards of the design canvas. They need the canvas
runtime (`support.js`), so open them on the canvas rather than directly in a
browser. `V4Main` takes `layout` (`left` / `top`) and `activeTab` props;
the others import it. `Main.dc.html` is the v1 window that `Palette.dc.html`
draws over.
