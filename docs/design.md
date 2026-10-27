# claude-mux: hub and backends

Status: step 1 done (2026-10-27); the rest is design. Today claude-mux is one Mac program that runs
claude sessions on this machine. This design splits it into a **hub** (the
window and the page) and **backends** (the sessions), so one window can
show sessions running here, over SSH, in Docker, in WSL and on Windows.

## Goals

- One window for claude sessions in several **environments**: this machine,
  SSH hosts, Docker containers, WSL distros, native Windows.
- The hub runs on Mac and Windows. Backends run on Mac, Windows and Linux.
- Nothing to install by hand in an environment: the hub puts the backend
  there on first connect, the way VS Code Remote installs its server.
- Everything claude-mux does today keeps working in every environment.

## Non-goals

- **Sessions that outlive the hub.** A backend is a child of the hub. When
  the hub quits or the connection drops, its sessions end, and they come
  back with `--resume` on the next connect.
- **Moving a conversation between environments.** Each environment has its
  own claude, its own login and its own `~/.claude`.
- **Claude that claude-mux didn't start.** Each environment shows only its
  own tabs (dropped on 2026-10-27, before this work began: no "Running in
  Terminal", no saved conversations, no typing into other windows).

## Components

Two programs, one Go module, one version.

| Program | Job | Built for |
|---|---|---|
| `claude-mux` (hub) | the window and the page; the environment list; starts one backend per environment and routes the page's requests to it; installs backends | Mac, Windows |
| `claude-mux-backend` | the tabs of the environment it runs in: ptys, claude processes, hooks, names, groups, the state file | Mac, Windows, Linux; amd64 and arm64 |

```
cmd/hub/            main for the hub
cmd/backend/        main for the backend
internal/backend/   tabs, ptys, hooks, the state file, the API
internal/hub/       environments, transport, install, routing, the page (web/)
```

Types both sides need move to a shared package when the hub first needs
one; in step 1 the hub only forwards bytes.

The backend is pure Go with no C dependencies (`CGO_ENABLED=0`), so one
Linux file runs in any distro or container. The hub may need C for its
window.

## Environments

An environment is a name, a kind, and the command that reaches it. The hub
keeps them in `~/.claude-mux/environments.json`.

| Kind | Reached with | Example |
|---|---|---|
| `local` | the backend run directly | this machine |
| `ssh` | `ssh <host> <cmd>` | a dev box, a Windows machine with OpenSSH |
| `docker` | `docker exec -i <container> <cmd>` | a sandbox for `--dangerously-skip-permissions` |
| `wsl` | `wsl.exe -d <distro> -- <cmd>` | Ubuntu on the Windows hub's machine |

`local` always exists. The others are added in the page (or the file).

## Transport: one pipe per environment

The hub starts the backend through the environment's command and talks to
it over that process's **stdin and stdout**. Nothing in the environment
listens on the network for the hub, and access is exactly what SSH, Docker
or WSL already grant.

- The pipe carries a stream multiplexer (`hashicorp/yamux`). The backend
  is the yamux server: its session is a `net.Listener`, so today's gin
  handler is served on it unchanged (`http.Serve(session, handler)`).
- The hub is the yamux client. It has one `httputil.ReverseProxy` per
  environment whose transport dials `session.Open()`. Requests, the
  server-sent events and the terminal WebSockets all pass through it as
  ordinary HTTP.
- The page talks only to the hub. `/env/<name>/api/…` goes to that
  environment's backend with the prefix cut; the backend never knows where
  it is mounted.
- The backend's stderr goes to the hub's log for that environment.

## Install: backends on demand

The hub carries no backend, not even its own OS's. Every backend, the local
one included, is installed on demand under the environment's
`~/.claude-mux/bin/<version>/claude-mux-backend` (`.exe` on Windows), as
VS Code installs `~/.vscode-server`.

On connect, the hub runs a small bootstrap through the environment's
command:

1. Detect the platform: `uname -sm` (POSIX), or
   `$env:PROCESSOR_ARCHITECTURE` over PowerShell for a Windows host.
2. If `~/.claude-mux/bin/<version>/claude-mux-backend` exists, run it:
   `exec … --stdio`.
3. Otherwise install it, then run it.

Where the file comes from:

- **From the hub (the default for now).** The hub downloads the build for
  that platform from the GitHub release of its own version, checks it
  against the release's `checksums.txt`, caches it in its own
  `~/.claude-mux/cache/<version>/`, and streams it through the command
  (`sh -c 'mkdir -p … && cat > ….tmp && chmod +x ….tmp && mv ….tmp …'`).
  This works without internet in the environment. The repository is
  private, so the hub downloads with the user's GitHub token
  (`gh auth token` or `GITHUB_TOKEN`).
- **From the environment itself (later).** With public releases, the
  bootstrap fetches the file with `curl` or `wget` and skips the stream.

The backend is always exactly the hub's version, so the two never speak
different protocols. A version directory nothing uses is deleted.

**Development:** `claude-mux -backend-dir dist/` takes the builds from a
local directory (`make backends` cross-compiles them) instead of a release,
so unreleased versions can be tried in every environment.

## Lifecycle

- The hub starts a backend when the environment connects: `local` at
  start, the others when added or when the page opens them.
- **End of input ends the backend.** When the hub quits or the pipe breaks
  (SSH drops, container stops), the backend reads EOF and runs today's
  shutdown: note which tabs run, hang up their claudes, wait, exit.
- **Every backend start resumes what was running.** With backends as
  children, quitting the hub is the same as a restart, so resuming is the
  default. A tab is marked running when its claude starts and unmarked only
  when claude exits by itself, so a backend that is killed resumes its tabs
  too. Restore messages are sent as today.
- A broken connection is retried with backoff; the page shows the
  environment as connecting, with the last error.

## Hooks inside an environment

The backend no longer serves its API on a port, but claude's hooks still
need to reach it.

- The backend listens on a random `127.0.0.1` port for hooks only.
  Its claude children get the address in `CLAUDE_MUX_HOOK`
  (`http://127.0.0.1:<port>/hook/<token>/<tab id>`), with a random token per
  backend start, so another user on a shared host can't post fake statuses.
- The hook command is the backend itself: `claude-mux-backend hook`. It
  reads the payload on stdin and posts it, so minimal containers need no
  `curl`. On Windows the path is written with forward slashes, since claude
  runs hooks through Git Bash there.
- A random port per backend keeps WSL distros, which share one network,
  from colliding.

## Platform differences

| What | Mac, Linux | Windows |
|---|---|---|
| pty | `creack/pty` | ConPTY |
| stopping a claude | SIGHUP, SIGKILL after 3s | close the pseudo-console, then `TerminateProcess` |
| names (`claude agents --json`) | the same | the same |

## The page

- The sidebar shows one block per environment, each with its tabs and
  groups. With only `local`, the page looks as it does today.
- An environment's header shows its state: connected, connecting (with the
  error), or a missing claude / login.
