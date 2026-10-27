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
- The two main actions, always visible: **New tab** (primary, ⌥N) and
  **Connect** (an SSH host, Docker container or WSL distro). Not hidden in
  settings or menus.
- **Jump to tab ⌘K**: the only search; opens the switcher.
- **Environment header**: a collapse chevron, the name (semibold, main
  text colour), the kind in a small outlined tag (`this Mac`, `ssh`,
  `docker`), then on the right the health dot (green connected, amber
  problem) and a short state (`connected`, `42 ms`, `no claude`). An
  environment without claude says *No claude here · Install*.
- **Group**: a small semibold muted label, indented under its environment,
  with the tab count on the right.
- **Tab row** (indented under the environment): the status dot, then two
  lines: the name (plus ↻ when it has a startup message) and the directory
  in small mono. Nothing else.
- At the bottom, only when a release is out: *0.9.3 is out · Update and
  restart*.

**Tabs on top** (Settings → Tab position): one strip for every environment,
`[env] (tab) (tab) [env] (tab) …`. An environment is a small square-cornered
chip (kind icon, name, health dot; click for a new tab there), separated
from the one before by a divider; its tabs follow as rounded pills (dot +
name). Groups don't show in the strip. On the right: *Update to …* when one
is out, **New tab**, **Connect** and settings.

**Above the terminal**: the tab's name, then
`env / group · directory · branch` in mono, and **…**, which opens Tab
settings. No status pill: the dot already says it.

**Terminal**: fills the whole area under that line, edge to edge, on the
`term` background. No inset, border or rounded frame.

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
  terminal font and size.
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
