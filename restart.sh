#!/usr/bin/env bash
# Rebuild the hub and the local backend and restart them on :7777. The old
# backend hangs up its sessions on the way out and the new one resumes them.
# Run it from a regular terminal: a session inside claude-mux is hung up by
# the restart along with the rest.
set -euo pipefail

dir=$(cd "$(dirname "$0")" && pwd)
url=http://127.0.0.1:7777
state=$HOME/.claude-mux
bin=$state/bin/dev

mkdir -p "$bin"
go -C "$dir" build -o "$state/claude-mux.new" ./cmd/hub
go -C "$dir" build -o "$bin/claude-mux-backend.new" ./cmd/backend

if pid=$(lsof -ti tcp:7777 -sTCP:LISTEN); then
	kill -INT "$pid"
	while kill -0 "$pid" 2>/dev/null; do sleep 0.1; done
fi

# Fresh files, not copies over the old ones: macOS kills a binary rewritten
# in place on its stale code signature.
mv -f "$state/claude-mux.new" "$state/claude-mux"
mv -f "$bin/claude-mux-backend.new" "$bin/claude-mux-backend"
nohup "$state/claude-mux" -backend "$bin/claude-mux-backend" -cwd "${CLAUDE_MUX_CWD:-$HOME/Documents/@Ankr/codespace}" >"$state/server.log" 2>&1 </dev/null &
until [ "$(curl -s "$url/env/local/api/state" | jq -r '.sessions | type' 2>/dev/null)" = array ]; do sleep 0.2; done
curl -s "$url/env/local/api/state" | jq -r '.sessions[] | "resumed \(.name)"'
echo "claude-mux on $url"
