const $ = (id) => document.getElementById(id);
const store = {
  get(k) { try { return localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch { /* storage blocked: nothing is kept */ } },
  remove(k) { try { localStorage.removeItem(k); } catch { /* storage blocked */ } },
};
const isMac = /Mac/.test(navigator.platform);
const MOD = isMac ? "⌘" : "Ctrl+";

// The hub sends everything over one WebSocket (streamHub): the environments,
// each with its backend's state, which holds its tabs. A tab's key is
// "<environment>/<id>".
let envs = [];                   // [{name, kind, target, state, platform, latency, step, error, backend: {claude, sessions, groups, defaultCwd}}]
let update = null;               // what the hub knows about updates
let hubSettings = null;          // {checkUpdates, everyDays, page}
let hubVersion = "", hubPlatform = "", hubStateDir = "";
let hubKinds = [];               // the kinds of environment this machine can add
let tabs = [];                   // every environment's tabs in sidebar order: {...session, env, key}
let selected = null;             // the selected tab's key
// The selection is per window: this window's own, or else the last one made
// in any window (a new window starts on it).
const selection = {
  get() { try { return sessionStorage.getItem("mux.selected"); } catch { return null; } },
  set(k) { try { sessionStorage.setItem("mux.selected", k); } catch { /* storage blocked */ } store.set("mux.selected", k); },
};
let pendingSelect = selection.get() || store.get("mux.selected"); // until its tab shows up
const terms = new Map();         // tab key -> {key, term, fit, ws, el, closed, retry}
const lastStatus = new Map();    // tab key -> status, for notifications

const STATUS_TEXT = { starting: "Starting", working: "Working", ready: "Ready", "needs-you": "Needs you", exited: "Exited", stopped: "Stopped" };
// The frequent flags, offered as checkboxes under the arguments.
const FLAGS = ["--dangerously-skip-permissions", "--chrome"];
const FLAG_SHORT = { "--dangerously-skip-permissions": "skip permissions", "--chrome": "chrome" };
const KIND_TEXT = { local: "This machine", ssh: "SSH host", docker: "Docker container", wsl: "WSL distro" };

// ---------- page settings ----------
// Kept by the hub in hub.json (settings.page), so the app's window and a
// browser see the same; a copy in this browser paints the first frame.
const PAGE_DEFAULTS = {
  confirmClose: true,
  theme: "auto", termTheme: "auto", termSize: 13, termScrollbar: false, tabRows: "one",
  notifyNeeds: true, notifyReady: true, notifyAway: true,
  // What New tab starts from: the options of the last tab started.
  lastTab: { startup: false, message: "", args: "", flags: [...FLAGS] },
};
// PAGE_STEPS upgrade the page's settings, one version each, like the
// hub's and the backends' files. Never remove one.
const PAGE_STEPS = [
  // 0 → 1 (v0.7): one layout, no sort, no font choice; New tab repeats the
  // last tab instead of its own defaults.
  (p) => {
    if (p.newArgs !== undefined || p.newFlags !== undefined) {
      const flags = p.newFlags || [];
      p.lastTab = {
        startup: false, message: "",
        args: [...flags.filter((f) => !FLAGS.includes(f)), p.newArgs || ""].join(" ").trim(),
        flags: flags.filter((f) => FLAGS.includes(f)),
      };
    }
    if (p.termSize > 20) p.termSize = 20;
    for (const k of ["layout", "sort", "termFont", "newEnv", "newCwd", "newArgs", "newFlags", "notifyExited", "badge"]) delete p[k];
  },
  // 1 → 2 (v0.7.12): compact tab rows by default. Every saved page carried
  // the old default, so a page on Detailed is asked once (askCompact).
  (p) => { if (p.tabRows === "two") p.askCompact = true; },
];
function upgradePage(saved) {
  const p = { ...saved };
  for (let v = p.version || 0; v < PAGE_STEPS.length; v++) PAGE_STEPS[v](p);
  p.version = Math.max(p.version || 0, PAGE_STEPS.length);
  return { ...PAGE_DEFAULTS, ...p };
}
let page = upgradePage((() => { try { return JSON.parse(store.get("mux.page") || "{}"); } catch { return {}; } })());
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
  document.body.classList.toggle("compact", page.tabRows === "one");
  document.body.classList.toggle("term-scrollbar", page.termScrollbar);
  const th = termTheme();
  for (const t of terms.values()) {
    t.term.options.theme = th;
    t.term.options.fontSize = page.termSize;
    t.el.style.background = th.background;
  }
  fitSelected();
  render();
}

// ---------- windows ----------
// Several windows can show this hub (New window). They find each other on a
// BroadcastChannel (hello on start, here every 2 s, bye on closing) and are
// numbered in the order they were opened, 1 to N with no gaps, so a later
// one moves down when an earlier one closes. A reload keeps its place. While
// more than one is open each shows "2/3" beside the name and "Window 2 of
// 3" in its title.
const winId = Math.random().toString(36).slice(2);
const winBorn = (() => {
  try {
    const kept = Number(sessionStorage.getItem("mux.windowBorn"));
    if (kept) return kept;
    sessionStorage.setItem("mux.windowBorn", String(Date.now()));
    return Number(sessionStorage.getItem("mux.windowBorn"));
  } catch { return Date.now(); }
})();
const peers = new Map(); // window id -> {born, seen}
const winChan = typeof BroadcastChannel !== "undefined" ? new BroadcastChannel("mux.windows") : null;
const announce = (type) => winChan?.postMessage({ type, id: winId, born: winBorn });
function winPlace() {
  let n = 1;
  for (const [id, p] of peers) if (p.born < winBorn || (p.born === winBorn && id < winId)) n++;
  return [n, peers.size + 1];
}
const winLabel = () => { const [n, of] = winPlace(); return of > 1 ? `Window ${n} of ${of}` : ""; };
function showWindowNo() {
  for (const [id, p] of peers) if (Date.now() - p.seen > 6000) peers.delete(id);
  const [n, of] = winPlace();
  $("winNo").textContent = of > 1 ? `${n}/${of}` : "";
  $("winNo").title = winLabel();
  $("winNo").hidden = of < 2;
  render();
}
if (winChan) {
  winChan.onmessage = ({ data }) => {
    if (data.type === "bye") peers.delete(data.id);
    else peers.set(data.id, { born: data.born, seen: Date.now() });
    if (data.type === "hello") announce("here");
    showWindowNo();
  };
  announce("hello");
  setInterval(() => { announce("here"); showWindowNo(); }, 2000);
  window.addEventListener("pagehide", () => announce("bye"));
}

