const $ = (id) => document.getElementById(id);
const store = {
  get(k) { try { return localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch {} },
};
const isMac = /Mac/.test(navigator.platform);
const MOD = isMac ? "⌘" : "Ctrl+";

// The hub reports the environments; each environment's backend reports its
// tabs. A tab's key is "<environment>/<id>".
let envs = [];                   // [{name, kind, target, state, platform, latency, step, error}]
let update = null;               // what the hub knows about updates
let hubSettings = null;          // {checkUpdates, everyDays, resume: {enabled, message}, page}
let hubVersion = "", hubPlatform = "";
const envData = new Map();       // environment -> its backend's state: {claude, sessions, defaultCwd}
const streams = new Map();       // environment -> EventSource of its backend's state
let tabs = [];                   // every environment's tabs in sidebar order: {...session, env, key}
let selected = null;             // the selected tab's key
let pendingSelect = store.get("mux.selected"); // the last selection, until its tab shows up
const terms = new Map();         // tab key -> {term, fit, ws, el, closed}
const lastStatus = new Map();    // tab key -> status, for notifications

const STATUS_TEXT = { starting: "Starting", working: "Working", ready: "Ready", "needs-you": "Needs you", exited: "Exited", stopped: "Stopped" };
const FLAGS = ["--dangerously-skip-permissions", "--chrome", "--model opus", "--permission-mode acceptEdits"];
const KIND_TEXT = { local: "This machine", ssh: "SSH", docker: "Docker", wsl: "WSL" };

// ---------- page settings ----------
// Kept by the hub in hub.json (settings.page), so the app's window and a
// browser see the same; a copy in this browser paints the first frame.
const PAGE_DEFAULTS = {
  layout: "left", confirmClose: true, sort: "manual",
  theme: "auto", termTheme: "auto", termFont: "JetBrains Mono", termSize: 13,
  newEnv: "", newCwd: "", newArgs: "", newFlags: [],
  tabRows: "two",
  notifyNeeds: true, notifyReady: true, notifyExited: false, notifyAway: true, badge: true,
};
let page = { ...PAGE_DEFAULTS, ...(() => { try { return JSON.parse(store.get("mux.page") || "{}"); } catch { return {}; } })() };
let saving = null; // a settings save waiting to go out

// Before v0.5 the page kept its choices in this browser only.
function oldPageSettings() {
  const old = {};
  if (store.get("mux.theme")) old.theme = store.get("mux.theme");
  if (store.get("mux.termTheme")) old.termTheme = store.get("mux.termTheme") === "tokyo-night" ? "tokyo" : store.get("mux.termTheme");
  if (store.get("mux.lastArgs")) old.newArgs = store.get("mux.lastArgs");
  const flags = [store.get("mux.skip") === "1" && FLAGS[0], store.get("mux.chrome") === "1" && FLAGS[1]].filter(Boolean);
  if (flags.length) old.newFlags = flags;
  return old;
}

function setPage(patch) {
  page = { ...page, ...patch };
  store.set("mux.page", JSON.stringify(page));
  applyPage();
  saveSettings({});
}

// saveSettings sends the hub every setting at once, a moment after the last
// change.
function saveSettings(patch) {
  hubSettings = { ...(hubSettings || {}), ...patch, page };
  clearTimeout(saving);
  saving = setTimeout(async () => {
    saving = null;
    if (!hubSettings.everyDays) return; // the hub's settings haven't come yet
    await request("PUT", "/hub/settings", hubSettings).catch((err) => say(err.message));
  }, 300);
}

function applyPage() {
  if (page.theme === "auto") document.documentElement.dataset.theme = systemDark.matches ? "dark" : "light";
  else document.documentElement.dataset.theme = page.theme;
  document.body.classList.toggle("top", page.layout === "top");
  document.body.classList.toggle("compact", page.tabRows === "one");
  const th = termTheme();
  for (const t of terms.values()) {
    t.term.options.theme = th;
    t.term.options.fontFamily = termFontFamily();
    t.term.options.fontSize = page.termSize;
    t.el.style.background = th.background;
  }
  fitSelected();
  render();
}

// ---------- api ----------
async function request(method, url, body) {
  const res = await fetch(url, {
    method,
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  let data = null;
  try { data = res.status === 204 ? null : await res.json(); } catch {}
  if (!res.ok) {
    const err = new Error(data?.error || res.statusText);
    err.data = data;
    throw err;
  }
  return data;
}
// api calls an environment's backend, through the hub.
const api = (env, method, path, body) => request(method, `/env/${encodeURIComponent(env)}${path}`, body);

// streamLines POSTs to url and hands each JSON line of the answer to onMsg
// as it comes.
async function streamLines(url, onMsg) {
  const res = await fetch(url, { method: "POST" });
  if (!res.ok) throw new Error((await res.json().catch(() => null))?.error || res.statusText);
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += value;
    let i;
    while ((i = buf.indexOf("\n")) >= 0) {
      onMsg(JSON.parse(buf.slice(0, i)));
      buf = buf.slice(i + 1);
    }
  }
}

// ---------- helpers ----------
function ago(ts) {
  const s = Math.max(0, (Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 60) return `${Math.floor(s)}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const home = (p) => String(p || "").replace(/^\/(Users|home)\/[^/]+/, "~");
const waiting = (t) => t.status === "needs-you" || (t.status === "ready" && t.unseen);
const tab = (key) => tabs.find((t) => t.key === key);
const env = (name) => envs.find((e) => e.name === name);
const dead = (t) => t.status === "exited" || t.status === "stopped";
const sessionsOf = (name) => envData.get(name)?.sessions || [];
const groupsOf = (name) => [...new Set(sessionsOf(name).map((s) => s.group).filter(Boolean))];
const bold = (t) => t.key === selected || t.unseen || t.status === "needs-you";
const rank = (t) => (t.status === "needs-you" ? 0 : t.status === "ready" && t.unseen ? 1 : 2);
const hasClaude = (name) => !!envData.get(name)?.claude?.path;
// health: ok (connected, with claude), problem (failed, or no claude) or connecting.
function health(e) {
  if (e.state === "connected") return envData.get(e.name) && !hasClaude(e.name) ? "problem" : "ok";
  return e.state === "failed" ? "problem" : "connecting";
}
function envMeta(e) {
  const parts = [e.kind === "local" ? KIND_TEXT.local : `${KIND_TEXT[e.kind] || e.kind} ${e.target}`];
  if (e.platform) parts.push(e.platform.replace("-", "/"));
  if (e.state === "connected" && e.kind !== "local" && e.latency) parts.push(`${e.latency} ms`);
  else if (e.state !== "connected") parts.push(e.state);
  return parts.join(" · ");
}
// kindTag is the small tag after an environment's name.
function kindTag(e) {
  if (e.kind !== "local") return e.kind;
  return { darwin: "this Mac", windows: "this PC" }[hubPlatform.split("-")[0]] || "this machine";
}
// envState is the environment's state in a word or two, beside its dot.
function envState(e) {
  if (e.state === "connected") return envData.get(e.name) && !hasClaude(e.name) ? "no claude" : e.kind === "local" ? "connected" : `${e.latency || "<1"} ms`;
  return e.state;
}
const KIND_ICON = {
  local: `<path d="M4 5h16v11H4zM2 19h20"/>`,
  ssh: `<rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/>`,
  docker: `<path d="M12 3 20 7.5v9L12 21l-8-4.5v-9zM12 12l8-4.5M12 12v9M12 12 4 7.5"/>`,
  wsl: `<path d="m5 8 4 4-4 4M12 16h7"/>`,
};
const kindIcon = (kind) => `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${KIND_ICON[kind] || KIND_ICON.ssh}</svg>`;
const CHEVRON = `<svg class="chev" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>`;
const PLUS = (size) => `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>`;
const PLUG = (size) => `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 2v5M15 2v5M6 7h12v4a6 6 0 0 1-12 0zM12 17v5"/></svg>`;
// A tab's own resume message shows as ↻; the default doesn't.
const ownResume = (t) => t.resume === "custom" && t.resumeMessage;

// ---------- groups ----------
// Collapsing a group is per browser, per environment.
const collapsed = new Set((() => { try { return JSON.parse(store.get("mux.collapsed") || "[]"); } catch { return []; } })());
function setCollapsed(key, shut) {
  shut ? collapsed.add(key) : collapsed.delete(key);
  store.set("mux.collapsed", JSON.stringify([...collapsed]));
}
// grouped splits items into runs: each group, at its first item, and every
// ungrouped item on its own.
function grouped(items, groupOf) {
  const runs = [];
  const byName = new Map();
  for (const it of items) {
    const g = groupOf(it);
    if (!g) { runs.push({ group: "", items: [it] }); continue; }
    if (!byName.has(g)) { byName.set(g, { group: g, items: [] }); runs.push(byName.get(g)); }
    byName.get(g).items.push(it);
  }
  return runs;
}
function urgent(list) {
  if (list.some((t) => t.status === "needs-you")) return "needs-you";
  if (list.some((t) => t.status === "ready" && t.unseen)) return "ready";
  if (list.some((t) => t.status === "working" || t.status === "starting")) return "working";
  return "";
}
// The tabs in the order they show: by environment, then as the user
// dragged them, or waiting ones first within each group.
function ordered(list) {
  if (page.sort !== "waiting") return list;
  return [...list].sort((a, b) => rank(a) - rank(b));
}
function visual() {
  const out = [];
  for (const e of envs) {
    for (const run of grouped(tabs.filter((t) => t.env === e.name), (t) => t.group)) out.push(...ordered(run.items));
  }
  return out;
}

// ---------- icon ----------
// The small mark (assets/icon/svg/icon-small.svg), with a dot when
// something waits: amber for a tab that needs you, green for one that
// finished unseen.
function markSvg(size, dot) {
  const badge = dot ? `<circle cx="52" cy="12" r="11" fill="${dot}" stroke="#0F1115" stroke-width="3"/>` : "";
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 64 64">` +
    `<rect width="64" height="64" rx="14" fill="#161A21"/><rect x="20" y="10" width="34" height="27" rx="5" fill="#2E3542"/>` +
    `<rect x="10" y="22" width="36" height="30" rx="5" fill="#0B0D10" stroke="#7AA2F7" stroke-width="4"/>` +
    `<path d="M17 31l6 5-6 5" fill="none" stroke="#E6E8EC" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"/>${badge}</svg>`;
}
$("mark1").innerHTML = markSvg(20);
function favicon(dot) {
  const href = "data:image/svg+xml," + encodeURIComponent(markSvg(64, dot));
  if ($("favicon").href !== href) $("favicon").href = href;
}

// ---------- rendering ----------
let dragKey = null;       // the tab being dragged; renders wait for the drop
let dragEnv = null;       // the environment being dragged, by its name
let dragGroup = null;     // the group being dragged: {env, group}
let renderPending = false;
let lastBadge = null;

function render() {
  if (dragKey || dragEnv || dragGroup) { renderPending = true; return; }
  const waitingCount = tabs.filter(waiting).length;
  const needs = tabs.some((t) => t.status === "needs-you");
  document.title = waitingCount && page.badge ? `(${waitingCount}) claude-mux` : "claude-mux";
  favicon(needs ? "#F5B14C" : waitingCount ? "#4FD18B" : null);
  const badge = page.badge && waitingCount ? String(waitingCount) : "";
  if (window.cmuxBadge && badge !== lastBadge) { lastBadge = badge; window.cmuxBadge(badge); }
  renderList();
  renderStrip();
  renderHead();
  renderUpdate();
}

// The sidebar: a block per environment (folds by its header), its groups
// with their counts, its tabs and a New tab row; Connect after the last.
function renderList() {
  let html = "";
  for (const e of envs) {
    const own = tabs.filter((t) => t.env === e.name);
    const eshut = collapsed.has("env:" + e.name);
    const edot = eshut ? urgent(own) : "";
    html += `<div class="envblock" data-env="${esc(e.name)}"><div class="envhead ${eshut ? "shut" : ""}" data-env="${esc(e.name)}" title="${esc(envMeta(e))} · click to ${eshut ? "show" : "hide"} · drag to reorder · right-click for actions" draggable="true">
      ${CHEVRON}<span class="ename">${esc(e.name)}</span><span class="kind">${esc(kindTag(e))}</span>${edot ? `<span class="dot ${edot}"></span>` : ""}
      <span class="gap"></span><span class="hdot ${health(e)}"></span><span class="estate">${esc(envState(e))}</span></div>`;
    if (eshut) { html += `</div>`; continue; }
    html += envNote(e);
    for (const run of grouped(own, (t) => t.group)) {
      if (!run.group) { html += tabRow(run.items[0]); continue; }
      const key = e.name + "/" + run.group;
      const shut = collapsed.has(key);
      const dot = shut ? urgent(run.items) : "";
      html += `<div class="glabel" data-env="${esc(e.name)}" data-group="${esc(run.group)}" title="Click to ${shut ? "show" : "hide"} · drag to move the group · right-click for actions" draggable="true">
        <span class="gname">${esc(run.group)}</span>${shut ? `<span class="shut">▸</span>` : ""}${dot ? `<span class="dot ${dot}"></span>` : ""}<span class="count">${run.items.length}</span></div>`;
      if (!shut) for (const t of ordered(run.items)) html += tabRow(t);
    }
    // A new tab opens where it was asked for; without claude, envNote offers Install.
    if (e.state === "connected" && hasClaude(e.name)) html += `<button class="newrow" data-new-env="${esc(e.name)}" title="New tab in ${esc(e.name)}">${PLUS(13)}New tab</button>`;
    html += `</div>`;
  }
  html += `<button class="connectrow" data-connect title="An SSH host, a Docker container or a WSL distro">${PLUG(13)}Connect environment…</button>`;
  $("list").innerHTML = html;
}

// envNote says what's wrong with an environment, if anything, with what
// can be done about it.
function envNote(e) {
  const n = esc(e.name);
  if (e.state === "failed") return `<div class="envnote problem">${esc(e.error)} · <button class="link" data-env-act="reconnect" data-env="${n}">Reconnect</button></div>`;
  if (e.state === "connecting") return `<div class="envnote">${esc(e.step ? e.step + "…" : "Connecting…")}</div>`;
  if (envData.get(e.name) && !hasClaude(e.name)) {
    return `<div class="envnote">No claude here · <button class="link" data-env-act="install" data-env="${n}">Install</button> · <button class="link" data-env-act="retry" data-env="${n}">Retry</button></div>`;
  }
  return "";
}

// A tab row: its dot, then its name and, in Detailed rows, its directory.
function tabRow(t) {
  const cls = ["tab", selected === t.key && "active", bold(t) && "bold", dead(t) && "dead"].filter(Boolean).join(" ");
  const mark = ownResume(t) ? `<span class="mark" title="Its own message when it starts again: ${esc(t.resumeMessage)}">↻</span>` : "";
  return `<button class="${cls}" data-key="${esc(t.key)}" title="${esc(STATUS_TEXT[t.status] || t.status)} for ${ago(t.statusSince)} · ${esc(home(t.cwd))}" draggable="true">
    <span class="dot ${esc(t.status)}"></span><span class="lines"><span class="line1"><span class="name">${esc(t.name)}</span>${mark}</span>
    ${page.tabRows === "one" ? "" : `<span class="cwd">${esc(home(t.cwd))}</span>`}</span></button>`;
}

// Tabs on top: one strip for every environment, each a chip (click for a
// new tab there), its tabs and a +; Connect after the last. Groups don't show.
function renderStrip() {
  if (page.layout !== "top") return;
  let html = `<span style="margin:0 10px 0 4px;display:flex">${markSvg(20)}</span>`;
  for (const e of envs) {
    html += `<div class="senv"><button class="echip" data-env="${esc(e.name)}" title="${esc(envMeta(e))}${e.error ? " · " + esc(e.error) : ""} · click for a new tab here">${kindIcon(e.kind)}${esc(e.name)}<span class="hdot ${health(e)}"></span></button>`;
    if (e.state === "connected" && envData.get(e.name) && !hasClaude(e.name)) html += `<button class="install" data-env-act="install" data-env="${esc(e.name)}">Install claude</button>`;
    for (const t of visual().filter((x) => x.env === e.name)) {
      const cls = ["ttab", selected === t.key && "active", bold(t) && "bold", dead(t) && "dead"].filter(Boolean).join(" ");
      html += `<button class="${cls}" data-key="${esc(t.key)}" title="${esc(home(t.cwd))}"><span class="dot ${esc(t.status)}"></span><span class="name">${esc(t.name)}</span></button>`;
    }
    if (e.state === "connected" && hasClaude(e.name)) html += `<button class="plus" data-new-env="${esc(e.name)}" title="New tab in ${esc(e.name)}" aria-label="New tab in ${esc(e.name)}">${PLUS(13)}</button>`;
    html += `</div>`;
  }
  html += `<button class="sconnect" data-connect title="An SSH host, a Docker container or a WSL distro">${PLUG(13)}Connect</button>`;
  html += `<span style="flex:1"></span>`;
  if (update?.available) html += `<button class="update" data-strip="update">Update to ${esc(update.latest)}</button>`;
  html += `<button class="icon-btn" data-strip="settings" title="Settings (${MOD},)" aria-label="Settings" style="width:32px;height:32px"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1"/><circle cx="15" cy="6" r="2"/><circle cx="9" cy="12" r="2"/><circle cx="17" cy="18" r="2"/></svg></button>`;
  $("strip").innerHTML = html;
}

// Above the terminal: the name, where it runs, and … for Tab settings.
function renderHead() {
  const t = tab(selected);
  if (!t) { $("head").innerHTML = `<span class="crumb">Pick a tab${page.layout === "top" ? "" : " on the left"}.</span>`; return; }
  const crumb = [t.env + (t.group ? " / " + t.group : ""), home(t.cwd), t.branch].filter(Boolean).join("  ·  ");
  $("head").innerHTML = `<span class="tname">${esc(t.name)}</span><span class="crumb" title="${esc(t.cwd)}">${esc(crumb)}</span>
    <button class="icon-btn" id="tabMore" title="Tab settings" aria-label="Tab settings" style="width:32px;height:32px"><svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/></svg></button>`;
}

function renderUpdate() {
  const box = $("updatebar");
  if (update?.updating) { box.hidden = false; box.innerHTML = `<span>Updating: ${esc(update.step || "starting")}…</span>`; return; }
  box.hidden = !update?.available;
  if (box.hidden) return;
  box.innerHTML = `<span>${esc(update.latest)} is out</span><button class="link" data-up="apply">Update and restart</button>`;
  // Tell once per version, even across reloads.
  if (store.get("mux.toldVersion") !== update.latest) {
    store.set("mux.toldVersion", update.latest);
    notify("claude-mux", `${update.latest} is out: update and restart from the sidebar.`);
  }
}

// ---------- themes ----------
// The terminal theme is separate from the page's; Match page follows it.
const TERM_THEMES = {
  dark: { background: "#0B0D10", foreground: "#D7DAE0", cursor: "#7AA2F7", selectionBackground: "#33467c" },
  light: {
    background: "#FBFBFC", foreground: "#1D2129", cursor: "#2F5FD0", selectionBackground: "#c8d6f5",
    black: "#1d2129", red: "#c4262e", green: "#1f8a4c", yellow: "#946300", blue: "#2f5fd0", magenta: "#8a3fc7", cyan: "#13808c", white: "#6a7282",
    brightBlack: "#5b6170", brightRed: "#d6373f", brightGreen: "#23a05a", brightYellow: "#a87200", brightBlue: "#3d6fe0", brightMagenta: "#9c4fd9", brightCyan: "#1793a0", brightWhite: "#3a404c",
  },
  tokyo: {
    background: "#1a1b26", foreground: "#c0caf5", cursor: "#c0caf5", selectionBackground: "#33467c",
    black: "#15161e", red: "#f7768e", green: "#9ece6a", yellow: "#e0af68", blue: "#7aa2f7", magenta: "#bb9af7", cyan: "#7dcfff", white: "#a9b1d6",
    brightBlack: "#414868", brightRed: "#f7768e", brightGreen: "#9ece6a", brightYellow: "#e0af68", brightBlue: "#7aa2f7", brightMagenta: "#bb9af7", brightCyan: "#7dcfff", brightWhite: "#c0caf5",
  },
  dracula: {
    background: "#282a36", foreground: "#f8f8f2", cursor: "#f8f8f2", selectionBackground: "#44475a",
    black: "#21222c", red: "#ff5555", green: "#50fa7b", yellow: "#f1fa8c", blue: "#bd93f9", magenta: "#ff79c6", cyan: "#8be9fd", white: "#f8f8f2",
    brightBlack: "#6272a4", brightRed: "#ff6e6e", brightGreen: "#69ff94", brightYellow: "#ffffa5", brightBlue: "#d6acff", brightMagenta: "#ff92df", brightCyan: "#a4ffff", brightWhite: "#ffffff",
  },
  "solarized-dark": {
    background: "#002b36", foreground: "#93a1a1", cursor: "#93a1a1", selectionBackground: "#073642",
    black: "#073642", red: "#dc322f", green: "#859900", yellow: "#b58900", blue: "#268bd2", magenta: "#d33682", cyan: "#2aa198", white: "#eee8d5",
    brightBlack: "#586e75", brightRed: "#cb4b16", brightGreen: "#859900", brightYellow: "#b58900", brightBlue: "#268bd2", brightMagenta: "#6c71c4", brightCyan: "#2aa198", brightWhite: "#fdf6e3",
  },
  "solarized-light": {
    background: "#fdf6e3", foreground: "#586e75", cursor: "#586e75", selectionBackground: "#eee8d5",
    black: "#073642", red: "#dc322f", green: "#859900", yellow: "#b58900", blue: "#268bd2", magenta: "#d33682", cyan: "#2aa198", white: "#657b83",
    brightBlack: "#586e75", brightRed: "#cb4b16", brightGreen: "#859900", brightYellow: "#b58900", brightBlue: "#268bd2", brightMagenta: "#6c71c4", brightCyan: "#2aa198", brightWhite: "#073642",
  },
};
const TERM_THEME_NAMES = [["auto", "Match page"], ["tokyo", "Tokyo Night"], ["dracula", "Dracula"], ["solarized-dark", "Solarized Dark"], ["solarized-light", "Solarized Light"], ["light", "Light"], ["dark", "Dark"]];
const TERM_FONTS = ["JetBrains Mono", "SF Mono", "Menlo", "Monaco", "Consolas", "Cascadia Code", "Fira Code", "Source Code Pro"];
const systemDark = matchMedia("(prefers-color-scheme: dark)");
const pageIsDark = () => document.documentElement.dataset.theme !== "light";
function termTheme() {
  const k = page.termTheme === "auto" ? (pageIsDark() ? "dark" : "light") : page.termTheme;
  return TERM_THEMES[k] || TERM_THEMES.dark;
}
const termFontFamily = () => `"${page.termFont}", "JetBrains Mono", "SF Mono", Menlo, Consolas, monospace`;
systemDark.addEventListener("change", applyPage);

// ---------- terminals ----------
function ensureTerm(key) {
  if (terms.has(key)) return terms.get(key);
  const el = document.createElement("div");
  el.className = "term";
  $("stage").appendChild(el);
  const term = new Terminal({
    fontFamily: termFontFamily(),
    fontSize: page.termSize,
    lineHeight: 1.15,
    cursorBlink: true,
    allowProposedApi: true,
    scrollback: 20000,
    theme: termTheme(),
  });
  el.style.background = termTheme().background;
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon.WebLinksAddon());
  term.open(el);
  const t = { term, fit, el, ws: null, closed: false, retry: 0 };

  // Shift+Enter: ESC CR, which Claude Code reads as a newline in the prompt.
  term.attachCustomKeyEventHandler((e) => {
    if (e.key === "Enter" && e.shiftKey) {
      if (e.type === "keydown") send(t, { t: "in", d: "\x1b\r" });
      return false;
    }
    return true;
  });
  // In a tab whose claude exited, Enter resumes it.
  term.onData((d) => {
    const cur = tab(key);
    if (cur && dead(cur)) {
      if (d === "\r") api(cur.env, "POST", `/api/sessions/${cur.id}/start`).catch((err) => say(err.message));
      return;
    }
    send(t, { t: "in", d });
  });
  term.onResize(({ cols, rows }) => send(t, { t: "resize", c: cols, r: rows }));
  connect(key, t);
  terms.set(key, t);
  return t;
}

function send(t, msg) {
  if (t.ws && t.ws.readyState === WebSocket.OPEN) t.ws.send(JSON.stringify(msg));
}

function connect(key, t) {
  const { env, id } = tab(key);
  const ws = new WebSocket(`ws://${location.host}/env/${encodeURIComponent(env)}/api/sessions/${encodeURIComponent(id)}/term`);
  ws.binaryType = "arraybuffer";
  t.ws = ws;
  ws.onopen = () => {
    t.retry = 0;
    t.term.reset(); // the server replays its scrollback first
    send(t, { t: "resize", c: t.term.cols, r: t.term.rows });
    const cur = tab(key);
    if (cur && dead(cur)) exitedLine(t);
  };
  ws.onmessage = (e) => t.term.write(new Uint8Array(e.data));
  ws.onclose = () => {
    if (t.closed) return;
    t.retry = Math.min(t.retry + 1, 6);
    setTimeout(() => { if (!t.closed && tab(key)) connect(key, t); }, 500 * t.retry);
  };
}

function exitedLine(t) {
  setTimeout(() => t.term.write("\r\n\x1b[33mclaude exited. Press Enter to resume.\x1b[0m\r\n"), 50);
}

function dropTerm(key) {
  const t = terms.get(key);
  if (!t) return;
  t.closed = true;
  t.ws?.close();
  t.term.dispose();
  t.el.remove();
  terms.delete(key);
}

function fitSelected() {
  terms.get(selected)?.fit.fit();
}

// ---------- selection ----------
function select(key) {
  const t = tab(key);
  if (!t) return;
  selected = key;
  store.set("mux.selected", key);
  if (t.group) setCollapsed(t.env + "/" + t.group, false);
  setCollapsed("env:" + t.env, false);
  for (const [k, x] of terms) x.el.classList.toggle("shown", k === key);
  $("placeholder").style.display = "none";
  const term = ensureTerm(key);
  term.el.classList.add("shown");
  requestAnimationFrame(() => { fitSelected(); term.term.focus(); });
  markSeen(key);
  render();
}

function markSeen(key) {
  const t = tab(key);
  if (t?.unseen && document.visibilityState === "visible") api(t.env, "POST", `/api/sessions/${t.id}/seen`).catch(() => {});
}

function step(dir) {
  const list = visual();
  if (!list.length) return;
  const i = list.findIndex((t) => t.key === selected);
  select(list[(i + dir + list.length) % list.length].key);
}

// ⌥J: the next tab that waits for you, permission prompts first.
function jumpWaiting() {
  const list = visual();
  const start = Math.max(0, list.findIndex((t) => t.key === selected));
  const rot = [...list.slice(start + 1), ...list.slice(0, start + 1)];
  const next = rot.find((t) => t.status === "needs-you") || rot.find((t) => t.status === "ready" && t.unseen);
  if (next) select(next.key);
}

// ---------- notifications ----------
function notify(title, body, onClick) {
  // In claude-mux's own window the hub posts the notification.
  if (window.cmuxNotify) { window.cmuxNotify(title, body); return; }
  if (typeof Notification === "undefined" || Notification.permission !== "granted") return;
  const n = new Notification(title, { body });
  n.onclick = () => { window.focus(); onClick?.(); n.close(); };
}

function notifyTransitions() {
  for (const t of tabs) {
    const prev = lastStatus.get(t.key);
    lastStatus.set(t.key, t.status);
    if (!prev || prev === t.status) continue;
    const term = terms.get(t.key);
    if (t.status === "starting" || prev === "exited" || prev === "stopped") {
      if (term && term.ws?.readyState !== WebSocket.OPEN) { term.retry = 0; connect(t.key, term); }
    }
    if (t.status === "exited" && term) exitedLine(term);
    const wanted = { "needs-you": page.notifyNeeds, ready: page.notifyReady, exited: page.notifyExited }[t.status];
    if (!wanted) continue;
    const away = document.visibilityState !== "visible" || !document.hasFocus();
    if (page.notifyAway ? !away : away === false && selected === t.key) continue;
    const body = { "needs-you": "needs you", ready: "finished", exited: "exited" }[t.status];
    notify(t.name, body, () => select(t.key));
  }
}

// ---------- state streams ----------
// refresh rebuilds the flat tab list after any state arrived.
function refresh() {
  tabs = envs.flatMap((e) => sessionsOf(e.name).map((s) => ({ ...s, env: e.name, key: e.name + "/" + s.id })));
  for (const key of [...terms.keys()]) if (!tab(key)) dropTerm(key);
  if (selected && !tab(selected)) {
    selected = null;
    $("placeholder").style.display = "";
  }
  if (pendingSelect && tab(pendingSelect)) { select(pendingSelect); pendingSelect = null; }
  notifyTransitions();
  if (selected) markSeen(selected);
  render();
  if ($("settingsPage").classList.contains("open") && section === "environments") renderSettings();
}

// Each connected environment streams its backend's state; one that goes
// away takes its tabs with it.
function syncStreams() {
  for (const e of envs) {
    const es = streams.get(e.name);
    if (e.state !== "connected" || (es && es.readyState !== EventSource.CLOSED)) continue;
    const next = new EventSource(`/env/${encodeURIComponent(e.name)}/api/events`);
    next.onmessage = (m) => {
      const st = JSON.parse(m.data);
      envData.set(e.name, { ...st, sessions: st.sessions || [] });
      refresh();
    };
    streams.set(e.name, next);
  }
  for (const [name, es] of streams) {
    if (env(name)) continue;
    es.close();
    streams.delete(name);
    envData.delete(name);
  }
}

function streamHub() {
  const es = new EventSource("/hub/events");
  es.onmessage = (m) => {
    const hub = JSON.parse(m.data);
    // A new hub (an update, a restart) serves a new page.
    if (hubVersion && hub.version !== hubVersion) { location.reload(); return; }
    hubVersion = hub.version;
    hubPlatform = hub.platform;
    envs = hub.envs || [];
    update = hub.update;
    if (!saving) {
      hubSettings = hub.settings;
      if (hub.settings?.page) {
        page = { ...PAGE_DEFAULTS, ...hub.settings.page };
        store.set("mux.page", JSON.stringify(page));
      } else {
        page = { ...page, ...oldPageSettings() };
        saveSettings({});
      }
      applyPage();
    }
    syncStreams();
    refresh();
    if ($("settingsPage").classList.contains("open") && ["updates", "environments", "resume"].includes(section) && !document.activeElement?.matches("input, textarea")) renderSettings();
  };
}

// ---------- tab settings and new tab ----------
// One dialog for both. Extra arguments are one text, the single truth; the
// frequent flags are checkboxes, each hidden once the text has that flag.
let dlg = null; // {mode: "new"|"edit", key, env, resume, checked: Set}

const words = (s) => s.trim().split(/\s+/).filter(Boolean);
const typed = (flag) => words($("tArgs").value).includes(flag.split(" ")[0]);
// splitArgs takes the frequent flags out of a tab's args: checked, the rest.
function splitArgs(args) {
  let rest = [...(args || [])];
  const checked = new Set();
  for (const f of FLAGS) {
    const parts = f.split(" ");
    for (let i = 0; i + parts.length <= rest.length; i++) {
      if (parts.every((p, j) => rest[i + j] === p)) { checked.add(f); rest.splice(i, parts.length); break; }
    }
  }
  return { checked, rest: rest.join(" ") };
}
function dlgArgs() {
  return [...FLAGS.filter((f) => dlg.checked.has(f) && !typed(f)), ...words($("tArgs").value)].join(" ");
}

function renderChecks() {
  $("tChecks").innerHTML = FLAGS.filter((f) => !typed(f))
    .map((f) => `<label><input type="checkbox" data-flag="${esc(f)}" ${dlg.checked.has(f) ? "checked" : ""}>${esc(f)}</label>`).join("");
  const t = dlg.mode === "edit" ? tab(dlg.key) : null;
  const resume = t?.claudeSessionId ? ` --resume ${t.claudeSessionId.slice(0, 8)}` : "";
  const args = dlgArgs();
  $("tCmd").textContent = `$ claude${resume}${args ? " " + args : ""}`;
}
$("tArgs").addEventListener("input", renderChecks);
$("tChecks").addEventListener("change", (e) => {
  const f = e.target.dataset.flag;
  if (!f) return;
  e.target.checked ? dlg.checked.add(f) : dlg.checked.delete(f);
  renderChecks();
});

// The resume message: Default (the one in Settings), Custom or None.
function renderResume() {
  const modes = [["", "Default"], ["custom", "Custom"], ["off", "None"]];
  $("tResume").innerHTML = modes.map(([v, l]) => `<button type="button" data-mode="${v}" aria-pressed="${dlg.resume === v}">${l}</button>`).join("");
  const def = hubSettings?.resume || {};
  const ta = $("tResumeMsg");
  ta.hidden = dlg.resume === "off";
  ta.readOnly = dlg.resume === "";
  if (dlg.resume === "") ta.value = def.message || "";
  ta.style.color = dlg.resume === "" ? "var(--muted)" : "";
  $("tResumeHint").textContent = dlg.resume === "" ? (def.enabled ? "Sent as its first prompt whenever its claude starts again (Settings › When tabs start again)." : "The default is off in Settings › When tabs start again, so nothing is sent.")
    : dlg.resume === "custom" ? "Sent as its first prompt whenever its claude starts again, instead of the default." : "Nothing is sent when it starts again.";
}
$("tResume").addEventListener("click", (e) => {
  const b = e.target.closest("button[data-mode]");
  if (!b) return;
  if (dlg.resume === "custom") dlg.custom = $("tResumeMsg").value;
  dlg.resume = b.dataset.mode;
  if (dlg.resume === "custom") $("tResumeMsg").value = dlg.custom ?? "";
  renderResume();
  if (dlg.resume === "custom") $("tResumeMsg").focus();
});

function fillGroups(name, current) {
  const gs = groupsOf(name);
  $("tGroup").innerHTML = [`<option value="">None</option>`, ...gs.map((g) => `<option value="${esc(g)}">${esc(g)}</option>`), `<option value="\u0000new">New group…</option>`].join("");
  $("tGroup").value = gs.includes(current) ? current : current ? "\u0000new" : "";
  $("tNewGroupRow").hidden = $("tGroup").value !== "\u0000new";
  $("tNewGroup").value = gs.includes(current) ? "" : current || "";
}
$("tGroup").addEventListener("change", () => {
  $("tNewGroupRow").hidden = $("tGroup").value !== "\u0000new";
  if (!$("tNewGroupRow").hidden) $("tNewGroup").focus();
});
const dlgGroup = () => ($("tGroup").value === "\u0000new" ? $("tNewGroup").value.trim() : $("tGroup").value);

function fillCwds(name) {
  const data = envData.get(name);
  const cwds = new Set([data?.defaultCwd, page.newCwd, ...sessionsOf(name).map((s) => s.cwd)]);
  $("cwds").innerHTML = [...cwds].filter(Boolean).map((c) => `<option value="${esc(c)}">`).join("");
}

function openTabSettings(key) {
  const t = tab(key);
  if (!t) return;
  const { checked, rest } = splitArgs(t.args);
  dlg = { mode: "edit", key, env: t.env, resume: t.resume || "", custom: t.resumeMessage || "", checked };
  $("tTitle").textContent = t.name;
  $("tMeta").textContent = [t.env + (t.group ? " / " + t.group : ""), t.branch, t.claudeSessionId?.slice(0, 8)].filter(Boolean).join(" · ");
  $("tEnvRow").hidden = true;
  $("tName").value = t.name;
  $("tName").readOnly = true;
  $("tNameHint").textContent = "Type /rename <name> in the tab to rename it.";
  fillGroups(t.env, t.group || "");
  fillCwds(t.env);
  $("tCwd").value = t.cwd;
  $("tResumeMsg").value = t.resumeMessage || "";
  $("tArgs").value = rest;
  $("tClose").hidden = false;
  $("tRestart").hidden = false;
  $("tRestart").textContent = dead(t) ? "Resume" : "Restart";
  $("tOk").textContent = "Save";
  $("tErr").textContent = "";
  renderResume();
  renderChecks();
  $("tabDlg").showModal();
  $("tCwd").focus();
}

function openNew(prefill = {}) {
  const sel = tab(selected);
  const connected = envs.filter((e) => e.state === "connected" && hasClaude(e.name));
  const envName = prefill.env ?? (page.newEnv && env(page.newEnv) ? page.newEnv : sel?.env) ?? connected[0]?.name ?? "local";
  dlg = { mode: "new", env: envName, resume: "", custom: "", checked: new Set(page.newFlags || []) };
  $("tTitle").textContent = "New tab";
  $("tMeta").textContent = "";
  $("tEnvRow").hidden = envs.length < 2;
  $("tEnv").innerHTML = connected.map((e) => `<option value="${esc(e.name)}">${esc(e.name)}</option>`).join("");
  $("tEnv").value = envName;
  $("tName").value = "";
  $("tName").readOnly = false;
  $("tName").placeholder = "defaults to the folder name";
  $("tNameHint").textContent = "";
  fillNewEnv(prefill.group ?? (sel && sel.env === envName ? sel.group || "" : ""));
  $("tResumeMsg").value = "";
  $("tArgs").value = page.newArgs || "";
  $("tClose").hidden = true;
  $("tRestart").hidden = true;
  $("tOk").textContent = "Start";
  $("tErr").textContent = "";
  renderResume();
  renderChecks();
  $("tabDlg").showModal();
  $("tName").focus();
}
// The directory and groups offered depend on the environment.
function fillNewEnv(group) {
  const name = $("tEnv").value || dlg.env;
  dlg.env = name;
  fillGroups(name, group);
  fillCwds(name);
  $("tCwd").value = store.get("mux.lastCwd." + name) || page.newCwd || envData.get(name)?.defaultCwd || "";
}
$("tEnv").addEventListener("change", () => fillNewEnv(""));

$("tabForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const resumeMessage = dlg.resume === "custom" ? $("tResumeMsg").value.trim() : (dlg.custom || "");
  const body = { group: dlgGroup(), cwd: $("tCwd").value.trim(), args: dlgArgs(), resume: dlg.resume, resumeMessage };
  if (dlg.resume === "custom" && !resumeMessage) { $("tErr").textContent = "Write the message, or pick Default or None."; return; }
  try {
    if (dlg.mode === "edit") {
      const t = tab(dlg.key);
      await api(t.env, "PUT", `/api/sessions/${t.id}`, body);
      $("tabDlg").close();
      return;
    }
    const name = dlg.env;
    const s = await api(name, "POST", "/api/sessions", { ...body, name: $("tName").value.trim() });
    store.set("mux.lastCwd." + name, body.cwd);
    $("tabDlg").close();
    const data = envData.get(name);
    if (data && !data.sessions.some((x) => x.id === s.id)) data.sessions.push(s);
    refresh();
    select(name + "/" + s.id);
  } catch (err) {
    $("tErr").textContent = err.message;
  }
});
$("tCancel").onclick = $("tX").onclick = () => $("tabDlg").close();
$("tClose").onclick = async () => { const key = dlg.key; $("tabDlg").close(); await act("close", { kind: "tab", key }); };
$("tRestart").onclick = async () => {
  const t = tab(dlg.key);
  $("tErr").textContent = "";
  if (t.status === "working" && !(await sure(`Restart ${t.name}?`, "It is working: the turn in progress is cut off. Its conversation continues.", "Restart"))) return;
  try {
    await api(t.env, "POST", `/api/sessions/${t.id}/${dead(t) ? "start" : "restart"}`);
    $("tabDlg").close();
  } catch (err) {
    $("tErr").textContent = err.message;
  }
};

// ---------- environments ----------
const kindHints = {
  ssh: ["Host, as ssh takes it: host, user@host or an alias from ~/.ssh/config. It must take your key without a password prompt.", "devbox"],
  docker: ["Container, running, with sh in it. Its tabs live as long as the container.", "my-container"],
  wsl: ["WSL distro, as wsl -l lists it.", "Ubuntu"],
};
let hubInfo = null;
async function fillTargets() {
  const kind = $("hKind").value;
  [$("hHint").textContent, $("hHost").placeholder] = kindHints[kind] || ["", ""];
  const names = await request("GET", `/hub/targets/${encodeURIComponent(kind)}`).catch(() => []);
  $("targets").innerHTML = (names || []).map((n) => `<option value="${esc(n)}">`).join("");
}
$("hKind").onchange = fillTargets;
async function addEnv() {
  hubInfo ||= await request("GET", "/hub/info").catch(() => ({ kinds: ["ssh"] }));
  $("hKind").innerHTML = hubInfo.kinds.map((k) => `<option value="${k}">${{ ssh: "SSH host", docker: "Docker container", wsl: "WSL distro" }[k] || k}</option>`).join("");
  $("hHost").value = "";
  $("hName").value = "";
  $("hostErr").textContent = "";
  fillTargets();
  $("hostDlg").showModal();
  $("hHost").focus();
}
$("hostCancel").onclick = () => $("hostDlg").close();
$("hostForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  try {
    await request("POST", "/hub/environments", { kind: $("hKind").value, target: $("hHost").value, name: $("hName").value });
    $("hostDlg").close();
  } catch (err) {
    $("hostErr").textContent = err.message;
  }
});