- New asks for the environment, defaulting to the selected tab's.
- ⌥1–9, ⌥J, the title count and the icon cover all environments.
- Add environment: kind plus host, container or distro. For WSL the hub
  lists installed distros (`wsl -l -q`, skipping `docker-desktop*`).

## Releases

A tag builds, in CI:

- `claude-mux-backend-<os>-<arch>` for darwin, linux and windows on amd64
  and arm64, plus `checksums.txt`;
- the hub for Mac (an `.app`, arm64 and amd64) and Windows (`.exe`).

Unsigned at first: the first launch needs right-click → Open on Mac and
"Run anyway" on Windows.

## Steps

Each step ships something that runs.

1. **Split.** `cmd/hub` and `cmd/backend`; the backend serves over stdio and
   yamux and takes its hooks on its own port; the hub runs the local
   backend as a child (`-backend <path>` for now) and routes `/env/local/`.
   Behaviour on the Mac is unchanged; restarts resume by default.
2. **Environments.** `environments.json`, environment blocks in the page,
   the `ssh` kind, the bootstrap with install from `-backend-dir`.
3. **Releases.** The CI matrix, install from GitHub releases, the local
   backend installed the same way.
4. **Docker and WSL** kinds.
5. **Windows.** ConPTY in the backend, the Windows hub, `wt.exe`.
6. **A native window** for the hub on Mac and Windows (until then the hub
   opens the page in the browser).

## Open questions

- The hub's window: `webview/webview_go` (small, C bindings) or Wails (more
  framework than needed)? Decided in step 6.
- Public releases, so environments download for themselves?
- What the page shows when an environment has no claude, or claude isn't
  logged in, and whether it offers to install it.