// ---------- api ----------
async function request(method, url, body) {
  const res = await fetch(url, {
    method,
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  let data = null;
  try { data = res.status === 204 ? null : await res.json(); } catch { /* not JSON: the status says it */ }
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
// dayAndTime says when a time was: "today at 14:20", "yesterday at 9:05", or the date.
function dayAndTime(ts) {
  const d = new Date(ts);
  const time = d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  const days = Math.round((new Date().setHours(0, 0, 0, 0) - new Date(ts).setHours(0, 0, 0, 0)) / 86400000);
  if (days === 0) return `today at ${time}`;
  if (days === 1) return `yesterday at ${time}`;
  return `${d.toLocaleDateString([], { month: "short", day: "numeric" })} at ${time}`;
}
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const home = (p) => String(p || "").replace(/^\/(Users|home)\/[^/]+/, "~");
const base = (p) => String(p || "").replace(/[/\\]+$/, "").split(/[/\\]/).pop();
const waiting = (t) => t.status === "needs-you" || (t.status === "ready" && t.unseen);
const tab = (key) => tabs.find((t) => t.key === key);
const env = (name) => envs.find((e) => e.name === name);
const dead = (t) => t.status === "exited" || t.status === "stopped";
// backendOf is an environment's backend state, once its backend has sent one.
const backendOf = (name) => env(name)?.backend;
const sessionsOf = (name) => backendOf(name)?.sessions || [];
const groupsIn = (name) => backendOf(name)?.groups || [];
const groupOf = (name, id) => groupsIn(name).find((g) => g.id === id);
// dotClass is a tab's dot: its status, and the ring of "look at me" when it
// finished and hasn't been seen (needs-you has its ring by status).
const dotClass = (t) => (t.status === "ready" && t.unseen ? "ready unseen" : t.status);
const rank = (t) => (t.status === "needs-you" ? 0 : t.status === "ready" && t.unseen ? 1 : 2);
const hasClaude = (name) => !!backendOf(name)?.claude?.path;
const canStart = (name) => env(name)?.state === "connected" && hasClaude(name);
// health: ok (connected, with claude), problem (failed, or no claude) or connecting.
function health(e) {
  if (e.state === "connected") return backendOf(e.name) && !hasClaude(e.name) ? "problem" : "ok";
  return e.state === "failed" ? "problem" : "connecting";
}
// envMeta is an environment's details, for its dot's hover.
function envMeta(e) {
  const parts = [e.kind === "local" ? KIND_TEXT.local : `${KIND_TEXT[e.kind] || e.kind} ${e.target}`];
  if (e.platform) parts.push(e.platform.replace("-", "/"));
  if (e.state === "connected" && e.kind !== "local") parts.push(`${e.latency || "<1"} ms`);
  else parts.push(e.state === "failed" ? "disconnected" : e.state);
  return parts.join(" · ");
}
// kindLabel is the kind in muted text after an environment's name.
function kindLabel(e) {
  if (e.kind !== "local") return e.kind;
  return { darwin: "this Mac", windows: "this PC" }[hubPlatform.split("-")[0]] || "this machine";
}
// envWord is the state beside the dot, only when something is wrong.
function envWord(e) {
  if (e.state === "failed") return "disconnected";
  if (e.state === "connected" && backendOf(e.name) && !hasClaude(e.name)) return "no claude";
  return "";
}
const CHEVRON = `<svg class="chev" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>`;
const PLUG = `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 2v5M15 2v5M6 7h12v4a6 6 0 0 1-12 0zM12 17v5"/></svg>`;
// A tab with a startup message shows ↻.
// The text a startup message starts with when it is switched on empty.
const DEFAULT_STARTUP = "You were restarted, and whatever ran in the background (shells, watchers, servers) is gone. Continue the work, if any.";
const hasStartup = (t) => t.resume === "custom" && t.resumeMessage;

// ---------- the tree ----------
// An environment holds groups and tabs, a group the same, to any depth, in
// one order (each node's parent and order key, from its backend). Folding
// is per browser: "env:<env>" and "g:<env>/<group id>".
const collapsed = new Set((() => { try { return JSON.parse(store.get("mux.collapsed") || "[]"); } catch { return []; } })());
function setCollapsed(key, shut) {
  shut ? collapsed.add(key) : collapsed.delete(key);
  store.set("mux.collapsed", JSON.stringify([...collapsed]));
}
const byOrder = (a, b) => (a.order < b.order ? -1 : a.order > b.order ? 1 : 0);
// kids are a container's children in order: {group} or {tab}.
const kids = (name, parent) => [
  ...groupsIn(name).filter((g) => (g.parent || "") === parent).map((g) => ({ group: g, order: g.order })),
  ...tabs.filter((t) => t.env === name && (t.parent || "") === parent).map((t) => ({ tab: t, order: t.order })),
].sort(byOrder);
// tabsUnder lists a container's tabs, all the way down, in tree order.
const tabsUnder = (name, parent) => kids(name, parent).flatMap((k) => (k.tab ? [k.tab] : tabsUnder(name, k.group.id)));
// pathOf is the groups from the environment down to parent.
function pathOf(name, parent) {
  const path = [];
  for (let g = groupOf(name, parent); g; g = groupOf(name, g.parent || "")) path.unshift(g);
  return path;
}
const placeOf = (name, parent) => [name, ...pathOf(name, parent).map((g) => g.name)].join(" / ");
const crumbOf = (t) => placeOf(t.env, t.parent || "");
// urgent is what a folded group shows: the most urgent tab's dot inside,
// ring and all.
function urgent(list) {
  for (const [st, has] of [["needs-you", (t) => t.status === "needs-you"], ["ready unseen", (t) => t.status === "ready" && t.unseen],
    ["working", (t) => t.status === "working" || t.status === "starting"], ["ready", (t) => t.status === "ready"], ["exited", dead]]) {
    if (list.some(has)) return st;
  }
  return "";
}
// The tabs in the order they show, every environment's, folded or not.
const visual = () => envs.flatMap((e) => tabsUnder(e.name, ""));

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
let drag = null;          // the tab or group being dragged: {env, id, kind}; renders wait for the drop
let dragEnv = null;       // the environment being dragged, by its name
let editing = null;       // the row being renamed: {fid, fresh}; fresh is a new group, which Esc removes
let lastBadge = null;

function render() {
  if (drag || dragEnv) return; // dragend renders
  const waitingCount = tabs.filter(waiting).length;
  const needs = tabs.some((t) => t.status === "needs-you");
  const name = winLabel() ? `claude-mux · ${winLabel()}` : "claude-mux";
  document.title = waitingCount ? `(${waitingCount}) ${name}` : name;
  favicon(needs ? "#F5B14C" : waitingCount ? "#4FD18B" : null);
  const badge = waitingCount ? String(waitingCount) : "";
  if (window.cmuxBadge && badge !== lastBadge) { lastBadge = badge; window.cmuxBadge(badge); }
  renderList();
  renderHead();
  renderUpdate();
}

// The sidebar is an ARIA tree: an environment, then its groups and tabs,
// each level indented 16 px (up to four), with a guide under each group's
// chevron; Connect environment after the last environment.
const INDENT = 16;
const pad = (depth) => 6 + Math.min(depth, 4) * INDENT;
const guides = (depth) => Array.from({ length: Math.max(0, depth - 1) }, (_, k) => `<span class="guide" style="left:${pad(k + 1) + 7}px"></span>`).join("");
const renameBox = (fid, value, label) => `<input class="rename" data-rename="${esc(fid)}" value="${esc(value)}" aria-label="${label}" spellcheck="false">`;

function renderList() {
  if (editing && $("list").querySelector(".rename")) return; // until the rename is done
  const focused = document.activeElement?.closest?.(".trow")?.dataset.fid;
  let html = "";
  for (const e of envs) {
    const fid = "env:" + e.name;
    const shut = collapsed.has(fid);
    const word = envWord(e);
    const name = editing?.fid === fid ? renameBox(fid, e.name, "Environment name") : `<span class="ename">${esc(e.name)}</span>`;
    html += `<div class="envblock" data-env="${esc(e.name)}" role="none">
      <div class="trow envhead" role="treeitem" aria-level="1" aria-expanded="${!shut}" tabindex="-1" data-fid="${esc(fid)}" data-kind="env" data-env="${esc(e.name)}" data-node="" data-depth="0" data-path=""
        title="Drag to reorder · right-click for actions" draggable="true">
      <button class="chevbtn" data-toggle="${esc(fid)}" tabindex="-1" aria-label="Fold ${esc(e.name)}">${CHEVRON}</button>${name}<span class="kind">${esc(kindLabel(e))}</span>
      <span class="gap"></span>${word ? `<span class="estate">${word}</span>` : ""}<span class="hdot ${health(e)}" title="${esc(envMeta(e))}"></span></div>`;
    if (!shut) html += envNote(e) + walk(e.name, "", 1, []);
    html += `</div>`;
  }
  html += `<button class="connectrow" data-connect title="An SSH host, a Docker container or a WSL distro">${PLUG}Connect environment</button>`;
  $("list").innerHTML = `<div role="tree" aria-label="Tabs">${html}</div>`;
  if (!$("list").querySelector('.trow[tabindex="0"]')) {
    const first = $("list").querySelector(`.trow[data-fid="${CSS.escape(focused || "t:" + selected)}"]`) || $("list").querySelector(".trow");
    if (first) first.tabIndex = 0;
  }
  if (focused) $("list").querySelector(`.trow[data-fid="${CSS.escape(focused)}"]`)?.focus();
  const input = $("list").querySelector(".rename");
  if (input) { input.focus(); input.select(); }
}

// walk renders a container's children at depth; path is its groups' ids.
function walk(name, parent, depth, path) {
  let html = "";
  for (const k of kids(name, parent)) {
    if (k.tab) { html += tabRow(k.tab, depth, path); continue; }
    const g = k.group;
    const fid = "g:" + name + "/" + g.id;
    const shut = collapsed.has(fid);
    const under = shut ? tabsUnder(name, g.id) : [];
    const dot = shut ? urgent(under) : "";
    const label = editing?.fid === fid ? renameBox(fid, g.name, "Group name") : `<span class="gname">${esc(g.name)}</span>`;
    html += `<div class="trow glabel" role="treeitem" aria-level="${depth + 1}" aria-expanded="${!shut}" tabindex="-1" data-fid="${esc(fid)}" data-kind="group" data-env="${esc(name)}" data-node="${esc(g.id)}" data-parent="${esc(parent)}" data-depth="${depth}" data-path="${esc(path.join("/"))}"
      style="padding-left:${pad(depth) - 2}px" title="Drag to move · double-click to rename · right-click for actions" draggable="true">${guides(depth)}
      <button class="chevbtn" data-toggle="${esc(fid)}" tabindex="-1" aria-label="Fold ${esc(g.name)}">${CHEVRON}</button>${label}
      ${shut ? `${dot ? `<span class="dot ${dot}"></span>` : ""}<span class="count">${under.length}</span>` : ""}</div>`;
    if (!shut) html += walk(name, g.id, depth + 1, [...path, g.id]);
  }
  return html;
}

// envNote says what's wrong with an environment, if anything, with what
// can be done about it.
function envNote(e) {
  const n = esc(e.name);
  if (e.state === "failed") return `<div class="envnote problem">${esc(e.error)} · <button class="link" data-env-act="reconnect" data-env="${n}">Reconnect</button></div>`;
  if (e.state === "connecting") return `<div class="envnote">${esc(e.step ? e.step + "…" : "Connecting…")}</div>`;
  if (backendOf(e.name) && !hasClaude(e.name)) {
    return `<div class="envnote">No claude here · <button class="link" data-env-act="install" data-env="${n}">Install</button> · <button class="link" data-env-act="retry" data-env="${n}">Look again</button></div>`;
  }
  return "";
}

// A tab row: its dot, then its name and, in Detailed rows, its directory.
function tabRow(t, depth, path) {
  const fid = "t:" + t.key;
  const cls = ["trow", "tab", selected === t.key && "active", dead(t) && "dead"].filter(Boolean).join(" ");
  const mark = hasStartup(t) ? `<span class="mark" title="Startup message: ${esc(t.resumeMessage)}">↻</span>` : "";
  const name = `<span class="name">${esc(t.name)}</span>${mark}`;
  return `<div class="${cls}" role="treeitem" aria-level="${depth + 1}" aria-selected="${selected === t.key}" tabindex="-1" data-fid="${esc(fid)}" data-kind="tab" data-key="${esc(t.key)}" data-env="${esc(t.env)}" data-node="${esc(t.id)}" data-parent="${esc(t.parent || "")}" data-depth="${depth}" data-path="${esc(path.join("/"))}"
    style="padding-left:${pad(depth) + 4}px" title="${esc(STATUS_TEXT[t.status] || t.status)} for ${ago(t.statusSince)} · ${esc(home(t.cwd))}" draggable="true">${guides(depth)}
    <span class="dot ${dotClass(t)}"></span><span class="lines"><span class="line1">${name}</span>
    ${page.tabRows === "one" ? "" : `<span class="cwd">${esc(home(t.cwd))}</span>`}</span></div>`;
}

// Above the terminal: the name, where it runs, and … for Tab settings.
function renderHead() {
  const t = tab(selected);
  if (!t) { $("head").innerHTML = `<span class="crumb">Pick a tab on the left.</span>`; return; }
  const crumb = [crumbOf(t), home(t.cwd), t.branch].filter(Boolean).join("  ·  ");
  $("head").innerHTML = `<span class="tname">${esc(t.name)}</span><span class="crumb" title="${esc(t.cwd)}">${esc(crumb)}</span>
    <button class="icon-btn" id="tabMore" title="Tab settings (${MOD}I)" aria-label="Tab settings"><svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="19" cy="12" r="1.8"/></svg></button>`;
}

function renderUpdate() {
  const box = $("updatebar");
  if (update?.updating) { box.hidden = false; box.innerHTML = `<span>Updating: ${esc(update.step || "starting")}…</span>`; return; }
  box.hidden = !update?.latest;
  if (box.hidden) return;
  box.innerHTML = `<span>${esc(update.latest)} is out</span><button class="link" data-up="apply">Update and restart</button>`;
  // Tell once per version, even across reloads.
  if (store.get("mux.toldVersion") !== update.latest) {
    store.set("mux.toldVersion", update.latest);
    notify("claude-mux", `${update.latest} is out: update and restart from the sidebar.`);
  }
}

// ---------- themes ----------
// The terminal theme is separate from the page's; Match theme follows it.
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
const TERM_THEME_NAMES = [["auto", "Match theme"], ["dark", "Dark"], ["light", "Light"], ["tokyo", "Tokyo Night"], ["dracula", "Dracula"], ["solarized-dark", "Solarized Dark"], ["solarized-light", "Solarized Light"]];
const TERM_FONT = `"JetBrains Mono", "SF Mono", Menlo, Consolas, monospace`;
const systemDark = matchMedia("(prefers-color-scheme: dark)");
const pageIsDark = () => document.documentElement.dataset.theme !== "light";
function termTheme() {
  const k = page.termTheme === "auto" ? (pageIsDark() ? "dark" : "light") : page.termTheme;
  return TERM_THEMES[k] || TERM_THEMES.dark;
}
systemDark.addEventListener("change", applyPage);

// ---------- terminals ----------
function ensureTerm(key) {
  if (terms.has(key)) return terms.get(key);
  const el = document.createElement("div");
  el.className = "term";
  $("stage").appendChild(el);
  const term = new Terminal({
    linkHandler: { activate: (e, uri) => openLink(uri) }, // links claude marks (OSC 8)
    fontFamily: TERM_FONT,
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
  term.loadAddon(new WebLinksAddon.WebLinksAddon((e, uri) => openLink(uri))); // links in plain text
  term.open(el);
  // key moves with its environment's name (renameEnvKeys); handlers read it from here.
  const t = { key, term, fit, el, ws: null, closed: false, retry: 0 };

  // Shift+Enter: ESC CR, which Claude Code reads as a newline in the prompt.
  // Alt+F4 isn't the terminal's: left alone, it closes the window on Windows.
  term.attachCustomKeyEventHandler((e) => {
    if (e.key === "Enter" && e.shiftKey) {
      if (e.type === "keydown") send(t, { t: "in", d: "\x1b\r" });
      return false;
    }
    if (e.key === "F4" && e.altKey) return false;
    // Ctrl+V pastes on Windows and Linux, as in their terminals, so a
    // screenshot goes through too; left to the terminal it is a bare ^V.
    if (!isMac && e.ctrlKey && !e.altKey && !e.metaKey && e.code === "KeyV") return false;
    // Copying as in Windows Terminal: Ctrl+Shift+C copies the selection, and
    // so does Ctrl+C while there is one; without one Ctrl+C interrupts.
    if (!isMac && e.ctrlKey && !e.altKey && !e.metaKey && e.code === "KeyC" && (e.shiftKey || term.hasSelection())) {
      if (e.type === "keydown") {
        e.preventDefault();
        if (term.hasSelection()) copyText(term.getSelection());
        if (!e.shiftKey) term.clearSelection();
      }
      return false;
    }
    return true;
  });
  // In a tab whose claude exited, Enter resumes it.
  term.onData((d) => {
    const cur = tab(t.key);
    if (cur && dead(cur)) {
      if (d === "\r") api(cur.env, "POST", `/api/sessions/${cur.id}/start`).catch((err) => say(err.message));
      return;
    }
    send(t, { t: "in", d });
  });
  term.onResize(({ cols, rows }) => send(t, { t: "resize", c: cols, r: rows }));
  // OSC 52: claude copies a selection by asking the terminal to, which is the
  // only way that reaches this machine's clipboard from an SSH host or a
  // container. Writes only; a request to read the clipboard ("?") is ignored.
  term.parser.registerOscHandler(52, (data) => {
    const b64 = data.slice(data.indexOf(";") + 1);
    if (!b64 || b64 === "?") return true;
    try {
      copyText(new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))));
    } catch { /* not base64: nothing to copy */ }
    return true;
  });
  // Files dropped or pasted here (a screenshot) go to the tab's environment,
  // and their paths are typed in, as a terminal does for a dropped file.
  el.addEventListener("dragover", (e) => {
    if (!e.dataTransfer.types.includes("Files")) return;
    e.preventDefault();
    el.classList.add("dropping");
  });
  el.addEventListener("dragleave", () => el.classList.remove("dropping"));
  el.addEventListener("drop", (e) => {
    el.classList.remove("dropping");
    if (!e.dataTransfer.files.length) return;
    e.preventDefault();
    attachFiles(t.key, [...e.dataTransfer.files], "drag");
  });
  el.addEventListener("paste", (e) => {
    const files = [...(e.clipboardData?.files || [])];
    if (!files.length) return;
    e.preventDefault();
    e.stopPropagation();
    attachFiles(t.key, files, "clipboard");
  }, true);
  connect(t);
  terms.set(key, t);
  return t;
}

// openLink opens a link clicked in a terminal in the browser; in
// claude-mux's own window the hub opens it.
function openLink(uri) {
  if (window.cmuxOpen) window.cmuxOpen(uri).catch((err) => say(err.message));
  else window.open(uri, "_blank", "noopener");
}

// attachFiles types the paths of files dropped or pasted (from "drag" or
// "clipboard") into a tab's terminal. A local tab in claude-mux's own window
// gets the files where they are, from the hub (cmuxPaths); otherwise, and
// for an image that is only on the clipboard, they are copied to the tab's
// environment first.
async function attachFiles(key, files, from) {
  const t = tab(key);
  if (!t) return;
  const windows = env(t.env)?.platform?.startsWith("windows");
  const quote = (p) => (windows ? `"${p}"` : p.replace(/[^\w/.,:@%+=~-]/g, "\\$&"));
  try {
    let paths = env(t.env)?.kind === "local" && window.cmuxPaths ? await window.cmuxPaths(from) : [];
    // The hub's list must be these very files, else copy.
    const names = files.map((f) => f.name).sort().join("\n");
    if (paths.map(base).sort().join("\n") !== names) paths = [];
    if (!paths.length) {
      for (const f of files) paths.push(await copyFile(t, f));
    }
    const term = terms.get(key);
    term?.term.paste(paths.map(quote).join(" ") + " ");
    term?.term.focus();
  } catch (err) {
    say(`The file didn't go through: ${err.message}`);
  }
}

// copyFile sends a file to the tab's backend, which keeps it until the tab
// closes, and gives its path there.
async function copyFile(t, f) {
  const data = await new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result).split(",")[1] || "");
    r.onerror = () => reject(r.error);
    r.readAsDataURL(f);
  });
  const { path } = await api(t.env, "POST", `/api/sessions/${t.id}/files`, { name: f.name || "pasted.png", data });
  return path;
}