// Install, look for claude again, reconnect, remove.
async function envAct(what, name) {
  if (what === "retry") { await api(name, "POST", "/api/claude/check").catch((err) => say(err.message)); return; }
  if (what === "reconnect") {
    const n = tabs.filter((t) => t.env === name).length;
    if (n && !(await sure(`Reconnect ${name}?`, `Its backend starts again: its ${n} tab${n > 1 ? "s are" : " is"} hung up and resumed.`, "Reconnect"))) return;
    await request("POST", `/hub/environments/${encodeURIComponent(name)}/reconnect`).catch((err) => say(err.message));
    return;
  }
  if (what === "remove") {
    if (await sure(`Remove ${name}?`, "It is disconnected and taken off the list. Its tabs are hung up and kept there: adding it again brings them back.", "Remove")) {
      await request("DELETE", `/hub/environments/${encodeURIComponent(name)}`).catch((err) => say(err.message));
    }
    return;
  }
  $("iTitle").textContent = `Install claude on ${name}`;
  $("iCmd").textContent = envData.get(name)?.claude?.installer || "";
  $("iOut").textContent = "";
  $("iOut").hidden = true;
  $("iRun").hidden = false;
  $("iRun").disabled = false;
  $("iClose").textContent = "Cancel";
  $("iState").textContent = "";
  $("iRun").onclick = async () => {
    $("iRun").disabled = true;
    $("iOut").hidden = false;
    $("iOut").textContent = "";
    const started = Date.now();
    const tick = () => { $("iState").textContent = `Installing… ${Math.round((Date.now() - started) / 1000)} s`; };
    tick();
    const timer = setInterval(tick, 1000);
    let result = { error: "the connection to the installer broke" };
    try {
      // The installer's output comes as JSON lines while it runs.
      await streamLines(`/env/${encodeURIComponent(name)}/api/claude/install`, (msg) => {
        if (msg.out === undefined) { result = msg; return; }
        const out = $("iOut");
        out.textContent += msg.out.replace(/\x1b\[[0-9;?]*[A-Za-z]/g, "");
        out.scrollTop = out.scrollHeight;
      });
    } catch (err) {
      result = { error: err.message };
    }
    clearInterval(timer);
    const secs = Math.round((Date.now() - started) / 1000);
    if (result.done) {
      const c = envData.get(name)?.claude;
      $("iState").textContent = `Done in ${secs} s${c?.version ? ": " + c.version : ""}. In a new tab there, claude asks you to log in the first time.`;
    } else {
      $("iState").textContent = `The installer failed after ${secs} s: ${result.error}`;
    }
    $("iRun").hidden = true;
    $("iClose").textContent = "Close";
  };
  $("iClose").onclick = () => $("installDlg").close();
  $("installDlg").showModal();
}

// ---------- updates ----------
async function applyUpdate() {
  const working = tabs.filter((t) => t.status === "working");
  const text = working.length
    ? `${working.length} tab${working.length > 1 ? "s are" : " is"} working (${working.map((t) => t.name).join(", ")}): the turn${working.length > 1 ? "s" : ""} in progress get${working.length > 1 ? "" : "s"} cut off. Every tab resumes after the restart.`
    : "Every tab is hung up and resumes after the restart.";
  if (!(await sure(`Update to ${update.latest} and restart?`, text, "Update and restart"))) return;
  let result = { error: "the connection to the hub broke" };
  try {
    await streamLines("/hub/update/apply", (msg) => { if (!msg.step) result = msg; });
  } catch (err) {
    result = { error: err.message };
  }
  if (result.error) say(`The update failed: ${result.error}`);
  // On success the hub restarts; the page reloads when the new one answers.
}
$("updatebar").addEventListener("click", (e) => { if (e.target.closest("[data-up=apply]")) applyUpdate(); });

// ---------- settings page ----------
const SECTIONS = [["tabs", "Tabs & layout"], ["appearance", "Appearance"], ["new", "New tabs"], ["resume", "When tabs start again"],
  ["notifications", "Notifications"], ["environments", "Environments"], ["updates", "Updates"], ["shortcuts", "Shortcuts"], ["about", "About"]];
let section = "tabs";

function openSettings(sec) {
  if (sec) section = sec;
  $("settingsPage").classList.add("open");
  renderSettings();
}
function closeSettings() {
  $("settingsPage").classList.remove("open");
  terms.get(selected)?.term.focus();
}
$("sBack").onclick = closeSettings;

const sw = (key, label, help) => `<div class="srow"><div class="sl"><b>${label}</b>${help ? `<small>${help}</small>` : ""}</div>
  <button class="switch" role="switch" aria-checked="${!!page[key]}" aria-label="${esc(label)}" data-page="${key}"></button></div>`;
const seg = (key, opts) => `<span class="seg">${opts.map(([v, l]) => `<button type="button" data-page-set="${key}" data-v="${v}" aria-pressed="${page[key] === v}">${l}</button>`).join("")}</span>`;
const row = (label, help, control) => `<div class="srow"><div class="sl"><b>${label}</b>${help ? `<small>${help}</small>` : ""}</div>${control}</div>`;

function renderSettings() {
  $("sNav").innerHTML = SECTIONS.map(([k, l]) => `<button data-sec="${k}" aria-current="${k === section ? "page" : "false"}">${l}</button>`).join("");
  const S = {
    tabs: () => `<h2>Tabs &amp; layout</h2><p class="lead">Where the tabs sit and how they're ordered.</p>
      <div class="slabel">Tab position</div>
      <div class="cards">
        <button class="card" data-page-set="layout" data-v="left" aria-pressed="${page.layout === "left"}">
          <div class="pic"><div class="ps"><i style="width:70%"></i><i></i><i style="width:80%"></i><i style="width:60%"></i></div><div class="pm"><div class="pterm"></div></div></div>
          <b>Left sidebar</b><small>Environments and groups as blocks. Best past ten tabs.</small></button>
        <button class="card" data-page-set="layout" data-v="top" aria-pressed="${page.layout === "top"}">
          <div class="pic"><div class="pm"><div class="pt"><i></i><i style="background:var(--accent)"></i><i></i><i></i></div><div class="pterm"></div></div></div>
          <b>Top strip</b><small>Browser-style tabs, one environment at a time. Widest terminal.</small></button>
      </div>
      ${sw("confirmClose", "Confirm before closing a tab", "Closing ends its claude and removes it for good.")}
      ${row("Sort tabs", "Manual keeps your drag order. Waiting first floats the tabs that need you, within each group.", seg("sort", [["manual", "Manual"], ["waiting", "Waiting first"]]))}`,
    appearance: () => `<h2>Appearance</h2><p class="lead">The page and the terminal separately. Claude Code picks its own colours with /theme.</p>
      ${row("Page theme", "Auto follows the system.", seg("theme", [["auto", "Auto"], ["light", "Light"], ["dark", "Dark"]]))}
      <div class="slabel">Terminal theme</div>
      <div class="tcards">${TERM_THEME_NAMES.map(([k, l]) => {
        const th = k === "auto" ? termTheme() : TERM_THEMES[k];
        return `<button class="tcard" data-page-set="termTheme" data-v="${k}" aria-pressed="${page.termTheme === k}">
          <div class="tp" style="background:${th.background}"><i style="width:70%;background:${th.foreground}"></i><i style="width:45%;background:${th.blue || th.cursor}"></i><i style="width:58%;background:${th.green || th.foreground}"></i></div>${l}</button>`;
      }).join("")}</div>
      ${row("Terminal font", "Any monospace font installed on this machine.",
        `<select class="input" style="width:180px;margin:0" id="sFont">${TERM_FONTS.map((f) => `<option ${f === page.termFont ? "selected" : ""}>${f}</option>`).join("")}</select>
         <span class="stepper"><button data-font="-1" aria-label="Smaller">−</button><span>${page.termSize} px</span><button data-font="1" aria-label="Larger">+</button></span>`)}
      ${row("Tab rows", "Compact: one line, the name. Detailed: two lines, the name and its directory.", seg("tabRows", [["one", "Compact"], ["two", "Detailed"]]))}`,
    new: () => `<h2>New tabs</h2><p class="lead">What the New tab dialog starts from. Each tab can still change it.</p>
      <div class="grid2">
        <label class="field">Environment<select id="sNewEnv"><option value="">The one in view</option>${envs.map((e) => `<option value="${esc(e.name)}" ${e.name === page.newEnv ? "selected" : ""}>${esc(e.name)}</option>`).join("")}</select></label>
        <label class="field">Directory<input id="sNewCwd" class="mono-in" value="${esc(page.newCwd)}" placeholder="the environment's default"></label>
      </div>
      <label class="field">Extra arguments<input id="sNewArgs" class="mono-in" value="${esc(page.newArgs)}" placeholder="e.g. --permission-mode acceptEdits"></label>
      <div class="slabel">Checked to start with</div>
      ${FLAGS.map((f) => `<div class="srow"><div class="sl"><b class="mono" style="font-size:12.5px">${esc(f)}</b></div>
        <button class="switch" role="switch" aria-checked="${(page.newFlags || []).includes(f)}" data-flag="${esc(f)}"></button></div>`).join("")}`,
    resume: () => {
      const r = hubSettings?.resume || { enabled: false, message: "" };
      const own = tabs.filter((t) => t.resume === "custom").length, off = tabs.filter((t) => t.resume === "off").length;
      return `<h2>When tabs start again</h2><p class="lead">A tab's claude starts again when claude-mux restarts or updates, on Restart and on Resume. It continues its conversation,
        but whatever ran in the old process (background tasks, watchers, servers) has stopped. A message tells it so.</p>
        <div class="srow"><div class="sl"><b>Send a message to every tab</b><small>As its first prompt, whenever its claude starts again. Not on a new tab's first start.</small></div>
          <button class="switch" role="switch" aria-checked="${!!r.enabled}" id="sResumeOn"></button></div>
        <label class="field">Message<textarea id="sResumeMsg" rows="5">${esc(r.message)}</textarea></label>
        <span class="hint">Tab settings can give a tab its own message, or none.${own || off ? ` Now ${own} with their own, ${off} with none.` : ""}</span>`;
    },
    notifications: () => `<h2>Notifications</h2><p class="lead">System notifications for tabs you aren't looking at.</p>
      ${sw("notifyNeeds", "A tab needs you", "Permission prompts and questions before startup.")}
      ${sw("notifyReady", "A tab finished", "Claude stopped and is ready for input.")}
      ${sw("notifyExited", "A tab exited", "Its claude quit or crashed.")}
      ${sw("notifyAway", "Only when the window is in the background", "Stay quiet while you're looking at claude-mux.")}
      ${sw("badge", "Count waiting tabs in the title and Dock", "Shows as (2) claude-mux.")}
      ${!window.cmuxNotify && typeof Notification !== "undefined" && Notification.permission === "default" ? `<p><button class="btn" id="sAllow">Allow notifications in this browser</button></p>` : ""}
      ${!window.cmuxNotify && typeof Notification !== "undefined" && Notification.permission === "denied" ? `<p class="hint">This browser blocks notifications for this page.</p>` : ""}`,
    environments: () => `<h2>Environments</h2><p class="lead">Where tabs run: this machine, SSH hosts, Docker containers, WSL distros. Drag their headers in the sidebar to reorder.</p>
      ${envs.map((e) => {
        const n = tabs.filter((t) => t.env === e.name).length;
        const h = health(e);
        const status = e.state === "connected" ? (hasClaude(e.name) ? (e.kind === "local" ? "Connected" : `${e.latency || "<1"} ms`) : "No claude") : e.state === "connecting" ? (e.step || "Connecting") : "Failed";
        return `<div class="envrow"><span class="hdot ${h}"></span><b style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${esc(e.name)}</b>
          <span class="k">${esc(e.kind === "local" ? KIND_TEXT.local : `${KIND_TEXT[e.kind] || e.kind} ${e.target}`)}</span>
          <span class="k mono">${esc((e.platform || "").replace("-", "/"))}</span>
          <span class="k" title="${esc(e.error || "")}" style="color:${h === "problem" ? "var(--needs)" : h === "ok" ? "var(--ready)" : ""}">${esc(status)}${n ? ` · ${n} tab${n > 1 ? "s" : ""}` : ""}</span>
          <span class="acts">${e.state === "connected" && envData.get(e.name) && !hasClaude(e.name) ? `<button class="btn" data-env-act="install" data-env="${esc(e.name)}">Install…</button>` : ""}
            <button class="btn" data-env-act="reconnect" data-env="${esc(e.name)}">${e.kind === "local" ? "Restart" : "Reconnect"}</button>
            ${e.kind !== "local" ? `<button class="btn danger" data-env-act="remove" data-env="${esc(e.name)}">Remove</button>` : ""}</span></div>`;
      }).join("")}
      <p><button class="btn primary" id="sAddEnv">Connect…</button></p>`,
    updates: () => {
      const u = update || {};
      let state = `claude-mux ${esc(u.current || "?")}`;
      if (u.dev) state += ": a development build, which doesn't update.";
      else if (u.latest) state += `; ${esc(u.latest)} is out.`;
      else if (u.lastCheck) state += `, the latest (checked ${ago(u.lastCheck)} ago).`;
      else state += ", not checked yet.";
      if (u.error) state += ` The last check failed: ${esc(u.error)}`;
      if (u.updating) state = `Updating: ${esc(u.step || "starting")}…`;
      return `<h2>Updates</h2><p class="lead">Releases come from GitHub; an update replaces claude-mux where it is and restarts it. Every tab resumes.</p>
        <div class="srow"><div class="sl"><b>Check for updates</b><small>Every
          <input id="sDays" class="input" type="number" min="1" max="365" value="${hubSettings?.everyDays || 1}" style="display:inline-block;width:64px;height:26px;margin:0 4px"> days</small></div>
          <button class="switch" role="switch" aria-checked="${!!hubSettings?.checkUpdates}" id="sCheck"></button></div>
        <p class="note" style="color:var(--text2)">${state}</p>
        <p style="display:flex;gap:8px"><button class="btn" id="sNow" ${u.dev || u.updating ? "disabled" : ""}>Check now</button>
          ${u.available && !u.updating ? `<button class="btn primary" id="sApply">Update and restart</button><button class="btn" id="sSkip">Skip this version</button>` : ""}
          ${u.latest && u.notes ? `<button class="btn" id="sNotes">What's new</button>` : ""}</p>
        <p class="hint">Kept in ~/.claude-mux/hub.json.</p>`;
    },
    shortcuts: () => `<h2>Shortcuts</h2><p class="lead">They work while the terminal has focus.</p>
      <div class="keys">${[["Jump to a tab", MOD + "K"], ["New tab", "⌥N"], ["Go to tab 1–9", "⌥1–9"], ["Previous / next tab", "⌥↑ ⌥↓"], ["Next tab that waits for you", "⌥J"], ["Settings", MOD + ","], ["Newline in the prompt", "⇧↵"], ["Resume an exited tab", "↵"]]
        .map(([l, k]) => `<span>${l}</span><kbd>${k}</kbd>`).join("")}</div>`,
    about: () => `<h2>About</h2><p class="lead">claude-mux: tabs for Claude Code, here and on other machines.</p>
      ${row("Version", "", `<span class="mono">${esc(hubVersion)} · ${esc((hubPlatform || "").replace("-", "/"))}</span>`)}
      ${row("Hub", "Settings, environments, the update check.", `<span class="mono k">~/.claude-mux/hub.json</span>`)}
      ${row("Tabs", "In each environment, kept by its backend.", `<span class="mono k">~/.claude-mux/backend.json</span>`)}
      ${row("Log", "When started from Finder or Explorer.", `<span class="mono k">~/.claude-mux/hub.log</span>`)}
      ${row("Source", "", `<a href="https://github.com/askolesov/claude-mux" target="_blank" style="color:var(--accent-text)">github.com/askolesov/claude-mux</a>`)}`,
  };
  $("sContent").innerHTML = S[section]();
}

$("sNav").addEventListener("click", (e) => {
  const b = e.target.closest("button[data-sec]");
  if (b) { section = b.dataset.sec; renderSettings(); }
});
$("sContent").addEventListener("click", async (e) => {
  const t = e.target;
  const set = t.closest("[data-page-set]");
  if (set) { setPage({ [set.dataset.pageSet]: set.dataset.v }); renderSettings(); return; }
  const toggle = t.closest("button[data-page]");
  if (toggle) { setPage({ [toggle.dataset.page]: !page[toggle.dataset.page] }); renderSettings(); return; }
  const flag = t.closest("button[data-flag]");
  if (flag) {
    const f = flag.dataset.flag;
    const now = new Set(page.newFlags || []);
    now.has(f) ? now.delete(f) : now.add(f);
    setPage({ newFlags: FLAGS.filter((x) => now.has(x)) });
    renderSettings();
    return;
  }
  const font = t.closest("button[data-font]");
  if (font) { setPage({ termSize: Math.min(22, Math.max(10, page.termSize + Number(font.dataset.font))) }); renderSettings(); return; }
  const ea = t.closest("button[data-env-act]");
  if (ea) { envAct(ea.dataset.envAct, ea.dataset.env); return; }
  if (t.closest("#sAddEnv")) { addEnv(); return; }
  if (t.closest("#sAllow")) { await Notification.requestPermission(); renderSettings(); return; }
  if (t.closest("#sResumeOn")) {
    saveSettings({ resume: { ...(hubSettings?.resume || {}), enabled: !hubSettings?.resume?.enabled } });
    renderSettings();
    return;
  }
  if (t.closest("#sCheck")) { saveSettings({ checkUpdates: !hubSettings?.checkUpdates }); renderSettings(); return; }
  if (t.closest("#sNow")) {
    t.closest("#sNow").disabled = true;
    await request("POST", "/hub/update/check").catch(() => {});
    setTimeout(renderSettings, 150); // the event with the result comes first
    return;
  }
  if (t.closest("#sApply")) { closeSettings(); applyUpdate(); return; }
  if (t.closest("#sSkip")) { await request("POST", "/hub/update/skip").catch((err) => say(err.message)); return; }
  if (t.closest("#sNotes")) window.open(update.notes, "_blank");
});
$("sContent").addEventListener("change", (e) => {
  const t = e.target;
  if (t.id === "sFont") setPage({ termFont: t.value });
  if (t.id === "sNewEnv") setPage({ newEnv: t.value });
  if (t.id === "sDays") {
    const days = Number(t.value);
    if (days >= 1 && days <= 365) saveSettings({ everyDays: days });
  }
});
$("sContent").addEventListener("input", (e) => {
  const t = e.target;
  if (t.id === "sNewCwd") setPage({ newCwd: t.value.trim() });
  if (t.id === "sNewArgs") setPage({ newArgs: t.value });
  if (t.id === "sResumeMsg") saveSettings({ resume: { ...(hubSettings?.resume || {}), message: t.value } });
});

// ---------- quick switcher ----------
let pal = { items: [], on: 0 };
function openPalette() {
  $("palette").classList.add("open");
  $("pIn").value = "";
  renderPalette();
  $("pIn").focus();
}
function closePalette() {
  $("palette").classList.remove("open");
  terms.get(selected)?.term.focus();
}
// fuzzy: every character of q, in order, in s.
function fuzzy(s, q) {
  s = s.toLowerCase();
  let i = 0;
  for (const c of q.toLowerCase()) { i = s.indexOf(c, i); if (i < 0) return false; i++; }
  return true;
}
function renderPalette() {
  const raw = $("pIn").value;
  const actionsOnly = raw.startsWith(">");
  const q = actionsOnly ? raw.slice(1).trim() : raw.trim();
  const vis = visual();
  const found = actionsOnly ? [] : vis.filter((t) => fuzzy(`${t.name} ${t.env} ${t.group || ""} ${t.branch || ""}`, q))
    .sort((a, b) => rank(a) - rank(b) || vis.indexOf(a) - vis.indexOf(b));
  const actions = [["new", "New tab", "⌥N"], ["tab-settings", "Tab settings", ""], ["settings", "Settings", MOD + ","], ["add-env", "Connect an environment", ""]]
    .filter(([k, l]) => fuzzy(l, q) && (k !== "tab-settings" || selected));
  pal.items = [...found.map((t) => ({ tab: t })), ...actions.map((a) => ({ action: a }))];
  pal.on = Math.min(pal.on, Math.max(0, pal.items.length - 1));
  let html = "", i = 0;
  if (found.length) html += `<div class="sec">Tabs</div>`;
  for (const t of found) {
    const n = vis.indexOf(t);
    html += `<button class="item ${i === pal.on ? "on" : ""}" data-i="${i++}"><span class="dot ${esc(t.status)}"></span><span class="name">${esc(t.name)}</span>
      <span class="where">${esc([t.env + (t.group ? " / " + t.group : ""), t.branch].filter(Boolean).join(" · "))}</span>
      <span class="st">${esc(STATUS_TEXT[t.status] || t.status)}${t.status === "ready" && t.unseen ? ", unseen" : ""}</span><kbd>${n < 9 ? "⌥" + (n + 1) : ""}</kbd></button>`;
  }
  if (actions.length) html += `<div class="sec">Actions</div>`;
  for (const [, l, k] of actions) html += `<button class="item ${i === pal.on ? "on" : ""}" data-i="${i++}"><span class="name" style="max-width:none;flex:1">${esc(l)}</span><kbd>${k}</kbd></button>`;
  $("pItems").innerHTML = html || `<div class="empty" style="padding:12px">Nothing matches.</div>`;
  $("pItems").querySelector(".on")?.scrollIntoView({ block: "nearest" });
}
function runPalette(i) {
  const it = pal.items[i];
  if (!it) return;
  closePalette();
  if (it.tab) { select(it.tab.key); return; }
  const k = it.action[0];
  if (k === "new") openNew();
  if (k === "tab-settings") openTabSettings(selected);
  if (k === "settings") openSettings();
  if (k === "add-env") addEnv();
}
$("pIn").addEventListener("input", () => { pal.on = 0; renderPalette(); });
$("pIn").addEventListener("keydown", (e) => {
  if (e.key === "ArrowDown") { pal.on = Math.min(pal.on + 1, pal.items.length - 1); renderPalette(); e.preventDefault(); }
  if (e.key === "ArrowUp") { pal.on = Math.max(pal.on - 1, 0); renderPalette(); e.preventDefault(); }
  if (e.key === "Enter") { runPalette(pal.on); e.preventDefault(); }
  if (e.key === "Escape") { closePalette(); e.preventDefault(); }
});
$("pItems").addEventListener("click", (e) => { const b = e.target.closest("[data-i]"); if (b) runPalette(Number(b.dataset.i)); });
$("palette").addEventListener("pointerdown", (e) => { if (e.target === $("palette")) closePalette(); });
$("jump").onclick = openPalette;
$("jumpKey").textContent = MOD + "K";

// ---------- sidebar and strip ----------
$("new2").onclick = () => openNew();
$("settings").onclick = () => openSettings();
$("head").addEventListener("click", (e) => { if (e.target.closest("#tabMore")) openTabSettings(selected); });
$("strip").addEventListener("click", (e) => {
  const t = e.target.closest(".ttab");
  if (t) { select(t.dataset.key); return; }
  const ea = e.target.closest("button[data-env-act]");
  if (ea) { envAct(ea.dataset.envAct, ea.dataset.env); return; }
  const chip = e.target.closest(".echip");
  if (chip) { newIn(chip.dataset.env); return; }
  const plus = e.target.closest("[data-new-env]");
  if (plus) { openNew({ env: plus.dataset.newEnv }); return; }
  if (e.target.closest("[data-connect]")) { addEnv(); return; }
  const b = e.target.closest("[data-strip]")?.dataset.strip;
  if (b === "settings") openSettings();
  if (b === "update") applyUpdate();
});

// newIn opens New tab in an environment, or, while it can't take one,
// Settings › Environments, which says why.
function newIn(name) {
  const e = env(name);
  if (e?.state === "connected" && hasClaude(name)) openNew({ env: name });
  else openSettings("environments");
}

$("list").addEventListener("click", (e) => {
  const b = e.target.closest("button[data-env-act]");
  if (b) { envAct(b.dataset.envAct, b.dataset.env); return; }
  const nr = e.target.closest("[data-new-env]");
  if (nr) { openNew({ env: nr.dataset.newEnv }); return; }
  if (e.target.closest("[data-connect]")) { addEnv(); return; }
  const eh = e.target.closest(".envhead");
  if (eh) {
    const key = "env:" + eh.dataset.env;
    setCollapsed(key, !collapsed.has(key));
    render();
    return;
  }
  const head = e.target.closest(".glabel");
  if (head) {
    const key = head.dataset.env + "/" + head.dataset.group;
    setCollapsed(key, !collapsed.has(key));
    render();
    return;
  }
  const row = e.target.closest(".tab");
  if (row) select(row.dataset.key);
});

// Drag a tab to reorder it within its environment; the backend keeps the
// order. Dropped on a tab, a tab takes that tab's group; dropped on a
// group's label, it joins the group at its end. A group moves whole by its
// label, before or after another group or an ungrouped tab. Environments
// reorder by their headers.
function dropSide(el, e) {
  const r = el.getBoundingClientRect();
  return e.clientY < r.top + r.height / 2 ? "before" : "after";
}
function clearDrop() {
  for (const t of $("list").querySelectorAll(".drop-before, .drop-after, .drop-into")) t.classList.remove("drop-before", "drop-after", "drop-into");
}
function dropTarget(e) {
  const el = e.target.closest(".tab, .glabel");
  if (!dragKey || !el) return null;
  const elEnv = el.classList.contains("glabel") ? el.dataset.env : tab(el.dataset.key)?.env;
  return elEnv === tab(dragKey)?.env ? el : null;
}
// An environment drops anywhere on another one's block: its top half puts
// it before, its bottom half after.
const envTarget = (e) => (dragEnv ? e.target.closest(".envblock") : null);
// groupDrop is where a dragged group would land: before or after a whole
// other group, or an ungrouped tab, in the same environment; with the row
// to mark.
function groupDrop(e) {
  const el = dragGroup && e.target.closest(".tab, .glabel");
  if (!el) return null;
  const over = el.classList.contains("glabel") ? { env: el.dataset.env, group: el.dataset.group } : tab(el.dataset.key);
  if (!over || over.env !== dragGroup.env || (over.group || "") === dragGroup.group) return null;
  const side = dropSide(el, e);
  if (!over.group) return { side, before: over.id, mark: el };
  const members = sessionsOf(over.env).filter((s) => s.group === over.group);
  const label = $("list").querySelector(`.glabel[data-env="${CSS.escape(over.env)}"][data-group="${CSS.escape(over.group)}"]`);
  const lastRow = $("list").querySelector(`.tab[data-key="${CSS.escape(over.env + "/" + members.at(-1).id)}"]`);
  return side === "before" ? { side, before: members[0].id, mark: label } : { side, after: members.at(-1).id, mark: lastRow || label };
}
$("list").addEventListener("dragstart", (e) => {
  const head = e.target.closest(".envhead");
  const label = e.target.closest(".glabel");
  const row = e.target.closest(".tab");
  if (head) dragEnv = head.dataset.env;
  else if (label) { dragGroup = { env: label.dataset.env, group: label.dataset.group }; label.classList.add("dragging"); }
  else if (row) { dragKey = row.dataset.key; row.classList.add("dragging"); }
  else return;
  e.dataTransfer.effectAllowed = "move";
  e.dataTransfer.setData("text/plain", dragEnv || dragKey || dragGroup.group);
});
$("list").addEventListener("dragover", (e) => {
  const he = envTarget(e);
  if (he) {
    e.preventDefault();
    clearDrop();
    if (he.dataset.env !== dragEnv) he.classList.add("drop-" + dropSide(he, e));
    return;
  }
  if (dragGroup) {
    const at = groupDrop(e);
    clearDrop();
    if (!at) return;
    e.preventDefault();
    at.mark?.classList.add("drop-" + at.side);
    return;
  }
  const el = dropTarget(e);
  if (!el) return;
  e.preventDefault();
  clearDrop();
  if (el.classList.contains("glabel")) el.classList.add("drop-into");
  else if (el.dataset.key !== dragKey) el.classList.add("drop-" + dropSide(el, e));
});
$("list").addEventListener("drop", async (e) => {
  const he = envTarget(e);
  if (he) {
    e.preventDefault();
    if (he.dataset.env === dragEnv) return;
    const names = envs.map((x) => x.name).filter((n) => n !== dragEnv);
    names.splice(names.indexOf(he.dataset.env) + (dropSide(he, e) === "after" ? 1 : 0), 0, dragEnv);
    envs = names.map((n) => envs.find((x) => x.name === n));
    await request("PUT", "/hub/environments/order", { names }).catch((err) => say(err.message));
    return;
  }
  if (dragGroup) {
    const at = groupDrop(e);
    if (!at) return;
    e.preventDefault();
    const { env: name, group } = dragGroup;
    const data = envData.get(name);
    const members = data.sessions.filter((s) => s.group === group).map((s) => s.id);
    const ids = data.sessions.map((s) => s.id).filter((id) => !members.includes(id));
    ids.splice(at.before ? ids.indexOf(at.before) : ids.indexOf(at.after) + 1, 0, ...members);
    data.sessions = ids.map((id) => data.sessions.find((x) => x.id === id));
    dragGroup = null;
    refresh();
    await api(name, "PUT", "/api/sessions/order", { ids }).catch((err) => say(err.message));
    return;
  }
  const el = dropTarget(e);
  if (!el || el.dataset.key === dragKey) return;
  e.preventDefault();
  const moved = tab(dragKey);
  const data = envData.get(moved.env);
  const ids = data.sessions.map((s) => s.id).filter((x) => x !== moved.id);
  let group;
  if (el.classList.contains("glabel")) {
    group = el.dataset.group;
    const last = data.sessions.filter((s) => s.group === group && s.id !== moved.id).pop();
    ids.splice(last ? ids.indexOf(last.id) + 1 : ids.length, 0, moved.id);
  } else {
    const over = tab(el.dataset.key);
    group = over.group || "";
    ids.splice(ids.indexOf(over.id) + (dropSide(el, e) === "after" ? 1 : 0), 0, moved.id);
  }
  const regroup = (moved.group || "") !== group;
  data.sessions.find((x) => x.id === moved.id).group = group;
  data.sessions = ids.map((id) => data.sessions.find((x) => x.id === id));
  refresh();
  try {
    if (regroup) await api(moved.env, "PUT", `/api/sessions/${moved.id}/group`, { group });
    await api(moved.env, "PUT", "/api/sessions/order", { ids });
  } catch (err) {
    say(err.message);
  }
});
$("list").addEventListener("dragend", () => {
  dragEnv = null;
  dragKey = null;
  dragGroup = null;
  renderPending = false;
  clearDrop();
  render();
});

// ---------- context menu ----------
// Everything here also has a visible place: Tab settings, the group label,
// Settings › Environments.
function menuItems(target) {
  if (target.kind === "env") {
    const e = env(target.env);
    return [["new-in-env", "New tab here…"], ["env-reconnect", e?.kind === "local" ? "Restart backend" : "Reconnect"],
      e?.state === "connected" && envData.get(e.name) && !hasClaude(e.name) && ["env-install", "Install claude…"],
      e?.kind !== "local" && null, e?.kind !== "local" && ["env-remove", "Remove environment", "danger"]].filter((x) => x !== false);
  }
  if (target.kind === "group") return [["new-in-group", "New tab in group…"], ["rename-group", "Rename group…"], null, ["ungroup", "Ungroup"]];
  const t = tab(target.key);
  if (!t) return [];
  return [
    ["tab-settings", "Tab settings…"],
    dead(t) ? ["start", "Resume"] : ["restart", "Restart"],
    null,
    t.claudeSessionId && ["copy-id", "Copy session id"],
    ["copy-cwd", "Copy directory"],
    null,
    ["close", "Close tab", "danger"],
  ].filter((x) => x !== false && x !== "" && x !== undefined);
}

function closeMenu() { $("menu").classList.remove("open"); }
function openMenu(e, target) {
  e.preventDefault();
  const menu = $("menu");
  menu.innerHTML = menuItems(target).map((it) => it ? `<button data-act="${it[0]}" class="${it[2] || ""}">${esc(it[1])}</button>` : "<hr>").join("");
  menu.onclick = (ev) => {
    const b = ev.target.closest("button[data-act]");
    if (!b) return;
    closeMenu();
    act(b.dataset.act, target);
  };
  menu.classList.add("open");
  const r = menu.getBoundingClientRect();
  menu.style.left = Math.min(e.clientX, innerWidth - r.width - 8) + "px";
  menu.style.top = Math.min(e.clientY, innerHeight - r.height - 8) + "px";
}
$("list").addEventListener("contextmenu", (e) => {
  const el = e.target.closest(".tab, .glabel, .envhead");
  if (!el) return;
  openMenu(e, el.classList.contains("tab") ? { kind: "tab", key: el.dataset.key }
    : el.classList.contains("glabel") ? { kind: "group", env: el.dataset.env, group: el.dataset.group }
      : { kind: "env", env: el.dataset.env });
});
$("strip").addEventListener("contextmenu", (e) => {
  const el = e.target.closest(".ttab, .echip");
  if (el?.classList.contains("echip")) openMenu(e, { kind: "env", env: el.dataset.env });
  else if (el) openMenu(e, { kind: "tab", key: el.dataset.key });
});
window.addEventListener("pointerdown", (e) => { if (!e.target.closest("#menu")) closeMenu(); }, true);
window.addEventListener("blur", closeMenu);

// act runs one action on a target.
async function act(what, target) {
  const t = target.kind === "tab" ? tab(target.key) : null;
  const name = t?.env ?? target.env;
  try {
    if (what === "start") {
      await api(name, "POST", `/api/sessions/${t.id}/start`);
      terms.get(t.key)?.term.focus();
    } else if (what === "restart") {
      if (t.status !== "working" || await sure(`Restart ${t.name}?`, "It is working: the turn in progress is cut off. Its conversation continues.", "Restart")) await api(name, "POST", `/api/sessions/${t.id}/restart`);
    } else if (what === "tab-settings") {
      openTabSettings(t.key);
    } else if (what === "close") {
      const ask = page.confirmClose || t.status === "working";
      const why = t.status === "working" ? "It is working: the turn in progress is cut off. " : "";
      if (!ask || await sure(`Close ${t.name}?`, `${why}Its claude ends and the tab is removed for good.`, "Close tab")) await api(name, "DELETE", `/api/sessions/${t.id}`);
    } else if (what === "rename-group") {
      const to = await ask("Rename group", "Renames it on every tab; an existing group's name merges the two.", target.group, groupsOf(name));
      if (to && to !== target.group) await api(name, "POST", "/api/groups/rename", { from: target.group, to });
    } else if (what === "ungroup") {
      await api(name, "POST", "/api/groups/rename", { from: target.group, to: "" });
    } else if (what === "new-in-group") {
      openNew({ env: name, group: target.group });
    } else if (what === "new-in-env") {
      openNew({ env: name });
    } else if (what.startsWith("env-")) {
      envAct(what.slice(4), name);
    } else if (what === "copy-id" || what === "copy-cwd") {
      const text = what === "copy-cwd" ? t.cwd : t.claudeSessionId;
      if (text) await navigator.clipboard.writeText(text);
    }
  } catch (err) {
    say(err.message);
  }
}

// sure and say stand in for confirm and alert, which the app's own window
// (a bare web view) answers without showing anything. ask asks for one line.
function sure(title, text, ok = "OK") {
  $("mTitle").textContent = title;
  $("mText").textContent = text;
  $("mOk").textContent = ok;
  $("mCancel").hidden = false;
  return new Promise((resolve) => {
    const done = (v) => { $("msgDlg").close(); resolve(v); };
    $("msgForm").onsubmit = (e) => { e.preventDefault(); done(true); };
    $("mCancel").onclick = () => done(false);
    $("msgDlg").oncancel = () => resolve(false);
    $("msgDlg").showModal();
    $("mOk").focus();
  });
}
function say(text) {
  const shown = sure("", text);
  $("mCancel").hidden = true;
  return shown;
}
function ask(title, hint, value, options) {
  $("groupsList").innerHTML = (options || []).map((g) => `<option value="${esc(g)}">`).join("");
  $("aTitle").textContent = title;
  $("aHint").textContent = hint;
  $("aLine").value = value || "";
  return new Promise((resolve) => {
    const done = (v) => { $("askDlg").close(); resolve(v); };
    $("askForm").onsubmit = (e) => { e.preventDefault(); done($("aLine").value.trim()); };
    $("aCancel").onclick = () => done(null);
    $("askDlg").oncancel = () => resolve(null);
    $("askDlg").showModal();
    $("aLine").select();
  });
}

// ---------- keys ----------
// Caught before the terminal sees them.
window.addEventListener("keydown", (e) => {
  const mod = isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
  if (mod && !e.altKey && e.code === "KeyK") { e.preventDefault(); e.stopPropagation(); $("palette").classList.contains("open") ? closePalette() : openPalette(); return; }
  if (mod && !e.altKey && e.code === "Comma") { e.preventDefault(); e.stopPropagation(); openSettings(); return; }
  if (e.key === "Escape") {
    closeMenu();
    if ($("settingsPage").classList.contains("open") && !document.querySelector("dialog[open]")) { closeSettings(); e.preventDefault(); }
    return;
  }
  if (!e.altKey || e.metaKey || e.ctrlKey) return;
  let handled = true;
  if (/^Digit[1-9]$/.test(e.code)) {
    const t = visual()[Number(e.code.slice(5)) - 1];
    if (t) select(t.key);
  } else if (e.code === "KeyN") openNew();
  else if (e.code === "KeyJ") jumpWaiting();
  else if (e.code === "ArrowUp") step(-1);
  else if (e.code === "ArrowDown") step(1);
  else handled = false;
  if (handled) { e.preventDefault(); e.stopPropagation(); }
}, true);

// The sidebar's right edge drags to resize it; the width is kept per browser.
function setSide(px) {
  const w = Math.min(600, Math.max(180, Math.round(px)));
  document.body.style.setProperty("--side", w + "px");
  return w;
}
{
  const saved = Number(store.get("mux.side"));
  if (saved) setSide(saved);
  const grip = $("grip");
  grip.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    grip.setPointerCapture(e.pointerId);
    grip.classList.add("dragging");
    document.body.classList.add("resizing");
  });
  grip.addEventListener("pointermove", (e) => {
    if (grip.hasPointerCapture(e.pointerId)) setSide(e.clientX);
  });
  grip.addEventListener("pointerup", (e) => {
    grip.releasePointerCapture(e.pointerId);
    grip.classList.remove("dragging");
    document.body.classList.remove("resizing");
    store.set("mux.side", String(setSide(e.clientX)));
    fitSelected();
  });
  grip.addEventListener("dblclick", () => { setSide(248); store.set("mux.side", "248"); fitSelected(); });
}

window.addEventListener("resize", fitSelected);
new ResizeObserver(fitSelected).observe($("stage"));
document.addEventListener("visibilitychange", () => { if (selected) markSeen(selected); });
window.addEventListener("focus", () => { if (selected) markSeen(selected); });
setInterval(render, 30000); // keep the "for 3m" titles moving
// Measure again once the terminal font has loaded.
document.fonts?.ready.then(() => { for (const t of terms.values()) t.term.options.fontFamily = termFontFamily(); fitSelected(); });

// ---------- boot ----------
applyPage();
streamHub();
