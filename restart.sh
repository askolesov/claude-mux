#!/usr/bin/env bash
# Rebuild claude-mux and restart it on :7777; -resume-running brings back
# the tabs that were running. Run it from a regular terminal: a session
# inside claude-mux would be hung up by the restart along with the rest.
set -euo pipefail

dir=$(cd "$(dirname "$0")" && pwd)
url=http://127.0.0.1:7777
state=$HOME/.claude-mux

go -C "$dir" build -o "$state/claude-mux.new" .

if pid=$(lsof -ti tcp:7777 -sTCP:LISTEN); then
	kill -INT "$pid"
	while kill -0 "$pid" 2>/dev/null; do sleep 0.1; done
fi

# A fresh file, not a copy over the old one: macOS kills a binary rewritten
# in place on its stale code signature.
mv -f "$state/claude-mux.new" "$state/claude-mux"
nohup "$state/claude-mux" -resume-running -cwd "$(cd "$dir/../.." && pwd)" >"$state/server.log" 2>&1 </dev/null &
until curl -s -o /dev/null "$url/api/state"; do sleep 0.1; done
curl -s "$url/api/state" | jq -r '.sessions[] | "resumed \(.name)"'
echo "claude-mux on $url"