// copyText puts text on the clipboard: the Clipboard API where the page
// may use it, else the older copy command.
function copyText(text) {
  navigator.clipboard?.writeText(text).catch(() => {
    const ta = document.createElement("textarea");
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand("copy");
    ta.remove();
  });
}

function send(t, msg) {
  if (t.ws && t.ws.readyState === WebSocket.OPEN) t.ws.send(JSON.stringify(msg));
}

function connect(t) {
  const { env: name, id } = tab(t.key);
  const ws = new WebSocket(`ws://${location.host}/env/${encodeURIComponent(name)}/api/sessions/${encodeURIComponent(id)}/term`);
  ws.binaryType = "arraybuffer";
  t.ws = ws;
  ws.onopen = () => {
    t.retry = 0;
    t.term.reset(); // the server replays its scrollback first
    send(t, { t: "resize", c: t.term.cols, r: t.term.rows });
    const cur = tab(t.key);
    if (cur && dead(cur)) exitedLine(t);
  };
  ws.onmessage = (e) => t.term.write(new Uint8Array(e.data));
  ws.onclose = () => {
    if (t.closed) return;
    t.retry = Math.min(t.retry + 1, 6);
    setTimeout(() => { if (!t.closed && tab(t.key)) connect(t); }, 500 * t.retry);
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

// claimSize gives the selected tab's claude this window's terminal size.
// A tab has one pty, and every window showing it sends its own size; a
// terminal only reports its size when it changes, so without this the tab
// would keep the size of whichever window resized last. Called when the
// window gets the focus or comes into view, and on selecting a tab: the
// window being looked at wins.
function claimSize() {
  const t = terms.get(selected);
  if (!t) return;
  t.fit.fit();
  send(t, { t: "resize", c: t.term.cols, r: t.term.rows });
}

// ---------- selection ----------
function select(key) {
  const t = tab(key);
  if (!t) return;
  selected = key;
  selection.set(key);
  for (const g of pathOf(t.env, t.parent || "")) setCollapsed("g:" + t.env + "/" + g.id, false);
  setCollapsed("env:" + t.env, false);
  for (const [k, x] of terms) x.el.classList.toggle("shown", k === key);
  $("placeholder").style.display = "none";
  const term = ensureTerm(key);
  term.el.classList.add("shown");
  requestAnimationFrame(() => { claimSize(); term.term.focus(); });
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
      if (term && term.ws?.readyState !== WebSocket.OPEN) { term.retry = 0; connect(term); }
    }
    if (t.status === "exited" && term) exitedLine(term);
    const wanted = { "needs-you": page.notifyNeeds, ready: page.notifyReady }[t.status];
    if (!wanted) continue;
    // Every window sees the change; the first to get here tells.
    const event = `${t.key} ${t.status} ${t.statusSince}`;
    if (store.get("mux.notified") === event) continue;
    store.set("mux.notified", event);
    // Away: no claude-mux window has the focus.
    const away = document.visibilityState !== "visible" || (!document.hasFocus() && store.get("mux.focus") !== "1");
    if (page.notifyAway ? !away : away === false && selected === t.key) continue;
    const body = { "needs-you": "needs you", ready: "finished" }[t.status];
    notify(t.name, body, () => select(t.key));
  }
}

// ---------- the hub's state ----------
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
  if ($("envDlg").open) renderEnvFacts();
  if ($("palette").classList.contains("open")) renderPalette();
}

// streamHub follows the hub's state over one WebSocket: the environments,
// each with its backend's own state, the update state and the settings. One
// per window, whatever the environments: a browser holds at most six plain
// HTTP connections to a server, and WebSockets don't count against them. A
// WebSocket doesn't reconnect by itself, so this does, with a growing pause.
let hubRetry = 0;
function streamHub() {
  const ws = new WebSocket(`ws://${location.host}/hub/ws`);
  ws.onopen = () => { hubRetry = 0; };
  ws.onclose = () => {
    hubRetry = Math.min(hubRetry + 1, 6);
    setTimeout(streamHub, 500 * hubRetry);
  };
  ws.onmessage = (m) => {
    const hub = JSON.parse(m.data);
    // A new hub (an update, a restart) serves a new page.
    if (hubVersion && hub.version !== hubVersion) { location.reload(); return; }
    hubVersion = hub.version;
    hubPlatform = hub.platform;
    hubKinds = hub.kinds || [];
    hubStateDir = hub.stateDir || "";
    envs = hub.envs || [];
    update = hub.update;
    if (!saving) {
      hubSettings = hub.settings;
      const saved = hub.settings?.page || oldPageSettings();
      page = upgradePage(saved);
      store.set("mux.page", JSON.stringify(page));
      if (!hub.settings?.page || saved.version !== page.version) saveSettings({});
      applyPage();
      if (page.askCompact && !askingCompact) askCompact();
      else if (!page.askCompact && askingCompact) { askingCompact = false; $("msgDlg").close(); } // answered in another window
    }
    refresh();
    if ($("setDlg").open && !$("setDlg").contains(document.activeElement?.matches("input, select, textarea") ? document.activeElement : null)) renderSettings();
  };
}

// ---------- new tab and tab settings ----------
// One form for both. The arguments are one text, the single truth; the
// frequent flags are checkboxes, each hidden once the text has that flag.
// New tab asks for a name and a directory; the rest repeats the last tab.
let dlg = null; // {mode: "new"|"edit", key?, env, parent, after, startup, message, checked: Set, argsOpen, orig?}

const words = (s) => s.trim().split(/\s+/).filter(Boolean);
const typed = (flag) => words($("tArgs").value).includes(flag);
// splitArgs takes the frequent flags out of a tab's args: checked, the rest.
function splitArgs(args) {
  const rest = [...(args || [])];
  const checked = new Set();
  for (const f of FLAGS) {
    const i = rest.indexOf(f);
    if (i >= 0) { checked.add(f); rest.splice(i, 1); }
  }
  return { checked, rest: rest.join(" ") };
}
const dlgFlags = () => FLAGS.filter((f) => dlg.checked.has(f) && !typed(f));
const dlgArgs = () => [...dlgFlags(), ...words($("tArgs").value)].join(" ");

function renderDlg() {
  $("tChecks").innerHTML = FLAGS.filter((f) => !typed(f))
    .map((f) => `<label><input type="checkbox" data-flag="${esc(f)}" ${dlg.checked.has(f) ? "checked" : ""}>${esc(f)}</label>`).join("");
  $("tArgsSum").textContent = [...dlgFlags().map((f) => FLAG_SHORT[f]), ...($("tArgs").value.trim() ? [$("tArgs").value.trim()] : [])].join(" · ") || "none";
  $("tArgsBtn").setAttribute("aria-expanded", dlg.argsOpen);
  $("tArgsBody").hidden = !dlg.argsOpen;
  $("tStartupSw").setAttribute("aria-checked", dlg.startup);
  $("tStartupBtn").setAttribute("aria-expanded", dlg.startup);
  $("tStartupSum").hidden = dlg.startup;
  $("tStartupSum").textContent = hubSettings?.startupMessage?.enabled ? "global only" : "none";
  $("tStartup").hidden = !dlg.startup;
  $("tName").placeholder = base($("tCwd").value) || "named after the folder";
  $("tPick").hidden = env(dlg.env)?.kind !== "local";
  if (dlg.mode !== "edit") return;
  // Any change restarts claude.
  const t = tab(dlg.key);
  const o = dlg.orig;
  const dirty = $("tCwd").value.trim() !== o.cwd || dlg.startup !== o.startup || (dlg.startup && $("tStartup").value.trim() !== o.message) || dlgArgs() !== o.args;
  dlg.restart = dirty && t && !dead(t);
  $("tNotice").hidden = !dlg.restart;
  $("tOk").textContent = dlg.restart ? "Save and restart" : "Save";
}
for (const id of ["tArgs", "tCwd", "tStartup"]) $(id).addEventListener("input", renderDlg);
$("tChecks").addEventListener("change", (e) => {
  const f = e.target.dataset.flag;
  if (!f) return;
  e.target.checked ? dlg.checked.add(f) : dlg.checked.delete(f);
  renderDlg();
});
$("tStartupBtn").onclick = () => {
  dlg.startup = !dlg.startup;
  // The global message already says it; a tab's own would repeat it.
  if (dlg.startup && !$("tStartup").value.trim() && !hubSettings?.startupMessage?.enabled) $("tStartup").value = DEFAULT_STARTUP;
  renderDlg();
  if (dlg.startup) $("tStartup").focus();
};
$("tArgsBtn").onclick = () => {
  dlg.argsOpen = !dlg.argsOpen;
  renderDlg();
  if (dlg.argsOpen) $("tArgs").focus();
};

// The directory completes from the environment's backend as it is typed;
// Tab takes the longest completion they share, like a shell.
let dirs = { prefix: null, list: [] };
let dirsTimer = null;
async function loadDirs() {
  const prefix = $("tCwd").value;
  if (prefix === dirs.prefix || !dlg) return;
  const list = await api(dlg.env, "GET", `/api/dirs?prefix=${encodeURIComponent(prefix)}`).catch(() => []);
  if ($("tCwd").value !== prefix) return;
  dirs = { prefix, list: list || [] };
  $("dirs").innerHTML = dirs.list.map((d) => `<option value="${esc(d)}">`).join("");
}
$("tCwd").addEventListener("input", () => { clearTimeout(dirsTimer); dirsTimer = setTimeout(loadDirs, 120); });
$("tCwd").addEventListener("keydown", async (e) => {
  if (e.key !== "Tab" || e.shiftKey) return;
  const value = $("tCwd").value;
  if (dirs.prefix !== value) await loadDirs();
  const list = dirs.list.filter((d) => d.startsWith(value));
  if (!list.length) return;
  let common = list[0];
  for (const d of list) while (!d.startsWith(common)) common = common.slice(0, -1);
  if (common.length <= value.length) return;
  e.preventDefault();
  $("tCwd").value = common;
  renderDlg();
  loadDirs();
});
// The folder button: the system's folder picker, for this machine only.
$("tPick").onclick = async () => {
  try {
    const { path } = await request("POST", "/hub/pick-folder", { start: $("tCwd").value.trim() });
    if (path) { $("tCwd").value = path; renderDlg(); }
  } catch (err) {
    $("tErr").textContent = err.message;
  }
};

function showDlg() {
  dirs = { prefix: null, list: [] };
  $("dirs").innerHTML = "";
  $("tErr").textContent = "";
  renderDlg();
  $("tabDlg").showModal();
}

// openNew opens New tab in an environment and container (parent), after a
// tab there (after); by default next to the selected tab.
function openNew(at = {}) {
  const sel = tab(selected);
  let where = at.env ? at : sel ? { env: sel.env, parent: sel.parent || "", after: sel.id } : null;
  if (!where || !canStart(where.env)) {
    const e = envs.find((x) => canStart(x.name));
    if (at.env || !e) { say(at.env ? `${at.env} can't start a tab: ${envWord(env(at.env)) || "it isn't connected yet"}.` : "No environment can start a tab yet."); return; }
    where = { env: e.name, parent: "", after: "" };
  }
  const last = page.lastTab || PAGE_DEFAULTS.lastTab;
  dlg = { mode: "new", env: where.env, parent: where.parent || "", after: where.after || "", startup: !!last.startup, checked: new Set(last.flags || []), argsOpen: false };
  $("tTitle").textContent = "New tab";
  $("tMeta").textContent = "in " + placeOf(where.env, dlg.parent);
  $("tName").value = "";
  $("tName").readOnly = false;
  $("tName").title = "";
  $("tCwd").value = store.get("mux.lastCwd." + where.env) || home(backendOf(where.env)?.defaultCwd || "");
  $("tStartup").value = last.message || "";
  $("tArgs").value = last.args || "";
  $("tClose").hidden = true;
  $("tNotice").hidden = true;
  $("tFootNote").textContent = "Same options as your last tab";
  $("tOk").textContent = "Start";
  showDlg();
  $("tName").focus();
}

function openTabSettings(key) {
  const t = tab(key);
  if (!t) return;
  const { checked, rest } = splitArgs(t.args);
  const startup = t.resume === "custom";
  dlg = { mode: "edit", key, env: t.env, startup, checked, argsOpen: true };
  $("tTitle").textContent = t.name;
  $("tMeta").textContent = [crumbOf(t), t.claudeSessionId && "session " + t.claudeSessionId.slice(0, 8)].filter(Boolean).join(" · ");
  $("tName").value = t.name;
  $("tName").readOnly = true;
  $("tName").title = "Type /rename <name> in the tab to rename it";
  $("tCwd").value = home(t.cwd);
  $("tStartup").value = t.resumeMessage || "";
  $("tArgs").value = rest;
  dlg.orig = { cwd: home(t.cwd), startup, message: (t.resumeMessage || "").trim(), args: dlgArgs() };
  $("tClose").hidden = false;
  $("tFootNote").textContent = "";
  showDlg();
  $("tCwd").focus();
}

$("tabForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const message = $("tStartup").value.trim();
  if (dlg.startup && !message) { $("tErr").textContent = "Write the startup message, or turn it off."; return; }
  const body = { cwd: $("tCwd").value.trim(), args: dlgArgs(), resume: dlg.startup ? "custom" : "off", resumeMessage: $("tStartup").value };
  try {
    if (dlg.mode === "edit") {
      const t = tab(dlg.key);
      const o = dlg.orig;
      if (body.cwd !== o.cwd || dlg.startup !== o.startup || message !== o.message || body.args !== o.args) {
        if (dlg.restart && t.status === "working" && !(await sure(`Restart ${t.name}?`, "It is working: the turn in progress is cut off. Its conversation continues.", "Save and restart"))) return;
        await api(t.env, "PUT", `/api/sessions/${t.id}`, body);
        if (dlg.restart) await api(t.env, "POST", `/api/sessions/${t.id}/restart`);
      }
      $("tabDlg").close();
      return;
    }
    const name = dlg.env;
    const s = await api(name, "POST", "/api/sessions", { ...body, name: $("tName").value.trim(), parent: dlg.parent, after: dlg.after });
    store.set("mux.lastCwd." + name, body.cwd);
    setPage({ lastTab: { startup: dlg.startup, message: $("tStartup").value, args: $("tArgs").value.trim(), flags: FLAGS.filter((f) => dlg.checked.has(f)) } });
    $("tabDlg").close();
    added(name, s);
  } catch (err) {
    $("tErr").textContent = err.message;
  }
});
$("tCancel").onclick = () => $("tabDlg").close();
$("tClose").onclick = async () => { const key = dlg.key; $("tabDlg").close(); await closeTab(key, false); };

// added shows a tab the backend just made, before its state comes round.
function added(name, s) {
  const data = backendOf(name);
  if (data && !data.sessions.some((x) => x.id === s.id)) data.sessions.push(s);
  refresh();
  select(name + "/" + s.id);
}

// duplicate starts a new tab like t, next to it: a new conversation.
async function duplicate(t) {
  const s = await api(t.env, "POST", "/api/sessions", {
    name: t.name + " copy", parent: t.parent || "", after: t.id, cwd: t.cwd,
    args: (t.args || []).join(" "), resume: t.resume || "off", resumeMessage: t.resumeMessage || "",
  });
  added(t.env, s);
}

// ---------- environments ----------
const kindHints = {
  ssh: ["Host, as ssh takes it: host, user@host or an alias from ~/.ssh/config. It must take your key without a password prompt.", "devbox"],
  docker: ["Container, running, with sh in it. Its tabs live as long as the container.", "my-container"],
  wsl: ["WSL distro, as wsl -l lists it.", "Ubuntu"],
};
async function fillTargets() {
  const kind = $("hKind").value;
  [$("hHint").textContent, $("hHost").placeholder] = kindHints[kind] || ["", ""];
  const names = await request("GET", `/hub/targets/${encodeURIComponent(kind)}`).catch(() => []);
  $("targets").innerHTML = (names || []).map((n) => `<option value="${esc(n)}">`).join("");
}
$("hKind").onchange = fillTargets;
function addEnv() {
  $("hKind").innerHTML = hubKinds.map((k) => `<option value="${k}">${KIND_TEXT[k] || k}</option>`).join("");
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

// Environment settings: its name, what it is, how it is.
let envDlgName = "";
function openEnvSettings(name) {
  const e = env(name);
  if (!e) return;
  envDlgName = name;
  $("eTitle").textContent = name;
  $("eMeta").textContent = e.kind === "local" ? KIND_TEXT.local : `${KIND_TEXT[e.kind] || e.kind} ${e.target}`;
  $("eName").value = name;
  $("eName").disabled = e.kind === "local";
  $("eName").title = e.kind === "local" ? "This machine keeps its name" : "";
  $("eRemove").hidden = e.kind === "local";
  $("eErr").textContent = "";
  renderEnvFacts();
  $("envDlg").showModal();
}
function renderEnvFacts() {
  const e = env(envDlgName);
  if (!e) { $("envDlg").close(); return; }
  const c = backendOf(e.name)?.claude;
  const n = tabs.filter((t) => t.env === e.name).length;
  const status = e.state === "connected" ? (e.kind === "local" ? "connected" : `connected · ${e.latency || "<1"} ms`)
    : e.state === "failed" ? `disconnected: ${e.error}` : e.step ? e.step + "…" : "connecting…";
  const claude = !backendOf(e.name) ? "" : c?.path ? `${esc(c.version || "")} <span class="mono">${esc(home(c.path))}</span>`
    : `not installed · <button type="button" class="link" data-env-act="install" data-env="${esc(e.name)}">Install</button>`;
  const facts = [["Status", esc(status)], ["Platform", esc((e.platform || "").replace("-", "/"))], ["Claude", claude], ["Tabs", String(n)]];
  $("eFacts").innerHTML = facts.filter(([, v]) => v).map(([k, v]) => `<span>${k}</span><span>${v}</span>`).join("");
}
$("eFacts").addEventListener("click", (e) => {
  const b = e.target.closest("[data-env-act]");
  if (b) { $("envDlg").close(); envAct(b.dataset.envAct, b.dataset.env); }
});
$("eCancel").onclick = () => $("envDlg").close();
$("eRemove").onclick = async () => { $("envDlg").close(); await envAct("remove", envDlgName); };
$("envForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const to = $("eName").value.trim();
  try {
    if (!$("eName").disabled && to && to !== envDlgName) await renameEnv(envDlgName, to);
    $("envDlg").close();
  } catch (err) {
    $("eErr").textContent = err.message;
  }
});
async function renameEnv(from, to) {
  await request("PUT", `/hub/environments/${encodeURIComponent(from)}`, { name: to });
  renameEnvKeys(from, to);
}

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
    if (await sure(`Remove ${name}?`, "It is disconnected and taken off the list. Its tabs are hung up and kept there: connecting it again brings them back.", "Remove")) {
      await request("DELETE", `/hub/environments/${encodeURIComponent(name)}`).catch((err) => say(err.message));
    }
    return;
  }
  if (what === "settings") { openEnvSettings(name); return; }
  $("iTitle").textContent = `Install claude on ${name}`;
  $("iCmd").textContent = backendOf(name)?.claude?.installer || "";
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
        // eslint-disable-next-line no-control-regex -- the terminal escapes are what it strips
        out.textContent += msg.out.replace(/\x1b\[[0-9;?]*[A-Za-z]/g, "");
        out.scrollTop = out.scrollHeight;
      });
    } catch (err) {
      result = { error: err.message };
    }
    clearInterval(timer);
    const secs = Math.round((Date.now() - started) / 1000);
    if (result.done) {
      const c = backendOf(name)?.claude;
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

// ---------- settings ----------
// A modal of short sections, one row per setting. Environments are managed
// in the tree; new tabs repeat the last one.
let checking = false; // Check now is waiting for its answer

function openSettings() {
  renderSettings();
  $("setDlg").showModal();
}
$("sX").onclick = () => $("setDlg").close();
$("setDlg").addEventListener("close", () => terms.get(selected)?.term.focus());

const swRow = (label, on, attrs) => `<button type="button" class="srow" role="switch" aria-checked="${!!on}" ${attrs}><span>${label}</span><span class="switch"></span></button>`;
const pageSw = (key, label) => swRow(label, page[key], `data-page="${key}"`);
const segOf = (key, opts) => `<div class="seg">${opts.map(([v, l]) => `<button type="button" data-page-set="${key}" data-v="${v}" aria-pressed="${page[key] === v}">${l}</button>`).join("")}</div>`;
const sRow = (label, control) => `<div class="srow"><span>${label}</span>${control}</div>`;

function renderSettings() {
  const u = update || {};
  const s = hubSettings || {};
  const startup = s.startupMessage || { enabled: false, message: "" };
  const days = s.everyDays || 1;
  let checked = checking ? "checking…" : u.lastCheck ? `checked ${dayAndTime(u.lastCheck)}` : "not checked yet";
  if (u.dev) checked = "a development build, which doesn't update";
  else if (u.error && !checking) checked += ` · <span title="${esc(u.error)}">the last check failed</span>`;
  const notifyAsk = !window.cmuxNotify && typeof Notification !== "undefined" && Notification.permission === "default";
  const checkNow = `<button type="button" class="icon-btn small ${checking ? "spin" : ""}" id="sNow" title="Check now" aria-label="Check now" ${u.dev || u.updating || checking ? "disabled" : ""}><svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.6-6.4"/><path d="M21 4v5h-5"/></svg></button>`;
  const latest = u.updating ? `<span class="quiet">Updating: ${esc(u.step || "starting")}…</span>`
    : u.latest ? `<button type="button" class="btn small primary" id="sApply" title="Restarts claude-mux; every tab resumes">Update to ${esc(u.latest)}</button>` : "";
  $("sBody").innerHTML = `
    <div class="ssec">Appearance</div>
    ${sRow("Theme", segOf("theme", [["auto", "Auto"], ["light", "Light"], ["dark", "Dark"]]))}
    ${sRow(`<label for="sTerm">Terminal colours</label>`, `<select id="sTerm">${TERM_THEME_NAMES.map(([k, l]) => `<option value="${k}" ${page.termTheme === k ? "selected" : ""}>${l}</option>`).join("")}</select>`)}
    ${pageSw("termScrollbar", "Terminal scroll bar")}
    ${sRow("Terminal font size", `<div class="stepper"><button type="button" data-font="-1" aria-label="Smaller">−</button><span>${page.termSize}</span><button type="button" data-font="1" aria-label="Larger">+</button></div>`)}
    ${sRow("Tab rows", segOf("tabRows", [["two", "Detailed"], ["one", "Compact"]]))}
    <div class="ssec">Tabs</div>
    ${pageSw("confirmClose", "Ask before closing a tab")}
    ${swRow("Startup message for every tab", startup.enabled, `id="sStartup"`)}
    <textarea id="sStartupText" class="stext" rows="2" aria-label="Startup message for every tab"
      placeholder="Sent first, before a tab's own, whenever claude starts again in any tab" ${startup.enabled ? "" : "hidden"}>${esc(startup.message || "")}</textarea>
    <div class="ssec">Notifications</div>
    ${pageSw("notifyNeeds", "When a tab needs me")}
    ${pageSw("notifyReady", "When a tab finishes")}
    ${pageSw("notifyAway", "Only while I am in another window")}
    ${notifyAsk ? sRow(`<span class="quiet">This browser asks first</span>`, `<button type="button" class="btn small" id="sAllow">Allow</button>`) : ""}
    <div class="ssec">Updates</div>
    <div class="srow every ${s.checkUpdates ? "" : "off"}"><span>Check every</span>
      <input id="sDays" class="num" type="number" min="1" max="365" value="${days}" aria-label="Days between checks" ${s.checkUpdates ? "" : "disabled"}>
      <span>${days === 1 ? "day" : "days"}</span><span class="fill"></span>
      <button type="button" class="switchbtn" role="switch" aria-checked="${!!s.checkUpdates}" aria-label="Check automatically" id="sCheck"><span class="switch"></span></button></div>
    <div class="srow ver"><span>${esc(hubVersion)}</span><span class="quiet">· ${checked}</span>${u.dev ? "" : checkNow}<span class="fill"></span>${latest}</div>`;
  $("sState").textContent = home(hubStateDir);
}

$("sBody").addEventListener("click", async (e) => {
  const t = e.target;
  const set = t.closest("[data-page-set]");
  if (set) { setPage({ [set.dataset.pageSet]: set.dataset.v }); renderSettings(); return; }
  const sw = t.closest("[data-page]");
  if (sw) { setPage({ [sw.dataset.page]: !page[sw.dataset.page] }); renderSettings(); return; }
  const font = t.closest("[data-font]");
  if (font) { setPage({ termSize: Math.min(20, Math.max(10, page.termSize + Number(font.dataset.font))) }); renderSettings(); return; }
  if (t.closest("#sAllow")) { await Notification.requestPermission(); renderSettings(); return; }
  if (t.closest("#sCheck")) { saveSettings({ checkUpdates: !hubSettings?.checkUpdates }); renderSettings(); return; }
  if (t.closest("#sStartup")) {
    const was = hubSettings?.startupMessage || { enabled: false, message: "" };
    saveSettings({ startupMessage: { enabled: !was.enabled, message: was.message || DEFAULT_STARTUP } });
    renderSettings();
    if (!was.enabled) $("sStartupText").focus();
    return;
  }
  if (t.closest("#sNow")) {
    checking = true;
    renderSettings();
    await request("POST", "/hub/update/check").catch(() => {}); // a failed check shows as update.error
    checking = false;
    setTimeout(renderSettings, 150); // the hub's message with the result comes first
    return;
  }
  if (t.closest("#sApply")) { $("setDlg").close(); applyUpdate(); }
});
$("sLog").onclick = () => request("POST", "/hub/open-log").catch((err) => say(err.message));
// The global startup message saves as it is typed (saveSettings waits for a pause).
$("sBody").addEventListener("input", (e) => {
  if (e.target.id === "sStartupText") saveSettings({ startupMessage: { enabled: true, message: e.target.value } });
});
$("sBody").addEventListener("change", (e) => {
  const t = e.target;
  if (t.id === "sTerm") setPage({ termTheme: t.value });
  if (t.id === "sDays") {
    const days = Number(t.value);
    if (days >= 1 && days <= 365) { saveSettings({ everyDays: days }); renderSettings(); }
  }
});

// ---------- jump and move to ----------
// Jump: fuzzy search over the tabs, waiting ones first, then a few
// actions. Move to… lists the groups a tab can go into, the same way.
let pal = { mode: "jump", items: [], on: 0, tab: null };
function openPalette(mode = "jump", t = null) {
  pal = { mode, items: [], on: 0, tab: t };
  $("pIn").value = "";
  $("pIn").placeholder = mode === "move" ? `Move ${t.name} to…` : "Jump to a tab, or type > for actions";
  $("pFoot").innerHTML = mode === "move"
    ? `<span><kbd>↑↓</kbd> move</span><span><kbd>↵</kbd> move here</span><span class="gap"></span><span>Groups in ${esc(t.env)}</span>`
    : `<span><kbd>↑↓</kbd> move</span><span><kbd>↵</kbd> open</span><span><kbd>${MOD}⌫</kbd> close tab</span><span class="gap"></span><span>Waiting tabs first</span>`;
  $("palette").classList.add("open");
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
// statusWord is how Jump says a tab's state, in its dot's colour.
function statusWord(t) {
  if (t.status === "needs-you") return ["Needs you", "var(--needs)"];
  if (t.status === "ready" && t.unseen) return ["Done, unseen", "var(--ready)"];
  if (t.status === "ready") return ["Ready", "var(--ready)"];
  if (dead(t)) return ["Exited", "var(--muted)"];
  return [STATUS_TEXT[t.status] || t.status, "var(--working)"];
}
const ACTIONS = [["new", "New tab", "⌥N"], ["new-group", "New group", "⇧⌥N"], ["new-window", "New window", MOD + "N"], ["tab-settings", "Tab settings", MOD + "I"], ["settings", "Settings", MOD + ","]];
function renderPalette() {
  if (!$("palette").classList.contains("open")) return;
  const raw = $("pIn").value;
  let html = "", i = 0;
  const item = (inner) => `<button class="item ${i === pal.on ? "on" : ""}" data-i="${i++}">${inner}</button>`;
  if (pal.mode === "move") {
    const t = pal.tab;
    const all = [{ parent: "", label: t.env }];
    const add = (parent) => { for (const k of kids(t.env, parent)) if (k.group) { all.push({ parent: k.group.id, label: placeOf(t.env, k.group.id) }); add(k.group.id); } };
    add("");
    const q = raw.trim().toLowerCase();
    // Groups whose name has the text as typed first, then looser matches.
    const plain = (c) => (c.label.toLowerCase().split(" / ").pop().includes(q) ? 0 : 1);
    const found = all.filter((c) => c.parent !== (t.parent || "") && fuzzy(c.label, q)).sort((a, b) => plain(a) - plain(b));
    pal.items = found.map((c) => ({ into: c.parent }));
    pal.on = Math.min(pal.on, Math.max(0, pal.items.length - 1));
    for (const c of found) html += item(`<span class="pad"></span><span class="label">${esc(c.label)}</span>`);
    $("pItems").innerHTML = html || `<div class="empty">No other group here.</div>`;
  } else {
    const actionsOnly = raw.startsWith(">");
    const q = actionsOnly ? raw.slice(1).trim() : raw.trim();
    const vis = visual();
    const found = actionsOnly ? [] : vis.filter((t) => fuzzy(`${t.name} ${crumbOf(t)} ${t.branch || ""}`, q))
      .sort((a, b) => rank(a) - rank(b) || vis.indexOf(a) - vis.indexOf(b));
    const actions = ACTIONS.filter(([k, l]) => fuzzy(l, q) && (k !== "tab-settings" || selected));
    pal.items = [...found.map((t) => ({ tab: t })), ...actions.map((a) => ({ action: a[0] }))];
    pal.on = Math.min(pal.on, Math.max(0, pal.items.length - 1));
    if (found.length) html += `<div class="sec">Tabs</div>`;
    for (const t of found) {
      const n = vis.indexOf(t);
      const [word, colour] = statusWord(t);
      html += item(`<span class="dot ${dotClass(t)}"></span><span class="name">${esc(t.name)}</span><span class="where">${esc(crumbOf(t))}</span>
        <span class="st" style="color:${colour}">${word}</span><kbd>${n < 9 ? "⌥" + (n + 1) : ""}</kbd>`);
    }
    if (actions.length) html += `<div class="sec">Actions</div>`;
    for (const [, l, k] of actions) html += item(`<span class="pad"></span><span class="label">${esc(l)}</span><kbd>${k}</kbd>`);
    $("pItems").innerHTML = html || `<div class="empty">Nothing matches.</div>`;
  }
  $("pItems").querySelector(".on")?.scrollIntoView({ block: "nearest" });
}
function runPalette(i) {
  const it = pal.items[i];
  if (!it) return;
  const t = pal.tab;
  closePalette();
  if (it.into !== undefined) {
    for (const g of [...pathOf(t.env, it.into)]) setCollapsed("g:" + t.env + "/" + g.id, false);
    place(t.env, t.id, { parent: it.into, before: "", after: "" });
    return;
  }
  if (it.tab) { select(it.tab.key); return; }
  if (it.action === "new") openNew();
  if (it.action === "new-group") newGroupNear();
  if (it.action === "new-window") newWindow();
  if (it.action === "tab-settings") openTabSettings(selected);
  if (it.action === "settings") openSettings();
}
$("pIn").addEventListener("input", () => { pal.on = 0; renderPalette(); });
$("pIn").addEventListener("keydown", (e) => {
  const mod = isMac ? e.metaKey : e.ctrlKey;
  if (e.key === "ArrowDown") { pal.on = Math.min(pal.on + 1, pal.items.length - 1); renderPalette(); e.preventDefault(); }
  if (e.key === "ArrowUp") { pal.on = Math.max(pal.on - 1, 0); renderPalette(); e.preventDefault(); }
  if (e.key === "Enter") { runPalette(pal.on); e.preventDefault(); }
  if (e.key === "Escape") { closePalette(); e.preventDefault(); }
  // ⌘⌫ closes the highlighted tab, asking first.
  if (mod && e.key === "Backspace" && pal.items[pal.on]?.tab) {
    e.preventDefault();
    const key = pal.items[pal.on].tab.key;
    $("palette").classList.remove("open");
    closeTab(key).then(() => { $("palette").classList.add("open"); renderPalette(); $("pIn").focus(); });
  }
});
$("pItems").addEventListener("click", (e) => { const b = e.target.closest("[data-i]"); if (b) runPalette(Number(b.dataset.i)); });
$("pItems").addEventListener("mousemove", (e) => {
  const b = e.target.closest("[data-i]");
  if (b && Number(b.dataset.i) !== pal.on) { pal.on = Number(b.dataset.i); for (const x of $("pItems").querySelectorAll(".item")) x.classList.toggle("on", x === b); }
});
$("palette").addEventListener("pointerdown", (e) => { if (e.target === $("palette")) closePalette(); });
$("jump").onclick = () => openPalette();
$("jumpKey").textContent = MOD + "K";
$("settings").title = `Settings (${MOD},)`;

// ---------- the sidebar ----------
$("new2").onclick = $("newTab").onclick = () => openNew();
$("settings").onclick = () => openSettings();
$("head").addEventListener("click", (e) => { if (e.target.closest("#tabMore")) openTabSettings(selected); });

$("list").addEventListener("click", (e) => {
  if (e.target.closest(".rename")) return;
  const b = e.target.closest("button[data-env-act]");
  if (b) { envAct(b.dataset.envAct, b.dataset.env); return; }
  if (e.target.closest("[data-connect]")) { addEnv(); return; }
  const row = e.target.closest(".trow");
  if (!row) return;
  if (row.dataset.kind === "tab") { select(row.dataset.key); return; }
  toggle(row.dataset.fid);
});
$("list").addEventListener("dblclick", (e) => {
  const row = e.target.closest(".trow");
  if (row && row.dataset.kind === "group" && !e.target.closest("button")) startRename(row.dataset.fid);
});

function toggle(fid) {
  setCollapsed(fid, !collapsed.has(fid));
  render();
}

// newGroup makes a group named "New group" in a container, after a child of
// it or at its end, then names it in place: Enter keeps it, Esc removes it.
async function newGroup(name, parent, after = "") {
  try {
    const g = await api(name, "POST", "/api/groups", { name: "New group", parent, after });
    const data = backendOf(name);
    if (data && !groupOf(name, g.id)) data.groups = [...(data.groups || []), g];
    setCollapsed("env:" + name, false);
    for (const p of pathOf(name, parent)) setCollapsed("g:" + name + "/" + p.id, false);
    startRename("g:" + name + "/" + g.id, true);
  } catch (err) {
    say(err.message);
  }
}
// newGroupNear (⇧⌥N) makes a group next to the focused row in the tree, or
// else the selected tab.
function newGroupNear() {
  const row = document.activeElement?.closest?.("#list .trow");
  if (row?.dataset.kind === "env") { newGroup(row.dataset.env, ""); return; }
  if (row) { newGroup(row.dataset.env, row.dataset.parent || "", row.dataset.node); return; }
  const t = tab(selected);
  if (t) { newGroup(t.env, t.parent || "", t.id); return; }
  const e = envs.find((x) => backendOf(x.name));
  if (e) newGroup(e.name, "");
}

// ---------- renaming ----------
// Groups and environments rename in place; local keeps its name. A tab's
// name is claude's: /rename typed in the tab is the only way to rename one.
const canRename = (row) => row.dataset.kind === "group" || (row.dataset.kind === "env" && env(row.dataset.env)?.kind !== "local");
function startRename(fid, fresh = false) {
  editing = { fid, fresh };
  render();
}
async function finishRename(input, save) {
  const { fid, fresh } = editing || { fid: input.dataset.rename };
  const to = input.value.trim();
  editing = null;
  try {
    if (fid.startsWith("env:")) {
      const from = fid.slice(4);
      if (save && to && to !== from) await renameEnv(from, to);
    } else {
      const [name, id] = fid.slice(2).split("/");
      if (!save && fresh) await api(name, "DELETE", `/api/groups/${id}`);
      else if (save && to && to !== groupOf(name, id)?.name) await api(name, "PUT", `/api/groups/${id}`, { name: to });
    }
  } catch (err) {
    say(err.message);
  }
  render();
  $("list").querySelector(`.trow[data-fid="${CSS.escape(fid)}"]`)?.focus();
}
$("list").addEventListener("keydown", (e) => {
  const input = e.target.closest(".rename");
  if (!input) return;
  e.stopPropagation();
  if (e.key === "Enter") { e.preventDefault(); finishRename(input, true); }
  if (e.key === "Escape") { e.preventDefault(); finishRename(input, false); }
});
$("list").addEventListener("focusout", (e) => { if (e.target.matches?.(".rename") && editing) finishRename(e.target, true); });
// renameEnvKeys carries what this page keeps by environment name over to
// the new one: folded rows, the last directory, the open terminals (tab keys
// start with the name) and the selection.
function renameEnvKeys(from, to) {
  for (const k of [...collapsed]) {
    if (k === "env:" + from) { setCollapsed(k, false); setCollapsed("env:" + to, true); }
    if (k.startsWith("g:" + from + "/")) { setCollapsed(k, false); setCollapsed("g:" + to + k.slice(2 + from.length), true); }
  }
  const cwd = store.get("mux.lastCwd." + from);
  if (cwd) { store.set("mux.lastCwd." + to, cwd); store.remove("mux.lastCwd." + from); }
  const moved = (key) => (key.startsWith(from + "/") ? to + key.slice(from.length) : key);
  for (const map of [terms, lastStatus]) {
    for (const [k, v] of [...map]) if (moved(k) !== k) { map.delete(k); map.set(moved(k), v); }
  }
  for (const t of terms.values()) t.key = moved(t.key);
  if (selected && moved(selected) !== selected) { selected = moved(selected); selection.set(selected); }
}

// ---------- moving ----------
// Drag a tab or a group: onto a group or an environment (its middle) to go
// in at the end, or between rows, where the line's left edge is the depth
// it lands at: drag left or right to go up or down a level. Only within
// its environment, and a group never into itself. Environments drag by
// their headers, anywhere on another environment: its top half before,
// its bottom half after.
function dropSide(el, e) {
  const r = el.getBoundingClientRect();
  return e.clientY < r.top + r.height / 2 ? "before" : "after";
}
function clearDrop() {
  for (const t of $("list").querySelectorAll(".drop-before, .drop-after, .drop-into")) t.classList.remove("drop-before", "drop-after", "drop-into");
  $("dropline").hidden = true;
}
const envTarget = (e) => (dragEnv ? e.target.closest(".envblock") : null);
const inside = (row, id) => row.dataset.node === id || row.dataset.path.split("/").includes(id);
// nodeDrop is where the dragged node would land, from the row under the
// pointer: {parent, before | after, into?, row?, line?}.
function nodeDrop(e) {
  const el = e.target.closest(".trow");
  if (!drag || !el || el.dataset.env !== drag.env) return null;
  const rows = [...el.closest(".envblock").querySelectorAll(".trow")].filter((r) => r.dataset.kind === "env" || !inside(r, drag.id));
  if (!rows.includes(el)) return null; // the dragged group's own rows
  const r = el.getBoundingClientRect();
  const y = (e.clientY - r.top) / r.height;
  const container = el.dataset.kind !== "tab";
  if (container && y > 0.25 && y < 0.75) return { parent: el.dataset.node, into: el };
  const i = rows.indexOf(el);
  const [a, b] = y < 0.5 ? [rows[i - 1], el] : [el, rows[i + 1]];
  if (!a) return null; // above the environment's own header
  const depth = (row) => Number(row.dataset.depth);
  const open = (row) => row.dataset.kind !== "tab" && row.getAttribute("aria-expanded") === "true";
  const max = open(a) ? depth(a) + 1 : depth(a);
  const min = b ? depth(b) : 1;
  const want = Math.round((e.clientX - $("list").getBoundingClientRect().left - pad(0) - 8) / INDENT);
  const d = Math.max(min, Math.min(max, want, 99), 1);
  const lineAt = { top: (y < 0.5 ? r.top : r.bottom), left: pad(d) };
  if (open(a) && d === depth(a) + 1) {
    return { parent: a.dataset.node, before: b && b.dataset.parent === a.dataset.node ? b.dataset.node : "", line: lineAt };
  }
  // The node at depth d that a is, or is inside: the new one goes after it.
  const x = depth(a) === d ? a : rows.find((row) => row.dataset.node === a.dataset.path.split("/")[d - 1]);
  if (!x) return null;
  return { parent: x.dataset.parent || "", after: x.dataset.node, line: lineAt };
}
$("list").addEventListener("dragstart", (e) => {
  const row = e.target.closest(".trow");
  if (!row || editing) return;
  if (row.dataset.kind === "env") dragEnv = row.dataset.env;
  else { drag = { env: row.dataset.env, id: row.dataset.node, kind: row.dataset.kind }; row.classList.add("dragging"); }
  e.dataTransfer.effectAllowed = "move";
  e.dataTransfer.setData("text/plain", dragEnv || drag.id);
});
$("list").addEventListener("dragover", (e) => {
  const he = envTarget(e);
  if (he) {
    e.preventDefault();
    clearDrop();
    if (he.dataset.env !== dragEnv) he.classList.add("drop-" + dropSide(he, e));
    return;
  }
  clearDrop();
  const at = nodeDrop(e);
  if (!at) return;
  e.preventDefault();
  if (at.into) { at.into.classList.add("drop-into"); return; }
  // The line sits in the sidebar, over the list.
  const side = $("list").parentElement.getBoundingClientRect();
  const list = $("list").getBoundingClientRect();
  const line = $("dropline");
  line.hidden = false;
  line.style.top = at.line.top - side.top - 1 + "px";
  line.style.left = list.left - side.left + 8 + at.line.left + "px";
  line.style.width = list.width - 16 - at.line.left + "px";
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
  const at = nodeDrop(e);
  if (!at) return;
  e.preventDefault();
  const { env: name, id } = drag;
  if (at.into) setCollapsed(at.into.dataset.fid, false);
  await place(name, id, { parent: at.parent, before: at.before || "", after: at.after || "" });
});
$("list").addEventListener("dragend", () => {
  dragEnv = null;
  drag = null;
  clearDrop();
  render();
});
async function place(name, id, where) {
  await api(name, "PUT", `/api/nodes/${id}/place`, where).catch((err) => say(err.message));
}
// shift moves a tab or a group one place up or down among its siblings.
function shift(name, id, dir) {
  const node = groupOf(name, id) || tabs.find((t) => t.env === name && t.id === id);
  if (!node) return;
  const sib = kids(name, node.parent || "").map((k) => (k.group || k.tab).id);
  const i = sib.indexOf(id);
  const other = sib[i + dir];
  if (other) place(name, id, { parent: node.parent || "", before: dir < 0 ? other : "", after: dir > 0 ? other : "" });
}

// ---------- the tree by keyboard ----------
// ↑/↓ move, → unfolds or steps in, ← folds or steps out, Enter opens a tab
// or folds, Home/End, F2 renames, ⌫ deletes a group, the Menu key or ⇧F10
// opens the row's menu, typing a name jumps to it.
let typeahead = { text: "", at: 0 };
$("list").addEventListener("keydown", (e) => {
  const row = e.target.closest?.(".trow");
  if (!row || e.target.closest(".rename") || e.metaKey || e.ctrlKey || e.altKey) return;
  const rows = [...$("list").querySelectorAll(".trow")];
  const i = rows.indexOf(row);
  const go = (r) => { if (!r) return; for (const x of rows) x.tabIndex = -1; r.tabIndex = 0; r.focus(); };
  const container = row.dataset.kind !== "tab";
  const open = row.getAttribute("aria-expanded") === "true";
  const parentRow = () => rows.slice(0, i).reverse().find((r) => Number(r.dataset.depth) < Number(row.dataset.depth));
  let handled = true;
  if (e.key === "ContextMenu" || (e.key === "F10" && e.shiftKey)) {
    const r = row.getBoundingClientRect();
    openMenu({ x: r.left + Math.min(pad(Number(row.dataset.depth)) + 18, 28), y: r.bottom - 4 }, targetOf(row));
  } else if (e.shiftKey) handled = false;
  else if (e.key === "ArrowDown") go(rows[i + 1]);
  else if (e.key === "ArrowUp") go(rows[i - 1]);
  else if (e.key === "Home") go(rows[0]);
  else if (e.key === "End") go(rows.at(-1));
  else if (e.key === "ArrowRight") { if (container && !open) toggle(row.dataset.fid); else if (container) go(rows[i + 1]); }
  else if (e.key === "ArrowLeft") { if (container && open) toggle(row.dataset.fid); else go(parentRow()); }
  else if (e.key === "Enter" || e.key === " ") { if (container) toggle(row.dataset.fid); else select(row.dataset.key); }
  else if (e.key === "F2") { if (canRename(row)) startRename(row.dataset.fid); }
  else if ((e.key === "Backspace" || e.key === "Delete") && row.dataset.kind === "group") act("delete-group", targetOf(row));
  else if (e.key.length === 1 && /\S/.test(e.key)) {
    typeahead = { text: (Date.now() - typeahead.at < 700 ? typeahead.text : "") + e.key.toLowerCase(), at: Date.now() };
    const name = (r) => (r.querySelector(".name, .gname, .ename")?.textContent || "").toLowerCase();
    go([...rows.slice(i + (typeahead.text.length > 1 ? 0 : 1)), ...rows.slice(0, i)].find((r) => name(r).startsWith(typeahead.text)));
  } else handled = false;
  if (handled) { e.preventDefault(); e.stopPropagation(); }
});

// ---------- context menu ----------
// Right-click, the Menu key or ⇧F10 on a row. Everything here also has a
// key or a visible place.
const targetOf = (row) => ({ kind: row.dataset.kind, key: row.dataset.key, env: row.dataset.env, id: row.dataset.node, fid: row.dataset.fid, row });
function menuItems(target) {
  if (target.kind === "env") {
    const e = env(target.env);
    const can = canStart(target.env);
    return [can && ["new-tab", "New tab", "⌥N"], backendOf(target.env) && ["new-group", "New group", "⇧⌥N"], null,
      ["env-reconnect", "Reconnect"], ["env-settings", "Environment settings…"],
      e?.state === "connected" && backendOf(e.name) && !hasClaude(e.name) && ["env-install", "Install claude…"],
      e?.kind !== "local" && null, e?.kind !== "local" && ["env-remove", "Remove environment", "", "danger"]].filter((x) => x !== false && x !== undefined);
  }
  if (target.kind === "group") {
    const shut = collapsed.has(target.fid);
    return [canStart(target.env) && ["new-tab", "New tab", "⌥N"], ["new-group", "New group", "⇧⌥N"], null,
      ["rename", "Rename", "F2"], ["fold", shut ? "Expand" : "Collapse", shut ? "→" : "←"], null,
      ["delete-group", "Delete group", "⌫", "danger"]].filter((x) => x !== false);
  }
  const t = tab(target.key);
  if (!t) return [];
  return [["tab-settings", "Tab settings…", MOD + "I"], ["duplicate", "Duplicate", ""], ["move", "Move to…", ""], null,
    dead(t) ? ["start", "Resume", "↵"] : ["restart", "Restart", ""], ["close", "Close tab", MOD + "W", "danger"]];
}

let menuFrom = null; // the element that had focus, for Esc
function closeMenu() {
  if (!$("menu").classList.contains("open")) return;
  $("menu").classList.remove("open");
}
function openMenu(at, target) {
  const menu = $("menu");
  menuFrom = document.activeElement;
  // A menu never starts or ends with a line, nor has two in a row.
  const items = menuItems(target).filter((it, i, all) => it || (i > 0 && all[i - 1] && i < all.length - 1));
  menu.innerHTML = items.map((it) => it ? `<button role="menuitem" tabindex="-1" data-act="${it[0]}" class="${it[3] || ""}"><span>${esc(it[1])}</span><kbd>${esc(it[2])}</kbd></button>` : `<hr role="separator">`).join("");
  menu.onclick = (ev) => {
    const b = ev.target.closest("button[data-act]");
    if (!b) return;
    closeMenu();
    act(b.dataset.act, target);
  };
  menu.classList.add("open");
  const r = menu.getBoundingClientRect();
  menu.style.left = Math.min(at.x, innerWidth - r.width - 8) + "px";
  menu.style.top = Math.min(at.y, innerHeight - r.height - 8) + "px";
  menu.querySelector("button")?.focus();
}
$("menu").addEventListener("keydown", (e) => {
  const items = [...$("menu").querySelectorAll("button")];
  const i = items.indexOf(document.activeElement);
  if (e.key === "ArrowDown") items[(i + 1) % items.length].focus();
  else if (e.key === "ArrowUp") items[(i - 1 + items.length) % items.length].focus();
  else if (e.key === "Home") items[0].focus();
  else if (e.key === "End") items.at(-1).focus();
  else if (e.key === "Escape" || e.key === "Tab") { closeMenu(); menuFrom?.focus?.(); }
  else return;
  e.preventDefault();
  e.stopPropagation();
});
$("list").addEventListener("contextmenu", (e) => {
  const el = e.target.closest(".trow");
  if (!el) return;
  e.preventDefault();
  openMenu({ x: e.clientX, y: e.clientY }, targetOf(el));
});
window.addEventListener("pointerdown", (e) => { if (!e.target.closest("#menu")) closeMenu(); }, true);
window.addEventListener("blur", closeMenu);

// closeTab closes a tab, asking first when Settings say so, when it is
// working, or always (⌘W).
async function closeTab(key, always = true) {
  const t = tab(key);
  if (!t) return;
  const why = t.status === "working" ? "It is working: the turn in progress is cut off. " : "";
  if ((always || page.confirmClose || why) && !(await sure(`Close ${t.name}?`, `${why}Its claude ends and the tab is removed for good.`, "Close tab"))) return;
  await api(t.env, "DELETE", `/api/sessions/${t.id}`).catch((err) => say(err.message));
}

// act runs one action on a target.
async function act(what, target) {
  const t = target.kind === "tab" ? tab(target.key) : null;
  const name = t?.env ?? target.env;
  const parent = target.kind === "group" ? target.id : "";
  try {
    if (what === "start") {
      await api(name, "POST", `/api/sessions/${t.id}/start`);
      terms.get(t.key)?.term.focus();
    } else if (what === "restart") {
      if (t.status !== "working" || await sure(`Restart ${t.name}?`, "It is working: the turn in progress is cut off. Its conversation continues.", "Restart")) await api(name, "POST", `/api/sessions/${t.id}/restart`);
    } else if (what === "tab-settings") {
      openTabSettings(t.key);
    } else if (what === "duplicate") {
      await duplicate(t);
    } else if (what === "move") {
      openPalette("move", t);
    } else if (what === "close") {
      await closeTab(t.key, false);
    } else if (what === "rename") {
      startRename(target.fid);
    } else if (what === "fold") {
      toggle(target.fid);
    } else if (what === "delete-group") {
      await api(name, "DELETE", `/api/groups/${target.id}`); // what it held moves up; no tab closes
    } else if (what === "new-tab") {
      openNew({ env: name, parent, after: "" });
    } else if (what === "new-group") {
      await newGroup(name, parent);
    } else if (what.startsWith("env-")) {
      envAct(what.slice(4), name);
    }
  } catch (err) {
    say(err.message);
  }
}

// newWindow opens another window on this hub, for another tab on another
// screen: a window of the app's own, or a browser window.
function newWindow() {
  if (window.cmuxNewWindow) window.cmuxNewWindow();
  else window.open(location.href, "_blank");
}

// askCompact offers the compact tab rows once to a page that was on Detailed
// when they became the default; closing the question keeps Detailed.
let askingCompact = false;
async function askCompact() {
  askingCompact = true;
  const yes = await sure("Compact tab rows", "Compact is now the recommended layout: each tab takes one row, with only its name. Switch to it? You can change this at any time in Settings › Appearance.", "Switch to Compact", "Keep Detailed");
  if (!askingCompact) return;
  askingCompact = false;
  setPage({ askCompact: undefined, ...(yes ? { tabRows: "one" } : {}) });
}

// sure and say stand in for confirm and alert, which the app's own window
// (a bare web view) answers without showing anything.
function sure(title, text, ok = "OK", cancel = "Cancel") {
  $("mTitle").textContent = title;
  $("mTitle").parentElement.hidden = !title;
  $("mText").textContent = text;
  $("mOk").textContent = ok;
  $("mCancel").textContent = cancel;
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

// ---------- keys ----------
// Caught before the terminal sees them.
window.addEventListener("keydown", (e) => {
  const mod = isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
  const dialog = document.querySelector("dialog[open]");
  if (mod && !e.altKey && e.code === "KeyK") { e.preventDefault(); e.stopPropagation(); $("palette").classList.contains("open") ? closePalette() : openPalette(); return; }
  if (mod && !e.altKey && !e.shiftKey && e.code === "KeyN") { e.preventDefault(); e.stopPropagation(); newWindow(); return; }
  if (mod && !e.altKey && e.code === "Comma") { e.preventDefault(); e.stopPropagation(); if (!dialog) openSettings(); return; }
  // ⌘W closes the selected tab, asking first.
  if (mod && !e.altKey && !e.shiftKey && e.code === "KeyW") {
    e.preventDefault(); e.stopPropagation();
    if (selected && !dialog) closeTab(selected);
    return;
  }
  if (mod && !e.altKey && !e.shiftKey && e.code === "KeyI") {
    e.preventDefault(); e.stopPropagation();
    if (selected && !dialog) openTabSettings(selected);
    return;
  }
  // ⌘⇧↑/↓ moves the focused row (or the selected tab) within its container.
  if (mod && e.shiftKey && !e.altKey && (e.code === "ArrowUp" || e.code === "ArrowDown")) {
    const row = document.activeElement?.closest?.(".trow");
    const t = tab(selected);
    const name = row && row.dataset.kind !== "env" ? row.dataset.env : t?.env;
    const id = row && row.dataset.kind !== "env" ? row.dataset.node : t?.id;
    if (name && id) { e.preventDefault(); e.stopPropagation(); shift(name, id, e.code === "ArrowUp" ? -1 : 1); }
    return;
  }
  if (e.key === "Escape") { closeMenu(); return; }
  if (!e.altKey || e.metaKey || e.ctrlKey || dialog) return;
  let handled = true;
  if (/^Digit[1-9]$/.test(e.code)) {
    const t = visual()[Number(e.code.slice(5)) - 1];
    if (t) select(t.key);
  } else if (e.code === "KeyN") e.shiftKey ? newGroupNear() : openNew();
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
// A file dropped anywhere else would make the window open it in place of the page.
for (const type of ["dragover", "drop"]) window.addEventListener(type, (e) => { if (e.dataTransfer?.types.includes("Files")) e.preventDefault(); });
new ResizeObserver(fitSelected).observe($("stage"));
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") claimSize();
  if (selected) markSeen(selected);
});
window.addEventListener("focus", () => { store.set("mux.focus", "1"); claimSize(); if (selected) markSeen(selected); });
window.addEventListener("blur", () => store.set("mux.focus", "0"));
setInterval(render, 30000); // keep the "for 3m" titles moving
// Measure again once the terminal font has loaded.
document.fonts?.ready.then(() => { for (const t of terms.values()) t.term.options.fontFamily = TERM_FONT; fitSelected(); });

// ---------- boot ----------
applyPage();
streamHub();
